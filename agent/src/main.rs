mod config;
mod grpc_client;
mod model_runtime;
mod telemetry;

pub mod proto {
    tonic::include_proto!("cami.device.v1");
}

use anyhow::Result;
use clap::Parser;
use config::Config;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::sync::watch;
use tokio::time::{interval, sleep};
use tracing::{error, info, warn};
use tracing_subscriber::EnvFilter;

use proto::{agent_service_client::AgentServiceClient, DeploymentAck, Heartbeat, RegisterRequest, WatchRequest};

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env().add_directive("cami_agent=info".parse()?))
        .init();

    let cfg = Config::parse();
    info!(device = %cfg.device_name, url = %cfg.control_plane_url, "cami agent starting");

    let (model_tx, model_rx) = watch::channel::<Option<model_runtime::ModelHandle>>(None);

    loop {
        if let Err(e) = run_agent(&cfg, model_tx.clone(), model_rx.clone()).await {
            error!("agent error: {e:#} — reconnecting in 5s");
            sleep(Duration::from_secs(5)).await;
        }
    }
}

async fn run_agent(
    cfg: &Config,
    model_tx: watch::Sender<Option<model_runtime::ModelHandle>>,
    model_rx: watch::Receiver<Option<model_runtime::ModelHandle>>,
) -> Result<()> {
    let mut client = grpc_client::connect(&cfg.control_plane_url, &cfg.cert_dir).await?;

    let labels = cfg.labels();
    let resp = client
        .register(RegisterRequest {
            device_name: cfg.device_name.clone(),
            labels,
            agent_version: env!("CARGO_PKG_VERSION").to_string(),
            arch: std::env::consts::ARCH.to_string(),
            os: std::env::consts::OS.to_string(),
        })
        .await?;
    let device_id = resp.into_inner().device_id;
    info!(%device_id, "registered with control plane");

    // Telemetry streamer (dedicated connection)
    {
        let telem_client = grpc_client::connect(&cfg.control_plane_url, &cfg.cert_dir).await?;
        let telem_did = device_id.clone();
        let rx = model_rx.clone();
        tokio::spawn(async move {
            telemetry::run_telemetry_loop(telem_client, telem_did, rx).await;
        });
    }

    // Heartbeat task (dedicated connection)
    {
        let hb_url = cfg.control_plane_url.clone();
        let hb_cert = cfg.cert_dir.clone();
        let hb_did = device_id.clone();
        tokio::spawn(async move {
            let mut ticker = interval(Duration::from_secs(10));
            // Use a single client; reconnect on error
            loop {
                match grpc_client::connect(&hb_url, &hb_cert).await {
                    Ok(mut hb_client) => loop {
                        ticker.tick().await;
                        let ts = unix_now();
                        if let Err(e) = hb_client
                            .send_heartbeat(Heartbeat {
                                device_id: hb_did.clone(),
                                ts_unix: ts,
                            })
                            .await
                        {
                            warn!("heartbeat error: {e} — reconnecting");
                            break;
                        }
                    },
                    Err(e) => {
                        warn!("heartbeat connect failed: {e}");
                        sleep(Duration::from_secs(5)).await;
                    }
                }
            }
        });
    }

    // Deployment watcher (main task — blocks until stream ends or errors)
    watch_deployments(&mut client, &device_id, model_tx, cfg).await
}

async fn watch_deployments(
    client: &mut AgentServiceClient<tonic::transport::Channel>,
    device_id: &str,
    model_tx: watch::Sender<Option<model_runtime::ModelHandle>>,
    cfg: &Config,
) -> Result<()> {
    info!(%device_id, "watching for deployments");

    let mut stream = client
        .watch_deployments(WatchRequest {
            device_id: device_id.to_string(),
        })
        .await?
        .into_inner();

    while let Some(instr) = stream.message().await? {
        info!(
            deployment_id = %instr.deployment_id,
            model_id = %instr.model_id,
            "received deployment instruction"
        );

        let cp_url = cfg.control_plane_url.clone();
        let cert_dir = cfg.cert_dir.clone();
        let did = device_id.to_string();
        let dep_id = instr.deployment_id.clone();
        let model_id = instr.model_id.clone();
        let artifact_url = instr.artifact_url.clone();
        let artifact_sha256 = instr.artifact_sha256.clone();
        let tx = model_tx.clone();
        let cfg = cfg.clone();

        tokio::spawn(async move {
            execute_deployment(
                cp_url,
                cert_dir,
                did,
                dep_id,
                model_id,
                artifact_url,
                artifact_sha256,
                tx,
                cfg,
            )
            .await;
        });
    }
    Ok(())
}

#[allow(clippy::too_many_arguments)]
async fn execute_deployment(
    control_plane_url: String,
    cert_dir: String,
    device_id: String,
    deployment_id: String,
    model_id: String,
    artifact_url: String,
    artifact_sha256: String,
    model_tx: watch::Sender<Option<model_runtime::ModelHandle>>,
    cfg: Config,
) {
    macro_rules! ack {
        ($status:expr, $msg:expr) => {{
            let url = control_plane_url.clone();
            let cert = cert_dir.clone();
            let did = device_id.clone();
            let dep = deployment_id.clone();
            let st = $status.to_string();
            let em = $msg.to_string();
            async move {
                match grpc_client::connect(&url, &cert).await {
                    Ok(mut c) => {
                        let _ = c
                            .ack_deployment(DeploymentAck {
                                deployment_id: dep,
                                device_id: did,
                                status: st,
                                error_msg: em,
                            })
                            .await;
                    }
                    Err(e) => warn!("ack connect failed: {e}"),
                }
            }
        }};
    }

    ack!("downloading", "").await;

    let bytes = match model_runtime::download_artifact(&model_id, &artifact_url).await {
        Ok(b) => b,
        Err(e) => {
            error!(%model_id, "download failed: {e:#}");
            ack!("failed", &e.to_string()).await;
            return;
        }
    };

    ack!("verifying", "").await;

    if let Err(e) = model_runtime::verify_artifact(&model_id, &bytes, &artifact_sha256) {
        error!(%model_id, "verification failed: {e:#}");
        ack!("failed", &e.to_string()).await;
        return;
    }

    let backend = cfg.resolve_backend(&model_id);
    let backend_name = backend.name();
    info!(%model_id, %backend_name, "resolved runtime backend");

    match model_runtime::load_model(&model_id, backend).await {
        Ok(handle) => {
            let mode = handle.backend.name();
            model_tx.send_replace(Some(handle));
            ack!("running", "").await;
            info!(%model_id, %mode, "model running");
        }
        Err(e) => {
            error!(%model_id, "model load failed: {e:#}");
            ack!("failed", &e.to_string()).await;
        }
    }
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}

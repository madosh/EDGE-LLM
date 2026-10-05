use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::sync::watch;
use tokio::time::{interval, sleep, MissedTickBehavior};
use tonic::transport::Channel;
use tracing::{info, warn};

use crate::model_runtime::{sample_telemetry, ModelHandle};
use crate::proto::{agent_service_client::AgentServiceClient, TelemetryReport};

/// Streams telemetry to the control plane every `every` while a model is
/// loaded. Each tick samples the model that is loaded *now*, so a new
/// deployment is measured as soon as it is running. Failed probes are sent
/// too (`status = "error"`), so an outage shows on the dashboard instead of
/// a gap. Runs until the task is aborted.
pub async fn run_telemetry_loop(
    client: AgentServiceClient<Channel>,
    device_id: String,
    model_rx: watch::Receiver<Option<ModelHandle>>,
    every: Duration,
) {
    loop {
        let first = wait_for_model(&model_rx).await;
        info!(model_id = %first.model_id, "starting telemetry collection");

        let (tx, rx) = tokio::sync::mpsc::channel::<TelemetryReport>(16);
        let did = device_id.clone();
        let latest = model_rx.clone();
        let producer = tokio::spawn(async move {
            let mut ticker = interval(every);
            ticker.set_missed_tick_behavior(MissedTickBehavior::Delay);
            loop {
                ticker.tick().await;
                let Some(handle) = latest.borrow().clone() else {
                    continue;
                };
                let telem = sample_telemetry(&handle).await;
                let report = TelemetryReport {
                    device_id: did.clone(),
                    model_id: handle.model_id.clone(),
                    tps: telem.tps,
                    ttft_ms: telem.ttft_ms,
                    mem_mb: telem.mem_mb,
                    status: telem.status,
                    source: telem.source,
                    ts_unix: unix_now(),
                };
                if tx.send(report).await.is_err() {
                    break;
                }
            }
        });

        let stream = tokio_stream::wrappers::ReceiverStream::new(rx);
        let mut client = client.clone();
        match client.stream_telemetry(stream).await {
            Ok(_) => info!("telemetry stream ended cleanly"),
            Err(e) => warn!("telemetry stream error: {e}"),
        }

        producer.abort();
        sleep(Duration::from_secs(3)).await;
    }
}

async fn wait_for_model(model_rx: &watch::Receiver<Option<ModelHandle>>) -> ModelHandle {
    if let Some(h) = model_rx.borrow().clone() {
        return h;
    }
    let mut rx = model_rx.clone();
    loop {
        if rx.changed().await.is_err() {
            // The sender is gone; nothing will ever load. Park this task.
            std::future::pending::<()>().await;
        }
        if let Some(h) = rx.borrow().clone() {
            return h;
        }
    }
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}

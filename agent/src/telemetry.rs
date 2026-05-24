use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::sync::watch;
use tokio::time::{interval, sleep};
use tonic::transport::Channel;
use tracing::{info, warn};

use crate::model_runtime::{sample_telemetry, ModelHandle};
use crate::proto::{agent_service_client::AgentServiceClient, TelemetryReport};

/// Streams telemetry to the control plane every 5 s while a model is loaded.
/// Reconnects after any error. Blocks permanently — run in a spawned task.
pub async fn run_telemetry_loop(
    mut client: AgentServiceClient<Channel>,
    device_id: String,
    model_rx: watch::Receiver<Option<ModelHandle>>,
) {
    loop {
        let handle = wait_for_model(&model_rx).await;
        info!(model_id = %handle.model_id, "starting telemetry collection");

        let mut ticker = interval(Duration::from_secs(5));

        let (tx, rx) = tokio::sync::mpsc::channel::<TelemetryReport>(16);

        let did = device_id.clone();
        let h = handle.clone();
        let producer = tokio::spawn(async move {
            loop {
                ticker.tick().await;
                let telem = sample_telemetry(&h).await;
                let report = TelemetryReport {
                    device_id: did.clone(),
                    model_id: h.model_id.clone(),
                    tps: telem.tps,
                    ttft_ms: telem.ttft_ms,
                    mem_mb: telem.mem_mb,
                    status: telem.status,
                    ts_unix: unix_now(),
                };
                if tx.send(report).await.is_err() {
                    break;
                }
            }
        });

        let stream = tokio_stream::wrappers::ReceiverStream::new(rx);
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
        rx.changed().await.ok();
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

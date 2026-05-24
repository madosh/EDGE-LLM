use anyhow::Result;
use std::time::Duration;
use tonic::transport::{Certificate, ClientTlsConfig, Endpoint, Identity};
use tracing::info;

use crate::proto::agent_service_client::AgentServiceClient;

pub async fn connect(url: &str, cert_dir: &str) -> Result<AgentServiceClient<tonic::transport::Channel>> {
    info!(%url, "connecting to control plane (mTLS)");

    let ca_pem = tokio::fs::read(format!("{cert_dir}/ca.crt")).await?;
    let client_cert = tokio::fs::read(format!("{cert_dir}/device.crt")).await?;
    let client_key = tokio::fs::read(format!("{cert_dir}/device.key")).await?;

    let ca = Certificate::from_pem(&ca_pem);
    let identity = Identity::from_pem(&client_cert, &client_key);

    // SNI hostname for TLS handshake — must match the server cert's CN / SAN
    let host = url
        .trim_start_matches("https://")
        .trim_start_matches("http://")
        .split(':')
        .next()
        .unwrap_or("control-plane");

    let tls = ClientTlsConfig::new()
        .domain_name(host)
        .ca_certificate(ca)
        .identity(identity);

    let channel = Endpoint::from_shared(url.to_owned())?
        .tls_config(tls)?
        .connect_timeout(Duration::from_secs(15))
        .tcp_keepalive(Some(Duration::from_secs(30)))
        .connect()
        .await?;

    info!("connected to control plane");
    Ok(AgentServiceClient::new(channel))
}

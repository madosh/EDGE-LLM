//! Integration tests for the Cami agent's library surface.
//!
//! These run without a gRPC server or an inference runtime: they exercise the
//! artifact pipeline (download, hash, verify, cache) against a local HTTP
//! server, and the stub backend end to end.

use std::path::{Path, PathBuf};

use cami_agent::model_runtime::{
    cached_artifact_path, download_artifact, load_model, sample_telemetry, verify_artifact, verify_fetched, Fetched,
    RuntimeBackend, SOURCE_STUB,
};
use sha2::{Digest, Sha256};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

fn temp_dir(tag: &str) -> PathBuf {
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    std::env::temp_dir().join(format!("cami-it-{tag}-{nanos}"))
}

/// Serves `body` once over plain HTTP and returns its URL.
async fn serve_once(body: &'static [u8]) -> String {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        let mut req = [0u8; 1024];
        let _ = socket.read(&mut req).await;
        let head = format!(
            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nContent-Type: application/octet-stream\r\nConnection: close\r\n\r\n",
            body.len()
        );
        socket.write_all(head.as_bytes()).await.unwrap();
        socket.write_all(body).await.unwrap();
        let _ = socket.shutdown().await;
    });
    format!("http://{addr}/model.bin")
}

/// Verify that sha256 verification works end-to-end with known data.
#[test]
fn test_artifact_verification_roundtrip() {
    let payload = b"test model artifact content for integration test";
    let expected = hex::encode(Sha256::digest(payload));

    assert!(verify_artifact("int-test-model", payload, &expected).is_ok());
    assert!(verify_artifact("int-test-model", payload, &"0".repeat(64)).is_err());
}

/// The downloaded file is streamed to disk, verified, and moved into the
/// cache under its digest — the path a backend then loads.
#[tokio::test]
async fn test_download_verify_and_cache() {
    static BODY: &[u8] = b"pretend these are model weights";
    let sha = hex::encode(Sha256::digest(BODY));
    let dir = temp_dir("dl");
    let url = serve_once(BODY).await;

    let fetched = download_artifact("m", &url, &sha, &dir).await.unwrap();
    assert!(matches!(fetched, Fetched::Downloaded { .. }));
    let path = verify_fetched("m", fetched, &sha, &dir).await.unwrap();

    assert_eq!(path, cached_artifact_path(&dir, &sha));
    assert_eq!(tokio::fs::read(&path).await.unwrap(), BODY);
    let _ = tokio::fs::remove_dir_all(&dir).await;
}

/// A server that sends different bytes than the deployment promised: nothing
/// is kept, and no file appears under the expected digest.
#[tokio::test]
async fn test_tampered_download_is_rejected() {
    static BODY: &[u8] = b"weights that were swapped in transit";
    let promised = hex::encode(Sha256::digest(b"the weights the operator published"));
    let dir = temp_dir("tamper");
    let url = serve_once(BODY).await;

    let fetched = download_artifact("m", &url, &promised, &dir).await.unwrap();
    assert!(verify_fetched("m", fetched, &promised, &dir).await.is_err());
    assert!(!cached_artifact_path(&dir, &promised).exists());

    let leftovers: Vec<_> = std::fs::read_dir(&dir).unwrap().collect();
    assert!(leftovers.is_empty(), "partial download left behind: {leftovers:?}");
    let _ = tokio::fs::remove_dir_all(&dir).await;
}

/// A malformed digest in the deployment is refused before any download.
#[tokio::test]
async fn test_malformed_digest_is_refused() {
    let dir = temp_dir("baddigest");
    let err = download_artifact("m", "http://unused.invalid/", "abc123", &dir).await;
    assert!(err.is_err());
    let _ = tokio::fs::remove_dir_all(&dir).await;
}

/// Verify stub backend loads and reports synthetic telemetry labelled as such.
#[tokio::test]
async fn test_stub_backend_load_and_sample() {
    let handle = load_model("integration-test-model", Path::new("/unused"), RuntimeBackend::Stub)
        .await
        .expect("stub load should succeed");
    assert_eq!(handle.model_id, "integration-test-model");
    assert_eq!(handle.backend.name(), "stub");

    let telem = sample_telemetry(&handle).await;
    assert!(telem.tps > 0.0, "tps should be positive");
    assert_eq!(telem.status, "running");
    assert_eq!(telem.source, SOURCE_STUB);
}

/// A LiteRT-LM server that is not there: loading fails instead of pretending.
#[tokio::test]
async fn test_litert_unreachable_fails_load() {
    let dir = temp_dir("litert");
    tokio::fs::create_dir_all(&dir).await.unwrap();
    let model = dir.join("model.litertlm");
    tokio::fs::write(&model, b"x").await.unwrap();

    let backend = RuntimeBackend::LiteRT {
        // Port 9 (discard) on localhost: nothing listens there in CI.
        url: "http://127.0.0.1:9".into(),
        accelerator: "cpu".into(),
    };
    assert!(load_model("m", &model, backend).await.is_err());
    let _ = tokio::fs::remove_dir_all(&dir).await;
}

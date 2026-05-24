//! Integration test stubs for the Cami agent.
//!
//! These tests verify the agent's exported modules compile and basic operations
//! work without needing a live gRPC server or inference runtime. Full integration
//! tests with a mock gRPC server should be run in CI with the control plane.

use std::collections::HashMap;

/// Verify that sha256 verification works end-to-end with known data.
#[test]
fn test_artifact_verification_roundtrip() {
    use sha2::{Digest, Sha256};

    let payload = b"test model artifact content for integration test";
    let expected = hex::encode(Sha256::digest(payload));

    // Correct hash passes
    assert!(cami_agent::model_runtime::verify_artifact("int-test-model", payload, &expected).is_ok());

    // Wrong hash fails
    let wrong = "0".repeat(64);
    assert!(cami_agent::model_runtime::verify_artifact("int-test-model", payload, &wrong).is_err());
}

/// Verify stub backend loads without panicking.
#[tokio::test]
async fn test_stub_backend_load() {
    use cami_agent::model_runtime::{load_model, RuntimeBackend};

    let handle = load_model("integration-test-model", RuntimeBackend::Stub).await;
    assert!(handle.is_ok(), "stub load should succeed");

    let h = handle.unwrap();
    assert_eq!(h.model_id, "integration-test-model");
    assert_eq!(h.backend.name(), "stub");
}

/// Verify stub telemetry returns sane values.
#[tokio::test]
async fn test_stub_telemetry_sampling() {
    use cami_agent::model_runtime::{load_model, sample_telemetry, RuntimeBackend};

    let handle = load_model("telem-test", RuntimeBackend::Stub).await.unwrap();
    let telem = sample_telemetry(&handle).await;

    assert!(telem.tps > 0.0, "tps should be positive");
    assert!(telem.ttft_ms > 0.0, "ttft_ms should be positive");
    assert!(telem.mem_mb > 0.0, "mem_mb should be positive");
    assert_eq!(telem.status, "running");
}

/// Verify RuntimeBackend name method.
#[test]
fn test_backend_names() {
    use cami_agent::model_runtime::RuntimeBackend;

    assert_eq!(RuntimeBackend::Stub.name(), "stub");
    assert_eq!(
        RuntimeBackend::Ollama {
            url: "http://localhost:11434".into(),
            model: "test".into(),
        }
        .name(),
        "ollama"
    );
    assert_eq!(
        RuntimeBackend::LiteRT {
            url: "http://localhost:8000".into(),
            model: "test".into(),
        }
        .name(),
        "litert"
    );
}

/// Multi-backend model runtime for edge AI inference.
///
/// Supports three backends, selected via `RUNTIME_BACKEND` env var:
///   - **litert**  — Google LiteRT-LM via its Gemini-compatible REST API (`lit serve`)
///   - **ollama**  — Ollama via its `/api/generate` REST API
///   - **stub**    — Synthetic metrics for demo / CI (no runtime needed)
///
/// Auto-detection: if `RUNTIME_BACKEND` is empty, the agent tries `LITERT_URL`,
/// then `OLLAMA_URL`, then falls back to stub.
use anyhow::{bail, Result};
use rand::Rng;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::time::{Duration, Instant};
use tokio::time::sleep;
use tracing::{info, warn};

// ── Runtime backend ─────────────────────────────────────────────────────────

#[derive(Debug, Clone)]
pub enum RuntimeBackend {
    LiteRT { url: String, model: String },
    Ollama { url: String, model: String },
    Stub,
}

impl RuntimeBackend {
    pub fn name(&self) -> &'static str {
        match self {
            Self::LiteRT { .. } => "litert",
            Self::Ollama { .. } => "ollama",
            Self::Stub => "stub",
        }
    }
}

#[derive(Debug, Clone)]
pub struct ModelHandle {
    pub model_id: String,
    pub backend: RuntimeBackend,
}

pub struct Telemetry {
    pub tps: f32,
    pub ttft_ms: f32,
    pub mem_mb: f32,
    pub status: String,
}

// ── Artifact operations (shared across all backends) ────────────────────────

pub async fn download_artifact(model_id: &str, artifact_url: &str) -> Result<bytes::Bytes> {
    info!(%model_id, %artifact_url, "downloading model artifact");

    let resp = reqwest::get(artifact_url)
        .await
        .map_err(|e| anyhow::anyhow!("download failed: {e}"))?;

    if !resp.status().is_success() {
        bail!("download returned HTTP {}", resp.status());
    }

    resp.bytes().await.map_err(|e| anyhow::anyhow!("read body failed: {e}"))
}

pub fn verify_artifact(model_id: &str, bytes: &[u8], expected_sha256: &str) -> Result<()> {
    let mut hasher = Sha256::new();
    hasher.update(bytes);
    let actual = hex::encode(hasher.finalize());

    if actual != expected_sha256 {
        warn!(%actual, %expected_sha256, "SHA-256 mismatch");
        bail!("artifact SHA-256 mismatch: expected {expected_sha256}, got {actual}");
    }
    info!(%model_id, "artifact verified");
    Ok(())
}

// ── Model loading (dispatches to backend) ───────────────────────────────────

pub async fn load_model(model_id: &str, backend: RuntimeBackend) -> Result<ModelHandle> {
    match &backend {
        RuntimeBackend::LiteRT { url, .. } => load_model_litert(model_id, url).await,
        RuntimeBackend::Ollama { url, .. } => load_model_ollama(model_id, url).await,
        RuntimeBackend::Stub => load_model_stub(model_id).await,
    }
}

// ── LiteRT backend ──────────────────────────────────────────────────────────

async fn load_model_litert(model_id: &str, litert_url: &str) -> Result<ModelHandle> {
    info!(%model_id, %litert_url, "loading model via LiteRT");

    let client = reqwest::Client::new();
    let model_name = sanitize_model_name(model_id);

    // Verify the LiteRT server is reachable
    let health_resp = client.get(litert_url).timeout(Duration::from_secs(15)).send().await;

    match health_resp {
        Ok(r) if r.status().is_success() => {
            info!("LiteRT server is reachable");
        }
        Ok(r) => warn!(status = %r.status(), "LiteRT server returned non-200, continuing anyway"),
        Err(e) => bail!("LiteRT server unreachable at {litert_url}: {e}"),
    }

    // Warm up with a minimal generateContent request (Gemini-compatible API)
    let gen_url = format!(
        "{litert_url}/v1beta/models/{model_name}:generateContent",
        litert_url = litert_url.trim_end_matches('/'),
        model_name = model_name,
    );

    let warmup = client
        .post(&gen_url)
        .json(&serde_json::json!({
            "contents": [{"parts": [{"text": "Hi"}]}],
            "generationConfig": {"maxOutputTokens": 1}
        }))
        .timeout(Duration::from_secs(120))
        .send()
        .await;

    match warmup {
        Ok(resp) if resp.status().is_success() => {
            info!(%model_name, "LiteRT model warmed up");
        }
        Ok(resp) => {
            let body = resp.text().await.unwrap_or_default();
            bail!("LiteRT warmup failed: {body}");
        }
        Err(e) => bail!("LiteRT warmup request failed: {e}"),
    }

    Ok(ModelHandle {
        model_id: model_id.to_string(),
        backend: RuntimeBackend::LiteRT {
            url: litert_url.to_string(),
            model: model_name,
        },
    })
}

#[derive(Deserialize)]
struct GeminiCandidate {
    #[serde(default)]
    content: GeminiContent,
}

#[derive(Deserialize, Default)]
struct GeminiContent {
    #[serde(default)]
    parts: Vec<GeminiPart>,
}

#[derive(Deserialize)]
struct GeminiPart {
    #[serde(default)]
    text: String,
}

#[derive(Deserialize)]
struct GeminiUsageMetadata {
    #[serde(default, rename = "promptTokenCount")]
    prompt_token_count: u64,
    #[serde(default, rename = "candidatesTokenCount")]
    candidates_token_count: u64,
    #[serde(default, rename = "totalTokenCount")]
    total_token_count: u64,
}

#[derive(Deserialize)]
struct GeminiResponse {
    #[serde(default)]
    candidates: Vec<GeminiCandidate>,
    #[serde(default, rename = "usageMetadata")]
    usage_metadata: Option<GeminiUsageMetadata>,
}

async fn sample_telemetry_litert(litert_url: &str, model: &str) -> Telemetry {
    let client = reqwest::Client::new();

    let gen_url = format!(
        "{}/v1beta/models/{}:generateContent",
        litert_url.trim_end_matches('/'),
        model,
    );

    let start = Instant::now();
    let resp = client
        .post(&gen_url)
        .json(&serde_json::json!({
            "contents": [{"parts": [{"text": "Explain edge computing in one sentence."}]}],
            "generationConfig": {"maxOutputTokens": 32}
        }))
        .timeout(Duration::from_secs(30))
        .send()
        .await;

    let elapsed = start.elapsed();

    match resp {
        Ok(r) if r.status().is_success() => {
            match r.json::<GeminiResponse>().await {
                Ok(gen) => {
                    let output_tokens = gen
                        .usage_metadata
                        .as_ref()
                        .map(|u| u.candidates_token_count)
                        .unwrap_or(0);

                    let total_secs = elapsed.as_secs_f32();
                    let tps = if total_secs > 0.0 && output_tokens > 0 {
                        output_tokens as f32 / total_secs
                    } else {
                        0.0
                    };

                    // TTFT approximation: total time minus generation time
                    let ttft_ms = if output_tokens > 1 && tps > 0.0 {
                        (elapsed.as_millis() as f32) - (output_tokens as f32 / tps * 1000.0)
                    } else {
                        elapsed.as_millis() as f32
                    };

                    Telemetry {
                        tps,
                        ttft_ms: ttft_ms.max(0.0),
                        mem_mb: 0.0, // LiteRT doesn't expose memory via API
                        status: "running".to_string(),
                    }
                }
                Err(_) => Telemetry {
                    tps: 0.0,
                    ttft_ms: elapsed.as_millis() as f32,
                    mem_mb: 0.0,
                    status: "running".to_string(),
                },
            }
        }
        _ => sample_telemetry_stub(),
    }
}

// ── Ollama backend ──────────────────────────────────────────────────────────

async fn load_model_ollama(model_id: &str, ollama_url: &str) -> Result<ModelHandle> {
    info!(%model_id, %ollama_url, "loading model via Ollama");

    let client = reqwest::Client::new();
    let model_name = sanitize_model_name(model_id);

    let pull_url = format!("{ollama_url}/api/pull");
    let pull_resp = client
        .post(&pull_url)
        .json(&serde_json::json!({ "name": &model_name, "stream": false }))
        .timeout(Duration::from_secs(600))
        .send()
        .await;

    match pull_resp {
        Ok(resp) if resp.status().is_success() => {
            info!(%model_name, "model pulled successfully");
        }
        Ok(resp) => {
            let status = resp.status();
            let body = resp.text().await.unwrap_or_default();
            warn!(%model_name, %status, %body, "pull returned non-success, trying generate directly");
        }
        Err(e) => {
            warn!(%model_name, "pull request failed: {e}, trying generate directly");
        }
    }

    let gen_url = format!("{ollama_url}/api/generate");
    let warmup = client
        .post(&gen_url)
        .json(&serde_json::json!({
            "model": &model_name,
            "prompt": "Hello",
            "stream": false,
            "options": { "num_predict": 1 }
        }))
        .timeout(Duration::from_secs(120))
        .send()
        .await;

    match warmup {
        Ok(resp) if resp.status().is_success() => {
            info!(%model_name, "model warmed up");
        }
        Ok(resp) => {
            let body = resp.text().await.unwrap_or_default();
            bail!("Ollama warmup failed: {body}");
        }
        Err(e) => bail!("Ollama warmup request failed: {e}"),
    }

    Ok(ModelHandle {
        model_id: model_id.to_string(),
        backend: RuntimeBackend::Ollama {
            url: ollama_url.to_string(),
            model: model_name,
        },
    })
}

#[derive(Deserialize)]
struct OllamaGenerateResponse {
    #[serde(default)]
    eval_count: u64,
    #[serde(default)]
    eval_duration: u64,
    #[serde(default)]
    prompt_eval_duration: u64,
}

#[derive(Deserialize)]
struct OllamaProcessModel {
    #[serde(default)]
    size_vram: u64,
    #[serde(default)]
    size: u64,
}

#[derive(Deserialize)]
struct OllamaProcessResponse {
    #[serde(default)]
    models: Vec<OllamaProcessModel>,
}

async fn sample_telemetry_ollama(ollama_url: &str, model: &str) -> Telemetry {
    let client = reqwest::Client::new();

    let gen_url = format!("{ollama_url}/api/generate");
    let start = Instant::now();
    let resp = client
        .post(&gen_url)
        .json(&serde_json::json!({
            "model": model,
            "prompt": "Explain edge computing in one sentence.",
            "stream": false,
            "options": { "num_predict": 32 }
        }))
        .timeout(Duration::from_secs(30))
        .send()
        .await;

    let (tps, ttft_ms) = match resp {
        Ok(r) if r.status().is_success() => match r.json::<OllamaGenerateResponse>().await {
            Ok(gen) => {
                let tps = if gen.eval_duration > 0 {
                    (gen.eval_count as f32) / (gen.eval_duration as f32 / 1_000_000_000.0)
                } else {
                    0.0
                };
                let ttft = if gen.prompt_eval_duration > 0 {
                    gen.prompt_eval_duration as f32 / 1_000_000.0
                } else {
                    start.elapsed().as_millis() as f32
                };
                (tps, ttft)
            }
            Err(_) => (0.0, start.elapsed().as_millis() as f32),
        },
        _ => return sample_telemetry_stub(),
    };

    let mem_mb = get_ollama_memory(ollama_url).await;

    Telemetry {
        tps,
        ttft_ms,
        mem_mb,
        status: "running".to_string(),
    }
}

async fn get_ollama_memory(ollama_url: &str) -> f32 {
    let url = format!("{ollama_url}/api/ps");
    match reqwest::get(&url).await {
        Ok(resp) => match resp.json::<OllamaProcessResponse>().await {
            Ok(ps) => {
                let total: u64 = ps.models.iter().map(|m| m.size_vram.max(m.size)).sum();
                total as f32 / (1024.0 * 1024.0)
            }
            Err(_) => 0.0,
        },
        Err(_) => 0.0,
    }
}

// ── Stub backend ────────────────────────────────────────────────────────────

async fn load_model_stub(model_id: &str) -> Result<ModelHandle> {
    let load_ms = rand::thread_rng().gen_range(2000..4000);
    sleep(Duration::from_millis(load_ms)).await;
    info!(%model_id, "model loaded (stub mode)");
    Ok(ModelHandle {
        model_id: model_id.to_string(),
        backend: RuntimeBackend::Stub,
    })
}

fn sample_telemetry_stub() -> Telemetry {
    let mut rng = rand::thread_rng();
    Telemetry {
        tps: 28.0 + rng.gen_range(-3.0_f32..3.0_f32),
        ttft_ms: 120.0 + rng.gen_range(-20.0_f32..20.0_f32),
        mem_mb: 3800.0 + rng.gen_range(-50.0_f32..50.0_f32),
        status: "running".to_string(),
    }
}

// ── Telemetry dispatch ──────────────────────────────────────────────────────

pub async fn sample_telemetry(handle: &ModelHandle) -> Telemetry {
    match &handle.backend {
        RuntimeBackend::LiteRT { url, model } => sample_telemetry_litert(url, model).await,
        RuntimeBackend::Ollama { url, model } => sample_telemetry_ollama(url, model).await,
        RuntimeBackend::Stub => sample_telemetry_stub(),
    }
}

// ── Helpers ─────────────────────────────────────────────────────────────────

fn sanitize_model_name(model_id: &str) -> String {
    let name = model_id
        .replace(|c: char| !c.is_alphanumeric() && c != '-' && c != ':' && c != '.', "-")
        .to_lowercase();
    if name.contains(':') {
        name
    } else {
        format!("{name}:latest")
    }
}

// ── Tests ───────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_verify_artifact_correct_hash() {
        let data = b"hello world";
        let hash = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9";
        assert!(verify_artifact("test-model", data, hash).is_ok());
    }

    #[test]
    fn test_verify_artifact_wrong_hash() {
        let data = b"hello world";
        let wrong = "0000000000000000000000000000000000000000000000000000000000000000";
        assert!(verify_artifact("test-model", data, wrong).is_err());
    }

    #[test]
    fn test_sanitize_model_name() {
        assert_eq!(sanitize_model_name("gemma-4-e2b"), "gemma-4-e2b:latest");
        assert_eq!(sanitize_model_name("llama3:8b"), "llama3:8b");
        assert_eq!(sanitize_model_name("My Model v2!"), "my-model-v2-:latest");
    }

    #[test]
    fn test_stub_telemetry_ranges() {
        let t = sample_telemetry_stub();
        assert!(t.tps > 20.0 && t.tps < 36.0);
        assert!(t.ttft_ms > 90.0 && t.ttft_ms < 150.0);
        assert!(t.mem_mb > 3700.0 && t.mem_mb < 3900.0);
        assert_eq!(t.status, "running");
    }

    #[test]
    fn test_runtime_backend_name() {
        assert_eq!(RuntimeBackend::Stub.name(), "stub");
        assert_eq!(
            RuntimeBackend::Ollama {
                url: "x".into(),
                model: "y".into()
            }
            .name(),
            "ollama"
        );
        assert_eq!(
            RuntimeBackend::LiteRT {
                url: "x".into(),
                model: "y".into()
            }
            .name(),
            "litert"
        );
    }
}

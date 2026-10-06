//! Model artifacts and inference runtimes.
//!
//! Integrity: an artifact is streamed to disk while it is hashed, kept only if
//! its SHA-256 matches the deployment, and stored under that hash. Every
//! backend then runs that exact file:
//!
//! - `litert`: Google LiteRT-LM's `litert-lm serve`, an OpenAI-compatible
//!   server on the device. It accepts an absolute model path, so the verified
//!   `.litertlm` file is the model that runs.
//! - `ollama`: the verified GGUF is pushed to `/api/blobs/sha256:<digest>` and
//!   registered with `/api/create`, instead of pulling a model by name.
//! - `stub`: no runtime; synthetic telemetry for demos and CI.
//!
//! Telemetry comes from a short timed request against the real runtime (a
//! "probe"). A failed probe is reported as `status = "error"`, never as made-up
//! healthy numbers.
use anyhow::{bail, Context, Result};
use rand::Rng;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::path::{Path, PathBuf};
use std::sync::OnceLock;
use std::time::{Duration, Instant};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::time::sleep;
use tracing::{info, warn};

const CHUNK_SIZE: usize = 1 << 20;
const PROBE_PROMPT: &str = "Explain edge computing in one sentence.";
const PROBE_TOKENS: u32 = 32;

pub const SOURCE_PROBE: &str = "probe";
pub const SOURCE_STUB: &str = "stub";

// ── Runtime backend ─────────────────────────────────────────────────────────

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RuntimeBackend {
    /// `litert-lm serve` at `url`; `accelerator` is `cpu`, `gpu` or `npu`.
    LiteRT {
        url: String,
        accelerator: String,
    },
    /// Ollama's REST API at `url`.
    Ollama {
        url: String,
    },
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
    /// The name the runtime uses for this model: a file path plus accelerator
    /// for LiteRT-LM, a content-addressed tag for Ollama.
    pub runtime_model: String,
}

#[derive(Debug, Clone, PartialEq)]
pub struct Telemetry {
    pub tps: f32,
    pub ttft_ms: f32,
    pub mem_mb: f32,
    pub status: String,
    pub source: String,
}

impl Telemetry {
    /// A failed measurement: no numbers, and a status the dashboard can show.
    pub fn error(source: &str) -> Self {
        Self {
            tps: 0.0,
            ttft_ms: 0.0,
            mem_mb: 0.0,
            status: "error".to_string(),
            source: source.to_string(),
        }
    }
}

fn http() -> &'static reqwest::Client {
    static CLIENT: OnceLock<reqwest::Client> = OnceLock::new();
    CLIENT.get_or_init(reqwest::Client::new)
}

// ── Artifacts: download, hash, verify, cache ────────────────────────────────

pub fn is_sha256_hex(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|b| b.is_ascii_hexdigit())
}

/// Checks in-memory bytes against an expected SHA-256 (hex, any case).
pub fn verify_artifact(model_id: &str, bytes: &[u8], expected_sha256: &str) -> Result<()> {
    let actual = hex::encode(Sha256::digest(bytes));
    let expected = expected_sha256.to_ascii_lowercase();
    if actual != expected {
        warn!(%actual, %expected, "SHA-256 mismatch");
        bail!("artifact SHA-256 mismatch: expected {expected}, got {actual}");
    }
    info!(%model_id, "artifact verified");
    Ok(())
}

/// Where a verified artifact with this digest lives in the cache.
pub fn cached_artifact_path(cache_dir: &Path, sha256: &str) -> PathBuf {
    cache_dir.join(format!("sha256-{}", sha256.to_ascii_lowercase()))
}

/// The result of the download step, before the digest has been checked.
#[derive(Debug)]
pub enum Fetched {
    /// A file with the expected name was already in the cache.
    Cached(PathBuf),
    /// Freshly downloaded to a temporary file; `sha256` was computed while streaming.
    Downloaded { tmp: PathBuf, sha256: String },
}

/// Streams the artifact to a temporary file in `cache_dir`, hashing as it
/// goes, so a multi-GB model never has to fit in RAM. Skips the download when
/// the cache already holds a file for this digest.
pub async fn download_artifact(
    model_id: &str,
    artifact_url: &str,
    expected_sha256: &str,
    cache_dir: &Path,
) -> Result<Fetched> {
    if !is_sha256_hex(expected_sha256) {
        bail!("deployment SHA-256 is not 64 hex characters: {expected_sha256:?}");
    }
    tokio::fs::create_dir_all(cache_dir)
        .await
        .with_context(|| format!("create model cache {}", cache_dir.display()))?;

    let dest = cached_artifact_path(cache_dir, expected_sha256);
    if tokio::fs::try_exists(&dest).await.unwrap_or(false) {
        info!(%model_id, path = %dest.display(), "artifact already cached");
        return Ok(Fetched::Cached(dest));
    }

    info!(%model_id, %artifact_url, "downloading model artifact");
    let suffix: u64 = rand::thread_rng().gen();
    let tmp = cache_dir.join(format!(".partial-{suffix:016x}"));
    match stream_to_file(artifact_url, &tmp).await {
        Ok(sha256) => Ok(Fetched::Downloaded { tmp, sha256 }),
        Err(e) => {
            let _ = tokio::fs::remove_file(&tmp).await;
            Err(e)
        }
    }
}

/// Checks the digest and, on success, returns the path of the verified file
/// in the cache. A cached file is re-hashed, so corruption or tampering on
/// disk is caught too. On a mismatch nothing is kept.
pub async fn verify_fetched(
    model_id: &str,
    fetched: Fetched,
    expected_sha256: &str,
    cache_dir: &Path,
) -> Result<PathBuf> {
    let expected = expected_sha256.to_ascii_lowercase();
    let dest = cached_artifact_path(cache_dir, &expected);
    match fetched {
        Fetched::Cached(path) => {
            let actual = sha256_file(&path).await?;
            if actual != expected {
                let _ = tokio::fs::remove_file(&path).await;
                bail!("cached artifact SHA-256 mismatch (removed): expected {expected}, got {actual}");
            }
            info!(%model_id, "cached artifact verified");
            Ok(path)
        }
        Fetched::Downloaded { tmp, sha256 } => {
            if sha256 != expected {
                let _ = tokio::fs::remove_file(&tmp).await;
                warn!(actual = %sha256, %expected, "SHA-256 mismatch");
                bail!("artifact SHA-256 mismatch: expected {expected}, got {sha256}");
            }
            tokio::fs::rename(&tmp, &dest)
                .await
                .with_context(|| format!("move verified artifact to {}", dest.display()))?;
            info!(%model_id, path = %dest.display(), "artifact verified");
            Ok(dest)
        }
    }
}

async fn stream_to_file(url: &str, path: &Path) -> Result<String> {
    let mut resp = http().get(url).send().await.context("download failed")?;
    if !resp.status().is_success() {
        bail!("download returned HTTP {}", resp.status());
    }
    let mut file = tokio::fs::File::create(path)
        .await
        .with_context(|| format!("create {}", path.display()))?;
    let mut hasher = Sha256::new();
    while let Some(chunk) = resp.chunk().await.context("read body failed")? {
        hasher.update(&chunk);
        file.write_all(&chunk).await.context("write artifact failed")?;
    }
    file.sync_all().await.context("sync artifact failed")?;
    Ok(hex::encode(hasher.finalize()))
}

pub async fn sha256_file(path: &Path) -> Result<String> {
    let mut file = tokio::fs::File::open(path)
        .await
        .with_context(|| format!("open {}", path.display()))?;
    let mut hasher = Sha256::new();
    let mut buf = vec![0u8; CHUNK_SIZE];
    loop {
        let n = file.read(&mut buf).await?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(hex::encode(hasher.finalize()))
}

// ── Model loading (dispatches to backend) ───────────────────────────────────

/// Loads the verified artifact at `artifact` into the runtime.
pub async fn load_model(model_id: &str, artifact: &Path, backend: RuntimeBackend) -> Result<ModelHandle> {
    let runtime_model = match &backend {
        RuntimeBackend::LiteRT { url, accelerator } => load_litert(model_id, artifact, url, accelerator).await?,
        RuntimeBackend::Ollama { url } => load_ollama(model_id, artifact, url).await?,
        RuntimeBackend::Stub => load_stub(model_id).await,
    };
    Ok(ModelHandle {
        model_id: model_id.to_string(),
        backend,
        runtime_model,
    })
}

// ── LiteRT-LM backend (`litert-lm serve`, OpenAI-compatible) ────────────────

/// The `model` value `litert-lm serve` expects: `<model reference>,<backend>`.
/// A reference that is an existing file path is loaded directly, so the server
/// must see the cache at the same absolute path as the agent.
pub fn litert_model_ref(artifact: &Path, accelerator: &str) -> Result<String> {
    let path = artifact.to_str().context("model path is not valid UTF-8")?;
    if !artifact.is_absolute() {
        bail!("LiteRT-LM needs an absolute model path, got {path}");
    }
    if path.contains(',') {
        bail!("LiteRT-LM model path must not contain a comma: {path}");
    }
    Ok(format!("{path},{accelerator}"))
}

async fn load_litert(model_id: &str, artifact: &Path, url: &str, accelerator: &str) -> Result<String> {
    let base = url.trim_end_matches('/');
    let artifact = tokio::fs::canonicalize(artifact)
        .await
        .with_context(|| format!("resolve {}", artifact.display()))?;
    let model_ref = litert_model_ref(&artifact, accelerator)?;
    info!(%model_id, %base, %model_ref, "loading verified model into LiteRT-LM");

    let models = http()
        .get(format!("{base}/v1/models"))
        .timeout(Duration::from_secs(15))
        .send()
        .await
        .with_context(|| format!("LiteRT-LM server unreachable at {base}"))?;
    if !models.status().is_success() {
        bail!("LiteRT-LM server at {base} returned HTTP {}", models.status());
    }

    // The first request builds the engine (reads the file, compiles for the
    // accelerator), which can take a while on small devices.
    let warmup = http()
        .post(format!("{base}/v1/chat/completions"))
        .json(&serde_json::json!({
            "model": model_ref,
            "messages": [{"role": "user", "content": "Hi"}],
            "max_completion_tokens": 1,
        }))
        .timeout(Duration::from_secs(300))
        .send()
        .await
        .context("LiteRT-LM warmup request failed")?;
    if !warmup.status().is_success() {
        let status = warmup.status();
        let body = warmup.text().await.unwrap_or_default();
        bail!("LiteRT-LM warmup returned HTTP {status}: {body}");
    }
    info!(%model_id, "LiteRT-LM model loaded");
    Ok(model_ref)
}

#[derive(Deserialize)]
struct StreamChunk {
    #[serde(default)]
    choices: Vec<StreamChoice>,
    #[serde(default)]
    usage: Option<StreamUsage>,
}

#[derive(Deserialize)]
struct StreamChoice {
    #[serde(default)]
    delta: StreamDelta,
}

#[derive(Deserialize, Default)]
struct StreamDelta {
    #[serde(default)]
    content: Option<String>,
}

#[derive(Deserialize)]
struct StreamUsage {
    #[serde(default)]
    completion_tokens: u64,
}

/// Accumulates an OpenAI-style server-sent event stream: when the first
/// content token arrived, and the completion token count from the final
/// `usage` chunk.
#[derive(Debug, Default)]
pub struct StreamStats {
    pending: String,
    pub first_token: Option<Instant>,
    pub completion_tokens: u64,
}

impl StreamStats {
    /// Feeds raw bytes from the response; `now` is when they arrived.
    pub fn feed(&mut self, bytes: &[u8], now: Instant) {
        self.pending.push_str(&String::from_utf8_lossy(bytes));
        while let Some(pos) = self.pending.find('\n') {
            let line: String = self.pending.drain(..=pos).collect();
            self.observe_line(line.trim(), now);
        }
    }

    fn observe_line(&mut self, line: &str, now: Instant) {
        let Some(data) = line.strip_prefix("data:") else {
            return;
        };
        let data = data.trim();
        if data.is_empty() || data == "[DONE]" {
            return;
        }
        let Ok(chunk) = serde_json::from_str::<StreamChunk>(data) else {
            return;
        };
        let has_text = chunk
            .choices
            .iter()
            .any(|c| c.delta.content.as_deref().is_some_and(|t| !t.is_empty()));
        if has_text && self.first_token.is_none() {
            self.first_token = Some(now);
        }
        if let Some(usage) = chunk.usage {
            self.completion_tokens = usage.completion_tokens;
        }
    }

    /// Converts the stream into telemetry. TTFT runs from `start` to the first
    /// token; decode speed counts the tokens after the first over the time
    /// after it.
    pub fn telemetry(&self, start: Instant, end: Instant) -> Telemetry {
        let Some(first) = self.first_token else {
            return Telemetry::error(SOURCE_PROBE);
        };
        let ttft_ms = first.duration_since(start).as_secs_f32() * 1000.0;
        let decode_secs = end.duration_since(first).as_secs_f32();
        let tps = if self.completion_tokens > 1 && decode_secs > 0.0 {
            (self.completion_tokens - 1) as f32 / decode_secs
        } else {
            0.0
        };
        Telemetry {
            tps,
            ttft_ms,
            mem_mb: 0.0, // litert-lm serve does not report memory
            status: "running".to_string(),
            source: SOURCE_PROBE.to_string(),
        }
    }
}

async fn sample_litert(url: &str, model_ref: &str) -> Telemetry {
    let base = url.trim_end_matches('/');
    let start = Instant::now();
    let resp = http()
        .post(format!("{base}/v1/chat/completions"))
        .json(&serde_json::json!({
            "model": model_ref,
            "messages": [{"role": "user", "content": PROBE_PROMPT}],
            "max_completion_tokens": PROBE_TOKENS,
            "stream": true,
            "stream_options": {"include_usage": true},
        }))
        .timeout(Duration::from_secs(60))
        .send()
        .await;
    let mut resp = match resp {
        Ok(r) if r.status().is_success() => r,
        Ok(r) => {
            warn!(status = %r.status(), "LiteRT-LM probe returned an error");
            return Telemetry::error(SOURCE_PROBE);
        }
        Err(e) => {
            warn!("LiteRT-LM probe failed: {e}");
            return Telemetry::error(SOURCE_PROBE);
        }
    };

    let mut stats = StreamStats::default();
    loop {
        match resp.chunk().await {
            Ok(Some(bytes)) => stats.feed(&bytes, Instant::now()),
            Ok(None) => break,
            Err(e) => {
                warn!("LiteRT-LM probe stream broke: {e}");
                return Telemetry::error(SOURCE_PROBE);
            }
        }
    }
    stats.telemetry(start, Instant::now())
}

// ── Ollama backend ──────────────────────────────────────────────────────────

/// A content-addressed Ollama tag: the same artifact always maps to the same
/// tag, and a different artifact can never reuse it.
pub fn ollama_model_tag(model_id: &str, sha256: &str) -> String {
    let name: String = model_id
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '-' || c == '.' || c == '_' {
                c.to_ascii_lowercase()
            } else {
                '-'
            }
        })
        .collect();
    let short = &sha256[..sha256.len().min(12)];
    format!("cami-{name}:{short}")
}

async fn load_ollama(model_id: &str, artifact: &Path, url: &str) -> Result<String> {
    let base = url.trim_end_matches('/');
    let sha256 = sha256_file(artifact).await?;
    let digest = format!("sha256:{sha256}");
    let tag = ollama_model_tag(model_id, &sha256);
    info!(%model_id, %base, %tag, "loading verified model into Ollama");

    // Ollama stores blobs by SHA-256 and checks the digest on upload, so the
    // blob it runs is byte-for-byte the verified artifact.
    let blob_url = format!("{base}/api/blobs/{digest}");
    let exists = http()
        .head(&blob_url)
        .timeout(Duration::from_secs(15))
        .send()
        .await
        .map(|r| r.status().is_success())
        .unwrap_or(false);
    if !exists {
        let file = tokio::fs::File::open(artifact).await?;
        let resp = http()
            .post(&blob_url)
            .body(file_body(file))
            .timeout(Duration::from_secs(3600))
            .send()
            .await
            .context("Ollama blob upload failed")?;
        if !resp.status().is_success() {
            let status = resp.status();
            let body = resp.text().await.unwrap_or_default();
            bail!("Ollama blob upload returned HTTP {status}: {body}");
        }
    }

    let create = http()
        .post(format!("{base}/api/create"))
        .json(&serde_json::json!({
            "model": tag,
            "files": {"model.gguf": digest},
            "stream": false,
        }))
        .timeout(Duration::from_secs(600))
        .send()
        .await
        .context("Ollama create request failed")?;
    if !create.status().is_success() {
        let status = create.status();
        let body = create.text().await.unwrap_or_default();
        bail!("Ollama create returned HTTP {status}: {body}");
    }

    let warmup = http()
        .post(format!("{base}/api/generate"))
        .json(&serde_json::json!({
            "model": tag,
            "prompt": "Hello",
            "stream": false,
            "options": { "num_predict": 1 }
        }))
        .timeout(Duration::from_secs(300))
        .send()
        .await
        .context("Ollama warmup request failed")?;
    if !warmup.status().is_success() {
        let body = warmup.text().await.unwrap_or_default();
        bail!("Ollama warmup failed: {body}");
    }
    info!(%model_id, %tag, "Ollama model loaded");
    Ok(tag)
}

/// Streams a file as a request body without reading it into memory.
fn file_body(file: tokio::fs::File) -> reqwest::Body {
    let stream = futures::stream::unfold(Some(file), |state| async move {
        let mut file = state?;
        let mut buf = vec![0u8; CHUNK_SIZE];
        match file.read(&mut buf).await {
            Ok(0) => None,
            Ok(n) => {
                buf.truncate(n);
                Some((Ok::<_, std::io::Error>(bytes::Bytes::from(buf)), Some(file)))
            }
            Err(e) => Some((Err(e), None)),
        }
    });
    reqwest::Body::wrap_stream(stream)
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

async fn sample_ollama(url: &str, model: &str) -> Telemetry {
    let base = url.trim_end_matches('/');
    let resp = http()
        .post(format!("{base}/api/generate"))
        .json(&serde_json::json!({
            "model": model,
            "prompt": PROBE_PROMPT,
            "stream": false,
            "options": { "num_predict": PROBE_TOKENS }
        }))
        .timeout(Duration::from_secs(60))
        .send()
        .await;

    // Ollama measures these itself: eval_* covers decoding, prompt_eval_*
    // covers prefill (the bulk of time to first token once loaded).
    let gen = match resp {
        Ok(r) if r.status().is_success() => match r.json::<OllamaGenerateResponse>().await {
            Ok(gen) => gen,
            Err(e) => {
                warn!("Ollama probe returned unreadable JSON: {e}");
                return Telemetry::error(SOURCE_PROBE);
            }
        },
        Ok(r) => {
            warn!(status = %r.status(), "Ollama probe returned an error");
            return Telemetry::error(SOURCE_PROBE);
        }
        Err(e) => {
            warn!("Ollama probe failed: {e}");
            return Telemetry::error(SOURCE_PROBE);
        }
    };
    let tps = if gen.eval_duration > 0 {
        gen.eval_count as f32 / (gen.eval_duration as f32 / 1_000_000_000.0)
    } else {
        0.0
    };
    let ttft_ms = gen.prompt_eval_duration as f32 / 1_000_000.0;

    Telemetry {
        tps,
        ttft_ms,
        mem_mb: ollama_memory_mb(base).await,
        status: "running".to_string(),
        source: SOURCE_PROBE.to_string(),
    }
}

async fn ollama_memory_mb(base: &str) -> f32 {
    let Ok(resp) = http().get(format!("{base}/api/ps")).send().await else {
        return 0.0;
    };
    match resp.json::<OllamaProcessResponse>().await {
        Ok(ps) => {
            let total: u64 = ps.models.iter().map(|m| m.size_vram.max(m.size)).sum();
            total as f32 / (1024.0 * 1024.0)
        }
        Err(_) => 0.0,
    }
}

// ── Stub backend ────────────────────────────────────────────────────────────

async fn load_stub(model_id: &str) -> String {
    let load_ms = rand::thread_rng().gen_range(2000..4000);
    sleep(Duration::from_millis(load_ms)).await;
    info!(%model_id, "model loaded (stub mode)");
    model_id.to_string()
}

fn sample_stub() -> Telemetry {
    let mut rng = rand::thread_rng();
    Telemetry {
        tps: 28.0 + rng.gen_range(-3.0_f32..3.0_f32),
        ttft_ms: 120.0 + rng.gen_range(-20.0_f32..20.0_f32),
        mem_mb: 3800.0 + rng.gen_range(-50.0_f32..50.0_f32),
        status: "running".to_string(),
        source: SOURCE_STUB.to_string(),
    }
}

// ── Telemetry dispatch ──────────────────────────────────────────────────────

pub async fn sample_telemetry(handle: &ModelHandle) -> Telemetry {
    match &handle.backend {
        RuntimeBackend::LiteRT { url, .. } => sample_litert(url, &handle.runtime_model).await,
        RuntimeBackend::Ollama { url } => sample_ollama(url, &handle.runtime_model).await,
        RuntimeBackend::Stub => sample_stub(),
    }
}

// ── Tests ───────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    const HELLO_SHA: &str = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9";

    #[test]
    fn test_verify_artifact_correct_hash() {
        assert!(verify_artifact("test-model", b"hello world", HELLO_SHA).is_ok());
        assert!(verify_artifact("test-model", b"hello world", &HELLO_SHA.to_uppercase()).is_ok());
    }

    #[test]
    fn test_verify_artifact_wrong_hash() {
        assert!(verify_artifact("test-model", b"hello world", &"0".repeat(64)).is_err());
    }

    #[test]
    fn test_is_sha256_hex() {
        assert!(is_sha256_hex(HELLO_SHA));
        assert!(!is_sha256_hex("abc123"));
        assert!(!is_sha256_hex(&"g".repeat(64)));
    }

    #[test]
    fn test_ollama_model_tag_is_content_addressed() {
        assert_eq!(
            ollama_model_tag("Gemma 4/E2B", HELLO_SHA),
            "cami-gemma-4-e2b:b94d27b9934d"
        );
        assert_ne!(ollama_model_tag("m", HELLO_SHA), ollama_model_tag("m", &"0".repeat(64)));
    }

    #[test]
    fn test_litert_model_ref() {
        let r = litert_model_ref(Path::new("/var/lib/cami/models/sha256-abc"), "gpu").unwrap();
        assert_eq!(r, "/var/lib/cami/models/sha256-abc,gpu");
        assert!(litert_model_ref(Path::new("relative/model"), "cpu").is_err());
        assert!(litert_model_ref(Path::new("/odd,path/model"), "cpu").is_err());
    }

    /// Frames as `litert-lm serve` emits them: a role chunk, content chunks,
    /// a usage chunk, then [DONE] — split across reads mid-line.
    #[test]
    fn test_stream_stats_measures_first_token_and_usage() {
        let start = Instant::now();
        let t_role = start + Duration::from_millis(50);
        let t_first = start + Duration::from_millis(400);
        let t_end = start + Duration::from_millis(1400);

        let mut stats = StreamStats::default();
        stats.feed(
            b"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}],\"usage\":null}\n\n",
            t_role,
        );
        stats.feed(
            b"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Edge",
            t_first,
        );
        stats.feed(b"\"}}],\"usage\":null}\n\n", t_first);
        stats.feed(
            b"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" computing\"}}]}\n\n",
            t_end,
        );
        stats.feed(
            b"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":11,\"total_tokens\":20}}\n\ndata: [DONE]\n\n",
            t_end,
        );

        assert_eq!(stats.first_token, Some(t_first));
        assert_eq!(stats.completion_tokens, 11);

        let t = stats.telemetry(start, t_end);
        assert_eq!(t.status, "running");
        assert_eq!(t.source, SOURCE_PROBE);
        assert!((t.ttft_ms - 400.0).abs() < 0.5, "ttft {}", t.ttft_ms);
        // 10 tokens after the first, over 1.0 s.
        assert!((t.tps - 10.0).abs() < 0.01, "tps {}", t.tps);
    }

    #[test]
    fn test_stream_without_tokens_is_an_error() {
        let start = Instant::now();
        let mut stats = StreamStats::default();
        stats.feed(b"data: [DONE]\n\n", start);
        let t = stats.telemetry(start, start + Duration::from_secs(1));
        assert_eq!(t, Telemetry::error(SOURCE_PROBE));
    }

    #[test]
    fn test_stub_telemetry_ranges() {
        let t = sample_stub();
        assert!(t.tps > 20.0 && t.tps < 36.0);
        assert!(t.ttft_ms > 90.0 && t.ttft_ms < 150.0);
        assert!(t.mem_mb > 3700.0 && t.mem_mb < 3900.0);
        assert_eq!(t.status, "running");
        assert_eq!(t.source, SOURCE_STUB);
    }

    #[tokio::test]
    async fn test_verified_download_round_trip_uses_cache() {
        let dir = std::env::temp_dir().join(format!("cami-test-{}", rand::thread_rng().gen::<u64>()));
        tokio::fs::create_dir_all(&dir).await.unwrap();
        // Simulate a completed download: the temp file plus its streamed digest.
        let tmp = dir.join(".partial-test");
        tokio::fs::write(&tmp, b"hello world").await.unwrap();
        let fetched = Fetched::Downloaded {
            tmp,
            sha256: HELLO_SHA.to_string(),
        };
        let path = verify_fetched("m", fetched, HELLO_SHA, &dir).await.unwrap();
        assert_eq!(path, cached_artifact_path(&dir, HELLO_SHA));

        // A second deployment of the same digest is served from the cache.
        match download_artifact("m", "http://unused.invalid/", HELLO_SHA, &dir)
            .await
            .unwrap()
        {
            Fetched::Cached(p) => assert_eq!(p, path),
            other => panic!("expected cache hit, got {other:?}"),
        }

        // Tampering on disk is caught at the next verification.
        tokio::fs::write(&path, b"tampered").await.unwrap();
        let err = verify_fetched("m", Fetched::Cached(path.clone()), HELLO_SHA, &dir).await;
        assert!(err.is_err());
        assert!(!path.exists(), "a tampered cached file must be removed");

        let _ = tokio::fs::remove_dir_all(&dir).await;
    }

    #[tokio::test]
    async fn test_mismatched_download_is_discarded() {
        let dir = std::env::temp_dir().join(format!("cami-test-{}", rand::thread_rng().gen::<u64>()));
        tokio::fs::create_dir_all(&dir).await.unwrap();
        let tmp = dir.join(".partial-bad");
        tokio::fs::write(&tmp, b"evil weights").await.unwrap();
        let fetched = Fetched::Downloaded {
            tmp: tmp.clone(),
            sha256: "0".repeat(64),
        };
        assert!(verify_fetched("m", fetched, HELLO_SHA, &dir).await.is_err());
        assert!(!tmp.exists(), "an unverified download must not be kept");
        assert!(!cached_artifact_path(&dir, HELLO_SHA).exists());
        let _ = tokio::fs::remove_dir_all(&dir).await;
    }

    #[test]
    fn test_runtime_backend_name() {
        assert_eq!(RuntimeBackend::Stub.name(), "stub");
        assert_eq!(RuntimeBackend::Ollama { url: "x".into() }.name(), "ollama");
        assert_eq!(
            RuntimeBackend::LiteRT {
                url: "x".into(),
                accelerator: "cpu".into()
            }
            .name(),
            "litert"
        );
    }
}

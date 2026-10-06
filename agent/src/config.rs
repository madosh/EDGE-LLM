use clap::Parser;
use std::collections::HashMap;

use crate::model_runtime::RuntimeBackend;

/// Default address of `litert-lm serve` running on the same device.
pub const DEFAULT_LITERT_URL: &str = "http://127.0.0.1:9379";
pub const DEFAULT_OLLAMA_URL: &str = "http://localhost:11434";

#[derive(Parser, Debug, Clone)]
#[command(name = "cami-agent", about = "Cami Fleet device agent")]
pub struct Config {
    /// Displayed name for this device (must be unique in the fleet, and must
    /// equal the common name on this device's client certificate)
    #[arg(long, env = "DEVICE_NAME", default_value = "device-001")]
    pub device_name: String,

    /// Comma-separated key=value labels, e.g. "location=barcelona,type=jetson"
    #[arg(long, env = "DEVICE_LABELS", default_value = "")]
    pub device_labels: String,

    /// gRPC endpoint of the control plane
    #[arg(long, env = "CONTROL_PLANE_URL", default_value = "https://localhost:9090")]
    pub control_plane_url: String,

    /// Directory containing ca.crt, device.crt, device.key
    #[arg(long, env = "CERT_DIR", default_value = "/certs")]
    pub cert_dir: String,

    /// Explicit runtime backend selection: "litert", "ollama", or "stub".
    /// When empty, auto-detects based on which URL env var is set.
    #[arg(long, env = "RUNTIME_BACKEND", default_value = "")]
    pub runtime_backend: String,

    /// Ollama API base URL (e.g. "http://ollama:11434").
    #[arg(long, env = "OLLAMA_URL", default_value = "")]
    pub ollama_url: String,

    /// URL of `litert-lm serve` (LiteRT-LM's OpenAI-compatible server).
    #[arg(long, env = "LITERT_URL", default_value = "")]
    pub litert_url: String,

    /// LiteRT-LM accelerator: "cpu", "gpu" or "npu".
    #[arg(long, env = "LITERT_ACCELERATOR", default_value = "cpu")]
    pub litert_accelerator: String,

    /// Where verified model artifacts are stored, named by SHA-256. With the
    /// litert backend, `litert-lm serve` must see this directory at the same
    /// absolute path.
    #[arg(long, env = "MODEL_CACHE_DIR", default_value = "/var/lib/cami/models")]
    pub model_cache_dir: String,

    /// Seconds between telemetry probes. Each probe runs a short real
    /// generation, so it costs device compute; keep it well above a few seconds.
    #[arg(long, env = "PROBE_INTERVAL_SECS", default_value_t = 30)]
    pub probe_interval_secs: u64,
}

impl Config {
    pub fn labels(&self) -> HashMap<String, String> {
        let mut map = HashMap::new();
        for pair in self.device_labels.split(',') {
            let pair = pair.trim();
            if pair.is_empty() {
                continue;
            }
            if let Some((k, v)) = pair.split_once('=') {
                map.insert(k.trim().to_string(), v.trim().to_string());
            }
        }
        map
    }

    /// Resolve the inference backend from explicit `RUNTIME_BACKEND` or
    /// auto-detect from URL env vars (LiteRT > Ollama > Stub).
    pub fn resolve_backend(&self) -> RuntimeBackend {
        let backend = self.runtime_backend.trim().to_lowercase();
        let litert = |url: String| RuntimeBackend::LiteRT {
            url,
            accelerator: self.accelerator(),
        };

        match backend.as_str() {
            "litert" => litert(non_empty(&self.litert_url).unwrap_or_else(|| DEFAULT_LITERT_URL.to_string())),
            "ollama" => RuntimeBackend::Ollama {
                url: non_empty(&self.ollama_url).unwrap_or_else(|| DEFAULT_OLLAMA_URL.to_string()),
            },
            "stub" => RuntimeBackend::Stub,
            _ => {
                if let Some(url) = non_empty(&self.litert_url) {
                    litert(url)
                } else if let Some(url) = non_empty(&self.ollama_url) {
                    RuntimeBackend::Ollama { url }
                } else {
                    RuntimeBackend::Stub
                }
            }
        }
    }

    /// The configured LiteRT-LM accelerator; anything unrecognised falls back
    /// to "cpu", which every LiteRT-LM build supports.
    pub fn accelerator(&self) -> String {
        match self.litert_accelerator.trim().to_lowercase().as_str() {
            a @ ("cpu" | "gpu" | "npu") => a.to_string(),
            _ => "cpu".to_string(),
        }
    }
}

fn non_empty(s: &str) -> Option<String> {
    let trimmed = s.trim();
    if trimmed.is_empty() {
        None
    } else {
        Some(trimmed.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn base_cfg() -> Config {
        Config {
            device_name: "test".into(),
            device_labels: "".into(),
            control_plane_url: "https://localhost:9090".into(),
            cert_dir: "/certs".into(),
            runtime_backend: "".into(),
            ollama_url: "".into(),
            litert_url: "".into(),
            litert_accelerator: "cpu".into(),
            model_cache_dir: "/var/lib/cami/models".into(),
            probe_interval_secs: 30,
        }
    }

    #[test]
    fn test_labels_parsing() {
        let mut cfg = base_cfg();
        cfg.device_labels = "location=barcelona,type=jetson-orin".into();
        let labels = cfg.labels();
        assert_eq!(labels.get("location").unwrap(), "barcelona");
        assert_eq!(labels.get("type").unwrap(), "jetson-orin");
        assert_eq!(labels.len(), 2);
    }

    #[test]
    fn test_labels_empty() {
        let cfg = base_cfg();
        assert!(cfg.labels().is_empty());
    }

    #[test]
    fn test_labels_whitespace() {
        let mut cfg = base_cfg();
        cfg.device_labels = " key1 = val1 , key2=val2 , ".into();
        let labels = cfg.labels();
        assert_eq!(labels.get("key1").unwrap(), "val1");
        assert_eq!(labels.get("key2").unwrap(), "val2");
        assert_eq!(labels.len(), 2);
    }

    #[test]
    fn test_auto_detect_stub_when_empty() {
        assert_eq!(base_cfg().resolve_backend(), RuntimeBackend::Stub);
    }

    #[test]
    fn test_auto_detect_ollama() {
        let mut cfg = base_cfg();
        cfg.ollama_url = "http://ollama:11434".into();
        assert_eq!(
            cfg.resolve_backend(),
            RuntimeBackend::Ollama {
                url: "http://ollama:11434".into()
            }
        );
    }

    #[test]
    fn test_auto_detect_litert() {
        let mut cfg = base_cfg();
        cfg.litert_url = "http://litert:9379".into();
        assert_eq!(
            cfg.resolve_backend(),
            RuntimeBackend::LiteRT {
                url: "http://litert:9379".into(),
                accelerator: "cpu".into()
            }
        );
    }

    #[test]
    fn test_litert_preferred_over_ollama() {
        let mut cfg = base_cfg();
        cfg.litert_url = "http://litert:9379".into();
        cfg.ollama_url = "http://ollama:11434".into();
        assert_eq!(cfg.resolve_backend().name(), "litert");
    }

    #[test]
    fn test_explicit_backend_overrides() {
        let mut cfg = base_cfg();
        cfg.litert_url = "http://litert:9379".into();
        cfg.ollama_url = "http://ollama:11434".into();
        cfg.runtime_backend = "ollama".into();
        assert_eq!(cfg.resolve_backend().name(), "ollama");
    }

    #[test]
    fn test_explicit_stub() {
        let mut cfg = base_cfg();
        cfg.ollama_url = "http://ollama:11434".into();
        cfg.runtime_backend = "stub".into();
        assert_eq!(cfg.resolve_backend(), RuntimeBackend::Stub);
    }

    #[test]
    fn test_explicit_litert_defaults_to_local_server() {
        let mut cfg = base_cfg();
        cfg.runtime_backend = "litert".into();
        cfg.litert_accelerator = "GPU".into();
        assert_eq!(
            cfg.resolve_backend(),
            RuntimeBackend::LiteRT {
                url: DEFAULT_LITERT_URL.into(),
                accelerator: "gpu".into()
            }
        );
    }

    #[test]
    fn test_unknown_accelerator_falls_back_to_cpu() {
        let mut cfg = base_cfg();
        cfg.litert_accelerator = "tpu".into();
        assert_eq!(cfg.accelerator(), "cpu");
    }
}

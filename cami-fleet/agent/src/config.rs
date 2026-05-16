use clap::Parser;
use std::collections::HashMap;

use crate::model_runtime::RuntimeBackend;

#[derive(Parser, Debug, Clone)]
#[command(name = "cami-agent", about = "Cami Fleet device agent")]
pub struct Config {
    /// Displayed name for this device (must be unique in the fleet)
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

    /// LiteRT server base URL (e.g. "http://litert:8000").
    #[arg(long, env = "LITERT_URL", default_value = "")]
    pub litert_url: String,

    /// Model name override for LiteRT or Ollama.
    /// Defaults to the model_id from the deployment instruction.
    #[arg(long, env = "LITERT_MODEL", default_value = "")]
    pub litert_model: String,
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
    /// auto-detect from URL env vars.  `model_id` is the deployment's model
    /// identifier used as a fallback model name.
    pub fn resolve_backend(&self, model_id: &str) -> RuntimeBackend {
        let backend = self.runtime_backend.trim().to_lowercase();

        match backend.as_str() {
            "litert" => {
                let url = non_empty(&self.litert_url)
                    .unwrap_or("http://localhost:8000".to_string());
                let model = non_empty(&self.litert_model)
                    .unwrap_or_else(|| model_id.to_string());
                RuntimeBackend::LiteRT { url, model }
            }
            "ollama" => {
                let url = non_empty(&self.ollama_url)
                    .unwrap_or("http://localhost:11434".to_string());
                RuntimeBackend::Ollama {
                    url,
                    model: model_id.to_string(),
                }
            }
            "stub" => RuntimeBackend::Stub,
            _ => {
                // Auto-detect: prefer LiteRT > Ollama > Stub
                if let Some(url) = non_empty(&self.litert_url) {
                    let model = non_empty(&self.litert_model)
                        .unwrap_or_else(|| model_id.to_string());
                    RuntimeBackend::LiteRT { url, model }
                } else if let Some(url) = non_empty(&self.ollama_url) {
                    RuntimeBackend::Ollama {
                        url,
                        model: model_id.to_string(),
                    }
                } else {
                    RuntimeBackend::Stub
                }
            }
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
            litert_model: "".into(),
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
        let cfg = base_cfg();
        assert_eq!(cfg.resolve_backend("model-a").name(), "stub");
    }

    #[test]
    fn test_auto_detect_ollama() {
        let mut cfg = base_cfg();
        cfg.ollama_url = "http://ollama:11434".into();
        let b = cfg.resolve_backend("my-model");
        assert_eq!(b.name(), "ollama");
        if let RuntimeBackend::Ollama { url, model } = b {
            assert_eq!(url, "http://ollama:11434");
            assert_eq!(model, "my-model");
        } else {
            panic!("expected Ollama");
        }
    }

    #[test]
    fn test_auto_detect_litert() {
        let mut cfg = base_cfg();
        cfg.litert_url = "http://litert:8000".into();
        let b = cfg.resolve_backend("gemma-4-e2b");
        assert_eq!(b.name(), "litert");
        if let RuntimeBackend::LiteRT { url, model } = b {
            assert_eq!(url, "http://litert:8000");
            assert_eq!(model, "gemma-4-e2b");
        } else {
            panic!("expected LiteRT");
        }
    }

    #[test]
    fn test_litert_preferred_over_ollama() {
        let mut cfg = base_cfg();
        cfg.litert_url = "http://litert:8000".into();
        cfg.ollama_url = "http://ollama:11434".into();
        assert_eq!(cfg.resolve_backend("m").name(), "litert");
    }

    #[test]
    fn test_explicit_backend_overrides() {
        let mut cfg = base_cfg();
        cfg.litert_url = "http://litert:8000".into();
        cfg.ollama_url = "http://ollama:11434".into();
        cfg.runtime_backend = "ollama".into();
        assert_eq!(cfg.resolve_backend("m").name(), "ollama");
    }

    #[test]
    fn test_explicit_stub() {
        let mut cfg = base_cfg();
        cfg.ollama_url = "http://ollama:11434".into();
        cfg.runtime_backend = "stub".into();
        assert_eq!(cfg.resolve_backend("m").name(), "stub");
    }

    #[test]
    fn test_litert_model_override() {
        let mut cfg = base_cfg();
        cfg.litert_url = "http://litert:8000".into();
        cfg.litert_model = "gemma3-1b".into();
        let b = cfg.resolve_backend("ignored-model-id");
        if let RuntimeBackend::LiteRT { model, .. } = b {
            assert_eq!(model, "gemma3-1b");
        } else {
            panic!("expected LiteRT");
        }
    }
}

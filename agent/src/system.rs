//! Facts about the device the agent runs on, read from `/proc` on Linux.
//! Everything here is read-only and returns "unknown" values elsewhere.

use serde_json::{json, Value};
use std::path::Path;

/// Reads a `/proc/meminfo` field (reported in kB) as MiB.
pub fn meminfo_mb(meminfo: &str, key: &str) -> Option<u64> {
    meminfo.lines().find_map(|line| {
        let rest = line.strip_prefix(key)?.strip_prefix(':')?;
        let kb: u64 = rest.split_whitespace().next()?.parse().ok()?;
        Some(kb / 1024)
    })
}

/// Total RAM in MiB, or 0 when it cannot be read (the control plane then
/// does not hold the device to memory requirements).
pub fn mem_total_mb() -> u64 {
    let meminfo = std::fs::read_to_string("/proc/meminfo").unwrap_or_default();
    meminfo_mb(&meminfo, "MemTotal").unwrap_or(0)
}

/// A JSON snapshot of the device: host, platform, uptime, load and memory.
pub fn device_status() -> Value {
    let read = |p: &str| std::fs::read_to_string(p).unwrap_or_default();
    let meminfo = read("/proc/meminfo");
    let hostname = read("/etc/hostname");
    let uptime = read("/proc/uptime");
    let loadavg = read("/proc/loadavg");

    let uptime_s = uptime
        .split_whitespace()
        .next()
        .and_then(|v| v.parse::<f64>().ok())
        .map(|v| v as u64);
    let load: Vec<&str> = loadavg.split_whitespace().take(3).collect();

    json!({
        "hostname": hostname.trim(),
        "arch": std::env::consts::ARCH,
        "os": std::env::consts::OS,
        "uptime_s": uptime_s,
        "load_avg_1_5_15m": load,
        "mem_total_mb": meminfo_mb(&meminfo, "MemTotal"),
        "mem_available_mb": meminfo_mb(&meminfo, "MemAvailable"),
    })
}

/// The verified models in the agent's cache, named `sha256-<digest>`.
pub fn cached_models(cache_dir: &Path) -> Value {
    let mut models = Vec::new();
    if let Ok(entries) = std::fs::read_dir(cache_dir) {
        for entry in entries.flatten() {
            let name = entry.file_name().to_string_lossy().into_owned();
            if let Some(sha) = name.strip_prefix("sha256-") {
                let size_mb = entry.metadata().map(|m| m.len() / (1024 * 1024)).unwrap_or(0);
                models.push(json!({ "sha256": sha, "size_mb": size_mb }));
            }
        }
    }
    json!({ "cache_dir": cache_dir.display().to_string(), "models": models })
}

#[cfg(test)]
mod tests {
    use super::*;

    const MEMINFO: &str = "MemTotal:        8245384 kB\nMemFree:          512000 kB\nMemAvailable:    6291456 kB\n";

    #[test]
    fn test_meminfo_parsing() {
        assert_eq!(meminfo_mb(MEMINFO, "MemTotal"), Some(8052));
        assert_eq!(meminfo_mb(MEMINFO, "MemAvailable"), Some(6144));
        assert_eq!(meminfo_mb(MEMINFO, "SwapTotal"), None);
        // "MemTotalX" must not match "MemTotal".
        assert_eq!(meminfo_mb("MemTotalX: 1024 kB\n", "MemTotal"), None);
    }

    #[test]
    fn test_device_status_shape() {
        let s = device_status();
        assert_eq!(s["arch"], std::env::consts::ARCH);
        assert!(s.get("mem_total_mb").is_some());
    }

    #[test]
    fn test_cached_models_lists_only_digests() {
        let dir = std::env::temp_dir().join(format!("cami-sys-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(dir.join("sha256-abc"), b"x").unwrap();
        std::fs::write(dir.join(".partial-123"), b"x").unwrap();
        let v = cached_models(&dir);
        let models = v["models"].as_array().unwrap();
        assert_eq!(models.len(), 1);
        assert_eq!(models[0]["sha256"], "abc");
        let _ = std::fs::remove_dir_all(&dir);
    }
}

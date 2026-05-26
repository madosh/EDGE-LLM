# Phase 1 — Observer

**Goal:** Fix `mem_mb: 0.0`. Give every LiteRT deployment real memory metrics,
latency histograms, and token counters — exported to Prometheus, visible in Grafana.

**Status:** ✅ Complete  
**Upstream impact:** Closes the `mem_mb: 0.0` gap in `madosh/EDGE-LLM` agent.
Opens LiteRT issue requesting a native `/v1beta/runtime/metrics` endpoint.

---

## The Problem This Solves

Every LiteRT deployment today shows `mem_mb: 0.0` in the Cami Fleet dashboard.
This line in `agent/src/model_runtime.rs:230` explains why:

```rust
mem_mb: 0.0, // LiteRT doesn't expose memory via API
```

`lit serve` has no memory reporting endpoint. Without real memory data:
- You cannot detect memory leaks across model loads.
- You cannot right-size the hub hardware.
- You cannot set meaningful OOM alerts.
- Every Grafana panel for memory shows a flat zero line.

---

## What Phase 1 Delivers

| Metric | Prometheus name | What it answers |
|---|---|---|
| RSS memory | `liteops_litert_memory_rss_mb` | How much RAM is LiteRT using right now? |
| Peak memory | `liteops_litert_memory_peak_mb` | What was the highest RSS since startup? |
| Request latency | `liteops_litert_request_latency_ms` | What are p50/p95/p99 latencies? |
| Tokens generated | `liteops_litert_tokens_generated_total` | What is the throughput in tokens/min? |
| Request outcomes | `liteops_litert_requests_total{status}` | What is the error rate? |

All five metrics are populated on every `observer.generate()` call. Memory is read
from `psutil.Process.memory_info().rss` — the same source as `htop` and `docker stats`.

---

## How It Works

```
Your code          LiteOps Observer           lit serve (port 8000)
──────────         ─────────────────          ─────────────────────
generate()    ──►  read mem_before       ──►  POST /v1beta/models/…
                   POST to lit serve
                   ◄── response              ◄── JSON response
              ◄──  read mem_after
                   record Prometheus metrics
                   attach _liteops metadata
              ◄──  return enriched response
```

The observer does not modify lit serve. It reads process memory from the OS
before and after each HTTP call and records the delta.

---

## Usage

### Install

```bash
pip install liteops
# or from source:
pip install -e ".[dev]"
```

### Minimal usage

```python
import asyncio
from liteops import LiteRTObserver

async def main():
    observer = LiteRTObserver(
        litert_url="http://localhost:8000",
        device_id="hub-barcelona-01",
        # litert_pid=12345   # optional — auto-discovered if omitted
        metrics_port=9101,   # starts Prometheus /metrics on this port
    )

    result = await observer.generate(
        model="gemma2-2b-it-q4",
        prompt="Explain edge AI in one sentence.",
        max_output_tokens=64,
    )

    meta = result["_liteops"]
    print(f"Memory:  {meta['mem_rss_mb']} MB")   # never 0.0
    print(f"Latency: {meta['latency_ms']} ms")
    print(f"Tokens:  {meta['output_tokens']}")

asyncio.run(main())
```

### Passive memory monitoring (no inference calls needed)

```python
# Poll memory every 5 seconds without making inference calls.
# Useful as a sidecar to an existing application.
import time
from liteops import LiteRTObserver

observer = LiteRTObserver("http://localhost:8000", "hub-01", metrics_port=9101)

while True:
    snap = observer.snapshot()
    print(snap.rss_mb, snap.peak_mb)
    time.sleep(5)
```

### With the quickstart Docker Compose

```bash
cd examples/quickstart
docker compose up -d
# Open http://localhost:3000  (Grafana admin/admin)
# Import dashboards/litert_fleet.json
```

---

## PID Resolution

The observer needs the lit serve PID to read memory. It resolves it in this order:

1. Use `litert_pid` if provided explicitly.
2. Auto-discover by scanning running processes for cmdlines containing `litert_process_name`
   (default: `"lit"`).
3. If no process is found, log a warning and return `mem_rss_mb: 0.0` — same as before,
   but now with an explicit log message instead of a silent hardcode.

In production, pass the PID explicitly to avoid the scan overhead:

```python
import subprocess, re

pid_output = subprocess.check_output(["pgrep", "-f", "lit serve"]).decode()
litert_pid = int(pid_output.strip().split()[0])
observer = LiteRTObserver(..., litert_pid=litert_pid)
```

---

## Prometheus Metrics Reference

Scrape endpoint: `http://{observer_host}:{metrics_port}/metrics`

```
# HELP liteops_litert_memory_rss_mb LiteRT process RSS memory in MB
# TYPE liteops_litert_memory_rss_mb gauge
liteops_litert_memory_rss_mb{device_id="hub-01",model="gemma2-2b"} 412.3

# HELP liteops_litert_memory_peak_mb LiteRT process peak RSS memory observed in MB
# TYPE liteops_litert_memory_peak_mb gauge
liteops_litert_memory_peak_mb{device_id="hub-01",model="gemma2-2b"} 445.7

# HELP liteops_litert_request_latency_ms End-to-end generateContent latency in ms
# TYPE liteops_litert_request_latency_ms histogram
liteops_litert_request_latency_ms_bucket{...,le="500"} 14
liteops_litert_request_latency_ms_bucket{...,le="1000"} 38

# HELP liteops_litert_tokens_generated_total Cumulative output tokens generated
# TYPE liteops_litert_tokens_generated_total counter
liteops_litert_tokens_generated_total{device_id="hub-01",model="gemma2-2b"} 8421

# HELP liteops_litert_requests_total Total generateContent requests by outcome
# TYPE liteops_litert_requests_total counter
liteops_litert_requests_total{device_id="hub-01",model="gemma2-2b",status="ok"} 207
liteops_litert_requests_total{device_id="hub-01",model="gemma2-2b",status="error"} 2
```

---

## Running the Tests

```bash
cd liteops
pip install -e ".[dev]"
pytest tests/test_observer.py -v
```

Expected output:

```
tests/test_observer.py::test_generate_returns_liteops_metadata          PASSED
tests/test_observer.py::test_generate_never_returns_zero_memory_...     PASSED
tests/test_observer.py::test_peak_memory_only_increases                 PASSED
tests/test_observer.py::test_auto_discovers_pid_by_process_name         PASSED
tests/test_observer.py::test_falls_back_to_zero_when_no_process_found   PASSED
tests/test_observer.py::test_http_error_propagates                      PASSED
tests/test_observer.py::test_snapshot_returns_current_rss               PASSED
tests/test_observer.py::test_snapshot_as_dict                           PASSED
tests/test_observer.py::test_read_rss_mb_returns_zero_on_none           PASSED
tests/test_observer.py::test_read_rss_mb_returns_zero_on_no_such_...    PASSED
tests/test_observer.py::test_read_rss_mb_converts_bytes_to_mb           PASSED
```

---

## Cami Fleet Integration

The Cami Fleet agent now reads real memory from LiteOps instead of hardcoding `0.0`.
See the fix in `agent/src/model_runtime.rs` — the `find_litert_rss_mb()` helper reads
`/proc/{pid}/status` directly in the Rust agent, with the same logic as LiteOps Observer
but without the Python dependency.

---

## Upstream LiteRT Issue

Phase 1 opens this issue on `google-ai-edge/LiteRT`:

> **[Feature Request] Add /v1beta/runtime/metrics endpoint to lit serve**
>
> Currently, lit serve exposes no memory, CPU, or throughput metrics via its HTTP API.
> Every deployment that uses the gRPC reporting pattern shows mem_mb: 0.0.
>
> Workaround: We are reading /proc/{pid}/status from a sidecar observer (LiteOps).
> This works but is fragile in containers and adds an external dependency.
>
> Proposed: `GET /v1beta/runtime/metrics` returning rss_mb, model_loaded_mb, peak_mb,
> active_requests, uptime_seconds.
>
> We are willing to implement this and have proof-of-concept data showing the values exist.

---

## What Phase 2 Adds

Phase 1 tells you *how much memory* LiteRT uses. Phase 2 tells you *how well it performs*.

Phase 2 (Evaluator) adds:
- `liteops.evaluator.EdgeEvaluator` — runs a question bank against every deployed model.
- Automatic quality score per device per deployment.
- Rollback trigger if quality drops below threshold after an OTA update.
- `liteops_litert_quality_score` Prometheus gauge added to the Grafana dashboard.

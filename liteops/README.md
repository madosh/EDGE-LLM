# LiteOps

**Production operations framework for LiteRT edge AI deployments.**

LiteRT (`lit serve`) has no memory metrics, no quality evaluation, no fleet management,
and no enterprise integration. LiteOps fills that gap — one phase at a time.

```bash
pip install liteops
```

---

## The problem

Every LiteRT deployment today shows this in its monitoring:

```
mem_mb: 0.0   # LiteRT doesn't expose memory via API
```

That single line means you are flying blind: no memory alerts, no leak detection,
no hardware right-sizing, no production quality gates.

## The solution (Phase 1, today)

```python
from liteops import LiteRTObserver

observer = LiteRTObserver(
    litert_url = "http://localhost:8000",
    device_id  = "hub-barcelona-01",
    metrics_port = 9101,          # Prometheus /metrics on this port
)

result = await observer.generate("gemma2-2b-it-q4", "Explain edge AI.")

print(result["_liteops"]["mem_rss_mb"])   # 412.3  — never 0.0
print(result["_liteops"]["latency_ms"])   # 684.2
print(result["_liteops"]["output_tokens"])# 38
```

Point Prometheus at `:9101/metrics`, open the bundled Grafana dashboard,
and for the first time you see real memory, latency percentiles, and token throughput
from your LiteRT hub.

---

## Phases

| # | What it adds | Status |
|---|---|---|
| **1** | `LiteRTObserver` — real memory, latency, tokens → Prometheus + Grafana | ✅ **Done** |
| **2** | `EdgeEvaluator` — quality gates, auto-rollback on regression | 🔜 Next |
| **3** | `ServiceNowConnector` — incidents, CMDB sync, change records | 📋 Planned |
| **4** | `HubRouter` — LangGraph query routing on the hub | 📋 Planned |
| **5** | `liteops_esp32` — MicroPython MQTT agent for ESP32 leaf nodes | 📋 Planned |
| **6** | Upstream LiteRT PRs — metrics API, MicroPython guide | 📋 Planned |

---

## Architecture

```
Grafana dashboard  ◄──  Prometheus  ◄──  LiteOps Observer (port 9101)
                                              │
                                         lit serve (port 8000)
                                              │
                                        LiteOps Hub Router  (Phase 4)
                                              │
                                    ┌─────────┴──────────┐
                                ESP32-S3             ESP32-S3
                             (liteops_esp32)      (liteops_esp32)
```

LiteOps is a sidecar layer — it does not replace or fork any existing component.

---

## Quick start

### 1. Install

```bash
pip install liteops
```

### 2. Start the full observability stack

```bash
cd examples/quickstart
# Set LITERT_URL to your lit serve address
docker compose up -d
```

### 3. Open Grafana

```
http://localhost:3000   (admin / admin)
```

Import `dashboards/litert_fleet.json`. You will see:

- **LiteRT Memory (MB)** — RSS + peak over time
- **Request Latency** — p50 / p95 / p99
- **Tokens Generated** — throughput per minute per device
- **Success Rate** — gauge (target: > 0.95)
- **Memory by Device** — bar chart for fleet comparison

---

## Requirements

- Python 3.11+
- A running `lit serve` instance (Google LiteRT)
- Prometheus + Grafana (provided in `examples/quickstart/`)

---

## Running tests

```bash
pip install -e ".[dev]"
pytest -v
```

---

## Project structure

```
liteops/
├── liteops/
│   ├── observer.py          Phase 1 — memory, latency, tokens
│   ├── evaluator.py         Phase 2 — quality evaluation (coming)
│   ├── router.py            Phase 4 — LangGraph hub router (coming)
│   └── connectors/
│       └── servicenow.py    Phase 3 — ServiceNow bridge (coming)
├── liteops_esp32/           Phase 5 — MicroPython leaf agent (coming)
├── tests/
├── dashboards/              Grafana JSON templates (drop-in)
├── docs/phases/             Per-phase documentation
└── examples/quickstart/     Full stack in one docker compose up
```

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
Each phase has a spec in `docs/phases/`. Open an issue before starting a new phase.

---

## License

MIT

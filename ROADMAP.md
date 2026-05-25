# Smart Hub Vision — Roadmap

**Author background:** Senior AI & Software Engineer — production RAG systems (LangChain, LangGraph,
OpenAI, Pinecone, LangFuse, GCP/Vertex AI), enterprise integration (ServiceNow, REST/SOAP, Kafka),
7+ years turning requirements into working systems. Maker background (Cairo Maker Fair, TIEC winner).
Languages: Python · JavaScript · Java · Bash. Learning: Rust (readable), C/ESP-IDF (target skill).

---

## The Vision

A three-layer edge AI system where Cami Fleet (this project) is the cloud brain, a LiteRT-powered
Linux board is the smart hub running an intelligent agent, and ESP32 microcontrollers are the
sensor/actuator leaf fleet — all unified under one operator dashboard and integrated with enterprise
service management.

```
┌──────────────────────────────────────────────────────────────────────┐
│                          CLOUD LAYER                                 │
│                     Cami Fleet Control Plane                         │
│              Go REST API · gRPC · PostgreSQL · ClickHouse            │
│            Grafana · Prometheus · Webhook alerts (NATS)              │
│                              ▲                                       │
│                 ServiceNow Integration Bridge ◄─── unique angle      │
│              (incidents · CMDB sync · change records)                │
└────────────────────────────┬─────────────────────────────────────────┘
                             │  gRPC / mTLS (internet)
                             │
┌────────────────────────────▼─────────────────────────────────────────┐
│                           HUB LAYER                                  │
│          Smart Hub  (Raspberry Pi 5 · Jetson Nano · Coral)           │
│                                                                      │
│   ┌──────────────┐   ┌──────────────────────┐   ┌────────────────┐  │
│   │  LiteRT-LM   │   │  Hub Agent           │   │  MQTT Broker   │  │
│   │  (lit serve) │◄──│  LangGraph router    │   │  (Mosquitto)   │  │
│   │  Gemini API  │   │  query classifier    │──►│  port 1883     │  │
│   └──────────────┘   │  gRPC upstream       │   └───────┬────────┘  │
│                      │  MQTT downstream     │           │           │
│                      └──────────────────────┘           │           │
└────────────────────────────────────────────────────────┬┘───────────┘
                              WiFi / BLE                  │
                    ┌─────────────────┬───────────────────┘
                    │                 │                │
        ┌───────────▼──┐  ┌───────────▼──┐  ┌─────────▼────┐
        │  ESP32 Node  │  │  ESP32 Node  │  │  ESP32-CAM   │
        │  MicroPython │  │  MicroPython │  │  MicroPython │
        │  MQTT client │  │  Sensors     │  │  Vision      │
        │  REST → hub  │  │  Actuators   │  │  MQTT stream │
        └──────────────┘  └──────────────┘  └──────────────┘
```

---

## Why This Architecture

| Challenge | Solution | Your skill |
|---|---|---|
| ESP32 cannot run `lit serve` (needs NPU/GPU) | Hub runs LiteRT; ESP32 delegates inference | Hub design |
| ESP32 cannot speak gRPC (RAM constraint) | ESP32 speaks MQTT/REST to hub; hub bridges upstream | Protocol design |
| Fleet of hundreds of ESP32s is unmanageable | Hub aggregates a single device view per cluster | Systems design |
| LiteRT memory usage is opaque (`mem_mb: 0.0`) | /proc monitoring + metrics API upstream PR | Observability |
| No enterprise alerting in Cami Fleet | ServiceNow webhook integration | Your unique angle |
| Hub just forwards inference — no intelligence | LangGraph agent on hub for query routing | Your superpower |

---

## How the Two Efforts Connect

```
Cami Fleet (this repo)             LiteRT (upstream: google-ai-edge/LiteRT)
──────────────────────             ────────────────────────────────────────
Observability layer            ←── PR: add /v1beta/runtime/metrics endpoint
Hub intelligence (LangGraph)   ←── Gemini-compatible API already in lit serve
ServiceNow alerts              ←── Deployment failure events motivate metrics PR
ESP32 MicroPython agent            TFLM alternative for heavier ESP32-S3 boards
```

Your production experience with LangFuse and RAG quality evaluation gives you the credibility
and concrete use case to argue for a proper metrics API in LiteRT. That is not a theoretical
request from the outside — it is a practitioner saying "I built this and here is what is missing."

---

## Honest Skill Mapping

Before the phases, an honest map of where you start strong vs. where you will grow:

| Area | Current Level | Role in This Roadmap |
|---|---|---|
| Python | Strong (production) | Simulator, observability tooling, MicroPython agent |
| LLM orchestration (LangChain / LangGraph) | Strong (production) | Hub intelligence layer |
| Observability (LangFuse, dashboards) | Strong (production) | Fix Cami Fleet telemetry gaps |
| Cloud / Docker / K8s | Strong (production) | Hub deployment, CI |
| REST / gRPC / integration | Strong (production) | ServiceNow bridge, control plane PRs |
| Go (control plane) | Readable, not fluent | Contribute with reading + targeted edits |
| Rust (agent) | Readable, not fluent | Read existing code, small targeted fixes |
| C / ESP-IDF / FreeRTOS | Maker background, needs refresh | Phase 5 — MicroPython first, C later |
| ServiceNow | Expert | Unique bridge contribution nobody else can build |

---

## Phased Roadmap

### Phase 0 — Understand the Stack (Weeks 1–2)

**Goal:** Run the full system, trace a deployment end-to-end before writing a line.

```bash
make up        # start all 12 services
make smoke     # verify connectivity
make logs      # watch all services
```

**Read in this order:**

1. `control-plane/proto/device.proto` — the gRPC contract (every device speaks this)
2. `agent/src/model_runtime.rs:89–143` — LiteRT HTTP client (Gemini-compatible REST)
3. `agent/src/model_runtime.rs:230` — the `mem_mb: 0.0` gap you will fix first
4. `agent/src/telemetry.rs` — the 5-second metrics loop
5. `control-plane/internal/notify/` — the webhook alerter (your ServiceNow entry point)
6. `monitoring/dashboards/fleet-overview.json` — Grafana dashboard you will extend

**Deliverable:** Map the full data flow on paper:
operator deploys model → control plane notifies agent → agent downloads + SHA-256 verifies →
agent loads into LiteRT → telemetry streams back → Grafana shows it → alert fires on failure.

---

### Phase 1 — Observability & Quality Layer (Month 1)

**This is your superpower.** You built LangFuse dashboards and caught a prompt regression
in week three of production at Media-Markt Saturn. Cami Fleet has a real gap here.

**Gap 1: `mem_mb: 0.0` in `agent/src/model_runtime.rs:230`**

LiteRT does not expose memory via its API. Fix it at the agent level:

```rust
// agent/src/model_runtime.rs — replace the placeholder
fn read_litert_rss_mb(pid: u32) -> f32 {
    // Read /proc/{pid}/status on Linux — VmRSS line
    // This is standard Linux, no LiteRT API needed
    let path = format!("/proc/{pid}/status");
    std::fs::read_to_string(path)
        .ok()
        .and_then(|s| {
            s.lines()
                .find(|l| l.starts_with("VmRSS:"))
                .and_then(|l| l.split_whitespace().nth(1))
                .and_then(|v| v.parse::<f32>().ok())
        })
        .map(|kb| kb / 1024.0) // kB → MB
        .unwrap_or(0.0)
}
```

You do not need to write Rust from scratch — read the existing pattern in `telemetry.rs`
and replicate the style. The logic itself is straightforward Linux file reading.

**Gap 2: No per-device AI quality metrics**

The current telemetry tracks tokens/sec and TTFT but has no concept of output quality.
Your LangFuse experience tells you exactly what is missing. Add to the Grafana dashboard:

```json
// monitoring/dashboards/fleet-overview.json — new panels:
// - Answer quality score (0–1, from evaluation hook)
// - Token cost per device per day
// - Model drift indicator (rolling TTFT stddev)
// - Deployment success rate per hub
```

**Gap 3: No evaluation pipeline**

The project has no equivalent of the evaluation cycle you ran before every release at
Media-Markt Saturn. Propose and document a simple one:

```
tools/eval/
├── eval_runner.py       # runs a fixed question set against each deployed model
├── question_bank.json   # 50 factual questions with expected grounding
├── score.py             # semantic similarity + grounding check
└── report.py            # generates pass/fail summary per device
```

**Cami Fleet PRs:**
- `fix(agent): read LiteRT memory usage from /proc instead of hardcoded 0.0`
- `feat(monitoring): add AI quality panels to Grafana fleet dashboard`
- `feat(tools): add evaluation pipeline for edge model quality checks`

---

### Phase 2 — Python Fleet Simulator (Month 2)

**Goal:** A Python simulator that spawns N devices with realistic profiles.
Pure Python, your strongest language, fills a real gap — only 2 stub agents exist.

```
tools/simulator/
├── device.py          # single simulated device — gRPC client
├── fleet.py           # spawn N devices concurrently with asyncio
├── profiles/
│   ├── esp32_s3.json  # 240MHz, 512KB SRAM — low tps, high ttft, low mem
│   ├── rpi5.json      # 2.4GHz quad-core, 8GB — hub-class device
│   └── jetson.json    # Jetson Nano — NPU-accelerated
└── scenarios/
    ├── normal_load.py     # steady-state telemetry
    ├── ota_rollout.py     # simulate a fleet-wide deployment
    └── device_churn.py    # devices going offline/online randomly
```

**Generate gRPC stubs first:**

```bash
pip install grpcio grpcio-tools
mkdir -p tools/simulator
python -m grpc_tools.protoc \
  -I control-plane/proto \
  --python_out=tools/simulator \
  --grpc_python_out=tools/simulator \
  control-plane/proto/device.proto
```

**Device sketch:**

```python
# tools/simulator/device.py
import asyncio, grpc, random, json, pathlib
from device_pb2_grpc import DeviceServiceStub

class SimulatedDevice:
    def __init__(self, device_id: str, profile_path: str, server: str):
        self.id = device_id
        self.profile = json.loads(pathlib.Path(profile_path).read_text())
        self.stub = DeviceServiceStub(grpc.aio.insecure_channel(server))

    async def run(self):
        await self._register()
        await asyncio.gather(
            self._heartbeat_loop(),
            self._telemetry_loop(),
            self._deployment_watch(),
        )

    async def _telemetry_loop(self):
        p = self.profile
        while True:
            await self.stub.StreamTelemetry({
                "device_id": self.id,
                "tokens_per_sec": random.gauss(p["tps_mean"], p["tps_std"]),
                "time_to_first_token_ms": random.gauss(p["ttft_mean"], p["ttft_std"]),
                "memory_used_mb": random.gauss(p["mem_mean"], p["mem_std"]),
            })
            await asyncio.sleep(5)
```

**Why this matters beyond testing:** The scenario runner (`ota_rollout.py`) lets you
demo a fleet-wide model deployment to 500 simulated devices in your laptop — no hardware needed.
That is a conference talk, a blog post, and a hiring conversation in one artifact.

**Cami Fleet PR:** `feat(tools): Python fleet simulator with device profiles and scenario runner`

---

### Phase 3 — ServiceNow Integration Bridge (Month 3)

**This is the contribution nobody else in this project can build.** You have production
ServiceNow expertise from VOIS and Schwarz Group. Cami Fleet has a webhook alerter
(`control-plane/internal/notify/`) that fires on device offline and deployment failure events
but only supports Slack/Discord/generic HTTP.

**What to build:**

```
integrations/
└── servicenow/
    ├── webhook_handler.py   # receives Cami Fleet webhook → creates SN records
    ├── incident.py          # device offline → P3 incident in ServiceNow
    ├── change.py            # model deployment → change record
    ├── cmdb_sync.py         # sync Cami Fleet device registry → SN CMDB
    ├── config.yaml          # SN instance URL, credentials, assignment groups
    └── README.md
```

**Event mapping:**

| Cami Fleet Event | ServiceNow Record | Priority |
|---|---|---|
| Device offline > 30s | Incident (P3) | Auto-assign to edge ops group |
| Deployment failure (SHA-256 mismatch) | Incident (P2) — security flag | Auto-assign + alert |
| Deployment success | Change record closed | Auto-close with artifact hash |
| New device registered | CMDB CI created | Class: Edge AI Device |
| Device tag updated | CMDB CI updated | Sync on change |

**CMDB sync sketch:**

```python
# integrations/servicenow/cmdb_sync.py
import requests

class CamiFleetCMDBSync:
    """Keeps ServiceNow CMDB in sync with Cami Fleet device registry."""

    def sync_device(self, cami_device: dict):
        """Upsert a Cami Fleet device as a ServiceNow CMDB CI."""
        ci_payload = {
            "name": cami_device["device_id"],
            "u_device_type": cami_device.get("tags", {}).get("type", "edge-ai"),
            "u_hub_id": cami_device.get("parent_hub_id", ""),
            "u_firmware": cami_device.get("model_id", ""),
            "u_last_heartbeat": cami_device["last_seen"],
            "ip_address": cami_device.get("tags", {}).get("ip", ""),
            "location": cami_device.get("tags", {}).get("location", ""),
        }
        # POST to /api/now/table/cmdb_ci_computer (or custom edge AI table)
        return self.sn_client.upsert("cmdb_ci_edge_ai_device", ci_payload)
```

**Why this matters:** Every retail and enterprise operator running edge AI already has ServiceNow.
This bridge makes Cami Fleet deployable inside corporate IT governance without process changes.
That is a go-to-market argument, not just a technical feature.

**Cami Fleet PR:** `feat(integrations): ServiceNow webhook handler — incidents, CMDB sync, change records`

---

### Phase 4 — Hub Intelligence Layer (Month 4–5)

**Goal:** Make the hub agent genuinely intelligent, not just a protocol bridge.
Your LangGraph production experience applies directly here.

The current agent at `agent/src/model_runtime.rs` does: receive request → forward to LiteRT.
A hub with a LangGraph router does: receive request → classify → route → aggregate → respond.

**Hub agent extended architecture:**

```python
# hub_agent/router.py — LangGraph routing on the hub
from langgraph.graph import StateGraph

def build_hub_graph():
    graph = StateGraph(HubState)

    graph.add_node("classify", classify_request)
    # classify: sensor_query | inference_request | fleet_command | unknown

    graph.add_node("route_to_litert", forward_to_litert)
    # LiteRT for heavy inference requests

    graph.add_node("route_to_leaf", forward_to_esp32)
    # ESP32 leaf for sensor reads / local commands

    graph.add_node("aggregate", aggregate_responses)
    # merge responses from multiple ESP32s

    graph.add_node("report_upstream", send_to_control_plane)
    # forward aggregated result + telemetry to Cami Fleet

    graph.add_conditional_edges("classify", routing_decision)
    return graph.compile()
```

**Why LangGraph here instead of a simple if/else:** The classification itself can be done by
a small on-device model (Gemma 2B via LiteRT). The hub becomes self-routing — it uses AI
to decide how to distribute AI workloads. That is the "smart" in smart hub.

**This mirrors exactly what you described as your biggest win at Media-Markt Saturn:**
"Routing queries through a classification step before retrieval made the biggest single
improvement to output quality." The same principle applies at the edge.

**Cami Fleet PR:** `feat(agent): hub intelligence mode — LangGraph query router on hub`

---

### Phase 5 — ESP32 MicroPython Agent (Month 5–6)

**Goal:** Connect real ESP32 hardware to the hub. Use MicroPython — it is Python,
you know it, and it is the right entry point before considering C/ESP-IDF.

MicroPython on ESP32-S3 gives you: WiFi, MQTT, HTTP client, JSON, ~256KB free RAM after runtime.

```
agent-micropython/
├── boot.py           # WiFi connect on power-up
├── main.py           # task loop: heartbeat + telemetry + OTA watch
├── mqtt_client.py    # umqtt.simple wrapper
├── telemetry.py      # collect ESP32 metrics (mem, temp, uptime)
├── ota.py            # receive .mpy or config updates from hub
└── config.json       # device_id, hub_ip, mqtt_port (stored in flash)
```

**MicroPython agent sketch:**

```python
# agent-micropython/main.py
import time, json, network
from mqtt_client import MQTTClient
from telemetry import read_metrics

DEVICE_ID = "esp32-living-01"
HUB_IP    = "192.168.1.10"

wlan = network.WLAN(network.STA_IF)
mqtt = MQTTClient(DEVICE_ID, HUB_IP, port=1883)
mqtt.connect()

# register on boot
mqtt.publish(f"hub/devices/{DEVICE_ID}/register",
             json.dumps({"type": "esp32-s3", "firmware": "0.1.0"}))

last_heartbeat = 0
last_telemetry = 0

while True:
    now = time.time()

    if now - last_heartbeat >= 10:
        mqtt.publish(f"hub/devices/{DEVICE_ID}/heartbeat", "{}")
        last_heartbeat = now

    if now - last_telemetry >= 5:
        mqtt.publish(f"hub/devices/{DEVICE_ID}/telemetry",
                     json.dumps(read_metrics()))
        last_telemetry = now

    mqtt.check_msg()   # non-blocking check for OTA/deploy messages
    time.sleep_ms(100)
```

**MQTT topic schema (mirrors Cami Fleet gRPC semantics):**

```
hub/devices/{id}/register       ← ESP32 publishes once on boot
hub/devices/{id}/heartbeat      ← ESP32 publishes every 10s
hub/devices/{id}/telemetry      ← ESP32 publishes every 5s (JSON)
hub/devices/{id}/deploy         → Hub publishes OTA / config command
hub/devices/{id}/deploy/ack     ← ESP32 acknowledges
hub/devices/{id}/status         ← ESP32 reports health
```

**Hardware needed to start:**

| Item | Role | Cost |
|---|---|---|
| Raspberry Pi 5 (8GB) | Smart Hub — runs `lit serve` + hub agent | ~$80 |
| ESP32-S3 DevKit N16R8 (×3) | Leaf nodes — 8MB PSRAM, good for MicroPython | ~$12 each |
| MicroSD 32GB | Hub OS + model storage | ~$8 |

**Total: ~$124 for a working 4-node testbed**

**Contribution:** `feat(agent-micropython): MicroPython MQTT agent for ESP32-S3`

---

### Phase 6 — LiteRT Upstream Contributions (Month 7–9)

**Goal:** Contribute upstream to `google-ai-edge/LiteRT` based on real gaps discovered
while building the hub. Your Phase 1 work (fixing `mem_mb: 0.0`) becomes the motivation.

**Contribution 1 — Runtime metrics API**

The `mem_mb: 0.0` fix in Phase 1 uses `/proc` as a workaround. The correct fix is upstream:

```
GET /v1beta/runtime/metrics

Response:
{
  "rss_mb": 412.3,
  "model_loaded_mb": 380.1,
  "peak_mb": 445.0,
  "active_requests": 2,
  "uptime_seconds": 3601
}
```

Open the GitHub issue first with your /proc workaround as a proof of concept.
Show the data exists and is useful. Then offer the PR.

**Contribution 2 — Evaluation tooling documentation**

Your `tools/eval/` from Phase 1 tests edge model quality. Document the methodology
upstream — LiteRT has no official guidance on evaluation cycles for production deployments.
Your production experience at Media-Markt Saturn (caught quality regression in week 3)
gives this credibility.

**Contribution 3 — MicroPython integration guide**

Document the full pipeline: `lit serve` on Pi 5 → MQTT bridge → MicroPython ESP32.
This is the "LiteRT for makers" guide that does not exist yet.

**Upstream repos to target:**
- `google-ai-edge/LiteRT` — metrics API, evaluation docs, MicroPython integration guide
- `micropython/micropython` — if you hit any MQTT/TLS gaps on ESP32-S3

---

### Phase 7 — Community & Visibility (Month 9+)

With everything above working, you have a unique positioning:

**The bridge builder:** Enterprise AI engineer (RAG, LangGraph, GCP, ServiceNow) who
built the stack that connects a LiteRT-powered hub to a fleet of ESP32 nodes and wired
the whole thing into enterprise IT governance.

**Write:**
- "Why I brought LangGraph to the edge: routing AI workloads across ESP32 clusters"
- "Closing the observability gap in LiteRT's lit serve: what mem_mb: 0.0 cost us"
- "ServiceNow as the control plane for your edge AI fleet"

**Extend the Grafana dashboard** (`monitoring/dashboards/fleet-overview.json`):
- Hub intelligence panel: query classification distribution
- Per-device cost panel: tokens consumed × $/token
- ServiceNow panel: open incidents by device

**Open issues to attract contributors:**
- STM32/nRF52 agent (same MQTT pattern, different SDK, needs C developer)
- Zigbee/BLE transport for battery-powered nodes
- n8n workflow templates for common fleet automation (your n8n experience)

---

## Contribution Dependency Graph

```
[Phase 0] Understand the stack
     │
     ▼
[Phase 1] Observability layer ──────────────────► LiteRT upstream metrics PR (Phase 6)
     │    (your strongest entry point)
     ▼
[Phase 2] Python fleet simulator
     │    (validates gRPC protocol, enables CI testing)
     ▼
[Phase 3] ServiceNow integration bridge
     │    (your unique contribution — nobody else has this background)
     ▼
[Phase 4] Hub intelligence layer (LangGraph)
     │    (your production RAG/routing experience applied to the edge)
     ▼
[Phase 5] ESP32 MicroPython agent
     │    (hardware track — Python first, C/FreeRTOS optional later)
     ▼
[Phase 6] LiteRT upstream
     │    (backed by Phases 1 + 5 as concrete justification)
     ▼
[Phase 7] Community
          (unique position: enterprise AI + edge hardware + maker background)
```

---

## C / FreeRTOS — Honest Assessment

The original roadmap assumed C/FreeRTOS proficiency. Your CV shows your maker background
(Cairo Maker Fair, TIEC winner) but C is not in your listed languages.

**The right path:**
1. Start with MicroPython on ESP32 (Phase 5) — it is Python, you know it
2. Once the MicroPython agent works end-to-end, reading the ESP-IDF C examples becomes
   much less intimidating — you know what the code is trying to do
3. C/FreeRTOS is a Phase 8+ investment, not a prerequisite

**If you want to accelerate the C track:** The FreeRTOS task model maps cleanly to Python's
`asyncio`. Every `xTaskCreate` is an `asyncio.create_task`. Every `xQueueSend` is an
`asyncio.Queue.put_nowait`. Once you see the pattern in Python first, the C translation
is mechanical.

---

## Summary

| Phase | What you build | Skill used | Unique value |
|---|---|---|---|
| 0 | Understand the stack | Reading, systems thinking | Foundation |
| 1 | Observability & quality layer | LangFuse expertise | Closes real gaps |
| 2 | Python fleet simulator | Strong Python | Load testing, CI, demos |
| 3 | ServiceNow integration bridge | Expert ServiceNow | Nobody else can build this |
| 4 | Hub intelligence (LangGraph) | Production LangGraph | Makes the hub actually smart |
| 5 | ESP32 MicroPython agent | Python → hardware | Closes the leaf-node gap |
| 6 | LiteRT upstream PRs | Practitioner credibility | Metrics API, eval docs |
| 7 | Community & visibility | All of the above | Unique positioning |

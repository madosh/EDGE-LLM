# LiteOps — Project Brief

**The project that combines everything you know into a niche nobody has built yet.**

---

## The One-Paragraph Pitch

LiteRT (`lit serve`) is Google's edge AI runtime — it runs Gemma and other models on Pixel phones,
Raspberry Pi boards, and ARM edge hardware. It has no production operations story. No memory metrics,
no quality evaluation, no fleet management, no enterprise integration, no IoT device support.
**LiteOps** is the open-source Python framework that fills that gap: a production observability,
evaluation, and fleet management layer for LiteRT deployments, with a MicroPython agent for ESP32
leaf nodes and a ServiceNow connector for enterprise governance. It is the missing operations layer
between "I ran `lit serve`" and "I run LiteRT in production across 200 edge devices."

---

## Why This Niche Is Open

| What exists today | What is missing |
|---|---|
| `lit serve` runs a model and exposes Gemini-compatible API | No memory/CPU metrics from the server |
| Cami Fleet manages device fleets | No AI quality evaluation per device |
| LangFuse traces LLM calls in the cloud | No equivalent for edge inference |
| ServiceNow manages enterprise IT | No connector to edge AI fleet events |
| MicroPython runs on ESP32 | No MQTT agent that speaks the Cami Fleet protocol |
| Grafana visualises infrastructure | No LiteRT-specific dashboard templates |

You are not competing with any existing project. You are connecting them.

---

## Your Unique Qualification

No one else has this exact combination to build this project:

```
Production LLM observability (LangFuse, LangFuse, prompt versioning at Media-Markt Saturn)
        +
Enterprise IT integration (ServiceNow production work at VOIS and Schwarz Group)
        +
Agentic workflow design (LangGraph multi-step routing in production)
        +
Edge hardware intuition (Cairo Maker Fair, ESP32 maker background)
        +
Cloud/DevOps (GCP/Vertex AI, Docker, Kubernetes, CI/CD)
        +
Data pipeline architecture (Kafka, Spark, ETL at CyberMAK and VOIS)
```

This combination does not exist in the LiteRT community. The people who know LiteRT well are
Google engineers who have never built a production ServiceNow integration. The people who know
ServiceNow are enterprise architects who have never touched `lit serve`. You are the bridge.

---

## The Project: LiteOps

### What it is

A standalone open-source Python package and ecosystem that wraps LiteRT deployments
with the operations layer they need for production use:

```
pip install liteops
```

### GitHub structure (your own repo, not a fork)

```
github.com/{you}/liteops
├── liteops/                     Python package — the core library
│   ├── __init__.py
│   ├── observer.py              wraps lit serve, captures real metrics
│   ├── evaluator.py             quality evaluation loop (LangFuse-inspired)
│   ├── router.py                LangGraph query router for hub-level routing
│   ├── fleet.py                 Cami Fleet client (thin wrapper)
│   └── connectors/
│       ├── servicenow.py        enterprise IT integration
│       ├── prometheus.py        Prometheus metrics exporter
│       └── langfuse.py          optional LangFuse bridge for cloud-side tracing
├── liteops_esp32/               MicroPython package for ESP32 leaf nodes
│   ├── agent.py                 MQTT agent (heartbeat + telemetry + OTA)
│   ├── telemetry.py             ESP32 hardware metrics
│   └── ota.py                   receive model updates from hub
├── dashboards/                  Grafana JSON templates (drop-in, no config needed)
│   ├── fleet_overview.json
│   ├── device_quality.json
│   └── servicenow_bridge.json
├── examples/
│   ├── quickstart/              single Pi + 1 ESP32 in 15 minutes
│   ├── retail_edge/             inspired by your Media-Markt experience
│   └── enterprise/              full ServiceNow + fleet integration
├── tests/
├── pyproject.toml               pip install liteops
└── docs/
```

---

## Full Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                      ENTERPRISE LAYER                               │
│                                                                     │
│   ServiceNow ◄──── LiteOps ServiceNow Connector ◄──── Cami Fleet   │
│   (incidents · CMDB · change records)        (webhook events)       │
│                                                                     │
│   LangFuse (optional) ◄──── LiteOps LangFuse Bridge                │
│   (cloud-side trace correlation)                                    │
└─────────────────────────────┬───────────────────────────────────────┘
                              │
┌─────────────────────────────▼───────────────────────────────────────┐
│                      FLEET LAYER                                    │
│                   Cami Fleet Control Plane                          │
│   LiteOps fleet.py ────────► REST/gRPC API                         │
│   (device registry · deployments · telemetry)                      │
│   LiteOps observer.py ──────► ClickHouse (enriched metrics)        │
│   LiteOps evaluator.py ─────► quality scores per device            │
└─────────────────────────────┬───────────────────────────────────────┘
                              │  gRPC / mTLS
┌─────────────────────────────▼───────────────────────────────────────┐
│                        HUB LAYER                                    │
│              Raspberry Pi 5 · Jetson Nano · Coral                   │
│                                                                     │
│   LiteRT (lit serve) ◄──── LiteOps observer.py                     │
│     port 8000                 (wraps every request, captures        │
│     Gemini API                 real memory, latency, quality)       │
│                                                                     │
│   LiteOps router.py                                                 │
│     LangGraph graph:                                                │
│       classify → route to LiteRT or to ESP32 leaf                  │
│       aggregate responses from multiple ESP32s                      │
│       report enriched telemetry upstream                            │
└─────────────────────────────┬───────────────────────────────────────┘
                              │  MQTT (Mosquitto, port 1883)
              ┌───────────────┼──────────────┐
              │               │              │
  ┌───────────▼──┐  ┌─────────▼──┐  ┌───────▼──────┐
  │  ESP32-S3    │  │  ESP32-S3  │  │  ESP32-CAM   │
  │  MicroPython │  │ MicroPython│  │ MicroPython  │
  │  liteops_esp32│ │ Sensors    │  │ Vision       │
  └──────────────┘  └────────────┘  └──────────────┘
```

---

## The Five Core Modules

### 1. `liteops.observer` — Fix the `mem_mb: 0.0` problem

**The gap:** `agent/src/model_runtime.rs:230` hardcodes `mem_mb: 0.0` because LiteRT has
no memory metrics API. This means every LiteRT deployment in Cami Fleet shows zero memory.

**LiteOps solution:** A Python observer that wraps every `lit serve` request, reads
`/proc/{pid}/status` for real memory, and exports all metrics to Prometheus:

```python
# liteops/observer.py
import httpx, psutil, time, asyncio
from prometheus_client import Gauge, Histogram, Counter

# These become real dashboard data instead of 0.0
LITERT_MEMORY_RSS    = Gauge("liteops_litert_memory_rss_mb", "LiteRT RSS memory", ["device_id", "model"])
LITERT_MEMORY_PEAK   = Gauge("liteops_litert_memory_peak_mb", "LiteRT peak memory", ["device_id", "model"])
LITERT_LATENCY       = Histogram("liteops_litert_latency_ms", "End-to-end latency", ["device_id", "model"])
LITERT_TOKENS_TOTAL  = Counter("liteops_litert_tokens_total", "Tokens generated", ["device_id", "model"])
LITERT_QUALITY_SCORE = Gauge("liteops_litert_quality_score", "Output quality 0-1", ["device_id", "model"])

class LiteRTObserver:
    """
    Transparent proxy in front of lit serve.
    Captures memory, latency, token counts, and quality scores.
    Exposes /metrics for Prometheus scraping.
    Fixes the mem_mb: 0.0 gap that exists in every LiteRT deployment today.
    """

    def __init__(self, litert_url: str, device_id: str, litert_pid: int):
        self.litert_url = litert_url
        self.device_id  = device_id
        self.litert_pid = litert_pid  # PID of the lit serve process

    async def generate(self, model: str, prompt: str, **kwargs) -> dict:
        proc   = psutil.Process(self.litert_pid)
        mem_before = proc.memory_info().rss / 1024 / 1024  # MB

        start  = time.perf_counter()
        resp   = await self._call_litert(model, prompt, **kwargs)
        latency_ms = (time.perf_counter() - start) * 1000

        mem_after = proc.memory_info().rss / 1024 / 1024
        tokens    = resp.get("usageMetadata", {}).get("candidatesTokenCount", 0)

        # Update Prometheus metrics
        LITERT_MEMORY_RSS.labels(self.device_id, model).set(mem_after)
        LITERT_MEMORY_PEAK.labels(self.device_id, model).set(
            max(mem_before, mem_after)
        )
        LITERT_LATENCY.labels(self.device_id, model).observe(latency_ms)
        LITERT_TOKENS_TOTAL.labels(self.device_id, model).inc(tokens)

        return resp

    async def _call_litert(self, model: str, prompt: str, **kwargs) -> dict:
        url = f"{self.litert_url}/v1beta/models/{model}:generateContent"
        payload = {
            "contents": [{"parts": [{"text": prompt}]}],
            "generationConfig": {"maxOutputTokens": kwargs.get("max_tokens", 256)},
        }
        async with httpx.AsyncClient(timeout=60) as client:
            r = await client.post(url, json=payload)
            r.raise_for_status()
            return r.json()
```

**This becomes a Cami Fleet PR** to replace `mem_mb: 0.0` at line 230 with real data.
**This becomes a LiteRT upstream issue** requesting a native `/v1beta/runtime/metrics` endpoint.

---

### 2. `liteops.evaluator` — Quality evaluation at the edge

**The gap:** There is no equivalent of your LangFuse evaluation cycle for edge models.
Models can drift silently after OTA updates. Nobody catches it before users do.

**The lesson from your Media-Markt experience:** "The first version of the system gave
confident, fluent answers that were factually wrong about half the time." That early failure
shaped every subsequent decision. Edge models have the same failure mode.

```python
# liteops/evaluator.py
import json, pathlib
from dataclasses import dataclass
from liteops.observer import LiteRTObserver

@dataclass
class EvalResult:
    question_id:   str
    score:         float   # 0.0 – 1.0
    grounded:      bool    # answer cites source content
    latency_ms:    float
    model_id:      str
    device_id:     str
    passed:        bool    # score >= threshold

class EdgeEvaluator:
    """
    Runs a fixed question bank against a deployed edge model.
    Catches quality regressions before users see them.
    Inspired by the evaluation cycle that ran before every release at Media-Markt Saturn.
    """

    def __init__(self, observer: LiteRTObserver, question_bank_path: str,
                 pass_threshold: float = 0.75):
        self.observer   = observer
        self.questions  = json.loads(pathlib.Path(question_bank_path).read_text())
        self.threshold  = pass_threshold

    async def run(self, model_id: str) -> list[EvalResult]:
        results = []
        for q in self.questions:
            resp = await self.observer.generate(model_id, q["prompt"])
            answer = self._extract_text(resp)
            score  = self._score(answer, q["expected_keywords"])
            results.append(EvalResult(
                question_id = q["id"],
                score       = score,
                grounded    = self._is_grounded(answer, q.get("source_content", "")),
                latency_ms  = resp.get("_latency_ms", 0),
                model_id    = model_id,
                device_id   = self.observer.device_id,
                passed      = score >= self.threshold,
            ))
        return results

    def _score(self, answer: str, keywords: list[str]) -> float:
        hits = sum(1 for k in keywords if k.lower() in answer.lower())
        return hits / len(keywords) if keywords else 0.0

    def _is_grounded(self, answer: str, source: str) -> bool:
        # simple overlap check — enough for edge model regression detection
        if not source:
            return True
        source_words = set(source.lower().split())
        answer_words = set(answer.lower().split())
        overlap = len(source_words & answer_words) / len(source_words)
        return overlap > 0.3
```

**Question bank format** (`eval/question_bank.json`):

```json
[
  {
    "id": "q001",
    "prompt": "What is the maximum operating temperature of sensor node alpha?",
    "expected_keywords": ["85", "celsius", "operating"],
    "source_content": "Sensor node alpha operates between -20°C and 85°C."
  },
  {
    "id": "q002",
    "prompt": "Which hub is responsible for zone B devices?",
    "expected_keywords": ["hub-02", "zone-b"],
    "source_content": "Hub-02 manages all devices in zone B."
  }
]
```

**Integration:** Evaluator runs automatically after every OTA deployment via a Cami Fleet
deployment hook. If `passed_rate < 0.8`, it triggers a rollback and fires a ServiceNow incident.

---

### 3. `liteops.router` — LangGraph intelligence on the hub

**The gap:** The hub currently just proxies requests to LiteRT. There is no intelligence
about which requests go to LiteRT, which go to a local ESP32, and which need aggregation.

**Your biggest win at Media-Markt Saturn was a routing step.** Same idea, applied to hardware.

```python
# liteops/router.py
from langgraph.graph import StateGraph, END
from typing import TypedDict, Literal

class HubState(TypedDict):
    request:        str
    request_type:   Literal["inference", "sensor_read", "fleet_command", "unknown"]
    target_devices: list[str]
    responses:      list[dict]
    final_answer:   str

def build_hub_router(litert_observer, esp32_fleet, litert_model: str):
    """
    LangGraph graph that routes hub requests intelligently:
    - inference requests   → LiteRT (lit serve)
    - sensor reads         → specific ESP32 leaf nodes
    - fleet commands       → broadcast to all matched devices
    - aggregation requests → fan out + merge
    """
    graph = StateGraph(HubState)

    graph.add_node("classify",           _classify_request)
    graph.add_node("route_to_litert",    _make_litert_handler(litert_observer, litert_model))
    graph.add_node("route_to_leaf",      _make_leaf_handler(esp32_fleet))
    graph.add_node("aggregate",          _aggregate_responses)
    graph.add_node("format_response",    _format_response)

    graph.set_entry_point("classify")
    graph.add_conditional_edges("classify", _routing_decision, {
        "inference":      "route_to_litert",
        "sensor_read":    "route_to_leaf",
        "fleet_command":  "route_to_leaf",
        "aggregation":    "route_to_leaf",
    })
    graph.add_edge("route_to_litert", "format_response")
    graph.add_edge("route_to_leaf",   "aggregate")
    graph.add_edge("aggregate",       "format_response")
    graph.add_edge("format_response", END)

    return graph.compile()

def _classify_request(state: HubState) -> HubState:
    req = state["request"].lower()
    if any(w in req for w in ["temperature", "humidity", "sensor", "read"]):
        state["request_type"] = "sensor_read"
    elif any(w in req for w in ["all devices", "fleet", "broadcast"]):
        state["request_type"] = "fleet_command"
    else:
        state["request_type"] = "inference"
    return state
```

**Why this matters for the community:** This is the first documented pattern for using
LangGraph as an edge inference router. Blog post title: "I put a LangGraph agent on a
Raspberry Pi and it made my ESP32 fleet smarter."

---

### 4. `liteops.connectors.servicenow` — Enterprise governance bridge

**The gap:** Cami Fleet's webhook notifier (`control-plane/internal/notify/webhook.go`)
fires `device.offline` and `deployment.failed` events. There is no enterprise-grade
consumer that turns those into governed IT records.

```python
# liteops/connectors/servicenow.py
import requests, logging
from dataclasses import dataclass
from typing import Optional

@dataclass
class SNConfig:
    instance_url:     str        # https://yourcompany.service-now.com
    username:         str
    password:         str
    assignment_group: str        # "Edge AI Operations"
    cmdb_table:       str        # "u_cmdb_ci_edge_ai_device" (custom) or "cmdb_ci_computer"

class ServiceNowConnector:
    """
    Receives Cami Fleet webhook events and creates governed IT records.
    Built on production ServiceNow patterns from VOIS and Schwarz Group.
    """

    EVENT_PRIORITY = {
        "device.offline":              "3",   # P3 — moderate impact
        "deployment.failed":           "2",   # P2 — significant impact
        "deployment.sha256_mismatch":  "1",   # P1 — security event
        "hub.offline":                 "2",   # P2 — cluster impact
    }

    def handle_webhook(self, payload: dict):
        event = payload.get("event")
        handlers = {
            "device.offline":              self.create_incident,
            "deployment.failed":           self.create_incident,
            "deployment.sha256_mismatch":  self._create_security_incident,
        }
        handler = handlers.get(event)
        if handler:
            handler(payload)
        self.sync_device_to_cmdb(payload.get("device_id"))

    def create_incident(self, payload: dict) -> str:
        incident = {
            "short_description": f"[Edge AI] {payload['event']} — {payload['device_id'][:8]}",
            "description":       payload.get("message", ""),
            "category":          "Edge AI",
            "subcategory":       "Device Fleet",
            "impact":            self.EVENT_PRIORITY.get(payload["event"], "3"),
            "urgency":           self.EVENT_PRIORITY.get(payload["event"], "3"),
            "assignment_group":  self.config.assignment_group,
            "u_device_id":       payload.get("device_id", ""),
            "u_hub_id":          payload.get("hub_id", ""),
            "u_model_id":        payload.get("model_id", ""),
        }
        r = self._post("incident", incident)
        logging.info("ServiceNow incident created: %s", r.get("number"))
        return r.get("number", "")

    def sync_device_to_cmdb(self, device_id: Optional[str]):
        """Keep CMDB in sync with Cami Fleet device registry."""
        if not device_id:
            return
        fleet_device = self._get_fleet_device(device_id)
        ci = {
            "name":              fleet_device["device_id"],
            "u_edge_ai_type":    fleet_device.get("tags", {}).get("type", "edge-ai"),
            "u_hub_id":          fleet_device.get("parent_hub_id", ""),
            "u_model_id":        fleet_device.get("current_model", ""),
            "u_last_heartbeat":  fleet_device.get("last_seen", ""),
            "location":          fleet_device.get("tags", {}).get("location", ""),
            "ip_address":        fleet_device.get("tags", {}).get("ip", ""),
        }
        self._upsert(self.config.cmdb_table, "name", fleet_device["device_id"], ci)
        logging.info("CMDB CI synced for device %s", device_id[:8])

    def _post(self, table: str, payload: dict) -> dict:
        r = requests.post(
            f"{self.config.instance_url}/api/now/table/{table}",
            auth=(self.config.username, self.config.password),
            json=payload,
            headers={"Content-Type": "application/json", "Accept": "application/json"},
        )
        r.raise_for_status()
        return r.json().get("result", {})

    def _upsert(self, table: str, key_field: str, key_value: str, payload: dict):
        existing = requests.get(
            f"{self.config.instance_url}/api/now/table/{table}",
            auth=(self.config.username, self.config.password),
            params={f"sysparm_query": f"{key_field}={key_value}", "sysparm_limit": "1"},
        ).json().get("result", [])

        if existing:
            sys_id = existing[0]["sys_id"]
            requests.patch(
                f"{self.config.instance_url}/api/now/table/{table}/{sys_id}",
                auth=(self.config.username, self.config.password),
                json=payload,
            )
        else:
            self._post(table, payload)
```

---

### 5. `liteops_esp32` — MicroPython MQTT agent

**The leaf node agent that closes the hardware loop.**

```python
# liteops_esp32/agent.py  (MicroPython — runs on ESP32-S3)
import time, json, network, gc
from umqtt.simple import MQTTClient

class LiteOpsAgent:
    """
    Minimal MicroPython agent for ESP32 leaf nodes.
    Speaks MQTT to a LiteOps hub. No gRPC, no TLS overhead.
    ~150 lines. Runs on ESP32-S3 with 8MB PSRAM.
    """

    HEARTBEAT_INTERVAL_S  = 10
    TELEMETRY_INTERVAL_S  = 5

    def __init__(self, device_id: str, hub_ip: str, wifi_ssid: str, wifi_pass: str):
        self.device_id = device_id
        self.hub_ip    = hub_ip
        self._connect_wifi(wifi_ssid, wifi_pass)
        self.mqtt = MQTTClient(device_id, hub_ip, port=1883, keepalive=30)
        self.mqtt.set_callback(self._on_message)
        self.mqtt.connect()
        self.mqtt.subscribe(f"liteops/devices/{device_id}/deploy")
        self._register()

    def run(self):
        last_hb  = 0
        last_tel = 0
        while True:
            now = time.time()
            self.mqtt.check_msg()
            if now - last_hb  >= self.HEARTBEAT_INTERVAL_S:
                self._heartbeat()
                last_hb = now
            if now - last_tel >= self.TELEMETRY_INTERVAL_S:
                self._telemetry()
                last_tel = now
            time.sleep_ms(100)

    def _register(self):
        import os
        self.mqtt.publish(
            f"liteops/devices/{self.device_id}/register",
            json.dumps({"type": "esp32-s3", "firmware": "1.0.0",
                        "free_heap": gc.mem_free()}),
        )

    def _heartbeat(self):
        self.mqtt.publish(
            f"liteops/devices/{self.device_id}/heartbeat",
            json.dumps({"ts": time.time()}),
        )

    def _telemetry(self):
        import machine
        self.mqtt.publish(
            f"liteops/devices/{self.device_id}/telemetry",
            json.dumps({
                "free_heap_kb": gc.mem_free() / 1024,
                "temp_c":       (machine.temperature() - 32) / 1.8
                                if hasattr(machine, "temperature") else None,
                "uptime_s":     time.time(),
            }),
        )

    def _on_message(self, topic: bytes, msg: bytes):
        topic = topic.decode()
        if "/deploy" in topic:
            payload = json.loads(msg)
            self._handle_ota(payload)

    def _handle_ota(self, payload: dict):
        # Receive model URL → download → verify SHA256 → apply
        import urequests, hashlib
        url = payload.get("artifact_url")
        expected_sha = payload.get("artifact_sha256")
        if not url:
            return
        r = urequests.get(url)
        actual_sha = hashlib.sha256(r.content).hexdigest()
        if actual_sha == expected_sha:
            # write to flash and reboot
            with open("/model.bin", "wb") as f:
                f.write(r.content)
            import machine
            machine.reset()

    def _connect_wifi(self, ssid: str, password: str):
        wlan = network.WLAN(network.STA_IF)
        wlan.active(True)
        if not wlan.isconnected():
            wlan.connect(ssid, password)
            timeout = 10
            while not wlan.isconnected() and timeout > 0:
                time.sleep(1)
                timeout -= 1
```

**Usage on the device** (`boot.py`):

```python
# boot.py — runs on every ESP32 power-up
from liteops_esp32.agent import LiteOpsAgent

agent = LiteOpsAgent(
    device_id = "esp32-zone-b-01",
    hub_ip    = "192.168.1.10",
    wifi_ssid = "EdgeNetwork",
    wifi_pass = "secret",
)
agent.run()
```

---

## Upstream LiteRT Contributions

These are the PRs and issues you open on `google-ai-edge/LiteRT` once your observer
has real data to back them up. Your `liteops.observer` is the proof-of-concept.

### Issue 1 — Request runtime metrics API

```markdown
Title: [Feature Request] Add /v1beta/runtime/metrics endpoint to lit serve

**Problem:**
lit serve exposes no memory, CPU, or throughput metrics via its HTTP API.
Every deployment shows mem_mb: 0.0 (see: cami-fleet/agent/src/model_runtime.rs:230).
This makes production monitoring impossible without OS-level workarounds.

**Workaround I'm using today:**
Reading /proc/{pid}/status from outside the process and exporting via Prometheus.
This works but is fragile (PID must be known, breaks in containers without /proc).

**Proposed endpoint:**
GET /v1beta/runtime/metrics
{
  "rss_mb": 412.3,
  "model_loaded_mb": 380.1,
  "peak_mb": 445.0,
  "active_requests": 2,
  "model_id": "gemma2-2b-it-q4",
  "uptime_seconds": 3601
}

**I am willing to implement this** if there is guidance on the preferred
Go or C++ module for adding endpoints to lit serve.
```

**Open this issue before writing the PR.** Get confirmation the direction is right first.

### Issue 2 — Request evaluation hooks

```markdown
Title: [Feature Request] Post-generation evaluation callback / webhook for lit serve

**Problem:**
In production edge deployments, model quality can degrade silently after OTA updates.
There is no hook to run a quality check after each generation or after model load.

**Proposed:**
Optional --eval-webhook flag on lit serve. After model load, fires POST to the
provided URL with a standard prompt and expects a quality score in return.
Allows deployment pipelines to gate on quality before marking a deployment successful.
```

### PR 1 — Runtime metrics endpoint

After issue 1 gets traction, implement it. Your observer code already proves the data
exists — you are just moving the collection inside the server.

### PR 2 — MicroPython integration guide in official docs

Document the full stack: `lit serve` on Pi 5 → MQTT bridge → MicroPython ESP32.
This is the "LiteRT for makers" guide the project currently lacks. Attach your
working `liteops_esp32` code as the reference implementation.

---

## Phased Development Plan

### Phase 1 — Observer (Weeks 1–3)
**Goal:** Fix `mem_mb: 0.0`. Ship real metrics to Prometheus and Grafana.

| Task | File | PR target |
|---|---|---|
| Implement `LiteRTObserver` | `liteops/observer.py` | New repo |
| Prometheus metrics definitions | `liteops/observer.py` | New repo |
| Grafana dashboard template | `dashboards/fleet_overview.json` | New repo |
| Fix `mem_mb: 0.0` in Cami Fleet | `agent/src/model_runtime.rs:230` | This repo |
| Open LiteRT issue #1 | GitHub issue | `google-ai-edge/LiteRT` |

**Deliverable:** A Grafana panel that shows real LiteRT memory usage for the first time.
That screenshot becomes the opening image of your first blog post.

---

### Phase 2 — Evaluator (Weeks 4–6)
**Goal:** Automatic quality gates on every edge model deployment.

| Task | File | PR target |
|---|---|---|
| Implement `EdgeEvaluator` | `liteops/evaluator.py` | New repo |
| Question bank format + examples | `eval/question_bank.json` | New repo |
| Deployment hook in Cami Fleet | `control-plane/internal/api/grpc/` | This repo |
| Open LiteRT issue #2 | GitHub issue | `google-ai-edge/LiteRT` |

**Deliverable:** Automatic rollback if quality drops below threshold after OTA update.

---

### Phase 3 — ServiceNow Connector (Weeks 7–9)
**Goal:** Enterprise IT governance for edge AI events.

| Task | File | PR target |
|---|---|---|
| Implement `ServiceNowConnector` | `liteops/connectors/servicenow.py` | New repo |
| CMDB sync logic | `liteops/connectors/servicenow.py` | New repo |
| Webhook receiver (FastAPI) | `liteops/webhook_server.py` | New repo |
| Expand Cami Fleet webhook events | `control-plane/internal/notify/webhook.go` | This repo |
| Example: retail_edge | `examples/retail_edge/` | New repo |

**Deliverable:** Device goes offline → ServiceNow incident created automatically.
SHA-256 mismatch on deployment → P1 security incident with artifact hash in description.

**This is your demo:** Run it live and show: device offline event fires in Cami Fleet →
webhook hits your connector → ServiceNow incident appears in real time.
That is what you demo at a conference. That is what goes in your CV.

---

### Phase 4 — LangGraph Hub Router (Weeks 10–12)
**Goal:** Intelligent query routing on the hub.

| Task | File | PR target |
|---|---|---|
| Implement `build_hub_router` | `liteops/router.py` | New repo |
| Classification node | `liteops/router.py` | New repo |
| ESP32 leaf handler | `liteops/router.py` | New repo |
| Hub mode flag in Cami Fleet agent | `agent/src/main.rs` | This repo |
| Example: hub routing demo | `examples/quickstart/` | New repo |

**Deliverable:** A Raspberry Pi that classifies incoming requests and routes them to
either LiteRT (heavy inference) or the appropriate ESP32 (sensor reads).

---

### Phase 5 — MicroPython ESP32 Agent (Weeks 13–17)
**Goal:** Real hardware in the loop.

| Task | File | PR target |
|---|---|---|
| Implement `LiteOpsAgent` | `liteops_esp32/agent.py` | New repo |
| OTA update handler | `liteops_esp32/ota.py` | New repo |
| Hardware metrics | `liteops_esp32/telemetry.py` | New repo |
| MQTT topic schema doc | `docs/mqtt-protocol.md` | New repo |
| Hub MQTT bridge | `liteops/hub_bridge.py` | New repo |

**Hardware needed:**

| Item | Purpose | Cost |
|---|---|---|
| Raspberry Pi 5 (8GB) | Hub — runs lit serve + LiteOps | ~$80 |
| ESP32-S3-DevKitC-1 N16R8 (×3) | Leaf nodes — 8MB PSRAM, good MicroPython | ~$12 each |
| MicroSD 32GB (Class 10) | Hub OS + model storage | ~$8 |
| USB-C power hub (65W) | Power all boards from one outlet | ~$20 |

**Total: ~$144 for a working 4-node testbed**

**Deliverable:** Flash MicroPython to ESP32-S3, run `boot.py`, watch the device appear
in the Cami Fleet dashboard. That is the end-to-end demo of the full stack.

---

### Phase 6 — LiteRT Upstream PRs (Weeks 18–24)
**Goal:** Contribute the metrics API and docs upstream.

| Task | Target |
|---|---|
| PR: `/v1beta/runtime/metrics` endpoint | `google-ai-edge/LiteRT` |
| PR: MicroPython integration guide | `google-ai-edge/LiteRT` docs |
| PR: Production deployment guide | `google-ai-edge/LiteRT` docs |
| Blog post #1: Observer + memory metrics | Medium / dev.to / your site |
| Blog post #2: LangGraph on the edge | Medium / dev.to |
| Blog post #3: ServiceNow meets edge AI | LinkedIn long post |

---

### Phase 7 — Package, Publish, Promote (Month 7+)

```bash
# Publish to PyPI
pip install build twine
python -m build
twine upload dist/*

# Result: pip install liteops
```

| Task | Impact |
|---|---|
| `pip install liteops` on PyPI | Anyone can use it in 30 seconds |
| GitHub Actions CI for the new repo | Shows production-quality open source practice |
| Grafana dashboard on grafana.com | Passive discovery by the Grafana community |
| Conference talk proposal | "Production Edge AI Operations: From `lit serve` to ServiceNow" |
| Open issues for STM32, nRF52, BLE | Attract contributors you can mentor |

---

## What This Looks Like on Your CV in 12 Months

### GitHub profile

- `liteops` — Python package with real GitHub stars from the LiteRT community
- PRs merged into `google-ai-edge/LiteRT` — your name in Google's edge AI project
- Contributions to `madosh/EDGE-LLM` (Cami Fleet) — visible across the stack

### LinkedIn headline option

> Senior AI Engineer · Edge AI Operations · LiteRT · LangGraph · ServiceNow Integration

### CV bullet points (under a "Personal Projects" or "Open Source" section)

```
LiteOps — Open Source Edge AI Operations Framework  (github.com/{you}/liteops)
• Built the production observability layer for Google's LiteRT edge AI runtime —
  fixing the memory metrics gap present in every LiteRT deployment (mem_mb: 0.0).
• LangGraph query router on Raspberry Pi hub: classifies and routes inference
  requests across a fleet of ESP32 leaf nodes, reducing LiteRT load by ~40%.
• ServiceNow connector: device fleet events → governed IT records (incidents,
  CMDB sync, change records) — making LiteRT deployable inside enterprise IT.
• MicroPython MQTT agent for ESP32-S3: 150-line leaf node that registers,
  streams telemetry, and receives OTA model updates.
• Upstream contributions: opened and implemented /v1beta/runtime/metrics
  in google-ai-edge/LiteRT (PR merged); authored production deployment guide.
• Published on PyPI: pip install liteops. Grafana dashboard on grafana.com.
```

### Positioning in interviews

You are not "an AI engineer who did a hobby project with Raspberry Pi."
You are **"the person who built the production operations layer that was missing from
Google's LiteRT"** — and you have a PyPI package, merged upstream PRs, and a working
ServiceNow integration to prove it. That is a very specific claim in a very specific niche.

---

## Dependency Graph

```
[Week 1–3]   Observer (mem_mb fix, Prometheus, Grafana)
     │
     ├──► Cami Fleet PR: fix mem_mb: 0.0
     ├──► LiteRT issue #1: request metrics API
     └──► Blog post #1: "Closing the observability gap in LiteRT"
          │
[Week 4–6]   Evaluator (quality gates, automatic rollback)
     │
     ├──► Cami Fleet PR: deployment evaluation hook
     └──► LiteRT issue #2: evaluation webhook request
          │
[Week 7–9]   ServiceNow Connector (incidents, CMDB, change records)
     │
     ├──► Cami Fleet PR: expand webhook event types
     └──► Blog post #3: "ServiceNow meets edge AI fleet management"
          │
[Week 10–12] LangGraph Router (hub intelligence)
     │
     ├──► Cami Fleet PR: hub mode in agent
     └──► Blog post #2: "LangGraph on a Raspberry Pi"
          │
[Week 13–17] MicroPython ESP32 Agent (real hardware)
     │
     ├──► Hardware testbed running end-to-end
     └──► Demo video: device registers → model deploys → ServiceNow incident
          │
[Week 18–24] LiteRT upstream PRs + PyPI publish
     │
     ├──► PR merged in google-ai-edge/LiteRT
     ├──► pip install liteops live
     └──► Conference talk / article about the full stack
          │
[Month 7+]   Community growth
     │
     └──► Others contribute STM32, nRF52, BLE, n8n workflow templates
```

---

## Starting Today

Three commands to start Phase 1 right now:

```bash
# 1. Create your new repo (locally — push to GitHub when ready)
mkdir liteops && cd liteops
git init
mkdir -p liteops/connectors liteops_esp32 dashboards examples/quickstart tests docs

# 2. Create the package skeleton
cat > pyproject.toml << 'EOF'
[build-system]
requires = ["setuptools>=68", "wheel"]
build-backend = "setuptools.backends.legacy:build"

[project]
name = "liteops"
version = "0.1.0"
description = "Production operations framework for LiteRT edge AI deployments"
requires-python = ">=3.11"
dependencies = [
    "httpx>=0.27",
    "psutil>=5.9",
    "prometheus-client>=0.20",
    "langchain>=0.2",
    "langgraph>=0.1",
    "langchain-openai>=0.1",
    "pydantic>=2.0",
]

[project.optional-dependencies]
servicenow = ["requests>=2.31"]
langfuse   = ["langfuse>=2.0"]
EOF

# 3. Get Cami Fleet running so you have a live LiteRT to observe
cd /path/to/EDGE-LLM && make up && make smoke
```

Then open `liteops/observer.py` and write the first 30 lines of `LiteRTObserver`.
That is the beginning of everything above.

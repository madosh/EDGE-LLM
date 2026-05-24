# Smart Hub Vision — Roadmap

**Hierarchical Edge AI: LiteRT-powered Hub managing a fleet of ESP32 sub-controllers**

---

## The Vision

A three-layer edge AI system where Cami Fleet (this project) becomes the cloud brain,
a LiteRT-powered Linux board acts as the local smart hub, and ESP32 microcontrollers
form the sensor/actuator leaf fleet — all unified under a single operator dashboard.

```
┌─────────────────────────────────────────────────────────────────┐
│                        CLOUD LAYER                              │
│                   Cami Fleet Control Plane                      │
│            Go REST API · gRPC · PostgreSQL · ClickHouse         │
│                    Grafana · Prometheus                         │
└────────────────────────────┬────────────────────────────────────┘
                             │  gRPC / mTLS (internet)
                             │
┌────────────────────────────▼────────────────────────────────────┐
│                         HUB LAYER                               │
│        Smart Hub  (Raspberry Pi 5 · Jetson Nano · Coral)        │
│                                                                 │
│   ┌──────────────┐   ┌─────────────────┐   ┌───────────────┐   │
│   │  LiteRT-LM   │   │   Hub Agent     │   │ MQTT Broker   │   │
│   │  (lit serve) │   │  (Rust / Go)    │   │  (Mosquitto)  │   │
│   │  port 8000   │◄──│  gRPC upstream  │   │  port 1883    │   │
│   │  Gemini API  │   │  MQTT downstream│──►│               │   │
│   └──────────────┘   └─────────────────┘   └───────┬───────┘   │
│                                                     │           │
│   Hub acts as: Cami Fleet agent (upstream)          │           │
│                Local fleet manager (downstream)     │           │
└─────────────────────────────────────────────────────┼───────────┘
                           WiFi / BLE / Zigbee        │
                    ┌──────────────┬──────────────────┘
                    │              │              │
        ┌───────────▼──┐  ┌───────▼──────┐  ┌───▼──────────┐
        │  ESP32 Node  │  │  ESP32 Node  │  │  ESP32-CAM   │
        │  FreeRTOS    │  │  FreeRTOS    │  │  FreeRTOS    │
        │  MQTT client │  │  TFLM infer  │  │  Vision ML   │
        │  Sensors     │  │  Actuators   │  │  MQTT stream │
        └──────────────┘  └──────────────┘  └──────────────┘
```

---

## Why This Architecture

| Challenge | Solution |
|---|---|
| ESP32 cannot run `lit serve` (needs NPU/GPU) | Hub runs LiteRT; ESP32 runs TFLM or delegates inference to hub |
| ESP32 cannot speak gRPC (RAM constraint) | ESP32 speaks MQTT to hub; hub bridges to gRPC upstream |
| Fleet of hundreds of ESP32s is unmanageable | Hub aggregates and reports a single device view per cluster |
| LiteRT memory usage is opaque (`mem_mb: 0.0`) | Hub-level memory monitoring closes this gap |
| No microcontroller support in Cami Fleet today | This roadmap adds it as a first-class citizen |

---

## How the Two Efforts Connect

```
Cami Fleet (this repo)          LiteRT (upstream: google-ai-edge/LiteRT)
─────────────────────           ────────────────────────────────────────
Hub device type             ←── lit serve metrics API (fix mem_mb gap)
Sub-device registration         TFLM for ESP32-S3 (Xtensa LX7)
Hierarchical telemetry      ←── Per-layer latency reporting
Deployment to hub           ←── Model packaging for TFLM (.tflite format)
OTA to ESP32 via hub        ←── Quantized model export tooling
```

Your contributions in this repo directly motivate and reference upstream LiteRT work.
They are not competing efforts — each one justifies the other.

---

## Phased Roadmap

### Phase 0 — Understand the Stack (Weeks 1–2)

**Goal:** Run the full system, trace a deployment end-to-end.

```bash
make up        # start everything
make smoke     # verify connectivity
make logs      # watch all services
```

Files to read in order:
1. `control-plane/proto/device.proto` — the gRPC contract every device must speak
2. `agent/src/main.rs` — what the existing agent does (read it as pseudocode)
3. `agent/src/model_runtime.rs:89–143` — the LiteRT HTTP client integration
4. `agent/src/telemetry.rs` — the 5-second metrics loop you will replicate on ESP32

**Deliverable:** Understand the full data flow: operator deploys model → control plane
notifies agent → agent downloads + verifies → agent loads into LiteRT → telemetry streams back.

---

### Phase 1 — Python Device Simulator (Month 1)

**Goal:** Build a Python script that simulates N ESP32-class devices simultaneously.
Leverages Python familiarity. Fills a real gap — only 2 stub agents exist today.

**What to build:**

```
tools/
└── simulator/
    ├── device.py          # single simulated device (gRPC client)
    ├── fleet.py           # spawn N devices concurrently with asyncio
    ├── profiles/
    │   ├── esp32.json     # ESP32-class constraints (64KB RAM, slow CPU)
    │   └── hub.json       # hub-class constraints (4GB RAM, NPU)
    └── README.md
```

**Implementation sketch:**

```python
# tools/simulator/device.py
import grpc
import asyncio
from device_pb2_grpc import DeviceServiceStub

# Generate stubs:
# python -m grpc_tools.protoc -I control-plane/proto \
#   --python_out=tools/simulator --grpc_python_out=tools/simulator \
#   control-plane/proto/device.proto

class SimulatedESP32:
    """Simulates an ESP32-class device: 240MHz, 512KB SRAM, WiFi."""

    def __init__(self, device_id: str, server_addr: str):
        self.device_id = device_id
        self.channel = grpc.aio.insecure_channel(server_addr)
        self.stub = DeviceServiceStub(self.channel)

    async def run(self):
        await self._register()
        await asyncio.gather(
            self._heartbeat_loop(),   # every 10s
            self._telemetry_loop(),   # every 5s
            self._deployment_watch(), # listen for model pushes
        )

    async def _telemetry_loop(self):
        # Simulate ESP32 constraints: low tokens/sec, limited RAM
        while True:
            await self.stub.StreamTelemetry(self._esp32_metrics())
            await asyncio.sleep(5)

    def _esp32_metrics(self):
        import random
        return {
            "tps": random.uniform(1.5, 8.0),     # ESP32 is slow
            "ttft_ms": random.uniform(400, 2000), # higher latency
            "mem_mb": random.uniform(0.1, 0.4),   # 512KB SRAM = ~0.5MB
        }
```

**Community value:** Load testing, CI without real hardware, demo environments.

**Cami Fleet PR:** `feat(tools): add Python fleet simulator with ESP32 device profiles`

---

### Phase 2 — ESP32 C Agent (Months 2–3)

**Goal:** A FreeRTOS-based C agent that runs on real ESP32 hardware and connects
to a hub running MQTT (not directly to the cloud gRPC server).

**Architecture maps directly to FreeRTOS patterns you already know:**

| FreeRTOS Concept | Cami Fleet Equivalent | Stack |
|---|---|---|
| `xTaskCreate(wifi_task)` | Network connectivity | ESP-IDF `wifi_init_sta()` |
| `xTaskCreate(mqtt_task)` | Hub communication | ESP-IDF `esp_mqtt_client` |
| `xTaskCreate(heartbeat_task)` | Liveness signal every 10s | MQTT publish |
| `xTaskCreate(telemetry_task)` | Metrics every 5s | MQTT publish |
| `xTaskCreate(ota_task)` | Receive model updates | MQTT subscribe + `esp_https_ota` |
| `xTaskCreate(infer_task)` | Run TFLM model | TensorFlow Lite Micro |
| `xQueueSend` | Pass metrics between tasks | FreeRTOS queue |
| `nvs_flash` | Store device ID, hub IP, certs | ESP-IDF NVS partition |

**Proposed file structure:**

```
agent-esp32/
├── CMakeLists.txt
├── sdkconfig.defaults        # ESP-IDF defaults (WiFi, MQTT, TLS, TFLM)
├── main/
│   ├── CMakeLists.txt
│   ├── main.c                # app_main: init + task launch
│   ├── config.h              # device ID, hub address, topic prefixes
│   ├── wifi.c / wifi.h       # WiFi STA init + reconnect logic
│   ├── mqtt_client.c         # connect to hub MQTT broker
│   ├── heartbeat.c           # 10s publish to hub/devices/{id}/heartbeat
│   ├── telemetry.c           # 5s publish to hub/devices/{id}/telemetry
│   ├── ota.c                 # subscribe to hub/devices/{id}/deploy
│   └── infer.c               # TFLite Micro inference task (optional)
└── components/
    └── tflite-micro/         # git submodule: tensorflow/tflite-micro
```

**MQTT topic schema (mirrors the gRPC proto semantics):**

```
hub/devices/{device_id}/register       ← ESP32 publishes on boot
hub/devices/{device_id}/heartbeat      ← ESP32 publishes every 10s
hub/devices/{device_id}/telemetry      ← ESP32 publishes every 5s (JSON)
hub/devices/{device_id}/deploy         → Hub publishes OTA command
hub/devices/{device_id}/deploy/ack     ← ESP32 acknowledges
hub/devices/{device_id}/deploy/status  ← ESP32 reports progress %
```

**Telemetry JSON (matches Cami Fleet proto fields):**

```json
{
  "device_id": "esp32-kitchen-01",
  "tokens_per_sec": 4.2,
  "time_to_first_token_ms": 680,
  "memory_used_mb": 0.31,
  "status": "running",
  "backend": "tflm",
  "timestamp_ms": 1748090400000
}
```

**Cami Fleet PR:** `feat(agent-esp32): FreeRTOS MQTT agent for ESP32 with TFLM support`

---

### Phase 3 — Smart Hub Agent (Month 3–4)

**Goal:** Extend the existing Rust agent to act as a hub: it speaks gRPC upstream
to Cami Fleet AND manages an MQTT broker downstream for ESP32 sub-devices.

**New hub responsibilities vs. current agent:**

| Current Agent | Hub Agent Extension |
|---|---|
| Registers self with control plane | Registers self + proxies ESP32 registrations |
| Streams own telemetry | Aggregates + forwards ESP32 telemetry |
| Watches own deployments | Fans out model deployments to ESP32 sub-fleet |
| Runs LiteRT locally | Exposes LiteRT to ESP32s as inference endpoint |

**Files to modify/add in this repo:**

```
agent/src/
├── main.rs                   # add --hub-mode flag
├── hub/                      # new module
│   ├── mod.rs
│   ├── mqtt_broker.rs        # embed or connect to Mosquitto
│   ├── sub_device_registry.rs # track ESP32 registrations locally
│   ├── telemetry_aggregator.rs # merge ESP32 metrics → upstream gRPC
│   └── deployment_fanout.rs  # receive from control plane → MQTT publish
```

**Key data model change in the control plane (Go):**

```go
// control-plane/internal/model/device.go — add hub relationship
type Device struct {
    ID       string
    Tags     map[string]string
    // new fields:
    DeviceType  string   // "hub" | "leaf" | "standalone"
    ParentHubID *string  // set for ESP32 leaf devices
    ChildCount  int      // set for hub devices
}
```

**Cami Fleet PRs:**
- `feat(agent): hub mode — MQTT broker + sub-device management`
- `feat(control-plane): hierarchical device model with hub/leaf types`
- `feat(web): hub device view with expandable sub-device list`

**Fixes the LiteRT memory gap:**

```rust
// agent/src/model_runtime.rs:230 — current state:
mem_mb: 0.0, // LiteRT doesn't expose memory via API

// After hub-level monitoring:
mem_mb: self.hub_metrics.litert_rss_mb, // read from /proc/{pid}/status
```

---

### Phase 4 — LiteRT Upstream Contributions (Months 5–8)

**Goal:** Contribute back to `google-ai-edge/LiteRT` based on real pain points
discovered while building the hub.

**Contribution 1 — Memory metrics API**

The `mem_mb: 0.0` gap at `agent/src/model_runtime.rs:230` exists because `lit serve`
has no memory reporting endpoint. Open a GitHub issue and PR upstream:

```
GET /v1beta/runtime/metrics
{
  "rss_mb": 412.3,
  "model_loaded_mb": 380.1,
  "peak_mb": 445.0
}
```

**Contribution 2 — ESP32-S3 TFLM Benchmark**

TensorFlow Lite Micro supports Xtensa LX7 (ESP32-S3's CPU) but has no official
benchmark suite for it. Your `agent-esp32/` becomes the reference implementation.
Contribute benchmark results and any needed kernel optimizations to `tflite-micro`.

**Contribution 3 — Quantized model export for ESP32**

Document and script the pipeline:
```
Gemma 2B (full precision)
  → Google AI Edge tools (quantize to INT4)
  → .tflite flatbuffer
  → Flash to ESP32-S3 via OTA
```

This closes the model lifecycle loop between LiteRT and TFLM.

**Upstream repos to target:**
- `google-ai-edge/LiteRT` — metrics API, `lit serve` improvements
- `tensorflow/tflite-micro` — ESP32-S3 kernels, benchmarks
- `espressif/esp-idf` — TFLM component packaging

---

### Phase 5 — Community & Visibility (Month 9+)

With a working system end-to-end, you have tangible artifacts for visibility:

**Write:**
- "How I connected ESP32 sensors to a LiteRT hub managed by Cami Fleet"
- "FreeRTOS task architecture for edge AI: heartbeat, telemetry, OTA in 400 lines of C"
- "Closing the mem_mb gap: adding memory metrics to LiteRT's lit serve"

**Extend the Grafana dashboard** (`monitoring/dashboards/fleet-overview.json`):
- Add ESP32-specific panels: heap free, WiFi RSSI, battery voltage
- Add hub panel: LiteRT memory, active sub-device count, inference queue depth

**Open issues** to attract collaborators:
- STM32 / nRF52 agent (Cortex-M4/M7) — same MQTT pattern, different SDK
- Zigbee/BLE transport for lower-power nodes
- Hub auto-discovery via mDNS

---

## Contribution Dependency Graph

```
[Phase 1] Python Simulator
     │
     ├──► validates control plane gRPC protocol
     └──► used in CI to test hub aggregation logic
          │
[Phase 2] ESP32 C Agent
     │
     ├──► proves MQTT topic schema is correct
     └──► generates real telemetry data for hub
          │
[Phase 3] Hub Agent Extension
     │
     ├──► closes mem_mb gap → motivation for Phase 4 upstream PR
     └──► proves hierarchical device model in control plane
          │
[Phase 4] LiteRT Upstream
     │
     ├──► memory metrics API → backport to hub agent
     └──► TFLM ESP32-S3 benchmarks → reference for Phase 2 optimizations
          │
[Phase 5] Community
     │
     └──► attract contributors for STM32, nRF52, Zigbee variants
```

---

## Hardware Shopping List

To build and test this yourself:

| Item | Role | Approx. Cost |
|---|---|---|
| Raspberry Pi 5 (8GB) | Smart Hub — runs `lit serve` + hub agent | $80 |
| ESP32-S3 DevKit (×3) | Leaf nodes — FreeRTOS MQTT agent | $10 each |
| ESP32-CAM | Vision leaf node — camera + TFLM | $8 |
| MicroSD (32GB) | Hub OS + model storage | $8 |
| USB power hub | Power all boards from one supply | $15 |

**Total: ~$141 for a complete 4-node testbed**

The Raspberry Pi 5 is preferred over Pi 4 because its improved memory bandwidth
handles LiteRT model loading significantly faster. A Coral Dev Board or Jetson Nano
works too and adds dedicated NPU/GPU for real inference acceleration.

---

## Getting Started Today

```bash
# 1. Get the stack running
make up && make smoke

# 2. Generate Python gRPC stubs (Phase 1 starting point)
pip install grpcio grpcio-tools
mkdir -p tools/simulator
python -m grpc_tools.protoc \
  -I control-plane/proto \
  --python_out=tools/simulator \
  --grpc_python_out=tools/simulator \
  control-plane/proto/device.proto

# 3. Verify stubs generated
ls tools/simulator/
# device_pb2.py  device_pb2_grpc.py

# 4. Read the proto to understand what each simulator device must implement
cat control-plane/proto/device.proto
```

From there, Phase 1 is writing `tools/simulator/device.py` — pure Python,
no Rust, no embedded C. Once the simulator works against the live control plane,
you understand the protocol well enough to implement it in C for ESP32.

---

## Summary

| Phase | What you build | Where it lands | LiteRT connection |
|---|---|---|---|
| 0 | Understand the stack | — | Read `model_runtime.rs` |
| 1 | Python fleet simulator | `tools/simulator/` in this repo | Validates gRPC protocol |
| 2 | ESP32 FreeRTOS C agent | New `agent-esp32/` repo | Uses TFLM directly |
| 3 | Hub mode in Rust agent | `agent/src/hub/` in this repo | Fixes `mem_mb: 0.0` gap |
| 4 | LiteRT upstream PRs | `google-ai-edge/LiteRT` | Metrics API + TFLM benchmarks |
| 5 | Community & docs | Blog posts, Grafana panels, issues | ESP32 as reference platform |

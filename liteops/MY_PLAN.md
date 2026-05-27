# My Plan — Mahmoud Elaskary

A single document that maps my background to this project, phase by phase,
with the concrete value I get from each step.

---

## My background at a glance

| Area | Evidence | Strength level |
|---|---|---|
| Production LLM systems | Built RAG from zero to production at Media-Markt Saturn | ★★★★★ |
| Observability | LangFuse, prompt versioning, caught silent regression in week 3 | ★★★★★ |
| Agentic workflows | LangGraph multi-step routing in production | ★★★★☆ |
| Cloud / DevOps | GCP Vertex AI, Docker, Kubernetes, CI/CD | ★★★★☆ |
| Enterprise integration | ServiceNow production work at VOIS and Schwarz Group | ★★★★★ |
| Data pipelines | Kafka, Spark, Sqoop, ETL at CyberMAK and VOIS | ★★★★☆ |
| Python | Primary language, 7+ years production use | ★★★★★ |
| Go (control plane) | Can read fluently, contribute with guidance | ★★☆☆☆ |
| Rust (agent) | Can read, copy patterns, small targeted fixes | ★★☆☆☆ |
| C / FreeRTOS | Maker background — needs refresh before production use | ★★☆☆☆ |
| MicroPython / ESP32 | Hardware intuition from maker background, Python familiar | ★★★☆☆ |

**The gap I fill that nobody else in this project can:**
Production LLM observability + enterprise IT governance (ServiceNow) + agentic
workflow design, combined in one person with hardware maker instincts.

---

## The project I am building: LiteOps

**One sentence:** The production operations layer that LiteRT does not have.

**Why it does not exist yet:** The people who know LiteRT deeply are Google engineers
who have never built a ServiceNow integration. The people who know ServiceNow are
enterprise architects who have never touched `lit serve`. I am the bridge.

**Where the code lives:** `liteops/` in this repo.  
**Where the detailed project brief lives:** `docs/liteops-project.md`  
**Where per-phase docs live:** `liteops/docs/phases/`

---

## Phase map — skills used, value returned

### Phase 1 — Observer `liteops/liteops/observer.py` ✅ Done

**What I build:** `LiteRTObserver` — wraps every lit serve call, captures real
memory via psutil, exports Prometheus metrics, fixes `mem_mb: 0.0` across every
LiteRT deployment.

**My skill used:** Observability. Same instinct that made me add LangFuse in
week 2 at Media-Markt Saturn before the silent regression happened in week 3.

**Value I get:**
- First concrete artifact in the LiteRT ecosystem with my name on it
- Screenshot of a real Grafana panel showing memory that was 0.0 before — the
  opening image of my first blog post
- Grounds the upstream LiteRT feature request: "We built this workaround and
  here is the data. Will you accept a PR for a native metrics endpoint?"

**Cami Fleet PR opened:** `fix(agent): replace mem_mb: 0.0 with /proc-based RSS`  
**LiteRT issue opened:** Feature request for `/v1beta/runtime/metrics` endpoint

---

### Phase 2 — Evaluator `liteops/liteops/evaluator.py` 🔜 Next

**What I build:** `EdgeEvaluator` — runs a fixed question bank against every
deployed model, scores output quality, triggers rollback if score drops below
threshold after an OTA update.

**My skill used:** Production AI quality management. The evaluation cycle I ran
before every release at Media-Markt Saturn was the thing that caught problems
before users saw them. I know exactly what questions to ask and why.

**The direct connection:** "The first version of the system gave confident, fluent
answers that were factually wrong about half the time." (My own words in my CV.)
That early failure at Media-Markt shapes every design decision in this evaluator.

**Value I get:**
- Closes a real gap: edge models can now fail deployments instead of silently
  degrading
- Differentiator in interviews: "I built automatic quality gates for edge AI
  that roll back bad OTA updates" — that is a very specific, verifiable claim
- Blog post: "Why I built an evaluation pipeline for edge models — and what I
  learned from the first version that was wrong half the time"

**Cami Fleet PR:** `feat(control-plane): deployment evaluation hook — quality
gate before marking deployment successful`

---

### Phase 3 — ServiceNow Bridge `liteops/liteops/connectors/servicenow.py` 📋 Next

**What I build:** `ServiceNowConnector` — receives Cami Fleet webhook events,
creates governed IT records: device offline → incident, deployment failure → change
record, device registry → CMDB sync.

**My skill used:** ServiceNow architecture. 4+ years of production ServiceNow
work at VOIS and Schwarz Group. I know the CMDB table structure, the incident
priority matrix, the change management flow. I can build this in a day.

**The direct connection:** Right now I build ServiceNow automation for Lidl and
Kaufland at Schwarz Group. This phase is doing the same thing, but for an
open-source edge AI fleet instead of a retail IT platform. Same skills, different
audience. The context switch is zero.

**Value I get:**
- The contribution nobody else in open-source edge AI can make
- Makes LiteOps deployable inside any enterprise IT governance process —
  this is the reason a large company would adopt it over building their own
- Conference talk at a ServiceNow user group or Now Create event:
  "How I connected edge AI fleet management to ServiceNow"
- My CV says "ServiceNow CAD & CTA in progress (2026)" — this is the proof
  of advanced architecture work that belongs next to those certifications

**Cami Fleet PR:** `feat(notify): add deployment_id and model_id fields to
webhook payload` — small expansion to the existing webhook.go so the ServiceNow
connector has all the context it needs to create useful records.

---

### Phase 4 — LangGraph Hub Router `liteops/liteops/router.py` 📋 Planned

**What I build:** `build_hub_router()` — a LangGraph graph that runs on the
Raspberry Pi hub, classifies incoming requests, routes inference to LiteRT or
sensor reads to specific ESP32 leaves, aggregates multi-device responses.

**My skill used:** LangGraph production workflow design. The biggest single
improvement to quality at Media-Markt Saturn was "routing queries through a
classification step before retrieval." I apply the exact same insight here:
classify first, then route to the right resource.

**The direct connection:**

```
Media-Markt Saturn:                   LiteOps Hub Router:
  query → classify type             request → classify type
         → RAG retrieval path               → LiteRT (heavy inference)
         → direct lookup path               → ESP32 leaf (sensor read)
         → structured search                → fleet broadcast
```

Same graph. Different hardware.

**Value I get:**
- Blog post with the highest reach potential: "I put a LangGraph agent on a
  Raspberry Pi and it made my ESP32 fleet smarter" — this bridges two communities
  (LangGraph developers and hardware makers) who do not currently talk to each other
- Demonstrates I can apply production AI patterns beyond the cloud

**Cami Fleet PR:** `feat(agent): hub mode flag — LangGraph routing layer for
multi-device request dispatch`

---

### Phase 5 — MicroPython ESP32 Agent `liteops/liteops_esp32/` 📋 Planned

**What I build:** `LiteOpsAgent` in MicroPython — ~150 lines that runs on ESP32-S3,
connects to hub via MQTT, sends heartbeat and telemetry every 5–10 seconds,
receives OTA updates from the hub.

**My skill used:** Python (MicroPython is Python). Hardware intuition from maker
background (Cairo Maker Fair). I do not need to learn C for this phase.

**Hardware I need to buy:**

| Item | Why | Cost |
|---|---|---|
| Raspberry Pi 5 (8GB) | Hub — runs lit serve + LiteOps | ~$80 |
| ESP32-S3-DevKitC-1 N16R8 ×3 | Leaf nodes — 8MB PSRAM, MicroPython works well | ~$12 each |
| MicroSD 32GB | Hub OS and model storage | ~$8 |

Total: ~$124. This is the hardware that makes everything real.

**Value I get:**
- Physical proof the full stack works: ESP32 appears in Cami Fleet dashboard
- Demo video: power on ESP32, watch it register, watch a model deploy to it,
  watch a ServiceNow incident fire when I disconnect it. That video is the
  entire project in 90 seconds.
- Opens C/FreeRTOS path: once I know what MicroPython does, reading ESP-IDF C
  examples is easy because I know what the code is trying to accomplish

---

### Phase 6 — LiteRT Upstream 📋 Planned

**What I build:** PR to `google-ai-edge/LiteRT` for the `/v1beta/runtime/metrics`
endpoint. MicroPython integration guide in the official LiteRT docs.

**My skill used:** All of the above. Phases 1–5 are the proof-of-concept. The
upstream PR is backed by real data from a working production-style system.

**The sequence that makes the PR credible:**

```
Phase 1: "We built a /proc workaround — here is the data it produces"
              ↓
Phase 3: "We need reliable memory data for ServiceNow incident thresholds"
              ↓
Phase 6: "Here is the PR. We have a working implementation and a user community."
```

**Value I get:**
- My name in Google's edge AI project commit history
- The strongest possible evidence line on a CV:
  "Upstream contributor to google-ai-edge/LiteRT"

---

## Timeline

```
Month 1    Phase 1 ✅ + Phase 2 🔜
             Observer done. Evaluator: evaluator.py + tests + phase-2 doc.

Month 2    Phase 3
             ServiceNow connector. Demo: device goes offline → SN incident.

Month 3    Phase 4
             LangGraph hub router. Blog post #2.

Month 4–5  Phase 5
             Buy hardware. Flash MicroPython. Demo video.

Month 6    Phase 6
             Open LiteRT PR. Publish to PyPI.

Month 7+   Community
             Conference talk. Open issues for STM32, nRF52 contributors.
```

---

## How this reads on my CV in 12 months

**Under "Open Source / Projects":**

```
LiteOps — Production operations framework for LiteRT edge AI  (github.com/{me}/liteops)
• Built the observability layer missing from every LiteRT deployment: real memory
  metrics (fixing the hardcoded mem_mb: 0.0), latency histograms, token throughput,
  and quality evaluation gates — exported to Prometheus with a drop-in Grafana dashboard.
• LangGraph query router on Raspberry Pi hub: classifies and routes inference
  requests across a fleet of ESP32-S3 leaf nodes.
• ServiceNow integration: device fleet events → governed IT records (P2/P3 incidents,
  CMDB CI sync, change records) — making LiteRT deployable inside enterprise IT.
• MicroPython MQTT agent for ESP32-S3: register, telemetry, OTA in 150 lines.
• Upstream contributor: /v1beta/runtime/metrics PR merged in google-ai-edge/LiteRT.
• Published: pip install liteops. Dashboard on grafana.com.
```

**What this does to my positioning:**

Before: "Senior AI engineer with LLM and ServiceNow experience."

After: "The person who built the production operations bridge between Google's LiteRT,
enterprise IT governance, and IoT hardware — and shipped it as an open-source package."

That second sentence is a specific claim in a specific niche. Nobody else has it.

---

## Navigation — where everything is

| I want to... | Go to |
|---|---|
| Understand the full project concept | `docs/liteops-project.md` |
| See the contribution roadmap for this repo | `ROADMAP.md` |
| Start the next phase | `liteops/docs/phases/` — read the next phase doc |
| Write code | `liteops/liteops/` |
| Write tests | `liteops/tests/` |
| See what Phase 1 delivered | `liteops/docs/phases/phase-1-observer.md` |
| Open a LiteRT upstream issue | Use the template in `docs/liteops-project.md` |
| Check PR conventions | `liteops/CONTRIBUTING.md` |

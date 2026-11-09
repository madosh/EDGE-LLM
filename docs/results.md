# Measured results

Numbers from running the stack, not estimates. Every table says how to
reproduce it. All of it ran on one machine, so treat these as a baseline for
a laptop-class host, not a capacity claim.

**Host:** AMD Ryzen AI Z2 Extreme (12 threads), 13.6 GB RAM, Windows 11 with
Docker Desktop (Linux VM: 12 vCPUs, 6.6 GB). Control plane, Postgres,
ClickHouse, NATS, Ollama, the agents and the load generator all share it.

## 1. Real inference through the fleet

A real model, deployed the way any model is: the control plane serves the
file, the agent downloads it, checks its SHA-256, uploads it to Ollama by
digest and starts probing it.

| Step | Result |
|------|--------|
| Model | Qwen2.5-0.5B-Instruct, GGUF Q4_K_M, 469 MB ([Qwen/Qwen2.5-0.5B-Instruct-GGUF](https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF), Apache-2.0) |
| Download + SHA-256 check | 25 s (`artifact verified`) |
| Load into Ollama | 43 s, including the blob upload and a warm-up generation |
| Ask the device a question | Answered in 2.5 s |

Steady state, from the agent's own probes (`scripts/fleet-bench.sh device-edge-ollama 200`):

| Device | Arch | RAM | Model | Samples | Decode tok/s p50 | p5 | TTFT ms p50 | p95 | Model RAM MB |
|--------|------|-----|-------|---------|------------------|----|-------------|-----|--------------|
| device-edge-ollama | x86_64 | 6.6 GB | qwen2.5-0.5b-instruct | 200 | 62.6 | 24.6 | 44 | 1321 | 553 |

CPU only, no GPU passthrough. The p95 time to first token includes probes
that found the model unloaded and had to load it again. The stub devices are
refused by the script ("no measured samples") rather than reported: simulated
numbers are labelled `stub` end to end and never mixed with measured ones.

What the real model exposed that the stub never could: asked "How is this
device doing?", the 0.5B model answered *without calling the `device_status`
tool* and described "high uptime" it had never read. See
[field notes](field-notes.md#a-small-model-skips-its-tools).

Reproduce:

```bash
curl -L -o artifacts/qwen2.5-0.5b-instruct-q4_k_m.gguf \
  https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf
sha256sum artifacts/qwen2.5-0.5b-instruct-q4_k_m.gguf | cut -d' ' -f1 > artifacts/qwen2.5-0.5b-instruct-q4_k_m.gguf.sha256
docker compose --profile ollama up -d
# deploy to {"runtime":"ollama"} with that digest, then after a few minutes:
scripts/fleet-bench.sh device-edge-ollama
```

## 2. Failure paths

`scripts/demo-failures.sh` against the live stack, with the real-model device
as the target of step 1. CI runs the same script after the end-to-end test.

| Scenario | What happened | Time |
|----------|---------------|------|
| Artifact changed after its digest was published | Agent refused it: `artifact SHA-256 mismatch`. Device kept serving qwen2.5-0.5b-instruct | < 1 s |
| Canary to `location=barcelona` | 25%: 0 of 2 devices (hash buckets, see field notes). Widened to 50%: 1 of 2 | 2 s |
| Promote to 100% | Both devices on the candidate | 5 s |
| Roll back | Both restored to the previous model | 1–5 s |
| Agent container stopped | Marked offline (heartbeat timeout 30 s) | 27 s |
| Agent restarted | Online again with no operator action | < 2 s |
| Certificate revoked (`scripts/revoke-device.sh`) | Open gRPC streams closed, reconnects refused with `BadCertificate`, then marked offline | streams: 5 s, offline: 36 s |
| Revocation lifted | Agent reconnected by itself | 5 s |

## 3. Control plane under load

`scripts/load-test.sh N`: N simulated agents, each with its **own client
certificate** (minted from the fleet CA, CN = its name) and its own mTLS gRPC
connection. Each registers, heartbeats every 10 s, streams telemetry every
30 s, holds `WatchDeployments` open, and acks deployments. After a 60 s soak,
one deployment targets all of them. Agents do not download the artifact: this
measures the control plane, not the network.

| Agents | Ramp (all registered + watching) | Heartbeat p50 / p95 / p99 ms | Register p50 / p95 ms | Errors | Control plane memory | Control plane CPU | Postgres connections |
|-------:|------:|------|------|---:|------:|----:|---:|
| 200 | 4.5 s | 5.3 / 23.8 / 55.3 | 39 / 267 | 0 | 64 MB | 4% | 18 |
| 500 | 10.9 s | 7.7 / 42.3 / 175 | 87 / 269 | 0 | 163 MB | 45% | 18 |
| 1,000 | 21.5 s | 11.1 / 68.6 / 277 | 57 / 201 | 0 | 312 MB | 69% | 18 |

Deployment fan-out, from `POST /api/deployments` until the deployment was
marked `completed` (every device acked `running`):

| Agents | Instruction reached device p50 / p95 / max | All acked, deployment completed |
|-------:|------|------:|
| 500 | 2.3 / 5.2 / 5.4 s | 5.4 s |
| 1,000 | 4.7 / 10.3 / 10.7 s | 10.7 s |

What this says:

- **Steady state is cheap.** 1,000 connected agents cost about 0.3 MB of
  control-plane memory each, Postgres stays on its 18 pooled connections, and
  heartbeats stay around 11 ms median with no errors.
- **Fan-out is linear, about 10.7 ms per device.** The median device waits
  half the total, so instructions go out one device at a time: creating a
  deployment targets each matching device in turn (requirements check, insert,
  push). That is the next bottleneck, not the network or gRPC. Batching the
  inserts and pushing concurrently would be the fix; at 1,000 devices it is
  10 s, at 100,000 it would be 18 minutes.
- **Tail latency grows with CPU.** The p99 heartbeat rises from 55 ms to 277 ms
  as the control plane reaches 69% CPU, on a host that is also running the
  1,000 simulated agents.

Reproduce (the simulated devices stay in the database as offline; the second
command removes them):

```bash
scripts/load-test.sh 1000
scripts/load-test.sh --cleanup
```

## 4. ESP32 display

| | |
|---|---|
| Firmware | RAM 50.7 KB of 320 KB, flash 989 KB of 3 MB (with mDNS and live telemetry) |
| Poll | Every 5 s: heartbeat + state, each its own mTLS connection over TLS 1.2 |
| State payload | ≤ 6 agents, each with hardware, newest deployment, requirements, and latest `tok/s`, `TTFT` and source |
| CI | Built on every push from `secrets.example.h`, so no real secret is ever needed to build |

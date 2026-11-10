# Changelog

## v1.0.0 — 2026-11-09

First release. Cami Fleet deploys AI models to edge devices chosen by label,
checks every byte before it runs, and reports measured inference back.

### What is in it

- **Control plane (Go).** REST API for operators; gRPC over mutual TLS 1.3 for
  agents; an HTTPS API over mutual TLS for microcontrollers. Postgres for state,
  ClickHouse for telemetry, NATS for live events, Prometheus metrics.
- **Targeting and rollout.** Label selectors, canary percentage with stable hash
  buckets, promote, rollback to each device's previous model, and requirements
  (RAM, CPU / GPU / NPU, architecture). Devices that do not qualify are recorded
  as skipped, with the reason.
- **Edge agent (Rust).** Hashes each download while streaming, keeps it only if
  the SHA-256 matches, and runs that exact file on LiteRT-LM, Ollama or a stub.
  Reports probe telemetry labelled `probe` or `stub`, and answers operator
  questions with three read-only tools.
- **Identity and revocation.** One certificate per device; the caller is the
  certificate's name on every call. `scripts/revoke-device.sh` cuts a device off:
  new handshakes fail, open streams close within 5 s, and an unreadable list
  refuses everyone.
- **Web dashboard (SvelteKit).** Fleet, deployments, device detail with "ask a
  device", and monitoring. The API key stays on the server.
- **ESP32-S3 display.** A touch screen that joins the fleet with its own
  certificate and shows each agent, what it was asked, and its live tokens/s.
- **Tooling.** A load generator with real per-agent identities, end-to-end and
  failure-path scripts, a benchmark that refuses simulated data, and CI for Go,
  Rust, web, firmware, Docker images and the whole stack.

### Measured

On one laptop-class machine (details and how to reproduce in
[docs/results.md](docs/results.md)):

- Qwen2.5-0.5B deployed through the fleet: 62.6 tok/s median, 44 ms median
  time to first token, CPU only.
- Tampered artifact refused in under a second; the running model kept serving.
- 1,000 simulated agents: zero errors, heartbeat p50 11 ms, control plane at
  312 MB; one deployment reached all of them in 10.7 s.

### Known limits

- One control-plane instance; live streams are tracked in memory.
- Deployment fan-out is serial, about 10.7 ms per device.
- Revocation is a local serial list; certificates are long-lived.
- Small models can skip their tools and answer from guesswork.
- Demo credentials (`changeme`, `cami`) are defaults for local use only; see
  [SECURITY.md](SECURITY.md).

Start with the [complete guide](docs/cami-fleet.html).

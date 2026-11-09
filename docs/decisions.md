# Design decisions

Short records of the choices that shape this system: what was decided, why,
and what it costs. Each one points at the code that implements it.

---

## 1. A device's identity is its certificate, not its request

**Decision.** Every device-facing call is authorised by the common name of the
verified client certificate. A `device_id` in a request body is only accepted
if it belongs to that name.

**Why.** Shared API keys or bearer tokens on devices leak, and when they do
every device can impersonate every other. With one certificate per device, a
stolen device can act only as itself, and cutting it off touches nobody else.

**Cost.** Certificates must be issued per device (`scripts/gen-certs.sh`) and
revoked when a device is lost (decision 7). Code: `peerName` and
`authorizeDevice` in `control-plane/internal/api/grpc/server.go`.

## 2. The model that runs is the file that was verified

**Decision.** Agents hash the artifact while streaming it, keep it only if the
SHA-256 matches the deployment, store it under that digest, and re-hash a
cached file before each use. Runtimes are pointed at the verified bytes: a file
path for LiteRT-LM, an upload by digest for Ollama. Nothing is pulled by name.

**Why.** "Pull `gemma` from a registry" means whatever the registry serves
today. A digest means exactly one file. A bad file is refused before the
running model is touched, which `scripts/demo-failures.sh` checks against a real
model.

**Cost.** Every model is a full download per device, with no layer sharing, and
the operator must publish the digest. Code: `agent/src/model_runtime.rs`.

## 3. Deployments describe what a device needs; skips carry a reason

**Decision.** A deployment can require minimum RAM, an accelerator and an
architecture. A device that does not qualify is recorded as `skipped` with a
sentence saying why ("needs gpu accelerator, device has cpu"). It is never
silently left out.

**Why.** In a mixed fleet the main question is "why didn't device X get
it?" The answer belongs in the data, where the dashboard and the ESP32 can show
it, not in a log line.

**Cost.** Devices must report capabilities truthfully at registration; there
is no attestation. Code: `control-plane/internal/fleet/fleet.go`.

## 4. Canaries use a stable hash, not random sampling

**Decision.** A device is in a rollout if `fnv(deployment_id, device_id) % 100
< percent`. Promoting only raises the percentage, so devices already in stay
in.

**Why.** Deterministic and stateless: any instance computes the same answer,
a restart changes nothing, and promotion only adds devices.

**Cost.** On small fleets percentages are lumpy. With two devices, 25% and even
50% can select none, which the failure demo hit. It widens in stages instead of
assuming. Code: `control-plane/internal/model/rollout.go`.

## 5. State in Postgres, telemetry in ClickHouse

**Decision.** Devices, deployments and tasks live in Postgres. Inference
telemetry goes to ClickHouse with async inserts.

**Why.** They have opposite shapes. State is small, relational and updated in
transactions. Telemetry is append-only, one row per device per interval, and
read as aggregates over time. At 1,000 agents Postgres stayed on 18 pooled
connections while telemetry flowed beside it ([results](results.md#3-control-plane-under-load)).

**Cost.** Two databases to run, and joins across them happen in Go: the display
attaches the latest telemetry to the Postgres agent list.

## 6. Microcontrollers get HTTPS + JSON, with the same trust model

**Decision.** The ESP32 talks to a separate listener on :8443 with JSON over
HTTPS/1.0, TLS 1.2 and a required client certificate, instead of gRPC. Every
call goes through the same authorisation code as gRPC.

**Why.** gRPC needs HTTP/2 and the agent's listener requires TLS 1.3; mbedTLS
on an ESP32 does neither comfortably. A second protocol is acceptable. A second
trust model is not, so the handler calls the gRPC server's own `RegisterAs`,
`HeartbeatAs` and `Authorize`.

**Cost.** No streaming: the display polls every 5 s. The fleet-wide agent list
is shown only to devices labelled `type=esp32-display`, and leaves out artifact
URLs and internal IDs (a test checks this). Code:
`control-plane/internal/api/devicehttp/`.

## 7. Revocation is a serial list the control plane re-reads

**Decision.** `revoked.txt` next to the server certificate lists revoked
serials. The control plane re-reads it whenever it changes, and checks it at
every TLS handshake, on every gRPC call, every 5 s on open streams, and on every
device HTTPS request. If the file exists but cannot be read, every certificate
is refused.

**Why.** Agents hold streams open for hours, so a handshake-only check would
let a revoked device keep receiving deployments. A CRL or OCSP responder is the
standard answer, but for a single control plane that is the CA's only relying
party, a watched file gives the same effect with nothing extra to run. Failing
closed means a broken list never lets a revoked device in.

**Cost.** One control plane only; several instances would need the list
distributed. Certificates still last years, so revocation is the only kill
switch until short-lived certificates exist. Code:
`control-plane/internal/mtls/mtls.go`, `.../api/grpc/revocation.go`,
`scripts/revoke-device.sh`.

## 8. Simulated numbers are labelled everywhere

**Decision.** Every telemetry row carries a `source`: `probe` when measured on a
real runtime, `stub` when synthetic. The dashboard, the benchmark script and
the ESP32 all show it, and the benchmark refuses stub data.

**Why.** A demo that mixes fake and real numbers loses trust in all of them.
When real inference arrived, nothing had to be relabelled.

**Cost.** A small amount of plumbing in every consumer. Code: `source` in
`TelemetryReport` (`control-plane/proto/device.proto`).

## 9. Load tests use real identities

**Decision.** `cmd/loadgen` mints a separate certificate for each simulated
agent from the fleet CA, and runs each over its own mTLS connection through
the real RPCs.

**Why.** A load test that skips TLS or shares one identity measures a
different system. With real identities, the 1,000-agent numbers include 1,000
handshakes, 1,000 open streams and per-call authorisation. They also exposed
the actual bottleneck: serial deployment fan-out at about 10.7 ms per device.

**Cost.** The load generator needs the CA private key, so it runs only as an
opt-in compose profile next to the stack, never as part of it.

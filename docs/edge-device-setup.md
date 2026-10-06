# Run Cami Fleet on a real edge device

This guide puts a physical device — a Raspberry Pi 5, or any 64-bit Linux
box — into your fleet, runs a real model on it with Google
[LiteRT-LM](https://github.com/google-ai-edge/LiteRT-LM), and measures it.

```
 Control-plane host (docker compose)            Edge device (e.g. Raspberry Pi 5)
 ┌──────────────────────────────────┐            ┌───────────────────────────────┐
 │ control plane  :8080 REST        │  mTLS gRPC │ cami-agent (systemd)          │
 │                :9090 gRPC  ◀─────┼────────────┤   │ verified .litertlm path   │
 │ artifacts/  (models + .sha256)   │  HTTP GET  │   ▼                           │
 │                :8080/artifacts ──┼───────────▶│ litert-lm serve 127.0.0.1:9379│
 └──────────────────────────────────┘            └───────────────────────────────┘
```

The device only ever dials out, so it needs no open ports.

## 1. On the control-plane host: issue the device's certificate

Each device has its own certificate; its common name is the device name. The
server certificate must also name the address the device will use to reach
this host.

```bash
# The LAN IP (or DNS name) of the machine running docker compose:
export SERVER_SANS="IP:192.168.1.20"
export EXTRA_DEVICE_NAMES="pi-1"

# Re-issue the server certificate with the new name, and issue pi-1's.
docker compose run --rm cert-gen sh -c \
  "apk add --no-cache openssl >/dev/null && rm -rf /certs/server && sh /scripts/gen-certs.sh"
docker compose up -d --force-recreate control-plane

# Copy pi-1's certificate directory out of the volume.
docker run --rm -v cami-fleet_certs:/certs alpine \
  tar -C /certs/devices/pi-1 -c . > pi-1-certs.tar
```

Copy `pi-1-certs.tar` to the device over a channel you trust (e.g. `scp`).
It contains that device's private key and nothing else.

## 2. On the device: install LiteRT-LM and the agent

```bash
# A service user with a home directory (uv installs into ~/.local).
sudo useradd -m -s /bin/bash cami
sudo mkdir -p /etc/cami/certs /var/lib/cami/models
sudo tar -C /etc/cami/certs -xf pi-1-certs.tar
sudo chown -R cami:cami /etc/cami/certs /var/lib/cami
sudo chmod 600 /etc/cami/certs/device.key

# LiteRT-LM (command-line tool + `serve`).
sudo -iu cami sh -c 'curl -LsSf https://astral.sh/uv/install.sh | sh && ~/.local/bin/uv tool install litert-lm'

# Build the agent on the device (or cross-compile for aarch64).
sudo apt-get install -y build-essential pkg-config protobuf-compiler
curl https://sh.rustup.rs -sSf | sh -s -- -y
git clone https://github.com/madosh/EDGE-LLM.git && cd EDGE-LLM/agent
cargo build --release
sudo install -m 755 target/release/cami-agent /usr/local/bin/cami-agent
```

## 3. Configure and start the services

```bash
sudo cp deploy/cami-agent.env.example /etc/cami/agent.env
sudo nano /etc/cami/agent.env        # set DEVICE_NAME=pi-1, CONTROL_PLANE_URL, labels

sudo cp deploy/systemd/litert-lm-serve.service deploy/systemd/cami-agent.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now litert-lm-serve cami-agent
journalctl -u cami-agent -f          # expect: "registered with control plane"
```

`pi-1` appears on the dashboard within a few seconds.

## 4. Deploy a real model

Put a `.litertlm` model and its digest in the control-plane host's
`artifacts/` directory. For example, from
[litert-community on Hugging Face](https://huggingface.co/litert-community):

```bash
# On the control-plane host
sha256sum artifacts/gemma-4-E4B-it.litertlm > artifacts/gemma-4-E4B-it.litertlm.sha256
SHA=$(cut -d' ' -f1 artifacts/gemma-4-E4B-it.litertlm.sha256)

curl -s -X POST http://localhost:8080/api/deployments \
  -H "X-Api-Key: $CAMI_API_KEY" -H "Content-Type: application/json" \
  -d "{
    \"model_id\": \"gemma-4-e4b\",
    \"artifact_url\": \"http://192.168.1.20:8080/artifacts/gemma-4-E4B-it.litertlm\",
    \"artifact_sha256\": \"$SHA\",
    \"tag_selector\": {\"runtime\": \"litert\"}
  }"
```

Note the `artifact_url` uses the host's LAN address, not `control-plane`: the
device downloads it directly. The agent streams the file to
`/var/lib/cami/models/sha256-<digest>`, refuses it if the digest differs, and
hands that exact path to `litert-lm serve`.

Pick a model that fits the device: a Raspberry Pi 5 with 8 GB of RAM can hold
small Gemma variants; check the model card for its memory needs.

## 5. Measure it

```bash
# On the device
scripts/bench-device.sh /var/lib/cami/models/sha256-<digest> cpu
scripts/bench-device.sh /var/lib/cami/models/sha256-<digest> gpu   # Pi 5 GPU is experimental
```

It runs `litert-lm benchmark` and prints a Markdown row — device, RAM, model,
backend, prefill and decode speed, time to first token, init time — ready to
paste into the README. Live numbers also stream to the dashboard every
`PROBE_INTERVAL_SECS`, labelled `probe`.

## Troubleshooting

| Symptom | Cause |
|---------|-------|
| `PermissionDenied: device_name must match` | `DEVICE_NAME` differs from the certificate's common name. |
| TLS error mentioning the server name | `CONTROL_PLANE_URL`'s host is not in the server certificate; set `SERVER_SANS` and re-issue (step 1). |
| Deployment `failed: download returned HTTP 404` | `artifact_url` is not reachable from the device; use the host's LAN address. |
| Deployment `failed: LiteRT-LM server unreachable` | `litert-lm-serve` is not running: `systemctl status litert-lm-serve`. |
| Telemetry shows `error` | The model loaded but generation fails; check `journalctl -u litert-lm-serve`. |

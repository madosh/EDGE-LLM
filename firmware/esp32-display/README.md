# ESP32-S3 status display firmware

Turns the 2.8" ESP32-S3 touch board (LCDWIKI ES3C28P, sold by diymore as a
"Xiaozhi" display) into a fleet device. It registers with its own
certificate, sends heartbeats, and shows on its screen what the control plane
decided for it: which deployment, and why it was skipped or failed. Tap the
screen to see fleet-wide counts.

The board cannot run a model. It talks to the control plane's device HTTPS
API (`:8443`, mutual TLS) instead of gRPC.

**Full technical guide, including setup, protocol, design, demo script and
troubleshooting: [docs/esp32-display.md](../../docs/esp32-display.md).**

## Quick start

```powershell
# 1. Issue a certificate for the board and start the stack (repo root)
Add-Content .env "EXTRA_DEVICE_NAMES=esp32-display-1"
docker compose up -d --build

# 2. Copy the board's certificate out of the Docker volume
docker run --rm -v cami-fleet_certs:/certs:ro -v "${PWD}\firmware\esp32-display\certs:/out" alpine `
  sh -c "cp /certs/devices/esp32-display-1/ca.crt /certs/devices/esp32-display-1/device.* /out/"

# 3. Generate include/secrets.h, then build and flash
cd firmware\esp32-display
python tools\make_secrets.py --certs certs --ssid "YourWifi" --password "..." --address <PC LAN IP>
pio run -t upload --upload-port COM7
pio device monitor -p COM7
```

## Layout

| Path | What it is |
|---|---|
| `include/board.h` | Pin map |
| `include/secrets.example.h` | Template for the git-ignored `secrets.h` |
| `src/main.cpp` | Network task (register, heartbeat, poll, ack) and UI loop |
| `src/fleet_client.cpp` | mTLS HTTP client for `/device/v1/*` |
| `src/ui.cpp` | ILI9341 driver config and the two screens |
| `src/touch.cpp` | FT6336 tap detection |
| `tools/make_secrets.py` | Writes `secrets.h` from the certificate files |

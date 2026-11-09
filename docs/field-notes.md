# Field notes

Problems hit while taking this from containers to real hardware and a real
model, written as symptom, cause and fix. Most were invisible in CI and showed
up only on a real board, a real network or a real model.

---

## A small model skips its tools

**Symptom.** With Qwen2.5-0.5B deployed, "How is this device doing?" got a
fluent answer in 2.5 s: "good operating condition with a high uptime, moderate
load". The task record showed `steps: []`, so no tool had been called.

**Cause.** The agent offers `device_status` and lets the model decide. A 0.5B
model often answers straight away, and makes up the facts it would have
fetched. The stub agent always called the tool, so tests never saw this.

**Takeaway.** Tool use is a property of the model, not the agent loop. Next
steps: require at least one tool call for status questions, or check the answer
against a tool result, and record "answered without tools" as a quality signal
in telemetry. A grounded-sounding answer with no evidence behind it is the
failure to measure.

## The Ollama profile had never worked on a current image

**Symptom.** `docker compose --profile ollama up` stopped with
`dependency failed to start: container ollama is unhealthy`, although Ollama's
log said `Listening on [::]:11434`.

**Cause.** The healthcheck ran `curl`, and the `ollama/ollama:latest` image no
longer ships curl: `/bin/sh: 1: curl: not found`. CI only uses the stub
backend, so nothing caught it.

**Fix.** `test: ["CMD", "ollama", "list"]`: the image's own CLI asks the
local server. Lesson: a healthcheck should only use what the image is
guaranteed to contain.

## A canary at 50% reached nobody

**Symptom.** The failure demo deployed a candidate at 50% to two devices:
`0 targeted, 2 held back`. The wait timed out.

**Cause.** Working as designed. Each device's bucket is a hash of
(deployment, device), so for two devices the chance that neither falls under
50 is about 25%. Percentages describe large fleets; small ones are lumpy.

**Fix.** The demo now widens in stages (25 → 50 → 75 → 100) until the first
device joins, as an operator would. The design note is in
[decisions](decisions.md#4-canaries-use-a-stable-hash-not-random-sampling).

## Shell scripts broke inside Linux containers

**Symptom.** `cert-gen` exited with `set: illegal option -`.

**Cause.** Git on Windows (`core.autocrlf=true`) checked the scripts out with
CRLF line endings. Alpine's `sh` read `set -e\r` and rejected the `\r`.

**Fix.** Converted the scripts to LF and added `.gitattributes` with
`*.sh text eol=lf`, so every clone gets LF whatever the client settings.

## The ESP32 lost the control plane after a DHCP renewal

**Symptom.** The display had worked for hours, then showed `TLS ... failed`
with errno 113 (host unreachable).

**Cause.** The firmware connected to a fixed IP, and the PC's DHCP lease moved
it from .129 to .132.

**Fix.** `CP_ADDRESS` can now be `pc-name.local`. The core's DNS client does not
do multicast DNS, so names ending in `.local` are looked up with
`MDNS.queryHost`. The address is cached and dropped on any connection failure,
so the next poll finds the PC at its new address. A DHCP reservation on the
router does the same job from the network side.

## The board could not join the Wi-Fi

**Symptom.** The board kept retrying, and the disconnect reason said the network
was not found or the authentication failed.

**Cause.** The ESP32-S3 has a 2.4 GHz radio only. The first network was 5 GHz
only, and the second was a Wi-Fi 7 multi-link (MLO) network the board cannot
join.

**Fix.** A plain 2.4 GHz SSID. The firmware now logs a scan and the disconnect
reason code, so this takes one look at the serial monitor next time.

## Reading the board's flash stalled

**Symptom.** Backing up the original firmware failed part-way with
`Packet content transfer stopped`.

**Cause.** The esptool bundled with PlatformIO (4.5.1) is unreliable over the
S3's built-in USB-Serial/JTAG.

**Fix.** esptool 5.x in its own virtualenv for reading and writing flash. The
16 MB backup was taken before the first flash.

## Revocation that only checks the handshake is not revocation

**Symptom.** A first sketch checked revoked serials in `VerifyConnection`. The
revoked agent's existing connection kept working: its gRPC streams were
opened before the revocation and HTTP/2 never handshakes again.

**Fix.** Check at the handshake, and also in gRPC interceptors: every unary call,
and every open stream every 5 s, which then ends with "certificate revoked".
The demo measured streams cut within 5 s, reconnects refused with
`BadCertificate`, and the device offline at 36 s.

## Windows got in the way of tooling

- **Go test binaries blocked.** Windows Application Control refused the freshly
  built `*.test.exe`. Tests now run in a `golang:1.23` container, which is also
  what CI runs, with `-race`.
- **PowerShell 5 pipes a JSON array as one object.** `Invoke-RestMethod ... |
  Where-Object name -eq x` matched nothing. Wrapping the call in parentheses
  makes it enumerate.
- **`Set-Content` is not UTF-8 in Windows PowerShell.** It turned `→` into `?`
  in a Go string. Write files with `[IO.File]::WriteAllText` instead.

## The API returns per-device status in one place only

**Symptom.** The first failure demo timed out waiting for a device to refuse a
tampered artifact, although the agent had logged `SHA-256 mismatch` within a
second.

**Cause.** `GET /api/deployments/{id}` returns the deployment without its
per-device rows; only the list endpoint includes them.

**Fix.** The demo reads the list, like `e2e.sh`. This is worth fixing in the
API: a client asking about one deployment should get its device statuses.

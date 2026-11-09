#!/usr/bin/env bash
# Summarises the inference telemetry a device has already reported and prints
# one Markdown table row. Works for any backend; only samples measured on a
# real runtime (source "probe") are counted, never stub numbers.
#
#   scripts/fleet-bench.sh DEVICE_NAME [SAMPLES]
#
# The agent probes its runtime every 30 s with a fixed short prompt, so this
# is steady-state decode speed and time to first token for that prompt, as
# seen through the fleet - not a synthetic prefill/decode sweep like
# scripts/bench-device.sh.
set -euo pipefail

API="${API:-http://localhost:8080}"
KEY="${CAMI_API_KEY:-changeme}"
NAME="${1:-}"
SAMPLES="${2:-100}"
[ -n "$NAME" ] || { echo "usage: $0 DEVICE_NAME [SAMPLES]" >&2; exit 2; }

api() { curl -sS -H "X-Api-Key: $KEY" "$API$1"; }

DEV=$(api /api/devices | jq --arg n "$NAME" '.[] | select(.name == $n)')
[ -n "$DEV" ] || { echo "no device named $NAME" >&2; exit 1; }
ID=$(echo "$DEV" | jq -r .id)

api "/api/devices/$ID/telemetry?limit=$SAMPLES" | jq -r --argjson dev "$DEV" '
    def pct(p): sort | .[((length - 1) * p | floor)];
    def r(n): . * n | round / n;
    [.[] | select(.source == "probe" and .status == "running" and .tps > 0)] as $s
    | if ($s | length) == 0 then error("no measured (probe) samples yet: is a real runtime loaded?") else . end
    | ($s | map(.model_id) | unique | join(", ")) as $model
    | ($s | map(.tps)) as $tps
    | ($s | map(.ttft_ms)) as $ttft
    | ($s | map(.mem_mb)) as $mem
    | "| Device | Arch | RAM | Model | Samples | Decode tok/s p50 | p5 | TTFT ms p50 | p95 | Model RAM MB |",
      "|--------|------|-----|-------|---------|------------------|----|-------------|-----|--------------|",
      "| \($dev.name) | \($dev.arch) | \(($dev.mem_total_mb / 1024) | r(10)) GB | \($model) | \($s | length) | \($tps | pct(0.5) | r(10)) | \($tps | pct(0.05) | r(10)) | \($ttft | pct(0.5) | round) | \($ttft | pct(0.95) | round) | \($mem | pct(0.5) | round) |"
'

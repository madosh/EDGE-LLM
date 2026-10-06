#!/bin/sh
# Benchmarks a .litertlm model on this device with LiteRT-LM and prints one
# Markdown table row you can paste into the README's results table.
#
#   scripts/bench-device.sh MODEL.litertlm [cpu|gpu|npu]
#
# Uses `litert-lm benchmark`, which reports prefill speed, decode speed,
# engine init time and time to first token, averaged over several runs.
set -e

MODEL="$1"
BACKEND="${2:-cpu}"
RUNS="${RUNS:-3}"
PREFILL="${PREFILL_TOKENS:-256}"
DECODE="${DECODE_TOKENS:-128}"

if [ -z "$MODEL" ] || [ ! -f "$MODEL" ]; then
    echo "usage: $0 MODEL.litertlm [cpu|gpu|npu]" >&2
    exit 2
fi
case "$BACKEND" in cpu|gpu|npu) ;; *) echo "backend must be cpu, gpu or npu" >&2; exit 2 ;; esac
command -v litert-lm >/dev/null || { echo "litert-lm not found: uv tool install litert-lm" >&2; exit 1; }

# Describe the hardware: the board name on a Raspberry Pi, else the CPU model.
if [ -r /proc/device-tree/model ]; then
    BOARD=$(tr -d '\0' < /proc/device-tree/model)
else
    BOARD=$(sed -n 's/^model name[[:space:]]*: //p' /proc/cpuinfo | head -1)
fi
MEM_GB=$(awk '/^MemTotal:/ {printf "%.1f", $2 / 1048576}' /proc/meminfo)
SHA=$(sha256sum "$MODEL" | cut -c1-12)

OUT=$(litert-lm benchmark "$MODEL" --backend "$BACKEND" \
        --prefill-tokens "$PREFILL" --decode-tokens "$DECODE" --runs "$RUNS" 2>&1) || {
    echo "$OUT" >&2
    exit 1
}
echo "$OUT" | grep -q "Decode speed" || { echo "$OUT" >&2; echo "benchmark failed" >&2; exit 1; }

value() { echo "$OUT" | sed -n "s/^$1:[[:space:]]*\([0-9.]*\).*/\1/p" | tail -1; }
PREFILL_TPS=$(value "Prefill speed")
DECODE_TPS=$(value "Decode speed")
TTFT_S=$(value "Time to first token")
INIT_S=$(value "Init time")
TTFT_MS=$(awk -v s="$TTFT_S" 'BEGIN { printf "%.0f", s * 1000 }')

echo "$OUT" | sed -n '/----- Results -----/,$p' >&2
echo
echo "| Device | RAM | Model (sha256) | Backend | Prefill tok/s | Decode tok/s | TTFT ms | Init s |"
echo "|--------|-----|----------------|---------|---------------|--------------|---------|--------|"
printf '| %s | %s GB | %s (%s) | %s | %s | %s | %s | %s |\n' \
    "$BOARD" "$MEM_GB" "$(basename "$MODEL")" "$SHA" "$BACKEND" \
    "$PREFILL_TPS" "$DECODE_TPS" "$TTFT_MS" "$INIT_S"

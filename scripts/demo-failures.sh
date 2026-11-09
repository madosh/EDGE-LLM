#!/usr/bin/env bash
# Failure-path demo against a running stack: what happens when things go
# wrong, shown live and checked. Run it with the ESP32 display on the desk to
# watch devices turn red and come back.
#
#   docker compose --profile ollama up -d
#   scripts/demo-failures.sh
#
# 1. A tampered artifact is refused, and the device keeps its current model.
# 2. A canary reaches part of the fleet, is promoted, then rolled back.
# 3. A device that stops is marked offline, then recovers by itself.
# 4. A revoked certificate is cut off, then let back in.
#
# Needs bash, curl, jq, sha256sum and docker compose. Uses the Ollama device
# (real model) for step 1 when it is running, else device-barcelona-1.
set -euo pipefail

API="${API:-http://localhost:8080}"
KEY="${CAMI_API_KEY:-changeme}"
ARTIFACTS="${ARTIFACTS_DIR:-artifacts}"
DEVICE_ARTIFACTS="http://control-plane:8080/artifacts"
COMPOSE="${COMPOSE:-docker compose}"
VICTIM="${VICTIM:-device-barcelona-2}"

step() { printf '\n── %s\n' "$*"; }
pass() { printf '  ✓ %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
fail() { printf '  ✗ %s\n' "$*" >&2; exit 1; }

api() {
    local method=$1 path=$2 body=${3:-}
    if [ -n "$body" ]; then
        curl -sS -X "$method" -H "X-Api-Key: $KEY" -H "Content-Type: application/json" -d "$body" "$API$path"
    else
        curl -sS -X "$method" -H "X-Api-Key: $KEY" "$API$path"
    fi
}

# wait_for SECONDS DESCRIPTION COMMAND...: retry every 2 s, report the time taken.
wait_for() {
    local timeout=$1 what=$2
    shift 2
    local start=$SECONDS deadline=$((SECONDS + timeout))
    until "$@" >/dev/null 2>&1; do
        [ "$SECONDS" -lt "$deadline" ] || fail "timed out after ${timeout}s waiting for: $what"
        sleep 2
    done
    pass "$what ($((SECONDS - start)) s)"
}

device_field() { api GET /api/devices | jq -r --arg n "$1" --arg f "$2" '.[] | select(.name == $n) | .[$f]'; }
device_status_is() { [ "$(device_field "$1" status)" = "$2" ]; }
# The list carries per-device status; GET /api/deployments/{id} does not.
deployment() { api GET /api/deployments | jq --arg id "$1" '.[] | select(.id == $id)'; }
device_dep_status() { deployment "$1" | jq -r --arg d "$2" '.devices[] | select(.device_id == $d) | .status'; }
device_dep_is() { [ "$(device_dep_status "$1" "$2")" = "$3" ]; }
model_is() { [ "$(api GET "/api/devices/$1" | jq -r .current_model_id)" = "$2" ]; }
running_on() {
    deployment "$1" | jq -e --argjson n "$2" \
        '(.devices | length) >= $n and all(.devices[]; .status == "running")'
}

deploy() { # deploy MODEL_ID FILE SHA SELECTOR_JSON [EXTRA_JSON]
    local extra=${5:-'{}'}
    jq -n --arg m "$1" --arg u "$DEVICE_ARTIFACTS/$2" --arg s "$3" --argjson sel "$4" --argjson x "$extra" \
        '{model_id: $m, artifact_url: $u, artifact_sha256: $s, tag_selector: $sel} + $x' |
        curl -sS -X POST -H "X-Api-Key: $KEY" -H "Content-Type: application/json" -d @- "$API/api/deployments"
}

# ── 1. Tampered artifact ─────────────────────────────────────────────────────
step "1. Tampered artifact"
if device_status_is device-edge-ollama online; then
    TARGET=device-edge-ollama SELECTOR='{"runtime":"ollama"}'
else
    TARGET=device-barcelona-1 SELECTOR='{"type":"jetson-orin"}'
fi
TID=$(device_field "$TARGET" id)
BEFORE=$(api GET "/api/devices/$TID" | jq -r .current_model_id)
info "target: $TARGET, currently running ${BEFORE}"

# Publish the digest of the real file, then change one byte: what a corrupted
# download or a swapped file on the artifact server looks like.
head -c 65536 /dev/urandom >"$ARTIFACTS/demo-tampered.bin"
GOOD_SHA=$(sha256sum "$ARTIFACTS/demo-tampered.bin" | cut -d' ' -f1)
printf 'X' | dd of="$ARTIFACTS/demo-tampered.bin" bs=1 seek=1000 conv=notrunc 2>/dev/null
info "published sha256 ${GOOD_SHA:0:12}…, served file now hashes to $(sha256sum "$ARTIFACTS/demo-tampered.bin" | cut -c1-12)…"

BAD=$(deploy tampered-model demo-tampered.bin "$GOOD_SHA" "$SELECTOR" | jq -r .id)
wait_for 90 "device refused the artifact" device_dep_is "$BAD" "$TID" failed
info "reason: $(deployment "$BAD" | jq -r --arg d "$TID" '.devices[] | select(.device_id == $d) | .error_msg')"
model_is "$TID" "$BEFORE" || fail "device stopped running $BEFORE"
pass "device still runs $BEFORE: a bad artifact never replaces a good model"
api POST "/api/deployments/$BAD/rollback" '{}' >/dev/null
rm -f "$ARTIFACTS/demo-tampered.bin"

# ── 2. Canary, promote, rollback ─────────────────────────────────────────────
step "2. Staged canary, promote, roll back"
SAMPLE_SHA=$(cut -d' ' -f1 "$ARTIFACTS/sample-gemma-4-e2b.tar.gz.sha256")
BASE=$(deploy gemma-4-e2b sample-gemma-4-e2b.tar.gz "$SAMPLE_SHA" '{"location":"barcelona"}' | jq -r .id)
wait_for 120 "baseline gemma-4-e2b running on both barcelona devices" running_on "$BASE" 2

head -c 65536 /dev/urandom >"$ARTIFACTS/demo-candidate.bin"
CAND_SHA=$(sha256sum "$ARTIFACTS/demo-candidate.bin" | cut -d' ' -f1)
# Each device's bucket hashes (deployment, device), so with two devices a given
# percentage can include neither. Widen in stages until the first one joins.
PCT=25
resp=$(deploy candidate-v2 demo-candidate.bin "$CAND_SHA" '{"location":"barcelona"}' "{\"rollout_percent\":$PCT}")
CAN=$(echo "$resp" | jq -r .id)
targeted=$(echo "$resp" | jq '.targeting.targeted | length')
info "canary at $PCT%: $targeted targeted, $(echo "$resp" | jq .targeting.held_back) held back"
while [ "$targeted" -eq 0 ] && [ "$PCT" -lt 100 ]; do
    PCT=$((PCT + 25))
    api POST "/api/deployments/$CAN/promote" "{\"rollout_percent\":$PCT}" >/dev/null
    sleep 2
    targeted=$(deployment "$CAN" | jq '.devices | length')
    info "widened to $PCT%: $targeted device(s) included"
done
wait_for 120 "canary running on $targeted of 2 devices at $PCT%" running_on "$CAN" 1
api POST "/api/deployments/$CAN/promote" '{"rollout_percent":100}' >/dev/null
wait_for 120 "promoted: candidate-v2 running on both devices" running_on "$CAN" 2
restored=$(api POST "/api/deployments/$CAN/rollback" '{}' | jq '.restored | length')
info "rollback restored $restored device(s) to the previous deployment"
for d in device-barcelona-1 device-barcelona-2; do
    wait_for 120 "$d back on gemma-4-e2b" model_is "$(device_field "$d" id)" gemma-4-e2b
done
rm -f "$ARTIFACTS/demo-candidate.bin"

# ── 3. Offline and recovery ──────────────────────────────────────────────────
step "3. $VICTIM loses power"
$COMPOSE stop "$VICTIM" >/dev/null
wait_for 90 "$VICTIM marked offline (heartbeat timeout is 30 s)" device_status_is "$VICTIM" offline
$COMPOSE start "$VICTIM" >/dev/null
wait_for 90 "$VICTIM back online by itself" device_status_is "$VICTIM" online

# ── 4. Certificate revocation ────────────────────────────────────────────────
step "4. $VICTIM's certificate is revoked"
"$(dirname "$0")/revoke-device.sh" "$VICTIM" | sed 's/^/    /'
wait_for 90 "$VICTIM cut off and marked offline" device_status_is "$VICTIM" offline
"$(dirname "$0")/revoke-device.sh" "$VICTIM" --undo | sed 's/^/    /'
wait_for 120 "$VICTIM reconnected after the revocation was lifted" device_status_is "$VICTIM" online

printf '\nAll failure paths behaved as designed.\n'

#!/usr/bin/env bash
# End-to-end test: drives a running `docker compose up` stack through the
# journeys a user relies on, against the real control plane, agents,
# Postgres, ClickHouse, NATS and web proxy.
#
#   cp .env.example .env && docker compose up -d --build && scripts/e2e.sh
#
# Needs bash, curl, jq and docker compose. Exits non-zero on the first
# failed check; CI then prints the container logs.
set -euo pipefail

API="${API:-http://localhost:8080}"
WEB="${WEB:-http://localhost:5173}"
KEY="${CAMI_API_KEY:-changeme}"
ARTIFACTS="${ARTIFACTS_DIR:-artifacts}"
DEVICE_ARTIFACTS="http://control-plane:8080/artifacts" # as devices see it
COMPOSE="${COMPOSE:-docker compose}"
LATE_CONTAINER="cami-e2e-late-device"

step() { printf '\n── %s\n' "$*"; }
pass() { printf '  ✓ %s\n' "$*"; }
fail() { printf '  ✗ %s\n' "$*" >&2; exit 1; }

api() { # api METHOD PATH [JSON]
    local method=$1 path=$2 body=${3:-}
    if [ -n "$body" ]; then
        curl -sS -X "$method" -H "X-Api-Key: $KEY" -H "Content-Type: application/json" -d "$body" "$API$path"
    else
        curl -sS -X "$method" -H "X-Api-Key: $KEY" "$API$path"
    fi
}

http_code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

# wait_for SECONDS DESCRIPTION COMMAND...: retry COMMAND every 3 s.
wait_for() {
    local timeout=$1 what=$2
    shift 2
    local deadline=$((SECONDS + timeout))
    until "$@" >/dev/null 2>&1; do
        [ "$SECONDS" -lt "$deadline" ] || fail "timed out after ${timeout}s waiting for: $what"
        sleep 3
    done
    pass "$what"
}

expect() { # expect DESCRIPTION JQ_FILTER JSON
    echo "$3" | jq -e "$2" >/dev/null || { echo "$3" | jq . >&2; fail "$1"; }
    pass "$1"
}

# ── Predicates ───────────────────────────────────────────────────────────────

healthz_ok() { [ "$(http_code "$API/healthz")" = 200 ]; }
devices_online() { [ "$(api GET /api/devices | jq '[.[] | select(.status == "online")] | length')" -ge "$1" ]; }
device_id() { api GET /api/devices | jq -r --arg n "$1" '.[] | select(.name == $n) | .id'; }
device_online() { [ "$(api GET /api/devices | jq -r --arg n "$1" '.[] | select(.name == $n) | .status')" = online ]; }
deployment() { api GET /api/deployments | jq --arg id "$1" '.[] | select(.id == $id)'; }
deployment_status_is() { [ "$(deployment "$1" | jq -r .status)" = "$2" ]; }
running_on() { # running_on DEPLOYMENT N: at least N devices, all running
    deployment "$1" | jq -e --argjson n "$2" \
        '(.devices | length) >= $n and all(.devices[]; .status == "running")'
}
model_is() { [ "$(api GET "/api/devices/$1" | jq -r .current_model_id)" = "$2" ]; }
has_stub_telemetry() {
    api GET "/api/devices/$1/telemetry?limit=5" | jq -e 'length > 0 and .[0].source == "stub" and .[0].status == "running"'
}
web_proxy_ok() { [ "$(curl -s "$WEB/api/devices" | jq 'length')" -ge 2 ]; }
task_done() { [ "$(api GET "/api/tasks/$1" | jq -r .status)" = done ]; }

deploy() { # deploy MODEL_ID FILE SHA SELECTOR_JSON [EXTRA_JSON]
    local extra=${5:-'{}'}
    jq -n --arg m "$1" --arg u "$DEVICE_ARTIFACTS/$2" --arg s "$3" --argjson sel "$4" --argjson x "$extra" \
        '{model_id: $m, artifact_url: $u, artifact_sha256: $s, tag_selector: $sel} + $x' |
        curl -sS -X POST -H "X-Api-Key: $KEY" -H "Content-Type: application/json" -d @- "$API/api/deployments"
}

cleanup() { docker rm -f "$LATE_CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# ── 1. The stack comes up ────────────────────────────────────────────────────
step "1. Stack is up"
wait_for 240 "control plane answers /healthz" healthz_ok
wait_for 180 "both demo devices registered and online" devices_online 2
D1=$(device_id device-barcelona-1)
D2=$(device_id device-barcelona-2)
[ -n "$D1" ] && [ -n "$D2" ] || fail "demo devices not found by name"
expect "devices report arch, RAM and a cpu accelerator" \
    'all(.[]; .arch != "" and .mem_total_mb > 0 and (.accelerators | index("cpu") != null))' \
    "$(api GET /api/devices)"

# ── 2. Auth and the web proxy ────────────────────────────────────────────────
step "2. API key and web proxy"
[ "$(http_code "$API/api/devices")" = 401 ] || fail "API answered without a key"
pass "API rejects requests without the key"
wait_for 180 "web app proxies /api with the key added server-side" web_proxy_ok

# ── 3. Validation ────────────────────────────────────────────────────────────
step "3. Request validation"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "X-Api-Key: $KEY" -H "Content-Type: application/json" \
    -d '{"model_id":"m","artifact_url":"http://x/y","artifact_sha256":"'"$(printf 'a%.0s' {1..64})"'"}' \
    "$API/api/deployments")
[ "$code" = 400 ] || fail "empty selector without all_devices returned $code, want 400"
pass "empty selector is refused without all_devices"

# ── 4. Deploy to both devices ────────────────────────────────────────────────
step "4. Deploy the sample model to location=barcelona"
wait_for 60 "sample artifact and digest present" test -s "$ARTIFACTS/sample-gemma-4-e2b.tar.gz.sha256"
SHA_A=$(cut -d' ' -f1 "$ARTIFACTS/sample-gemma-4-e2b.tar.gz.sha256")
resp=$(deploy gemma-4-e2b sample-gemma-4-e2b.tar.gz "$SHA_A" '{"location":"barcelona"}')
A=$(echo "$resp" | jq -r .id)
expect "sent to both devices" '.targeting.targeted | length == 2' "$resp"
wait_for 120 "downloaded, verified and running on both devices" running_on "$A" 2
wait_for 30 "deployment marked completed" deployment_status_is "$A" completed
model_is "$D1" gemma-4-e2b || fail "device-barcelona-1 does not report gemma-4-e2b"
pass "devices report the deployed model"

# ── 5. Telemetry ─────────────────────────────────────────────────────────────
step "5. Telemetry reaches ClickHouse"
wait_for 120 "device-barcelona-1 telemetry stored, labelled stub" has_stub_telemetry "$D1"

# ── 6. Requirements ──────────────────────────────────────────────────────────
step "6. Model requirements"
head -c 65536 /dev/urandom >"$ARTIFACTS/e2e-model-b.bin"
SHA_B=$(sha256sum "$ARTIFACTS/e2e-model-b.bin" | cut -d' ' -f1)
echo "$SHA_B" >"$ARTIFACTS/e2e-model-b.bin.sha256"
resp=$(deploy too-big e2e-model-b.bin "$SHA_B" '{"location":"barcelona"}' '{"requirements":{"min_mem_mb":100000000}}')
SKIP=$(echo "$resp" | jq -r .id)
expect "both devices skipped with a reason" \
    '(.targeting.targeted | length) == 0 and (.targeting.skipped | length) == 2 and all(.targeting.skipped[]; test("needs"))' "$resp"
wait_for 30 "deployment nobody qualifies for is marked failed" deployment_status_is "$SKIP" failed

# ── 7. New model, then roll back ─────────────────────────────────────────────
step "7. Roll out a new model, then roll back"
C=$(deploy e2e-model-b e2e-model-b.bin "$SHA_B" '{"location":"barcelona"}' | jq -r .id)
wait_for 120 "new model running on both devices" running_on "$C" 2
model_is "$D1" e2e-model-b || fail "device did not switch to the new model"
pass "devices switched to the new model"
resp=$(api POST "/api/deployments/$C/rollback" '{}')
# $A is a UUID, so it is safe to splice into the jq filter.
expect "both devices restored to the previous deployment" \
    '(.restored | length) == 2 and all(.restored[]; . == "'"$A"'")' "$resp"
wait_for 120 "device-barcelona-1 back on gemma-4-e2b" model_is "$D1" gemma-4-e2b
wait_for 120 "device-barcelona-2 back on gemma-4-e2b" model_is "$D2" gemma-4-e2b
deployment_status_is "$C" rolled_back || fail "rolled-back deployment has status $(deployment "$C" | jq -r .status)"
pass "deployment marked rolled_back"

# ── 8. Canary and promote ────────────────────────────────────────────────────
step "8. Canary at 1%, then promote to 100%"
resp=$(deploy e2e-canary e2e-model-b.bin "$SHA_B" '{"location":"barcelona"}' '{"rollout_percent":1}')
E=$(echo "$resp" | jq -r .id)
expect "every matching device is either targeted or held back" \
    '(.targeting.targeted | length) + .targeting.held_back == 2' "$resp"
resp=$(api POST "/api/deployments/$E/promote" '{"rollout_percent":100}')
expect "promotion reaches the held-back devices" '.rollout_percent == 100' "$resp"
wait_for 120 "promoted model running on both devices" running_on "$E" 2

# ── 9. Ask a device ──────────────────────────────────────────────────────────
step "9. Ask a device"
T=$(api POST "/api/devices/$D1/tasks" '{"prompt":"How is this device doing?"}' | jq -r .id)
wait_for 60 "on-device agent answered" task_done "$T"
expect "answer and tool call recorded" \
    '(.answer | contains("Stub agent")) and .steps[0].tool == "device_status" and (.model_id | length > 0)' \
    "$(api GET "/api/tasks/$T")"

# ── 10. Control plane restart ────────────────────────────────────────────────
step "10. Restart the control plane"
$COMPOSE restart control-plane >/dev/null
wait_for 120 "control plane healthy again" healthz_ok
wait_for 120 "devices reconnected" devices_online 2
deployment_status_is "$E" completed || fail "deployment state lost across restart"
pass "deployment state survived the restart"

# ── 11. A device that joins later ────────────────────────────────────────────
step "11. A device that joins after the deployment"
L=$(deploy gemma-4-e2b sample-gemma-4-e2b.tar.gz "$SHA_A" '{"location":"lab"}' | jq -r .id)
deployment_status_is "$L" pending || fail "deployment with no matching devices should be pending"
pass "deployment waits for a matching device"
# device-edge-litert (labels location=lab) has its certificate already; run it
# with the stub backend so no LiteRT server is needed.
$COMPOSE run -d --no-deps --name "$LATE_CONTAINER" \
    -e RUNTIME_BACKEND=stub -e LITERT_URL= device-edge-litert >/dev/null
wait_for 120 "late device registered" device_online device-edge-litert
wait_for 120 "late device received the earlier deployment and runs it" running_on "$L" 1

printf '\nAll end-to-end checks passed.\n'

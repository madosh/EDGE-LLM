#!/bin/sh
# Load test: N simulated agents with real mTLS identities against the running
# stack, plus the control plane's and Postgres's resource use while they run.
#
#   scripts/load-test.sh 500            # 500 agents, defaults otherwise
#   scripts/load-test.sh 1000 -soak 2m  # extra flags go to cmd/loadgen
#
# Prints Markdown: RPC latency percentiles, deployment fan-out time, and
# docker stats for the control plane and Postgres at the end of the soak.
# The simulated devices stay in the database as offline afterwards; remove
# them with scripts/load-test.sh --cleanup.
set -e

COMPOSE="${COMPOSE:-docker compose}"
export MSYS_NO_PATHCONV=1

pg() { $COMPOSE exec -T postgres psql -U "${POSTGRES_USER:-cami}" -d "${POSTGRES_DB:-cami}" -tAc "$1"; }

if [ "$1" = "--cleanup" ]; then
    pg "DELETE FROM device_deployments WHERE device_id IN (SELECT id FROM devices WHERE labels ? 'loadgen');
        DELETE FROM devices WHERE labels ? 'loadgen';
        DELETE FROM deployments WHERE model_id = 'loadgen-model';"
    echo "removed simulated devices and their deployments"
    exit 0
fi

AGENTS="${1:-100}"
[ $# -gt 0 ] && shift

$COMPOSE --profile loadtest build loadgen >/dev/null

# Sample resource use near the end of the soak, while every agent is active.
SOAK_S=60
(
    sleep $((AGENTS / 50 + SOAK_S - 10))
    echo
    echo "Resources near the end of the soak:"
    echo
    echo "| Container | CPU | Memory |"
    echo "|-----------|-----|--------|"
    docker stats --no-stream --format '| {{.Name}} | {{.CPUPerc}} | {{.MemUsage}} |' \
        $($COMPOSE ps -q control-plane postgres)
    echo
    echo "Postgres connections: $(pg 'SELECT count(*) FROM pg_stat_activity')"
) &
SAMPLER=$!

$COMPOSE --profile loadtest run --rm -T loadgen -agents "$AGENTS" "$@"
wait $SAMPLER 2>/dev/null || true

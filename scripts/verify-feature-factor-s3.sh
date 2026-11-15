#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_DIR="$REPO_ROOT/app/risk-control-service"
POSTGRES_PORT="${POSTGRES_PORT:-15433}"
CLICKHOUSE_PORT="${CLICKHOUSE_PORT:-18123}"
POSTGRES_FORWARD_PID=""
CLICKHOUSE_FORWARD_PID=""

cleanup() {
  if [[ -n "$POSTGRES_FORWARD_PID" ]]; then
    kill "$POSTGRES_FORWARD_PID" 2>/dev/null || true
  fi
  if [[ -n "$CLICKHOUSE_FORWARD_PID" ]]; then
    kill "$CLICKHOUSE_FORWARD_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

wait_for_port() {
  local port="$1"
  for _ in {1..40}; do
    if nc -z 127.0.0.1 "$port" 2>/dev/null; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

if ! nc -z 127.0.0.1 "$POSTGRES_PORT" 2>/dev/null; then
  kubectl --context "$KUBE_CONTEXT" -n postgres \
    port-forward service/postgres "$POSTGRES_PORT:5432" \
    >/tmp/feature-factor-postgres-forward.log 2>&1 &
  POSTGRES_FORWARD_PID="$!"
  wait_for_port "$POSTGRES_PORT"
fi

if ! nc -z 127.0.0.1 "$CLICKHOUSE_PORT" 2>/dev/null; then
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    port-forward service/risk-clickhouse "$CLICKHOUSE_PORT:8123" \
    >/tmp/feature-factor-clickhouse-forward.log 2>&1 &
  CLICKHOUSE_FORWARD_PID="$!"
  wait_for_port "$CLICKHOUSE_PORT"
fi

TASK_PG_PASSWORD="$(
  kubectl --context "$KUBE_CONTEXT" -n postgres \
    get secret postgres-auth -o jsonpath='{.data.password}' |
  base64 --decode
)"
TASK_CH_USER="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    get secret risk-warehouse-clickhouse -o jsonpath='{.data.username}' |
  base64 --decode
)"
TASK_CH_PASSWORD="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    get secret risk-warehouse-clickhouse -o jsonpath='{.data.password}' |
  base64 --decode
)"

export TEST_POSTGRES_DSN="postgres://local_platform:${TASK_PG_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/local_platform?sslmode=disable"
export TEST_CLICKHOUSE_ENDPOINT="http://127.0.0.1:${CLICKHOUSE_PORT}"
export TEST_CLICKHOUSE_USER="$TASK_CH_USER"
export TEST_CLICKHOUSE_PASSWORD="$TASK_CH_PASSWORD"
export GOPATH="${GOPATH:-/tmp/local-platform-gopath}"
export GOCACHE="${GOCACHE:-/tmp/local-platform-go-cache}"

cd "$SERVICE_DIR"

go test ./internal/featureplatform \
  -run TestKindFeatureFactorsDriveCELDecisionWithSnapshotEvidence \
  -count=1 -v

go test ./internal/risk \
  -run TestPolicyPublisherPublishesFeatureFactorWithRegisteredProvider \
  -count=1 -v

printf '\nFeature Factor S3 verified:\n'
printf '  Feature Factor -> Feature Reference -> one batch read\n'
printf '  Feature values -> CEL -> Decision + Action candidates\n'
printf '  Snapshot as_of, source_run_id and fingerprint -> Factor Evidence\n'
printf '  No business Action was executed.\n'

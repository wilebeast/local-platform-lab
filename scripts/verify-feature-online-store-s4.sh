#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_DIR="$REPO_ROOT/app/risk-control-service"
POSTGRES_PORT="${POSTGRES_PORT:-15433}"
CLICKHOUSE_PORT="${CLICKHOUSE_PORT:-18123}"
REDIS_PORT="${REDIS_PORT:-16379}"
POSTGRES_FORWARD_PID=""
CLICKHOUSE_FORWARD_PID=""
REDIS_FORWARD_PID=""
RESTORE_REDIS_REPLICAS=""

cleanup() {
  for pid in "$POSTGRES_FORWARD_PID" "$CLICKHOUSE_FORWARD_PID" "$REDIS_FORWARD_PID"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  if [[ -n "$RESTORE_REDIS_REPLICAS" ]]; then
    kubectl --context "$KUBE_CONTEXT" -n redis scale deployment/redis \
      --replicas="$RESTORE_REDIS_REPLICAS" >/dev/null
  fi
}
trap cleanup EXIT

wait_for_port() {
  local port="$1"
  for _ in {1..60}; do
    if nc -z 127.0.0.1 "$port" 2>/dev/null; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

start_forward() {
  local namespace="$1"
  local service="$2"
  local mapping="$3"
  local log_file="$4"
  kubectl --context "$KUBE_CONTEXT" -n "$namespace" \
    port-forward "service/$service" "$mapping" >"$log_file" 2>&1 &
  printf '%s' "$!"
}

redis_replicas="$(
  kubectl --context "$KUBE_CONTEXT" -n redis get deployment/redis \
    -o jsonpath='{.spec.replicas}'
)"
if [[ "$redis_replicas" == "0" ]]; then
  RESTORE_REDIS_REPLICAS="0"
  kubectl --context "$KUBE_CONTEXT" -n redis scale deployment/redis --replicas=1 >/dev/null
  kubectl --context "$KUBE_CONTEXT" -n redis rollout status deployment/redis --timeout=120s
fi

if ! nc -z 127.0.0.1 "$POSTGRES_PORT" 2>/dev/null; then
  POSTGRES_FORWARD_PID="$(start_forward postgres postgres "$POSTGRES_PORT:5432" /tmp/feature-s4-postgres-forward.log)"
  wait_for_port "$POSTGRES_PORT"
fi
if ! nc -z 127.0.0.1 "$CLICKHOUSE_PORT" 2>/dev/null; then
  CLICKHOUSE_FORWARD_PID="$(start_forward risk-control-lab risk-clickhouse "$CLICKHOUSE_PORT:8123" /tmp/feature-s4-clickhouse-forward.log)"
  wait_for_port "$CLICKHOUSE_PORT"
fi
if ! nc -z 127.0.0.1 "$REDIS_PORT" 2>/dev/null; then
  REDIS_FORWARD_PID="$(start_forward redis redis "$REDIS_PORT:6379" /tmp/feature-s4-redis-forward.log)"
  wait_for_port "$REDIS_PORT"
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
export TEST_REDIS_ADDR="127.0.0.1:${REDIS_PORT}"
export GOPATH="${GOPATH:-/tmp/local-platform-gopath}"
export GOCACHE="${GOCACHE:-/tmp/local-platform-go-cache}"

cd "$SERVICE_DIR"

go test ./internal/featureplatform \
  -run 'TestRedisCurrentFeatureStorePreservesTypesAndMonotonicWrites|TestPostgresShadowComparisonRecorderIsIdempotent|TestKindRedisOnlineStoreMatchesClickHouseAndPreservesDecision' \
  -count=1 -v

printf '\nFeature Online Store S4 verified:\n'
printf '  Redis preserves typed Current Snapshots and rejects conflicting replay.\n'
printf '  Matching Redis and ClickHouse values produce matched evidence.\n'
printf '  Deliberately mismatched Redis values produce mismatch evidence.\n'
printf '  Both paths return the ClickHouse-backed Decision; no Action was executed.\n'

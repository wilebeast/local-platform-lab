#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_DIR="$REPO_ROOT/app/risk-control-service"
MIGRATION="$SERVICE_DIR/migrations/postgres/003_feature_registry.sql"
POSTGRES_PORT="${POSTGRES_PORT:-15433}"
POSTGRES_FORWARD_PID=""

cleanup() {
  if [[ -n "$POSTGRES_FORWARD_PID" ]]; then
    kill "$POSTGRES_FORWARD_PID" 2>/dev/null || true
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

if ! nc -z 127.0.0.1 "$POSTGRES_PORT" 2>/dev/null; then
  kubectl --context "$KUBE_CONTEXT" -n postgres \
    port-forward service/postgres "$POSTGRES_PORT:5432" \
    >/tmp/feature-s6-postgres-forward.log 2>&1 &
  POSTGRES_FORWARD_PID="$!"
  wait_for_port "$POSTGRES_PORT"
fi

for _ in 1 2; do
  kubectl --context "$KUBE_CONTEXT" -n postgres exec -i postgres-0 -- \
    sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1 -U local_platform -d local_platform' \
    <"$MIGRATION" >/dev/null
done

TASK_PG_PASSWORD="$(
  kubectl --context "$KUBE_CONTEXT" -n postgres \
    get secret postgres-auth -o jsonpath='{.data.password}' |
  base64 --decode
)"
export TEST_POSTGRES_DSN="host=127.0.0.1 port=${POSTGRES_PORT} user=local_platform password=${TASK_PG_PASSWORD} dbname=local_platform sslmode=disable"
export GOPATH="${GOPATH:-/tmp/local-platform-gopath}"
export GOCACHE="${GOCACHE:-/tmp/local-platform-go-cache}"

cd "$SERVICE_DIR"

go test ./internal/featureplatform \
  -run 'TestFeatureServingGovernance|TestBearerGovernancePrincipalResolver|TestFeatureServingGovernanceHTTPHandler|TestOnlineHealthMonitor|TestPostgresFeatureServingGovernance|TestPostgresHealthAlertStore|TestFeatureSetRetirementRequiresShadowRouteAndNoPendingServingChange|TestKindS6GovernanceAlertRollbackAndRetirementLifecycle' \
  -count=1 -v

go test ./cmd/risk-feature-admin ./cmd/risk-feature-health-monitor -count=1 -v

kubectl --context "$KUBE_CONTEXT" -n postgres exec postgres-0 -- \
  sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1 -U local_platform -d local_platform -Atc "
    SELECT to_regclass('\''risk_control.feature_serving_change_requests'\'') IS NOT NULL
       AND to_regclass('\''risk_control.feature_serving_health_alerts'\'') IS NOT NULL;"' |
  grep -qx 't'

printf '\nFeature Platform S6 verified:\n'
printf '  Promotion requires an operator request and a different reviewer.\n'
printf '  Online reads continue comparison and health evidence collection.\n'
printf '  Threshold breaches persist an idempotent rollback recommendation.\n'
printf '  Emergency rollback restores Shadow without changing Feature or Factor meaning.\n'
printf '  Online routes, pending changes and active Factor bindings block retirement.\n'
printf '  The PostgreSQL migration is replay-safe.\n'

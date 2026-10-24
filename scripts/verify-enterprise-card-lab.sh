#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
GATEWAY_PORT="${ENTERPRISE_CARD_GATEWAY_PORT:-29084}"
key="apply-org-coach-$(date +%s)"

kubectl --context "$KUBE_CONTEXT" -n apisix port-forward service/apisix-gateway "$GATEWAY_PORT:80" >/tmp/enterprise-card-gateway-port-forward.log 2>&1 &
gateway_pid=$!
cleanup() {
  kill "$gateway_pid" >/dev/null 2>&1 || true
  wait "$gateway_pid" 2>/dev/null || true
}
trap cleanup EXIT
sleep 2
if ! kill -0 "$gateway_pid" 2>/dev/null; then
  cat /tmp/enterprise-card-gateway-port-forward.log >&2
  exit 1
fi

body='{"organization_id":"org-coach","holder_id":"user-coach","alias":"Engineering"}'
first="$(curl -fsS -X POST "http://127.0.0.1:$GATEWAY_PORT/api/v3/card-lab/cards" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" --data-binary "$body")"
second="$(curl -fsS -X POST "http://127.0.0.1:$GATEWAY_PORT/api/v3/card-lab/cards" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" --data-binary "$body")"
first_id="$(printf '%s' "$first" | jq -r .card_id)"
second_id="$(printf '%s' "$second" | jq -r .card_id)"
[ -n "$first_id" ] && [ "$first_id" = "$second_id" ]
[ "$(printf '%s' "$first" | jq -r .status)" = pending ]

for _ in $(seq 1 30); do
  list="$(curl -fsS "http://127.0.0.1:$GATEWAY_PORT/api/v3/card-lab/cards?organization_id=org-coach")"
  card_status="$(printf '%s' "$list" | jq -r --arg id "$first_id" '.cards[] | select(.id == $id) | .status')"
  [ "$card_status" = active ] && break
  sleep 1
done
[ "$card_status" = active ]
provider_card_id="$(printf '%s' "$list" | jq -r --arg id "$first_id" '.cards[] | select(.id == $id) | .provider_card_id')"
[ -n "$provider_card_id" ] && [ "$provider_card_id" != null ]

operation_count="$(kubectl --context "$KUBE_CONTEXT" -n postgres exec postgres-0 -- env CARD_ID="$first_id" sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U local_platform -d local_platform -At -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM card_lab_channel_operations WHERE card_id = '\''$CARD_ID'\'' AND status = '\''completed'\'' AND attempt_count = 1;"')"
[ "$operation_count" = 1 ]

echo "Enterprise card provider slice passed: pending acceptance, HTTP idempotency, worker claim, provider activation, completed operation."
echo "card_id=$first_id"
echo "provider_card_id=$provider_card_id"
echo "idempotency_key=$key"

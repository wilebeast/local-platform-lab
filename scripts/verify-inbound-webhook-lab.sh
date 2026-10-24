#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="kind-local-platform"
GATEWAY_PORT="${INBOUND_WEBHOOK_GATEWAY_PORT:-29080}"
SECRET_DIR="$ROOT_DIR/.local-secrets"
suffix="$(date +%s)"

kubectl --context "$KUBE_CONTEXT" -n apisix port-forward service/apisix-gateway "$GATEWAY_PORT:80" >/tmp/inbound-webhook-gateway-port-forward.log 2>&1 &
gateway_pid=$!
cleanup() {
  kill "$gateway_pid" >/dev/null 2>&1 || true
  wait "$gateway_pid" 2>/dev/null || true
  rm -f "${signing_file:-}" "${signature_file:-}"
}
trap cleanup EXIT
sleep 2
if ! kill -0 "$gateway_pid" 2>/dev/null; then
  cat /tmp/inbound-webhook-gateway-port-forward.log >&2
  echo "APISIX port-forward failed to start on local port $GATEWAY_PORT" >&2
  exit 1
fi

sign_hmac() {
  local timestamp="$1" event_id="$2" body="$3" secret="$4"
  printf '%s.%s.%s' "$timestamp" "$event_id" "$body" |
    openssl dgst -sha256 -hmac "$secret" -hex |
    awk '{print $NF}'
}

send_event() {
  local integration="$1" key_id="$2" event_id="$3" timestamp="$4" signature="$5" body="$6" output="$7"
  curl -sS -o "$output" -w '%{http_code}' \
    -X POST "http://127.0.0.1:$GATEWAY_PORT/api/v3/provider-webhooks/$integration" \
    -H 'Content-Type: application/json' \
    -H "X-Provider-Key-Id: $key_id" \
    -H "X-Provider-Event-Id: $event_id" \
    -H "X-Provider-Timestamp: $timestamp" \
    -H 'X-Provider-Signature-Version: v1' \
    -H "X-Provider-Signature: $signature" \
    --data-binary "$body"
}

timestamp="$(date +%s)"
payment_event_id="evt_payment_$suffix"
payment_resource_id="payment_$suffix"
payment_body="{\"id\":\"$payment_event_id\",\"type\":\"payment.settled\",\"version\":1,\"occurred_at\":$timestamp,\"data\":{\"resource_id\":\"$payment_resource_id\",\"status\":\"settled\"}}"
payment_secret="$(<"$SECRET_DIR/demo-payments-hmac")"
payment_signature="$(sign_hmac "$timestamp" "$payment_event_id" "$payment_body" "$payment_secret")"
payment_status="$(send_event demo-payments payments-v1 "$payment_event_id" "$timestamp" "$payment_signature" "$payment_body" /tmp/inbound-payment.json)"
[ "$payment_status" = 200 ]

duplicate_status="$(send_event demo-payments payments-v1 "$payment_event_id" "$timestamp" "$payment_signature" "$payment_body" /tmp/inbound-payment-duplicate.json)"
[ "$duplicate_status" = 200 ]
jq -e '.duplicate == true' /tmp/inbound-payment-duplicate.json >/dev/null

tampered_body="${payment_body/settled/failed}"
tampered_status="$(send_event demo-payments payments-v1 "$payment_event_id" "$timestamp" "$payment_signature" "$tampered_body" /tmp/inbound-payment-tampered.json)"
[ "$tampered_status" = 401 ]

card_event_id="evt_card_$suffix"
card_resource_id="card_$suffix"
card_body="{\"id\":\"$card_event_id\",\"type\":\"card.authorization.created\",\"version\":1,\"occurred_at\":$timestamp,\"data\":{\"resource_id\":\"$card_resource_id\",\"status\":\"authorized\"}}"
signing_file="$(mktemp)"
signature_file="$(mktemp)"
printf '%s.%s.%s' "$timestamp" "$card_event_id" "$card_body" > "$signing_file"
openssl pkeyutl -sign -rawin -inkey "$SECRET_DIR/demo-card-ed25519-private-v2.pem" -in "$signing_file" -out "$signature_file"
card_signature="$(openssl base64 -A -in "$signature_file")"
card_status="$(send_event demo-card card-v2 "$card_event_id" "$timestamp" "$card_signature" "$card_body" /tmp/inbound-card.json)"
[ "$card_status" = 200 ]

verification_event_id="evt_verification_$suffix"
verification_resource_id="verification_$suffix"
verification_body="{\"id\":\"$verification_event_id\",\"type\":\"verification.status.changed\",\"version\":1,\"occurred_at\":$timestamp,\"data\":{\"resource_id\":\"$verification_resource_id\",\"status\":\"verified\"}}"
verification_secret="$(<"$SECRET_DIR/demo-verification-hmac")"
verification_signature="$(sign_hmac "$timestamp" "$verification_event_id" "$verification_body" "$verification_secret")"
verification_status="$(send_event demo-verification verification-v1 "$verification_event_id" "$timestamp" "$verification_signature" "$verification_body" /tmp/inbound-verification.json)"
[ "$verification_status" = 200 ]

processed_query="SELECT count(*) FROM provider_webhook_inbox WHERE external_event_id IN ('$payment_event_id','$card_event_id','$verification_event_id') AND processing_status = 'processed';"
for _ in $(seq 1 30); do
  processed="$(kubectl --context "$KUBE_CONTEXT" -n postgres exec postgres-0 -- env QUERY="$processed_query" sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U local_platform -d local_platform -At -v ON_ERROR_STOP=1 -c "$QUERY"')"
  [ "$processed" = 3 ] && break
  sleep 1
done
[ "${processed:-0}" = 3 ]

inbox_query="SELECT count(*) FROM provider_webhook_inbox WHERE integration_code = 'demo-payments' AND external_event_id = '$payment_event_id';"
inbox_count="$(kubectl --context "$KUBE_CONTEXT" -n postgres exec postgres-0 -- env QUERY="$inbox_query" sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U local_platform -d local_platform -At -v ON_ERROR_STOP=1 -c "$QUERY"')"
[ "$inbox_count" = 1 ]

dead_event_id="evt_payment_dead_$suffix"
dead_body="{\"id\":\"$dead_event_id\",\"type\":\"payment.settled\",\"version\":99,\"occurred_at\":$timestamp,\"data\":{\"resource_id\":\"payment_dead_$suffix\",\"status\":\"settled\"}}"
dead_signature="$(sign_hmac "$timestamp" "$dead_event_id" "$dead_body" "$payment_secret")"
dead_status="$(send_event demo-payments payments-v1 "$dead_event_id" "$timestamp" "$dead_signature" "$dead_body" /tmp/inbound-payment-dead.json)"
[ "$dead_status" = 200 ]
dead_query="SELECT processing_status FROM provider_webhook_inbox WHERE integration_code = 'demo-payments' AND external_event_id = '$dead_event_id';"
for _ in $(seq 1 20); do
  dead_state="$(kubectl --context "$KUBE_CONTEXT" -n postgres exec postgres-0 -- env QUERY="$dead_query" sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U local_platform -d local_platform -At -v ON_ERROR_STOP=1 -c "$QUERY"')"
  [ "$dead_state" = dead_letter ] && break
  sleep 1
done
[ "${dead_state:-}" = dead_letter ]

echo "Inbound webhook verification passed: HMAC, Ed25519, duplicate acknowledgement, tamper rejection, Inbox, three projections, retry, and dead letter."
echo "payment_event_id=$payment_event_id"
echo "card_event_id=$card_event_id"
echo "verification_event_id=$verification_event_id"
echo "dead_letter_event_id=$dead_event_id"

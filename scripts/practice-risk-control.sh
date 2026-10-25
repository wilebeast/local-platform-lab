#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:29092}"
CUSTOMER_ID="coach-customer-$(date +%s)"

put_facts() {
  curl -fsS -X PUT "$BASE_URL/api/v1/customers/$CUSTOMER_ID/facts" -H 'Content-Type: application/json' --data-binary "$1"
}
evaluate() {
  curl -fsS -X POST "$BASE_URL/api/v1/evaluations" -H 'Content-Type: application/json' --data-binary "$1"
}

echo "[1/4] Missing POA becomes review"
put_facts '{"verification_status":"approved","residence_country":"SG","poa_verified":false,"questionnaire_completed":false}' | jq
FIRST="eval-review-${CUSTOMER_ID}"
FIRST_RESULT="$(evaluate '{"evaluation_id":"'"$FIRST"'","customer_id":"'"$CUSTOMER_ID"'","product_code":"business_card"}')"
printf '%s\n' "$FIRST_RESULT" | jq
ACTION_ID="$(printf '%s' "$FIRST_RESULT" | jq -r '.actions[0].action_id')"

echo "[2/4] Complete POA action and re-evaluate"
curl -fsS -X POST "$BASE_URL/api/v1/actions/$ACTION_ID/complete" -H 'Content-Type: application/json' --data-binary '{"poa_verified":true}' | jq
SECOND="eval-allow-${CUSTOMER_ID}"
evaluate '{"evaluation_id":"'"$SECOND"'","customer_id":"'"$CUSTOMER_ID"'","product_code":"business_card"}' | jq

echo "[3/4] Denied country overrides remediation"
put_facts '{"verification_status":"approved","residence_country":"KP","poa_verified":true,"questionnaire_completed":false}' | jq
THIRD="eval-deny-${CUSTOMER_ID}"
evaluate '{"evaluation_id":"'"$THIRD"'","customer_id":"'"$CUSTOMER_ID"'","product_code":"business_card"}' | jq

echo "[4/4] Exact replay reuses one evaluation"
curl -fsS -D /tmp/risk-control-replay-headers.txt -o /tmp/risk-control-replay.json -X POST "$BASE_URL/api/v1/evaluations" -H 'Content-Type: application/json' --data-binary '{"evaluation_id":"'"$THIRD"'","customer_id":"'"$CUSTOMER_ID"'","product_code":"business_card"}'
grep -i '^Idempotent-Replay:' /tmp/risk-control-replay-headers.txt
jq '{evaluation_id,decision,policy_version,fact_version}' /tmp/risk-control-replay.json

printf 'customer_id=%s\nreview_evaluation=%s\nallow_evaluation=%s\ndeny_evaluation=%s\n' "$CUSTOMER_ID" "$FIRST" "$SECOND" "$THIRD"

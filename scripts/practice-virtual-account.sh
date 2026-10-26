#!/usr/bin/env bash
set -euo pipefail

API="${VA_API:-http://127.0.0.1:29100}"
OWNER_ID="coach-org-$(date +%s)"
KEY="coach-opening-$(date +%s)"
BODY="{\"idempotency_key\":\"$KEY\",\"owner_id\":\"$OWNER_ID\",\"currency\":\"USD\"}"

echo '1. Objective capability catalog'
curl -fsS "$API/api/v1/va/capabilities" | jq

echo '2. Idempotent asynchronous opening'
first="$(curl -fsS -X POST "$API/api/v1/va/openings" -H 'Content-Type: application/json' --data-binary "$BODY")"
printf '%s\n' "$first" | jq
opening_id="$(printf '%s' "$first" | jq -r .opening_id)"
sleep 2
curl -fsS "$API/api/v1/va/openings/$opening_id" | jq

echo '3. Replay returns the same opening'
curl -fsS -X POST "$API/api/v1/va/openings" -H 'Content-Type: application/json' --data-binary "$BODY" | jq

echo "opening_id=$opening_id"

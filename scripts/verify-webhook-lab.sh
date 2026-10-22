#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
PRODUCER_PORT="${WEBHOOK_PRODUCER_PORT:-18090}"
RECEIVER_PORT="${WEBHOOK_RECEIVER_PORT:-18091}"
EVENT_USER_ID="coach-user-$(date +%s)"

kubectl --context "$KUBE_CONTEXT" -n webhook-lab port-forward service/webhook-event-producer "$PRODUCER_PORT:8080" >/tmp/webhook-producer-port-forward.log 2>&1 &
producer_pid=$!
kubectl --context "$KUBE_CONTEXT" -n webhook-lab port-forward service/merchant-webhook-receiver "$RECEIVER_PORT:8080" >/tmp/webhook-receiver-port-forward.log 2>&1 &
receiver_pid=$!
trap 'kill "$producer_pid" "$receiver_pid" >/dev/null 2>&1 || true' EXIT
sleep 2

curl -fsS -X POST "http://127.0.0.1:$PRODUCER_PORT/events/profile-updated" \
  -H 'Content-Type: application/json' \
  -d "{\"user_id\":\"$EVENT_USER_ID\",\"verification_status\":\"verified\"}" >/tmp/webhook-published-event.json

for _ in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:$RECEIVER_PORT/received-events" | grep -q "$EVENT_USER_ID"; then
    echo "Webhook end-to-end verification passed for $EVENT_USER_ID."
    curl -fsS "http://127.0.0.1:$RECEIVER_PORT/received-events"
    exit 0
  fi
  sleep 1
done

echo "Webhook event was not delivered within 30 seconds." >&2
exit 1

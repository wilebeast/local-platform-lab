#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="kind-local-platform"
GRPC_PORT="${NOTIFICATION_GRPC_PORT:-19003}"
MAILPIT_PORT="${MAILPIT_HTTP_PORT:-18025}"
RECIPIENT="coach-$(date +%s)@example.test"
CODE_RECIPIENT="code-$(date +%s)@example.test"

kubectl --context "$KUBE_CONTEXT" -n notification-lab port-forward service/notification-service "$GRPC_PORT:9003" >/tmp/notification-grpc-port-forward.log 2>&1 &
grpc_pid=$!
kubectl --context "$KUBE_CONTEXT" -n notification-lab port-forward service/mailpit "$MAILPIT_PORT:8025" >/tmp/mailpit-port-forward.log 2>&1 &
mailpit_pid=$!
trap 'kill "$grpc_pid" "$mailpit_pid" >/dev/null 2>&1 || true' EXIT
sleep 2

cd "$ROOT_DIR/app/notification-service"
GOCACHE=/tmp/local-platform-go-cache go run ./cmd/notification-client \
  -action send-template -recipient "$RECIPIENT" -address "127.0.0.1:$GRPC_PORT" >/tmp/notification-template-response.json

template_arrived=false
for _ in $(seq 1 20); do
  if curl -fsS "http://127.0.0.1:$MAILPIT_PORT/api/v1/messages" |
    jq -e --arg recipient "$RECIPIENT" '.messages[] | select(.To[0].Address == $recipient)' >/dev/null; then
    template_arrived=true
    break
  fi
  sleep 1
done

if [[ "$template_arrived" != true ]]; then
  echo "Template email did not arrive in Mailpit." >&2
  exit 1
fi

GOCACHE=/tmp/local-platform-go-cache go run ./cmd/notification-client \
  -action issue-code -recipient "$CODE_RECIPIENT" -purpose sign_in \
  -address "127.0.0.1:$GRPC_PORT" >/tmp/notification-code-response.json

message_id=""
for _ in $(seq 1 20); do
  message_id="$(
    curl -fsS "http://127.0.0.1:$MAILPIT_PORT/api/v1/messages" |
      jq -r --arg recipient "$CODE_RECIPIENT" \
        '[.messages[] | select(.To[0].Address == $recipient)][0].ID // empty'
  )"
  [[ -n "$message_id" ]] && break
  sleep 1
done

if [[ -z "$message_id" ]]; then
  echo "Verification email did not arrive in Mailpit." >&2
  exit 1
fi

verification_code="$(
  curl -fsS "http://127.0.0.1:$MAILPIT_PORT/api/v1/message/$message_id" |
    jq -r '.HTML' |
    grep -Eo '[0-9]{6}' |
    head -n 1
)"

if [[ ! "$verification_code" =~ ^[0-9]{6}$ ]]; then
  echo "Could not extract a six-digit verification code." >&2
  exit 1
fi

GOCACHE=/tmp/local-platform-go-cache go run ./cmd/notification-client \
  -action verify-code -recipient "$CODE_RECIPIENT" -purpose sign_in \
  -code "$verification_code" -address "127.0.0.1:$GRPC_PORT" \
  >/tmp/notification-code-first-verify.json

GOCACHE=/tmp/local-platform-go-cache go run ./cmd/notification-client \
  -action verify-code -recipient "$CODE_RECIPIENT" -purpose sign_in \
  -code "$verification_code" -address "127.0.0.1:$GRPC_PORT" \
  >/tmp/notification-code-second-verify.json

jq -e '.valid == true' /tmp/notification-code-first-verify.json >/dev/null
jq -e '.valid == false' /tmp/notification-code-second-verify.json >/dev/null

echo "Template email arrived for $RECIPIENT."
echo "Verification code was accepted once and rejected on replay for $CODE_RECIPIENT."
echo "Mailpit UI: http://localhost:$MAILPIT_PORT"

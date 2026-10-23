#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
MAILPIT_PORT="${MAILPIT_HTTP_PORT:-18025}"

echo "Mailpit UI: http://localhost:$MAILPIT_PORT"
echo "Keep this terminal open; press Ctrl-C to stop."
exec kubectl --context "$KUBE_CONTEXT" -n notification-lab \
  port-forward service/mailpit "$MAILPIT_PORT:8025"

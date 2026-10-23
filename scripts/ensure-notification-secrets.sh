#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"

kubectl --context "$KUBE_CONTEXT" create namespace notification-lab --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

password="$(kubectl --context "$KUBE_CONTEXT" -n postgres get secret postgres-auth -o jsonpath='{.data.password}' | base64 --decode)"
dsn="postgres://local_platform:${password}@postgres.postgres.svc.cluster.local:5432/local_platform?sslmode=disable"
pepper="$(kubectl --context "$KUBE_CONTEXT" -n notification-lab get secret notification-runtime -o jsonpath='{.data.email-code-pepper}' 2>/dev/null | base64 --decode || true)"
if [ -z "$pepper" ]; then
  pepper="$(openssl rand -hex 32)"
fi

kubectl --context "$KUBE_CONTEXT" -n notification-lab create secret generic notification-runtime \
  --from-literal=postgres-dsn="$dsn" \
  --from-literal=email-code-pepper="$pepper" \
  --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

echo "Notification runtime secrets are present only in Kubernetes."

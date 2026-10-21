#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"

kubectl --context "$KUBE_CONTEXT" create namespace postgres --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null
kubectl --context "$KUBE_CONTEXT" create namespace user-service --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

password="$(kubectl --context "$KUBE_CONTEXT" -n postgres get secret postgres-auth -o jsonpath='{.data.password}' 2>/dev/null | base64 --decode || true)"
if [ -z "$password" ]; then
  password="$(openssl rand -hex 24)"
fi

kubectl --context "$KUBE_CONTEXT" -n postgres create secret generic postgres-auth \
  --from-literal=password="$password" \
  --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

dsn="postgres://local_platform:${password}@postgres.postgres.svc.cluster.local:5432/local_platform?sslmode=disable"
kubectl --context "$KUBE_CONTEXT" -n user-service create secret generic postgres-auth \
  --from-literal=dsn="$dsn" \
  --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

echo "PostgreSQL credentials are present in the cluster and are not stored in Git."

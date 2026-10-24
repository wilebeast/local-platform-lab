#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SECRET_DIR="$ROOT_DIR/.local-secrets"
WEBHOOK_SECRET_FILE="$SECRET_DIR/enterprise-card-webhook-secret"
mkdir -p "$SECRET_DIR"
if [ ! -s "$WEBHOOK_SECRET_FILE" ]; then
  openssl rand -hex 32 >"$WEBHOOK_SECRET_FILE"
  chmod 600 "$WEBHOOK_SECRET_FILE"
fi
webhook_secret="$(tr -d '\n' <"$WEBHOOK_SECRET_FILE")"
postgres_password="$(kubectl --context "$KUBE_CONTEXT" -n postgres get secret postgres-auth -o jsonpath='{.data.password}' | base64 --decode)"
postgres_dsn="postgres://local_platform:${postgres_password}@postgres.postgres.svc.cluster.local:5432/local_platform?sslmode=disable"

kubectl --context "$KUBE_CONTEXT" create namespace enterprise-card-lab --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null
kubectl --context "$KUBE_CONTEXT" -n enterprise-card-lab create secret generic enterprise-card-runtime \
  --from-literal=postgres-dsn="$postgres_dsn" \
  --from-literal=provider-webhook-secret="$webhook_secret" \
  --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

echo "Enterprise card runtime credentials are stored only in local files and a Kubernetes Secret."

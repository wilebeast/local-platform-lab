#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SECRET_DIR="$ROOT_DIR/.local-secrets"
SECRET_FILE="$SECRET_DIR/webhook-shared-secret"
KUBE_CONTEXT="kind-local-platform"

mkdir -p "$SECRET_DIR"
chmod 700 "$SECRET_DIR"
if [ ! -s "$SECRET_FILE" ]; then
  openssl rand -base64 48 | tr -d '\n' > "$SECRET_FILE"
  chmod 600 "$SECRET_FILE"
fi

kubectl --context "$KUBE_CONTEXT" create namespace webhook-lab --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f -
kubectl --context "$KUBE_CONTEXT" -n webhook-lab create secret generic webhook-shared-secret \
  --from-literal="secret=$(<"$SECRET_FILE")" \
  --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f -

echo "Webhook Secret is ready in namespace webhook-lab."

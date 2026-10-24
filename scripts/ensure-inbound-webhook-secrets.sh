#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SECRET_DIR="$ROOT_DIR/.local-secrets"
KUBE_CONTEXT="kind-local-platform"

mkdir -p "$SECRET_DIR"
chmod 700 "$SECRET_DIR"

for name in demo-payments-hmac demo-verification-hmac; do
  file="$SECRET_DIR/$name"
  if [ ! -s "$file" ]; then
    openssl rand -base64 48 | tr -d '\n' > "$file"
    chmod 600 "$file"
  fi
done

card_private_key="$SECRET_DIR/demo-card-ed25519-private.pem"
if [ ! -s "$card_private_key" ]; then
  openssl genpkey -algorithm ED25519 -out "$card_private_key"
  chmod 600 "$card_private_key"
fi
card_public_key="$(openssl pkey -in "$card_private_key" -pubout -outform DER | tail -c 32 | openssl base64 -A)"

card_private_key_v2="$SECRET_DIR/demo-card-ed25519-private-v2.pem"
if [ ! -s "$card_private_key_v2" ]; then
  openssl genpkey -algorithm ED25519 -out "$card_private_key_v2"
  chmod 600 "$card_private_key_v2"
fi
card_public_key_v2="$(openssl pkey -in "$card_private_key_v2" -pubout -outform DER | tail -c 32 | openssl base64 -A)"

postgres_password="$(kubectl --context "$KUBE_CONTEXT" -n postgres get secret postgres-auth -o jsonpath='{.data.password}' | base64 --decode)"
postgres_dsn="postgres://local_platform:${postgres_password}@postgres.postgres.svc.cluster.local:5432/local_platform?sslmode=disable"

kubectl --context "$KUBE_CONTEXT" create namespace inbound-webhook-lab --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null
kubectl --context "$KUBE_CONTEXT" -n inbound-webhook-lab create secret generic inbound-webhook-runtime \
  --from-literal=postgres-dsn="$postgres_dsn" \
  --from-literal=demo-payments-hmac="$(<"$SECRET_DIR/demo-payments-hmac")" \
  --from-literal=demo-verification-hmac="$(<"$SECRET_DIR/demo-verification-hmac")" \
  --from-literal=demo-card-ed25519-public="$card_public_key" \
  --from-literal=demo-card-ed25519-public-v2="$card_public_key_v2" \
  --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

echo "Inbound webhook runtime credentials are present only in local files and Kubernetes Secret."

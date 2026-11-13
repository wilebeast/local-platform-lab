#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
NAMESPACE="xxl-job-lab"
SECRET_NAME="xxl-job-runtime"

kubectl --context "$KUBE_CONTEXT" create namespace "$NAMESPACE" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

read_key() {
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get secret "$SECRET_NAME" \
    -o "jsonpath={.data.$1}" 2>/dev/null | base64 --decode || true
}

mysql_password="$(read_key mysql-root-password)"
admin_password="$(read_key admin-password)"
executor_token="$(read_key executor-access-token)"
sso_token_key="$(read_key sso-token-key)"
postgres_dsn="$(read_key postgres-dsn)"

[ -n "$mysql_password" ] || mysql_password="$(openssl rand -hex 24)"
[ -n "$admin_password" ] || admin_password="$(openssl rand -hex 10)"
[ -n "$executor_token" ] || executor_token="$(openssl rand -hex 24)"
[ -n "$sso_token_key" ] || sso_token_key="$(openssl rand -hex 32)"
[ -n "$postgres_dsn" ] || postgres_dsn="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    get secret risk-control-runtime \
    -o jsonpath='{.data.postgres-dsn}' |
  base64 --decode
)"
[ -n "$postgres_dsn" ] || {
  printf 'risk-control-lab/risk-control-runtime is missing postgres-dsn\n' >&2
  exit 1
}

kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" create secret generic "$SECRET_NAME" \
  --from-literal=mysql-root-password="$mysql_password" \
  --from-literal=admin-password="$admin_password" \
  --from-literal=executor-access-token="$executor_token" \
  --from-literal=sso-token-key="$sso_token_key" \
  --from-literal=postgres-dsn="$postgres_dsn" \
  --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

printf 'XXL-JOB credentials are stored only in Kubernetes Secret %s/%s.\n' "$NAMESPACE" "$SECRET_NAME"

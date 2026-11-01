#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
postgres_password="$(kubectl --context "$KUBE_CONTEXT" -n postgres get secret postgres-auth -o jsonpath='{.data.password}' | base64 --decode)"
postgres_dsn="postgres://local_platform:${postgres_password}@postgres.postgres.svc.cluster.local:5432/local_platform?sslmode=disable"

kubectl --context "$KUBE_CONTEXT" create namespace risk-control-lab --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null
kubectl --context "$KUBE_CONTEXT" -n risk-control-lab create secret generic risk-control-runtime \
  --from-literal=postgres-dsn="$postgres_dsn" \
  --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

if ! kubectl --context "$KUBE_CONTEXT" -n risk-control-lab get secret risk-raw-minio >/dev/null 2>&1; then
  minio_access_key="riskraw"
  minio_secret_key="$(openssl rand -hex 24)"
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab create secret generic risk-raw-minio \
    --from-literal=access-key="$minio_access_key" \
    --from-literal=secret-key="$minio_secret_key" \
    >/dev/null
fi

if ! kubectl --context "$KUBE_CONTEXT" -n risk-control-lab get secret risk-warehouse-clickhouse >/dev/null 2>&1; then
  clickhouse_password="$(openssl rand -hex 24)"
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab create secret generic risk-warehouse-clickhouse \
    --from-literal=username="risk" \
    --from-literal=password="$clickhouse_password" \
    >/dev/null
fi

echo "Risk control database, object-storage and warehouse credentials are stored only in Kubernetes Secrets."

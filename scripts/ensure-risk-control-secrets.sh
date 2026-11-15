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

if ! kubectl --context "$KUBE_CONTEXT" -n risk-control-lab get secret risk-feature-admin-runtime >/dev/null 2>&1; then
  operator_token="$(openssl rand -hex 32)"
  reviewer_token="$(openssl rand -hex 32)"
  emergency_token="$(openssl rand -hex 32)"
  operator_digest="$(printf '%s' "$operator_token" | shasum -a 256 | awk '{print $1}')"
  reviewer_digest="$(printf '%s' "$reviewer_token" | shasum -a 256 | awk '{print $1}')"
  emergency_digest="$(printf '%s' "$emergency_token" | shasum -a 256 | awk '{print $1}')"
  credentials_json="$(
    jq -cn \
      --arg operator "$operator_digest" \
      --arg reviewer "$reviewer_digest" \
      --arg emergency "$emergency_digest" \
      '[
        {subject:"feature-operator",token_sha256:$operator,roles:["feature_operator"]},
        {subject:"feature-reviewer",token_sha256:$reviewer,roles:["feature_reviewer"]},
        {subject:"feature-incident-commander",token_sha256:$emergency,roles:["feature_emergency_operator"]}
      ]'
  )"
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab create secret generic risk-feature-admin-runtime \
    --from-literal=credentials-json="$credentials_json" \
    --from-literal=operator-token="$operator_token" \
    --from-literal=reviewer-token="$reviewer_token" \
    --from-literal=emergency-token="$emergency_token" \
    >/dev/null
fi

echo "Risk control database, object-storage, warehouse and Feature Admin credentials are stored only in Kubernetes Secrets."

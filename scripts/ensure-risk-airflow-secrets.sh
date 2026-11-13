#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
NAMESPACE="risk-control-lab"

kubectl --context "$KUBE_CONTEXT" create namespace "$NAMESPACE" --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

password="$(kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get secret risk-airflow-postgresql \
  -o jsonpath='{.data.password}' 2>/dev/null | base64 --decode || true)"
if [ -z "$password" ]; then
  password="$(openssl rand -hex 24)"
fi

kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" create secret generic risk-airflow-postgresql \
  --from-literal=password="$password" \
  --from-literal=postgres-password="$password" \
  --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

connection="postgresql://airflow:${password}@risk-airflow-postgresql.${NAMESPACE}.svc.cluster.local:5432/airflow"
kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" create secret generic risk-airflow-metadata \
  --from-literal=connection="$connection" \
  --dry-run=client -o yaml |
  kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

echo "Airflow database credentials are stored only in Kubernetes Secrets."

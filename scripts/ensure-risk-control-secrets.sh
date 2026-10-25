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

echo "Risk control database credentials are stored only in a Kubernetes Secret."

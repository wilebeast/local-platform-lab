#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"

kubectl --context "$KUBE_CONTEXT" -n user-service rollout status deployment/user-service --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n user-service get pods -o wide
kubectl --context "$KUBE_CONTEXT" -n argocd get application user-service -o custom-columns=APP:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status

response="$(curl --fail --silent --show-error -H 'X-User-ID: grpc-demo-user' http://localhost:18080/api/v3/profile)"
grep -q '"userId":"grpc-demo-user"' <<<"$response"
grep -q '"source":"user-service-grpc"' <<<"$response"
echo "$response"

#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
LOCAL_PORT="${API_BFF_PORT:-18888}"

kubectl --context "$KUBE_CONTEXT" -n api-bff rollout status deployment/api-bff --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n api-bff get pods -o wide
kubectl --context "$KUBE_CONTEXT" -n argocd get application api-bff -o custom-columns=APP:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status

kubectl --context "$KUBE_CONTEXT" -n api-bff port-forward service/api-bff "$LOCAL_PORT:80" >/tmp/api-bff-port-forward.log 2>&1 &
forward_pid=$!
trap 'kill "$forward_pid" >/dev/null 2>&1 || true' EXIT
sleep 2

curl --fail --silent --show-error "http://localhost:$LOCAL_PORT/healthz"
printf '\n'
curl --fail --silent --show-error -H 'X-User-ID: local-demo-user' "http://localhost:$LOCAL_PORT/api/v3/profile"
printf '\n'

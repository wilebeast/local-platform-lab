#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
LOCAL_PORT="${PLATFORM_DEMO_PORT:-18081}"

kubectl --context "$KUBE_CONTEXT" -n platform-demo rollout status deployment/platform-demo --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n platform-demo get pods -o wide
kubectl --context "$KUBE_CONTEXT" -n argocd get application platform-demo -o custom-columns=APP:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status
kubectl --context "$KUBE_CONTEXT" -n platform-demo port-forward service/platform-demo "$LOCAL_PORT:80" >/tmp/platform-demo-port-forward.log 2>&1 &
forward_pid=$!
trap 'kill "$forward_pid" >/dev/null 2>&1 || true' EXIT
sleep 2

curl --fail --silent --show-error "http://localhost:$LOCAL_PORT/healthz"
printf '\n'
curl --fail --silent --show-error "http://localhost:$LOCAL_PORT/"
printf '\n'

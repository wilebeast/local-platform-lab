#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
kubectl --context "$KUBE_CONTEXT" -n account-service rollout status deployment/account-service --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n account-service get pods -o wide
kubectl --context "$KUBE_CONTEXT" -n argocd get application account-service -o custom-columns=APP:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status

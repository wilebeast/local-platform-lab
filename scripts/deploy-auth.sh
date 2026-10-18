#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="kind-local-platform"

"$ROOT_DIR/scripts/ensure-auth-secrets.sh"
git -C "$ROOT_DIR/gitops" add apps/redis
if ! git -C "$ROOT_DIR/gitops" diff --cached --quiet; then
  git -C "$ROOT_DIR/gitops" commit -m "deploy redis for authentication"
fi
"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/gitops/argocd/redis.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application redis argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n redis rollout status deployment/redis --timeout=180s

"$ROOT_DIR/scripts/deploy-user-service.sh"
"$ROOT_DIR/scripts/deploy-api-bff.sh"
"$ROOT_DIR/scripts/deploy-local-gateway.sh"

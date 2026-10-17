#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="kind-local-platform"
git -C "$ROOT_DIR/gitops" revert --no-edit HEAD
"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application platform-demo argocd.argoproj.io/refresh=hard --overwrite
echo "Rollback commit pushed. Run make verify."

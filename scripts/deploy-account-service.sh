#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/account-service:$TAG"
KUSTOMIZATION="$GITOPS_DIR/apps/account-service/overlays/local/kustomization.yaml"
KUBE_CONTEXT="kind-local-platform"

docker build -f "$ROOT_DIR/app/account-service/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$KUSTOMIZATION"
rm -f "$KUSTOMIZATION.bak"
git -C "$GITOPS_DIR" add apps/account-service
git -C "$GITOPS_DIR" commit -m "deploy account-service $TAG"
"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/account-service.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application account-service argocd.argoproj.io/refresh=hard --overwrite
echo "Published $IMAGE"

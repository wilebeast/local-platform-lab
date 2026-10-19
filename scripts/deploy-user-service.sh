#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/user-service:$TAG"
LOCAL_KUSTOMIZATION="$GITOPS_DIR/apps/user-service/overlays/local/kustomization.yaml"
KUBE_CONTEXT="kind-local-platform"

"$ROOT_DIR/scripts/ensure-auth-secrets.sh"

docker build -f "$ROOT_DIR/app/user-service/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"

sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$LOCAL_KUSTOMIZATION"
rm -f "$LOCAL_KUSTOMIZATION.bak"
git -C "$GITOPS_DIR" add apps/user-service
git -C "$GITOPS_DIR" commit -m "deploy user-service $TAG"
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/user-service.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application user-service argocd.argoproj.io/refresh=hard --overwrite
echo "Published $IMAGE"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/user-service:$TAG"
LOCAL_KUSTOMIZATION="$ROOT_DIR/gitops/apps/user-service/overlays/local/kustomization.yaml"
KUBE_CONTEXT="kind-local-platform"

"$ROOT_DIR/scripts/ensure-auth-secrets.sh"

docker build -f "$ROOT_DIR/app/user-service/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"

sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$LOCAL_KUSTOMIZATION"
rm -f "$LOCAL_KUSTOMIZATION.bak"
git -C "$ROOT_DIR/gitops" add apps/user-service
git -C "$ROOT_DIR/gitops" commit -m "deploy user-service $TAG"
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/gitops/argocd/user-service.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application user-service argocd.argoproj.io/refresh=hard --overwrite
echo "Published $IMAGE"

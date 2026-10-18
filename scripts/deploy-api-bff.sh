#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/api-bff:$TAG"
LOCAL_KUSTOMIZATION="$ROOT_DIR/gitops/apps/api-bff/overlays/local/kustomization.yaml"
KUBE_CONTEXT="kind-local-platform"

"$ROOT_DIR/scripts/ensure-auth-secrets.sh"

docker build -f "$ROOT_DIR/app/api-bff/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"

sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$LOCAL_KUSTOMIZATION"
rm -f "$LOCAL_KUSTOMIZATION.bak"
git -C "$ROOT_DIR/gitops" add apps/api-bff
git -C "$ROOT_DIR/gitops" commit -m "deploy api-bff $TAG"
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/gitops/argocd/api-bff.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application api-bff argocd.argoproj.io/refresh=hard --overwrite
echo "Published $IMAGE"

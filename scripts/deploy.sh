#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/platform-demo:$TAG"
KUBE_CONTEXT="kind-local-platform"

docker build -t "$IMAGE" "$ROOT_DIR/demo"
docker push "$IMAGE"

LOCAL_KUSTOMIZATION="$ROOT_DIR/gitops/apps/platform-demo/overlays/local/kustomization.yaml"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$LOCAL_KUSTOMIZATION"
rm -f "$LOCAL_KUSTOMIZATION.bak"
git -C "$ROOT_DIR/gitops" add apps/platform-demo/overlays/local/kustomization.yaml
git -C "$ROOT_DIR/gitops" commit -m "deploy platform-demo $TAG"
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" -n argocd annotate application platform-demo argocd.argoproj.io/refresh=hard --overwrite
echo "Published $IMAGE"

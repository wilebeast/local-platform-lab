#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/api-bff:$TAG"
LOCAL_KUSTOMIZATION="$GITOPS_DIR/apps/api-bff/overlays/local/kustomization.yaml"
KUBE_CONTEXT="kind-local-platform"

"$ROOT_DIR/scripts/ensure-auth-secrets.sh"

docker build -f "$ROOT_DIR/app/api-bff/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"

sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$LOCAL_KUSTOMIZATION"
rm -f "$LOCAL_KUSTOMIZATION.bak"
git -C "$GITOPS_DIR" add apps/api-bff
git -C "$GITOPS_DIR" commit -m "deploy api-bff $TAG"
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/api-bff.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application api-bff argocd.argoproj.io/refresh=hard --overwrite
echo "Published $IMAGE"

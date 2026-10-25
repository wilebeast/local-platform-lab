#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="kind-local-platform"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/risk-control-service:$TAG"
BUILD_GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

docker build --build-arg GOPROXY="$BUILD_GOPROXY" -f "$ROOT_DIR/app/risk-control-service/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"
"$ROOT_DIR/scripts/ensure-risk-control-secrets.sh"

KUSTOMIZATION="$GITOPS_DIR/apps/risk-control-lab/overlays/local/kustomization.yaml"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$KUSTOMIZATION"
rm -f "$KUSTOMIZATION.bak"

git -C "$GITOPS_DIR" add apps/risk-control-lab argocd/risk-control-lab.yaml
if ! git -C "$GITOPS_DIR" diff --cached --quiet; then
  git -C "$GITOPS_DIR" commit -m "deploy risk control lab $TAG"
fi
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/risk-control-lab.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application risk-control-lab argocd.argoproj.io/refresh=hard --overwrite
for _ in $(seq 1 60); do
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab get deployment risk-control-service >/dev/null 2>&1 && break
  sleep 1
done
kubectl --context "$KUBE_CONTEXT" -n risk-control-lab rollout status deployment/risk-control-service --timeout=240s

echo "Risk control lab deployed with image tag $TAG."

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="kind-local-platform"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/enterprise-card-service:$TAG"
BUILD_GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

docker build --build-arg GOPROXY="$BUILD_GOPROXY" -f "$ROOT_DIR/app/enterprise-card-service/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"
"$ROOT_DIR/scripts/ensure-enterprise-card-secrets.sh"

KUSTOMIZATION="$GITOPS_DIR/apps/enterprise-card-lab/overlays/local/kustomization.yaml"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$KUSTOMIZATION"
rm -f "$KUSTOMIZATION.bak"

git -C "$GITOPS_DIR" add apps/enterprise-card-lab apps/apisix argocd/enterprise-card-lab.yaml
if ! git -C "$GITOPS_DIR" diff --cached --quiet; then
  git -C "$GITOPS_DIR" commit -m "deploy enterprise card lab $TAG"
fi
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/enterprise-card-lab.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application enterprise-card-lab argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application apisix argocd.argoproj.io/refresh=hard --overwrite

for deployment in enterprise-card-service card-provider-simulator enterprise-card-worker; do
  for _ in $(seq 1 60); do
    kubectl --context "$KUBE_CONTEXT" -n enterprise-card-lab get deployment "$deployment" >/dev/null 2>&1 && break
    sleep 1
  done
  kubectl --context "$KUBE_CONTEXT" -n enterprise-card-lab rollout status "deployment/$deployment" --timeout=240s
done
kubectl --context "$KUBE_CONTEXT" -n apisix rollout restart deployment/apisix
kubectl --context "$KUBE_CONTEXT" -n apisix rollout status deployment/apisix --timeout=240s

echo "Enterprise card lab deployed with image tag $TAG."

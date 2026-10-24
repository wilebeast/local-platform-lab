#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="kind-local-platform"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/inbound-webhook-service:$TAG"

docker build -f "$ROOT_DIR/app/inbound-webhook-service/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"
"$ROOT_DIR/scripts/ensure-postgres-secrets.sh"
"$ROOT_DIR/scripts/ensure-inbound-webhook-secrets.sh"

KUSTOMIZATION="$GITOPS_DIR/apps/inbound-webhook-lab/overlays/local/kustomization.yaml"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$KUSTOMIZATION"
rm -f "$KUSTOMIZATION.bak"

git -C "$GITOPS_DIR" add apps/inbound-webhook-lab apps/apisix argocd/inbound-webhook-lab.yaml
if ! git -C "$GITOPS_DIR" diff --cached --quiet; then
  git -C "$GITOPS_DIR" commit -m "deploy inbound webhook lab $TAG"
fi
"$ROOT_DIR/scripts/push-gitops.sh"

kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/inbound-webhook-lab.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application inbound-webhook-lab argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application apisix argocd.argoproj.io/refresh=hard --overwrite

for deployment in inbound-webhook-receiver inbound-webhook-worker; do
  found=false
  for _ in $(seq 1 60); do
    if kubectl --context "$KUBE_CONTEXT" -n inbound-webhook-lab get deployment "$deployment" >/dev/null 2>&1; then
      found=true
      break
    fi
    sleep 1
  done
  if [[ "$found" != true ]]; then
    echo "Deployment $deployment was not created by Argo CD within 60 seconds." >&2
    exit 1
  fi
  kubectl --context "$KUBE_CONTEXT" -n inbound-webhook-lab rollout status "deployment/$deployment" --timeout=240s
done
kubectl --context "$KUBE_CONTEXT" -n apisix rollout restart deployment/apisix
kubectl --context "$KUBE_CONTEXT" -n apisix rollout status deployment/apisix --timeout=240s

echo "Inbound webhook lab deployed with image tag $TAG."

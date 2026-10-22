#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="kind-local-platform"
TAG="$(date +%Y%m%d%H%M%S)"

for service in webhook-event-producer webhook-sender merchant-webhook-receiver; do
  image="localhost:5001/platform-lab/$service:$TAG"
  docker build -f "$ROOT_DIR/app/$service/Dockerfile" -t "$image" "$ROOT_DIR"
  docker push "$image"
done

"$ROOT_DIR/scripts/ensure-webhook-secrets.sh"

KUSTOMIZATION="$GITOPS_DIR/apps/webhook-lab/overlays/local/kustomization.yaml"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/g" "$KUSTOMIZATION"
rm -f "$KUSTOMIZATION.bak"

git -C "$GITOPS_DIR" add apps/kafka apps/webhook-lab argocd/kafka.yaml argocd/webhook-lab.yaml
if ! git -C "$GITOPS_DIR" diff --cached --quiet; then
  git -C "$GITOPS_DIR" commit -m "deploy webhook lab $TAG"
fi
"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/kafka.yaml" -f "$GITOPS_DIR/argocd/webhook-lab.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application kafka argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application webhook-lab argocd.argoproj.io/refresh=hard --overwrite

kubectl --context "$KUBE_CONTEXT" -n kafka rollout status statefulset/kafka --timeout=300s
kubectl --context "$KUBE_CONTEXT" -n webhook-lab rollout status deployment/merchant-webhook-receiver --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n webhook-lab rollout status deployment/webhook-event-producer --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n webhook-lab rollout status deployment/webhook-sender --timeout=180s

echo "Webhook lab deployed with image tag $TAG."

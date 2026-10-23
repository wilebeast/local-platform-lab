#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="kind-local-platform"
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/notification-service:$TAG"

docker build -f "$ROOT_DIR/app/notification-service/Dockerfile" -t "$IMAGE" "$ROOT_DIR"
docker push "$IMAGE"
"$ROOT_DIR/scripts/ensure-postgres-secrets.sh"
"$ROOT_DIR/scripts/ensure-notification-secrets.sh"

KUSTOMIZATION="$GITOPS_DIR/apps/notification-lab/overlays/local/kustomization.yaml"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$KUSTOMIZATION"
rm -f "$KUSTOMIZATION.bak"

git -C "$GITOPS_DIR" add apps/notification-lab argocd/notification-lab.yaml
if ! git -C "$GITOPS_DIR" diff --cached --quiet; then
  git -C "$GITOPS_DIR" commit -m "deploy notification lab $TAG"
fi
"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/notification-lab.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application notification-lab argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n notification-lab rollout status deployment/mailpit --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n notification-lab rollout status deployment/notification-service --timeout=240s

echo "Notification lab deployed with image tag $TAG."

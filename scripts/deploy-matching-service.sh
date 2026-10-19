#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="kind-local-platform"
SERVICE_DIR="${MATCHING_SERVICE_DIR:-$ROOT_DIR/../matching-service}"
if [ ! -f "$SERVICE_DIR/go.mod" ]; then
  echo "Independent matching-service project not found: $SERVICE_DIR" >&2
  exit 1
fi
python3 -B -m unittest discover -s "$ROOT_DIR/scripts/tests" -v
TAG="$(date +%Y%m%d%H%M%S)"
IMAGE="localhost:5001/platform-lab/matching-service:$TAG"
# Always pin the local context; never use the user's current context.
kubectl --context "$KUBE_CONTEXT" get node local-platform-control-plane >/dev/null
(cd "$SERVICE_DIR" && go test -race ./... && go vet ./...)
docker build -f "$SERVICE_DIR/Dockerfile" -t "$IMAGE" "$SERVICE_DIR"
docker push "$IMAGE"
kubectl --context "$KUBE_CONTEXT" create namespace matching-service --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f -
if ! kubectl --context "$KUBE_CONTEXT" -n matching-service get secret matching-admin >/dev/null 2>&1; then
  secret_file="$(mktemp)"
  trap 'rm -f "$secret_file"' EXIT
  openssl rand -hex 32 > "$secret_file"
  kubectl --context "$KUBE_CONTEXT" -n matching-service create secret generic matching-admin --from-file=token="$secret_file"
  rm -f "$secret_file"
  trap - EXIT
fi
python3 - "$GITOPS_DIR/apps/matching-service/overlays/local/kustomization.yaml" "$TAG" <<'PYCODE'
import sys,re
from pathlib import Path
p=Path(sys.argv[1]);p.write_text(re.sub(r'newTag: .*', 'newTag: "'+sys.argv[2]+'"', p.read_text()))
PYCODE
git -C "$GITOPS_DIR" add apps/matching-service argocd/matching-service.yaml
git -C "$GITOPS_DIR" commit -m "deploy matching-service $TAG"
GITOPS_REVISION="$(git -C "$GITOPS_DIR" rev-parse HEAD)"
# Push only to the cluster-local Git server, without rewriting history.
kubectl --context "$KUBE_CONTEXT" -n git-system port-forward service/git-server 19419:9418 > /tmp/matching-git-forward.log 2>&1 &
forward_pid=$!
trap 'kill "$forward_pid" >/dev/null 2>&1 || true' EXIT
for i in $(seq 1 30); do
  if git -C "$GITOPS_DIR" ls-remote git://127.0.0.1:19419/platform.git >/dev/null 2>&1; then break; fi
  sleep 1
done
git -C "$GITOPS_DIR" push git://127.0.0.1:19419/platform.git main
kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/matching-service.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application matching-service argocd.argoproj.io/refresh=hard --overwrite
python3 "$ROOT_DIR/scripts/wait-matching-release.py" "$IMAGE" "$GITOPS_REVISION" --timeout 180
echo "Published $IMAGE"

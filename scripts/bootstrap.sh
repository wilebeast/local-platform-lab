#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
CLUSTER_NAME="local-platform"
REGISTRY_NAME="local-registry"
KUBE_CONTEXT="kind-$CLUSTER_NAME"
COLIMA_DNS_PRIMARY="${COLIMA_DNS_PRIMARY:-1.1.1.1}"
COLIMA_DNS_SECONDARY="${COLIMA_DNS_SECONDARY:-8.8.8.8}"
LOCAL_HTTP_PROXY="${LOCAL_HTTP_PROXY:-}"

"$ROOT_DIR/scripts/doctor.sh"

if ! colima status >/dev/null 2>&1; then
  colima start --cpu 4 --memory 8 --disk 30 --dns "$COLIMA_DNS_PRIMARY"
fi

# Some macOS proxy/DNS combinations leave the VM's systemd-resolved stub absent.
# Repair it locally without changing the host resolver.
if ! colima ssh -- test -e /etc/resolv.conf; then
  colima ssh -- sudo rm -f /etc/resolv.conf
  colima ssh -- sh -lc "printf 'nameserver %s\nnameserver %s\n' '$COLIMA_DNS_PRIMARY' '$COLIMA_DNS_SECONDARY' | sudo tee /etc/resolv.conf >/dev/null"
fi

if ! docker inspect "$REGISTRY_NAME" >/dev/null 2>&1; then
  docker run -d --restart=always -p 5001:5000 --name "$REGISTRY_NAME" registry:2
elif [ "$(docker inspect -f '{{.State.Running}}' "$REGISTRY_NAME")" != "true" ]; then
  docker start "$REGISTRY_NAME" >/dev/null
fi

if ! kind get clusters | grep -qx "$CLUSTER_NAME"; then
  kind create cluster --name "$CLUSTER_NAME" --config "$ROOT_DIR/kind/cluster.yaml"
fi

# Image pulls happen inside the Kind node. Forward an explicitly configured
# proxy to containerd, or remove a stale local proxy configuration.
docker exec "$CLUSTER_NAME-control-plane" mkdir -p /etc/systemd/system/containerd.service.d
if [ -n "$LOCAL_HTTP_PROXY" ]; then
  {
    printf '[Service]\n'
    printf 'Environment="HTTP_PROXY=%s"\n' "$LOCAL_HTTP_PROXY"
    printf 'Environment="HTTPS_PROXY=%s"\n' "$LOCAL_HTTP_PROXY"
    printf 'Environment="NO_PROXY=localhost,127.0.0.1,local-registry,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,.svc,.cluster.local"\n'
  } | docker exec -i "$CLUSTER_NAME-control-plane" tee /etc/systemd/system/containerd.service.d/http-proxy.conf >/dev/null
else
  docker exec "$CLUSTER_NAME-control-plane" rm -f /etc/systemd/system/containerd.service.d/http-proxy.conf
fi
docker exec "$CLUSTER_NAME-control-plane" systemctl daemon-reload
docker exec "$CLUSTER_NAME-control-plane" systemctl restart containerd

if [ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "$REGISTRY_NAME")" = "null" ]; then
  docker network connect kind "$REGISTRY_NAME"
fi

docker build -f "$ROOT_DIR/platform/git-server.Dockerfile" -t localhost:5001/platform-lab/git-server:local "$ROOT_DIR/platform"
docker push localhost:5001/platform-lab/git-server:local

kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/platform/git-server.yaml"
kubectl --context "$KUBE_CONTEXT" -n git-system rollout status deployment/git-server --timeout=180s

helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
helm repo update argo
for attempt in 1 2 3; do
  if helm upgrade --install argocd argo/argo-cd \
    --kube-context "$KUBE_CONTEXT" \
    --namespace argocd --create-namespace \
    --set configs.params."server\.insecure"=true \
    --wait --timeout 8m; then
    break
  fi
  if [ "$attempt" -eq 3 ]; then
    echo "Argo CD chart install failed after 3 attempts" >&2
    exit 1
  fi
  sleep 3
done

git_pod="$(kubectl --context "$KUBE_CONTEXT" -n git-system get pod -l app=git-server -o jsonpath='{.items[0].metadata.name}')"
if ! kubectl --context "$KUBE_CONTEXT" -n git-system exec "$git_pod" -- test -d /srv/git/platform.git; then
  kubectl --context "$KUBE_CONTEXT" -n git-system exec "$git_pod" -- git init --bare --initial-branch=main /srv/git/platform.git
fi

"$ROOT_DIR/scripts/push-gitops.sh" --seed
"$ROOT_DIR/scripts/ensure-auth-secrets.sh"
"$ROOT_DIR/scripts/ensure-postgres-secrets.sh"
"$ROOT_DIR/scripts/ensure-webhook-secrets.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd"

echo "Bootstrap complete. Run: make deploy && make verify"

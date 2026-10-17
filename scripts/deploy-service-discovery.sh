#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="kind-local-platform"

git -C "$ROOT_DIR/gitops" add \
  apps/etcd \
  apps/nacos \
  argocd/etcd.yaml \
  argocd/nacos.yaml
if ! git -C "$ROOT_DIR/gitops" diff --cached --quiet; then
  git -C "$ROOT_DIR/gitops" commit -m "deploy local service discovery"
fi

"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/gitops/argocd/etcd.yaml"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/gitops/argocd/nacos.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application etcd argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application nacos argocd.argoproj.io/refresh=hard --overwrite

echo "Published Nacos and Etcd GitOps applications"

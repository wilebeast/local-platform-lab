#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="kind-local-platform"

git -C "$GITOPS_DIR" add \
  apps/etcd \
  apps/nacos \
  argocd/etcd.yaml \
  argocd/nacos.yaml
if ! git -C "$GITOPS_DIR" diff --cached --quiet; then
  git -C "$GITOPS_DIR" commit -m "deploy local service discovery"
fi

"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/etcd.yaml"
kubectl --context "$KUBE_CONTEXT" apply -f "$GITOPS_DIR/argocd/nacos.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application etcd argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application nacos argocd.argoproj.io/refresh=hard --overwrite

echo "Published Nacos and Etcd GitOps applications"

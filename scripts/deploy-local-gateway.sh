#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="kind-local-platform"

git -C "$ROOT_DIR/gitops" add \
  apps/apisix \
  apps/local-alb \
  apps/platform-demo/overlays/local/kustomization.yaml

if ! git -C "$ROOT_DIR/gitops" diff --cached --quiet; then
  git -C "$ROOT_DIR/gitops" commit -m "deploy local ALB and APISIX gateway"
fi

"$ROOT_DIR/scripts/push-gitops.sh"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/gitops/argocd/apisix.yaml"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT_DIR/gitops/argocd/local-alb.yaml"
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application apisix argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application local-alb argocd.argoproj.io/refresh=hard --overwrite
kubectl --context "$KUBE_CONTEXT" -n argocd annotate application platform-demo argocd.argoproj.io/refresh=hard --overwrite

for namespace in apisix local-alb; do
  for attempt in $(seq 1 60); do
    if kubectl --context "$KUBE_CONTEXT" get namespace "$namespace" >/dev/null 2>&1; then
      break
    fi
    if [ "$attempt" -eq 60 ]; then
      echo "Timed out waiting for namespace/$namespace" >&2
      exit 1
    fi
    sleep 2
  done
done

for workload in apisix/apisix local-alb/local-alb; do
  namespace="${workload%%/*}"
  deployment="${workload##*/}"
  for attempt in $(seq 1 60); do
    if kubectl --context "$KUBE_CONTEXT" -n "$namespace" get deployment "$deployment" >/dev/null 2>&1; then
      break
    fi
    if [ "$attempt" -eq 60 ]; then
      echo "Timed out waiting for deployment/$deployment in $namespace" >&2
      exit 1
    fi
    sleep 2
  done
done

kubectl --context "$KUBE_CONTEXT" -n apisix rollout status deployment/apisix --timeout=300s
kubectl --context "$KUBE_CONTEXT" -n local-alb rollout status deployment/local-alb --timeout=300s

echo "Local gateway ready: http://localhost:18080"

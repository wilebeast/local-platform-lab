#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"

kubectl --context "$KUBE_CONTEXT" -n etcd rollout status statefulset/etcd --timeout=300s
kubectl --context "$KUBE_CONTEXT" -n nacos rollout status deployment/nacos --timeout=300s

kubectl --context "$KUBE_CONTEXT" -n etcd exec statefulset/etcd -- /usr/local/bin/etcdctl endpoint health
kubectl --context "$KUBE_CONTEXT" -n nacos exec deployment/nacos -- curl --fail --silent --show-error http://127.0.0.1:8848/nacos/v1/console/health/readiness
printf '\n'

kubectl --context "$KUBE_CONTEXT" -n argocd get application etcd nacos \
  -o custom-columns=APP:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status

echo "Nacos: nacos.nacos.svc.cluster.local:8848"
echo "Etcd:  etcd-client.etcd.svc.cluster.local:2379"

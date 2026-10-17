#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"

nacos_instances="$(kubectl --context "$KUBE_CONTEXT" -n nacos exec deployment/nacos -- \
  curl --silent --show-error 'http://127.0.0.1:8848/nacos/v1/ns/instance/list?serviceName=user-service&groupName=DEFAULT_GROUP')"
grep -q '"healthy":true' <<<"$nacos_instances"
grep -q '"serviceName":"DEFAULT_GROUP@@user-service"' <<<"$nacos_instances"

etcd_instances="$(kubectl --context "$KUBE_CONTEXT" -n etcd exec statefulset/etcd -- \
  /usr/local/bin/etcdctl get /local-platform/services/user-service/ --prefix)"
grep -q '/local-platform/services/user-service/' <<<"$etcd_instances"

health="$(curl --fail --silent --show-error http://localhost:18080/healthz)"
profile="$(curl --fail --silent --show-error -H 'X-User-ID: registry-verification' http://localhost:18080/api/v3/profile)"
grep -q '"discoveryProvider":"nacos"' <<<"$health"
grep -q '"userId":"registry-verification"' <<<"$profile"

echo "Nacos registration: healthy"
echo "Etcd registration: healthy"
echo "GET /healthz -> $health"
echo "GET /api/v3/profile -> $profile"

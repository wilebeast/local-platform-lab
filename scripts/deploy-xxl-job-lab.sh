#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
XXL_JOB_DIR="${XXL_JOB_DIR:-$ROOT_DIR/../xxl-job}"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
TAG="${XXL_JOB_IMAGE_TAG:-$(date +%Y%m%d%H%M%S)}"
REGISTRY="${LOCAL_REGISTRY:-localhost:5001/platform-lab}"
RISK_PREVIEW_IMAGE="$REGISTRY/risk-control-offline-preview:$TAG"
SERVICES_DOCKERFILE="$XXL_JOB_DIR/docker/services.Dockerfile"
SERVICES_BUILD_ARGS=(--build-arg "RISK_PREVIEW_IMAGE=$RISK_PREVIEW_IMAGE")

if [ -n "${XXL_JOB_PREBUILT_BUILD_IMAGE:-}" ]; then
  SERVICES_DOCKERFILE="$XXL_JOB_DIR/docker/runtime-services.Dockerfile"
  SERVICES_BUILD_ARGS+=(--build-arg "XXL_JOB_BUILD_IMAGE=$XXL_JOB_PREBUILT_BUILD_IMAGE")
  printf 'Reusing verified build image %s.\n' "$XXL_JOB_PREBUILT_BUILD_IMAGE"
fi

for required in "$XXL_JOB_DIR/pom.xml" "$GITOPS_DIR/apps/xxl-job-lab/overlays/local/kustomization.yaml"; do
  [ -f "$required" ] || { printf 'missing required file: %s\n' "$required" >&2; exit 1; }
done

docker build -f "$XXL_JOB_DIR/docker/mysql-bootstrap.Dockerfile" \
  -t "$REGISTRY/xxl-job-mysql:$TAG" "$XXL_JOB_DIR"
docker build --build-arg "GOPROXY=${GOPROXY:-https://goproxy.cn,direct}" \
  -f "$ROOT_DIR/app/risk-control-service/Dockerfile.offline-preview" \
  -t "$RISK_PREVIEW_IMAGE" "$ROOT_DIR"
docker build -f "$SERVICES_DOCKERFILE" "${SERVICES_BUILD_ARGS[@]}" --target admin \
  -t "$REGISTRY/xxl-job-admin:$TAG" "$XXL_JOB_DIR"
docker build -f "$SERVICES_DOCKERFILE" "${SERVICES_BUILD_ARGS[@]}" --target risk-shadow-executor \
  -t "$REGISTRY/xxl-job-risk-shadow-executor:$TAG" "$XXL_JOB_DIR"

docker push "$REGISTRY/xxl-job-mysql:$TAG"
docker push "$RISK_PREVIEW_IMAGE"
docker push "$REGISTRY/xxl-job-admin:$TAG"
docker push "$REGISTRY/xxl-job-risk-shadow-executor:$TAG"

kustomization="$GITOPS_DIR/apps/xxl-job-lab/overlays/local/kustomization.yaml"
sed -i.bak "s/newTag: .*/newTag: \"$TAG\"/" "$kustomization"
rm -f "$kustomization.bak"

"$ROOT_DIR/scripts/ensure-xxl-job-secrets.sh"
kubectl --context "$KUBE_CONTEXT" apply -k "$GITOPS_DIR/apps/xxl-job-lab/overlays/local"

kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab rollout status statefulset/xxl-job-mysql --timeout=240s
kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab rollout status deployment/xxl-job-admin --timeout=240s
kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab rollout status deployment/xxl-job-risk-shadow-executor --timeout=240s

printf 'XXL-JOB lab deployed with immutable image tag %s.\n' "$TAG"
printf 'GitOps image tags changed locally and are not committed or pushed.\n'

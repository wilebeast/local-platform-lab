#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="${1:-light}"
KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"

if [ "${LAB_PROFILE_DRY_RUN:-0}" = "1" ]; then
  exec "$ROOT_DIR/scripts/lab-profile.sh" apply "$PROFILE"
fi

if ! colima status >/dev/null 2>&1; then
  echo "Starting Colima..."
  colima start
fi

if ! kind get clusters 2>/dev/null | grep -qx local-platform; then
  PROFILE="$PROFILE" exec "$ROOT_DIR/scripts/bootstrap.sh"
fi

if [ "$(docker inspect -f '{{.State.Running}}' local-platform-control-plane 2>/dev/null || true)" != "true" ]; then
  docker start local-platform-control-plane >/dev/null
fi

for _ in $(seq 1 30); do
  if kubectl --context "$KUBE_CONTEXT" get namespace argocd >/dev/null 2>&1; then
    exec "$ROOT_DIR/scripts/lab-profile.sh" apply "$PROFILE"
  fi
  sleep 2
done

printf 'Kind cluster resumed, but Argo CD did not become ready within 60 seconds.\n' >&2
printf 'Run make bootstrap PROFILE=%s to repair the platform.\n' "$PROFILE" >&2
exit 1

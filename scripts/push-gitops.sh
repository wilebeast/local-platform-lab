#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${GIT_FORWARD_PORT:-19418}"
seed="${1:-}"
KUBE_CONTEXT="kind-local-platform"

kubectl --context "$KUBE_CONTEXT" -n git-system port-forward service/git-server "$PORT:9418" >/tmp/platform-lab-git-port-forward.log 2>&1 &
forward_pid=$!
trap 'kill "$forward_pid" >/dev/null 2>&1 || true' EXIT
sleep 2

if [ "$seed" = "--seed" ]; then
  git -C "$ROOT_DIR/gitops" init -b main >/dev/null 2>&1 || true
  git -C "$ROOT_DIR/gitops" config user.name "Local Platform Local CI"
  git -C "$ROOT_DIR/gitops" config user.email "local-ci@local.test"
  git -C "$ROOT_DIR/gitops" add .
  git -C "$ROOT_DIR/gitops" commit -m "bootstrap local GitOps" >/dev/null 2>&1 || true
fi

if git -C "$ROOT_DIR/gitops" remote get-url local >/dev/null 2>&1; then
  git -C "$ROOT_DIR/gitops" remote set-url local "git://localhost:$PORT/platform.git"
else
  git -C "$ROOT_DIR/gitops" remote add local "git://localhost:$PORT/platform.git"
fi
git -C "$ROOT_DIR/gitops" push --force local main

#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
LOCAL_PORT="${AUTH_HTTPS_PORT:-18443}"

echo "Local authenticated API: https://localhost:$LOCAL_PORT"
echo "The local certificate is self-signed; curl should use --insecure unless you install the local CA."
kubectl --context "$KUBE_CONTEXT" -n local-alb port-forward service/local-alb "$LOCAL_PORT:443"

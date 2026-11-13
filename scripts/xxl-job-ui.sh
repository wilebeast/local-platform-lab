#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
PORT="${XXL_JOB_UI_PORT:-18080}"

printf 'XXL-JOB UI: http://127.0.0.1:%s\n' "$PORT"
printf 'Username: admin\n'
printf 'Read the generated password with:\n'
printf '  kubectl --context %s -n xxl-job-lab get secret xxl-job-runtime -o jsonpath='"'"'{.data.admin-password}'"'"' | base64 --decode; echo\n' "$KUBE_CONTEXT"
kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab port-forward service/xxl-job-admin "$PORT:8080"

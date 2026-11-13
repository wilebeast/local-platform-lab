#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CUTOVER_SCRIPT="$ROOT_DIR/scripts/cutover-xxl-job.sh"

grep -Fq '"enabled":false' "$CUTOVER_SCRIPT"
grep -Fq 'wait_for_argocd_idle' "$CUTOVER_SCRIPT"
grep -Fq "patch cronjob offline-risk-scan" "$CUTOVER_SCRIPT"
grep -Fq '"suspend":true' "$CUTOVER_SCRIPT"

if grep -Fq 'apply -k' "$CUTOVER_SCRIPT"; then
  printf 'cutover must not replay the complete risk-control overlay\n' >&2
  exit 1
fi

printf 'PASS: cutover disables automated sync, waits for Argo CD, and patches only the scheduler field.\n'

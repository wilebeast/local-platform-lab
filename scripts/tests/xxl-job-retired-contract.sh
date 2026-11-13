#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/verify-xxl-job-retired.sh"

grep -Fq 'get cronjob offline-risk-scan --ignore-not-found' "$SCRIPT"
grep -Fq "'1:0:0 5 * * * ? *:0'" "$SCRIPT"
grep -Fq 'true:true:Synced' "$SCRIPT"
grep -Fq 'Synced:Healthy' "$SCRIPT"

if grep -Eq 'kubectl .* (patch|apply|delete)|startJob|stopJob' "$SCRIPT"; then
  printf 'retired-state verification must remain read-only\n' >&2
  exit 1
fi

printf 'PASS: retired-state verification checks GitOps and scheduler ownership without mutation.\n'

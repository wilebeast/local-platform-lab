#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT_DIR/scripts/verify-xxl-job-retirement-readiness.sh"

grep -Fq 'MIN_SCHEDULED_SUCCESSES:-3' "$SCRIPT"
grep -Fq 'EXPECTED_PRODUCTION_SCHEDULE:-0 5 * * * ? *' "$SCRIPT"
grep -Fq "l.executor_param='{}'" "$SCRIPT"
grep -Fq "status='failed'" "$SCRIPT"
grep -Fq 'get cronjob offline-risk-scan' "$SCRIPT"
grep -Fq "scheduler_state" "$SCRIPT"

if grep -Eq 'kubectl .* (patch|apply|delete)|startJob|stopJob' "$SCRIPT"; then
  printf 'retirement readiness check must remain read-only\n' >&2
  exit 1
fi

printf 'PASS: S5 readiness gate counts natural schedules, checks business failures, and remains read-only.\n'

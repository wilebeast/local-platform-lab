#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
AS_OF="${AS_OF:?set AS_OF to an hour-aligned persisted baseline timestamp}"

if ! printf '%s' "$AS_OF" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:00:00Z$'; then
  printf 'AS_OF must be hour-aligned RFC3339 UTC, got %s\n' "$AS_OF" >&2
  exit 1
fi

mysql_query() {
  local query="$1"
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec statefulset/xxl-job-mysql -- \
    env QUERY="$query" \
    sh -lc 'mysql -N -B -uroot -p"$MYSQL_ROOT_PASSWORD" xxl_job -e "$QUERY"' \
    2>/dev/null
}

before_log_id="$(mysql_query 'SELECT COALESCE(MAX(id), 0) FROM xxl_job_log WHERE job_id=3;')"
invalid_parameter="$(jq -cn --arg asOf "$AS_OF" \
  '{as_of:$asOf,command:"forbidden"}')"
trigger_payload="$(jq -cn --arg parameter "$invalid_parameter" \
  '{id:3,executorParam:$parameter,addressList:""}')"

trigger_response="$(
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec deployment/xxl-job-risk-shadow-executor -- \
    env TRIGGER_PAYLOAD="$trigger_payload" \
    sh -lc 'curl -fsS -X POST http://xxl-job-admin:8080/api/triggerJob \
      -H "Content-Type: application/json" \
      -H "XXL-JOB-APPNAME: risk-shadow-executor" \
      -H "XXL-JOB-ACCESS-TOKEN: $XXL_JOB_EXECUTOR_ACCESS_TOKEN" \
      --data "$TRIGGER_PAYLOAD"'
)"

[ "$(printf '%s' "$trigger_response" | jq -r '.code')" = '200' ] || {
  printf 'failed to submit controlled failure: %s\n' "$trigger_response" >&2
  exit 1
}

failure_log_id=''
for _ in $(seq 1 30); do
  failure_log_id="$(mysql_query "
    SELECT id
    FROM xxl_job_log
    WHERE job_id=3
      AND id > $before_log_id
      AND executor_param = '$invalid_parameter'
      AND handle_code IN (200, 500)
    ORDER BY id DESC
    LIMIT 1;
  ")"
  [ -n "$failure_log_id" ] && break
  sleep 1
done

[ -n "$failure_log_id" ] || {
  printf 'timed out waiting for controlled failure\n' >&2
  exit 1
}

failure_state="$(mysql_query "
  SELECT CONCAT(trigger_code, ':', handle_code, ':', handle_msg)
  FROM xxl_job_log
  WHERE id=$failure_log_id;
")"

case "$failure_state" in
  200:500:*'unsupported field: command'*) ;;
  *)
    printf 'unexpected controlled failure state: %s\n' "$failure_state" >&2
    exit 1
    ;;
esac

printf 'Controlled failure: log_id=%s trigger=200 handle=500 reason=unsupported_field\n' \
  "$failure_log_id"

AS_OF="$AS_OF" KUBE_CONTEXT="$KUBE_CONTEXT" \
  "$ROOT_DIR/scripts/compare-xxl-job-business-shadow.sh"

printf 'PASS: invalid parameters are auditable, and a fixed-as_of historical rerun recovers without Shadow side effects.\n'

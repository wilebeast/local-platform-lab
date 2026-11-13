#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
AS_OF="${AS_OF:?set AS_OF to an hour-aligned RFC3339 UTC timestamp}"
POLICY_KEY="${POLICY_KEY:-rapid_fund_movement}"
POLICY_VERSION="${POLICY_VERSION:-1}"

if ! printf '%s' "$AS_OF" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:00:00Z$'; then
  printf 'AS_OF must be hour-aligned RFC3339 UTC, got %s\n' "$AS_OF" >&2
  exit 1
fi
if ! printf '%s' "$POLICY_KEY" | grep -Eq '^[a-z][a-z0-9_]{0,63}$'; then
  printf 'POLICY_KEY must use lower snake case, got %s\n' "$POLICY_KEY" >&2
  exit 1
fi
if ! printf '%s' "$POLICY_VERSION" | grep -Eq '^[1-9][0-9]*$'; then
  printf 'POLICY_VERSION must be a positive integer, got %s\n' "$POLICY_VERSION" >&2
  exit 1
fi

postgres_query() {
  local query="$1"
  kubectl --context "$KUBE_CONTEXT" -n postgres exec postgres-0 -- \
    env QUERY="$query" \
    sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U local_platform -d local_platform -At -v ON_ERROR_STOP=1 -c "$QUERY"'
}

mysql_query() {
  local query="$1"
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec statefulset/xxl-job-mysql -- \
    env QUERY="$query" \
    sh -lc 'mysql -N -B -uroot -p"$MYSQL_ROOT_PASSWORD" xxl_job -e "$QUERY"' \
    2>/dev/null
}

original_result="$(postgres_query "
  SELECT result_json::text
  FROM risk_offline_runs
  WHERE as_of = '$AS_OF'::timestamptz
    AND policy_key = '$POLICY_KEY'
    AND policy_version = $POLICY_VERSION
  ORDER BY created_at DESC
  LIMIT 1;
")"

[ -n "$original_result" ] || {
  printf 'no persisted CronJob result exists for as_of=%s policy=%s/v%s\n' \
    "$AS_OF" "$POLICY_KEY" "$POLICY_VERSION" >&2
  exit 1
}

before_log_id="$(mysql_query 'SELECT COALESCE(MAX(id), 0) FROM xxl_job_log WHERE job_id=3;')"
executor_parameter="$(jq -cn --arg asOf "$AS_OF" '{as_of:$asOf}')"
trigger_payload="$(jq -cn --arg parameter "$executor_parameter" \
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
  printf 'XXL-JOB trigger failed: %s\n' "$trigger_response" >&2
  exit 1
}

log_id=''
for _ in $(seq 1 30); do
  log_id="$(mysql_query "
    SELECT id
    FROM xxl_job_log
    WHERE job_id=3
      AND id > $before_log_id
      AND executor_param = '$executor_parameter'
      AND handle_code IN (200, 500)
    ORDER BY id DESC
    LIMIT 1;
  ")"
  [ -n "$log_id" ] && break
  sleep 1
done

[ -n "$log_id" ] || {
  printf 'timed out waiting for XXL-JOB Shadow execution\n' >&2
  exit 1
}

execution_state="$(mysql_query "
  SELECT CONCAT(trigger_code, ':', handle_code, ':', handle_msg)
  FROM xxl_job_log
  WHERE id=$log_id;
")"

case "$execution_state" in
  200:200:*) ;;
  *)
    printf 'XXL-JOB Shadow execution failed: %s\n' "$execution_state" >&2
    exit 1
    ;;
esac

task_log_path="$(
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec deployment/xxl-job-risk-shadow-executor -- \
    sh -lc 'find /tmp/xxl-job/jobhandler -type f -name "$1.log" -print -quit' sh "$log_id"
)"

[ -n "$task_log_path" ] || {
  printf 'executor task log not found for log_id=%s\n' "$log_id" >&2
  exit 1
}

preview_result="$(
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec deployment/xxl-job-risk-shadow-executor -- \
    sh -lc 'sed -n "s/.* preview=//p" "$1" | tail -n 1' sh "$task_log_path"
)"

[ "$(printf '%s' "$preview_result" | jq -r '.status')" = 'preview' ]
[ "$(printf '%s' "$preview_result" | jq -r '.dry_run')" = 'true' ]

normalize_filter='{
  as_of,
  window_start,
  window_end,
  policy_key,
  policy_version,
  subjects_evaluated,
  findings_created,
  evaluations: ([.evaluations[] | {
    subject_type,
    subject_id,
    decision,
    reason_codes: (.reason_codes | sort),
    matched_strategy_key,
    matched_rule_key,
    features,
    finding_candidate: (
      if .finding == null then null else {
        risk_type: .finding.risk_type,
        severity: .finding.severity,
        status: .finding.status
      } end
    ),
    action_candidates: (
      if .restriction == null then [] else [{
        action_code: "activate_restriction",
        params: {capability: .restriction.capability}
      }] end
    )
  }] | sort_by(.subject_type, .subject_id, .features.currency))
}'

original_normalized="$(printf '%s' "$original_result" | jq -Sc "$normalize_filter")"
preview_normalized="$(printf '%s' "$preview_result" | jq -Sc "$normalize_filter")"

if [ "$original_normalized" != "$preview_normalized" ]; then
  printf '%s\n' 'business-output mismatch:' >&2
  diff -u \
    <(printf '%s' "$original_normalized" | jq .) \
    <(printf '%s' "$preview_normalized" | jq .) >&2 || true
  exit 1
fi

shadow_run_id="$(printf '%s' "$preview_result" | jq -r '.run_id')"
if ! printf '%s' "$shadow_run_id" | grep -Eq '^xxl-shadow-[0-9]{8}T[0-9]{6}Z$'; then
  printf 'unexpected Shadow run id: %s\n' "$shadow_run_id" >&2
  exit 1
fi

side_effect_rows="$(postgres_query "
  SELECT
    (SELECT count(*) FROM risk_offline_runs WHERE run_id='$shadow_run_id') || ':' ||
    (SELECT count(*) FROM risk_findings WHERE run_id='$shadow_run_id') || ':' ||
    (SELECT count(*)
     FROM risk_restrictions r
     JOIN risk_findings f ON f.finding_id=r.finding_id
     WHERE f.run_id='$shadow_run_id');
")"

[ "$side_effect_rows" = '0:0:0' ] || {
  printf 'Shadow produced forbidden side effects: runs:findings:restrictions=%s\n' \
    "$side_effect_rows" >&2
  exit 1
}

printf 'Original persisted path: as_of=%s subjects=%s findings=%s\n' \
  "$AS_OF" \
  "$(printf '%s' "$original_result" | jq -r '.subjects_evaluated')" \
  "$(printf '%s' "$original_result" | jq -r '.findings_created')"
printf 'XXL-JOB Preview: log_id=%s subjects=%s findings=%s dry_run=true\n' \
  "$log_id" \
  "$(printf '%s' "$preview_result" | jq -r '.subjects_evaluated')" \
  "$(printf '%s' "$preview_result" | jq -r '.findings_created')"
printf 'PASS: Decision, reason codes, features and Action candidates match; Shadow wrote no Run, Finding or Restriction.\n'
printf 'S4 remains blocked until the configured continuous observation window also passes.\n'

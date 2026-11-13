#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
MIN_SCHEDULED_SUCCESSES="${MIN_SCHEDULED_SUCCESSES:-3}"
MAX_LAST_SUCCESS_AGE_SECONDS="${MAX_LAST_SUCCESS_AGE_SECONDS:-7200}"
EXPECTED_PRODUCTION_SCHEDULE="${EXPECTED_PRODUCTION_SCHEDULE:-0 5 * * * ? *}"

case "$MIN_SCHEDULED_SUCCESSES:$MAX_LAST_SUCCESS_AGE_SECONDS" in
  *[!0-9:]*|:*)
    printf 'readiness thresholds must be positive integers\n' >&2
    exit 2
    ;;
esac

[ "$MIN_SCHEDULED_SUCCESSES" -gt 0 ] || {
  printf 'MIN_SCHEDULED_SUCCESSES must be greater than zero\n' >&2
  exit 2
}

[ "$MAX_LAST_SUCCESS_AGE_SECONDS" -gt 0 ] || {
  printf 'MAX_LAST_SUCCESS_AGE_SECONDS must be greater than zero\n' >&2
  exit 2
}

mysql_query() {
  local query="$1"
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec statefulset/xxl-job-mysql -- \
    env QUERY="$query" \
    sh -lc 'mysql -N -B -uroot -p"$MYSQL_ROOT_PASSWORD" xxl_job -e "$QUERY"' \
    2>/dev/null
}

postgres_query() {
  local query="$1"
  kubectl --context "$KUBE_CONTEXT" -n postgres \
    exec postgres-0 -- \
    env QUERY="$query" \
    sh -lc 'PGPASSWORD="$POSTGRES_PASSWORD" psql \
      -U local_platform -d local_platform -At -F ":" -c "$QUERY"' \
    2>/dev/null
}

cron_suspended="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    get cronjob offline-risk-scan -o jsonpath='{.spec.suspend}'
)"

scheduler_state="$(mysql_query "
  SELECT CONCAT(
    (SELECT trigger_status FROM xxl_job_info
      WHERE executor_handler='offlineRiskScan' ORDER BY id LIMIT 1), ':',
    (SELECT trigger_status FROM xxl_job_info
      WHERE executor_handler='offlineRiskScanShadow' ORDER BY id LIMIT 1), ':',
    (SELECT schedule_conf FROM xxl_job_info
      WHERE executor_handler='offlineRiskScan' ORDER BY id LIMIT 1)
  );
")"

readiness_state="$(mysql_query "
  SELECT
    COUNT(*),
    COALESCE(SUM(CASE WHEN trigger_code=200 AND handle_code=200 THEN 1 ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN trigger_code<>200 OR handle_code<>200 THEN 1 ELSE 0 END), 0),
    COALESCE(DATE_FORMAT(MAX(handle_time), '%Y-%m-%dT%H:%i:%s'), 'none')
  FROM (
    SELECT l.trigger_time, l.handle_time, l.trigger_code, l.handle_code
    FROM xxl_job_log l
    JOIN xxl_job_info i ON i.id=l.job_id
    WHERE i.executor_handler='offlineRiskScan'
      AND l.executor_param='{}'
    ORDER BY l.id DESC
    LIMIT $MIN_SCHEDULED_SUCCESSES
  ) recent;
")"

IFS=$'\t' read -r observed successes failures latest_schedule_time \
  <<<"$readiness_state"

business_state="$(postgres_query "
  SELECT status || ':' || as_of
  FROM risk_offline_runs
  ORDER BY created_at DESC
  LIMIT 1;
")"
latest_business_age="$(postgres_query "
  SELECT GREATEST(EXTRACT(EPOCH FROM (now() - created_at))::bigint, 0)
  FROM risk_offline_runs
  ORDER BY created_at DESC
  LIMIT 1;
")"
failed_business_runs="$(postgres_query "
  SELECT count(*)
  FROM risk_offline_runs
  WHERE status='failed'
    AND created_at >= now() - make_interval(hours => $MIN_SCHEDULED_SUCCESSES + 1);
")"

printf 'S5 readiness evidence:\n'
printf '  cron_suspended=%s\n' "$cron_suspended"
printf '  xxl_production_and_shadow=%s\n' "$scheduler_state"
printf '  scheduled_observed=%s required=%s successes=%s failures=%s\n' \
  "$observed" "$MIN_SCHEDULED_SUCCESSES" "$successes" "$failures"
printf '  latest_schedule_time=%s\n' "$latest_schedule_time"
printf '  latest_business_age_seconds=%s allowed=%s\n' \
  "$latest_business_age" "$MAX_LAST_SUCCESS_AGE_SECONDS"
printf '  latest_business_run=%s\n' "$business_state"
printf '  failed_business_runs_in_observation=%s\n' "$failed_business_runs"

ready=true

[ "$cron_suspended" = true ] || ready=false
[ "$scheduler_state" = "1:0:$EXPECTED_PRODUCTION_SCHEDULE" ] || ready=false
[ "$observed" -ge "$MIN_SCHEDULED_SUCCESSES" ] || ready=false
[ "$successes" -eq "$observed" ] || ready=false
[ "$failures" -eq 0 ] || ready=false
[ "$latest_schedule_time" != none ] || ready=false
[ -n "$latest_business_age" ] || ready=false
[ "$latest_business_age" -le "$MAX_LAST_SUCCESS_AGE_SECONDS" ] || ready=false
case "$business_state" in completed:*) ;; *) ready=false ;; esac
[ "$failed_business_runs" -eq 0 ] || ready=false

if [ "$ready" != true ]; then
  printf 'NOT_READY: keep the suspended CronJob as the rollback path.\n' >&2
  exit 1
fi

printf 'READY: S5 evidence threshold is satisfied; CronJob retirement may enter review.\n'

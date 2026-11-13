#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"

mysql_query() {
  local query="$1"
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec statefulset/xxl-job-mysql -- \
    env QUERY="$query" \
    sh -lc 'mysql -N -B -uroot -p"$MYSQL_ROOT_PASSWORD" xxl_job -e "$QUERY"' \
    2>/dev/null
}

cron_state="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    get cronjob offline-risk-scan \
    -o jsonpath='{.spec.suspend}{"\t"}{.spec.schedule}'
)"
[ "$cron_state" = $'true\t5 * * * *' ] || {
  printf 'unexpected CronJob state: %s\n' "$cron_state" >&2
  exit 1
}

scheduler_state="$(mysql_query "
  SELECT CONCAT(
    (SELECT COUNT(*) FROM xxl_job_info WHERE executor_handler='offlineRiskScanShadow' AND trigger_status=0), ':',
    (SELECT COUNT(*) FROM xxl_job_info WHERE executor_handler='offlineRiskScan' AND trigger_status=1 AND schedule_conf='0 5 * * * ? *'), ':',
    (SELECT COUNT(*) FROM xxl_job_info WHERE executor_handler='offlineRiskScan'), ':',
    (SELECT COUNT(*) FROM xxl_job_info WHERE executor_handler IN ('commandJobHandler','httpJobHandler'))
  );
")"
[ "$scheduler_state" = '1:1:1:0' ] || {
  printf 'unexpected XXL-JOB cutover state: %s\n' "$scheduler_state" >&2
  exit 1
}

latest_production_log="$(mysql_query "
  SELECT CONCAT(l.id, ':', l.trigger_code, ':', l.handle_code, ':', l.handle_msg)
  FROM xxl_job_log l
  JOIN xxl_job_info i ON i.id=l.job_id
  WHERE i.executor_handler='offlineRiskScan'
  ORDER BY l.id DESC
  LIMIT 1;
")"
case "$latest_production_log" in
  *:200:200:offline\ execution\ completed) ;;
  *)
    printf 'production job has no successful execution evidence: %s\n' \
      "${latest_production_log:-none}" >&2
    exit 1
    ;;
esac

printf 'PASS: CronJob suspended; XXL production schedule enabled; Shadow schedule stopped.\n'
printf 'PASS: production Handler has successful execution evidence and arbitrary command/HTTP jobs remain absent.\n'

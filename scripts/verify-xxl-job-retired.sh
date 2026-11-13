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

retired_cronjob="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    get cronjob offline-risk-scan --ignore-not-found -o name
)"
[ -z "$retired_cronjob" ] || {
  printf 'offline-risk-scan still exists: %s\n' "$retired_cronjob" >&2
  exit 1
}

scheduler_state="$(mysql_query "
  SELECT CONCAT(
    (SELECT trigger_status FROM xxl_job_info
      WHERE executor_handler='offlineRiskScan' ORDER BY id LIMIT 1), ':',
    (SELECT trigger_status FROM xxl_job_info
      WHERE executor_handler='offlineRiskScanShadow' ORDER BY id LIMIT 1), ':',
    (SELECT schedule_conf FROM xxl_job_info
      WHERE executor_handler='offlineRiskScan' ORDER BY id LIMIT 1), ':',
    (SELECT COUNT(*) FROM xxl_job_info
      WHERE executor_handler IN ('commandJobHandler','httpJobHandler'))
  );
")"
[ "$scheduler_state" = '1:0:0 5 * * * ? *:0' ] || {
  printf 'unexpected XXL retirement state: %s\n' "$scheduler_state" >&2
  exit 1
}

latest_production_log="$(mysql_query "
  SELECT CONCAT(l.trigger_code, ':', l.handle_code, ':', l.handle_msg)
  FROM xxl_job_log l
  JOIN xxl_job_info i ON i.id=l.job_id
  WHERE i.executor_handler='offlineRiskScan'
  ORDER BY l.id DESC
  LIMIT 1;
")"
case "$latest_production_log" in
  200:200:offline\ execution\ completed) ;;
  *)
    printf 'latest XXL production execution is not successful: %s\n' \
      "${latest_production_log:-none}" >&2
    exit 1
    ;;
esac

risk_argo="$(
  kubectl --context "$KUBE_CONTEXT" -n argocd get application risk-control-lab \
    -o jsonpath='{.spec.syncPolicy.automated.enabled}{":"}{.spec.syncPolicy.automated.selfHeal}{":"}{.status.sync.status}'
)"
[ "$risk_argo" = 'true:true:Synced' ] || {
  printf 'unexpected risk-control Argo state: %s\n' "$risk_argo" >&2
  exit 1
}

xxl_argo="$(
  kubectl --context "$KUBE_CONTEXT" -n argocd get application xxl-job-lab \
    -o jsonpath='{.status.sync.status}{":"}{.status.health.status}'
)"
[ "$xxl_argo" = 'Synced:Healthy' ] || {
  printf 'unexpected XXL Argo state: %s\n' "$xxl_argo" >&2
  exit 1
}

printf 'PASS: CronJob retired; XXL is the only formal scheduler and both Argo applications are synchronized.\n'

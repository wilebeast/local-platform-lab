#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"

cron_state="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    get cronjob offline-risk-scan \
    -o jsonpath='{.spec.schedule}{"\t"}{.spec.suspend}{"\t"}{.status.lastScheduleTime}'
)"

latest_cron_job="$(
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab get jobs -o json |
    jq -r '
      [.items[] |
        select(any(.metadata.ownerReferences[]?; .name == "offline-risk-scan"))] |
      sort_by(.metadata.creationTimestamp) |
      last |
      [
        .metadata.name,
        (.status.succeeded // 0),
        (.status.failed // 0),
        .metadata.creationTimestamp
      ] |
      @tsv'
)"

xxl_state="$(
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec statefulset/xxl-job-mysql -- \
    sh -lc 'mysql -N -B -uroot -p"$MYSQL_ROOT_PASSWORD" xxl_job -e "
      SELECT
        i.schedule_conf,
        i.trigger_status,
        COALESCE(l.trigger_code, 0),
        COALESCE(l.handle_code, 0),
        COALESCE(l.handle_msg, '\''no execution yet'\'')
      FROM xxl_job_info i
      LEFT JOIN xxl_job_log l
        ON l.id = (
          SELECT MAX(id)
          FROM xxl_job_log
          WHERE job_id = i.id
        )
      WHERE i.id = 3;"' \
    2>/dev/null
)"

IFS=$'\t' read -r cron_schedule cron_suspended cron_last <<<"$cron_state"
IFS=$'\t' read -r cron_job cron_succeeded cron_failed cron_created <<<"$latest_cron_job"
IFS=$'\t' read -r xxl_schedule xxl_enabled xxl_trigger_code xxl_handle_code xxl_message <<<"$xxl_state"

[ "$cron_schedule" = '5 * * * *' ]
[ "$cron_suspended" = 'false' ]
[ "${cron_succeeded:-0}" -ge 1 ]
[ "${cron_failed:-0}" -eq 0 ]
[ "$xxl_schedule" = '0 5 * * * ? *' ]
[ "$xxl_enabled" = '1' ]
[ "$xxl_trigger_code" = '200' ]
[ "$xxl_handle_code" = '200' ]

printf 'Kubernetes CronJob: schedule=%s suspended=%s last=%s latest_job=%s succeeded=%s failed=%s created=%s\n' \
  "$cron_schedule" "$cron_suspended" "$cron_last" "$cron_job" "$cron_succeeded" "$cron_failed" "$cron_created"
printf 'XXL-JOB Shadow: schedule=%s enabled=%s trigger_code=%s handle_code=%s message=%s\n' \
  "$xxl_schedule" "$xxl_enabled" "$xxl_trigger_code" "$xxl_handle_code" "$xxl_message"
printf 'PASS: scheduler-level shadow comparison is healthy. Business-output equivalence is not claimed yet.\n'

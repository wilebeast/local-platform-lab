#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
CUTOVER_CONFIRM="${CUTOVER_CONFIRM:-}"

[ "$CUTOVER_CONFIRM" = 'replace-cronjob' ] || {
  printf 'set CUTOVER_CONFIRM=replace-cronjob to perform the reversible S4 cutover\n' >&2
  exit 1
}

mysql_query() {
  local query="$1"
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec statefulset/xxl-job-mysql -- \
    env QUERY="$query" \
    sh -lc 'mysql -N -B -uroot -p"$MYSQL_ROOT_PASSWORD" xxl_job -e "$QUERY"' \
    2>/dev/null
}

admin_call() {
  local uri="$1"
  local payload="$2"
  kubectl --context "$KUBE_CONTEXT" -n xxl-job-lab \
    exec deployment/xxl-job-risk-shadow-executor -- \
    env ADMIN_URI="$uri" ADMIN_PAYLOAD="$payload" \
    sh -lc 'curl -fsS -X POST "http://xxl-job-admin:8080/api/$ADMIN_URI" \
      -H "Content-Type: application/json" \
      -H "XXL-JOB-APPNAME: risk-shadow-executor" \
      -H "XXL-JOB-ACCESS-TOKEN: $XXL_JOB_EXECUTOR_ACCESS_TOKEN" \
      --data "$ADMIN_PAYLOAD"'
}

wait_for_argocd_idle() {
  local attempt
  local idle_observations=0

  for attempt in $(seq 1 30); do
    if kubectl --context "$KUBE_CONTEXT" -n argocd \
      get application risk-control-lab -o json | \
      jq -e 'has("operation") | not' >/dev/null; then
      idle_observations=$((idle_observations + 1))
      if [ "$idle_observations" -ge 3 ]; then
        return 0
      fi
    else
      idle_observations=0
    fi
    sleep 1
  done

  printf 'Argo CD did not become idle after automated sync was disabled\n' >&2
  return 1
}

shadow_job_id="$(mysql_query "SELECT id FROM xxl_job_info WHERE executor_handler='offlineRiskScanShadow' ORDER BY id LIMIT 1;")"
production_job_id="$(mysql_query "SELECT id FROM xxl_job_info WHERE executor_handler='offlineRiskScan' ORDER BY id LIMIT 1;")"

[ -n "$shadow_job_id" ] || { printf 'shadow job is missing\n' >&2; exit 1; }
[ -n "$production_job_id" ] || { printf 'production job is missing\n' >&2; exit 1; }

production_validation="$(mysql_query "
  SELECT CONCAT(trigger_code, ':', handle_code, ':', handle_msg)
  FROM xxl_job_log
  WHERE job_id=$production_job_id
  ORDER BY id DESC
  LIMIT 1;
")"

case "$production_validation" in
  200:200:offline\ execution\ completed) ;;
  *)
    printf 'production handler has no successful manual validation: %s\n' \
      "${production_validation:-none}" >&2
    exit 1
    ;;
esac

cutover_complete=false
rollback() {
  if [ "$cutover_complete" = true ]; then
    return
  fi
  set +e
  printf 'cutover failed; restoring CronJob and Shadow scheduling\n' >&2
  admin_call stopJob "$(jq -cn --argjson id "$production_job_id" '{id:$id}')" >/dev/null
  kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
    patch cronjob offline-risk-scan --type merge -p '{"spec":{"suspend":false}}' >/dev/null
  admin_call startJob "$(jq -cn --argjson id "$shadow_job_id" '{id:$id}')" >/dev/null
  kubectl --context "$KUBE_CONTEXT" -n argocd patch application risk-control-lab \
    --type merge -p '{"spec":{"syncPolicy":{"automated":{"enabled":true,"prune":true,"selfHeal":true}}}}' >/dev/null
}
trap rollback ERR

kubectl --context "$KUBE_CONTEXT" -n argocd patch application risk-control-lab \
  --type merge -p '{"spec":{"syncPolicy":{"automated":{"enabled":false,"prune":true,"selfHeal":false}}}}' >/dev/null

wait_for_argocd_idle

kubectl --context "$KUBE_CONTEXT" -n risk-control-lab \
  patch cronjob offline-risk-scan --type merge -p '{"spec":{"suspend":true}}' >/dev/null

admin_call stopJob "$(jq -cn --argjson id "$shadow_job_id" '{id:$id}')" >/dev/null
admin_call startJob "$(jq -cn --argjson id "$production_job_id" '{id:$id}')" >/dev/null

cutover_complete=true
trap - ERR

printf 'S4 cutover applied: CronJob suspended, XXL production job enabled, Shadow schedule stopped.\n'
printf 'Argo CD automated sync is temporarily disabled until the GitOps change is committed and pushed.\n'

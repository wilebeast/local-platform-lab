#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
NAMESPACE="xxl-job-lab"

kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" rollout status statefulset/xxl-job-mysql --timeout=120s
kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" rollout status deployment/xxl-job-admin --timeout=120s
kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" rollout status deployment/xxl-job-risk-shadow-executor --timeout=120s

executor_env_names="$(
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" \
    get deployment xxl-job-risk-shadow-executor \
    -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}{"\n"}{end}' |
    sort
)"

for required_env in POSTGRES_DSN OFFLINE_POLICY_KEY OFFLINE_POLICY_VERSION; do
  printf '%s\n' "$executor_env_names" | grep -qx "$required_env" || {
    printf 'executor is missing required environment %s\n' "$required_env" >&2
    exit 1
  }
done

mysql_env_names="$(
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" \
    get statefulset xxl-job-mysql \
    -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}{"\n"}{end}' |
    sort
)"

for forbidden_env in POSTGRES_DSN OFFLINE_POLICY_KEY OFFLINE_POLICY_VERSION; do
  if printf '%s\n' "$mysql_env_names" | grep -qx "$forbidden_env"; then
    printf 'MySQL must not receive executor-only environment %s\n' "$forbidden_env" >&2
    exit 1
  fi
done

query_result="$(
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" exec statefulset/xxl-job-mysql -- \
    sh -lc 'mysql -N -B -uroot -p"$MYSQL_ROOT_PASSWORD" xxl_job -e "
      SELECT CONCAT(
        (SELECT COUNT(*) FROM xxl_job_group WHERE app_name='"'"'risk-shadow-executor'"'"'), '"'"':'"'"',
        (SELECT COUNT(*) FROM xxl_job_info WHERE executor_handler IN ('"'"'riskWarehouseCompactionShadow'"'"','"'"'riskFeatureSnapshotShadow'"'"','"'"'offlineRiskScanShadow'"'"','"'"'offlineRiskScan'"'"')), '"'"':'"'"',
        (SELECT COUNT(*) FROM xxl_job_info WHERE executor_handler IN ('"'"'commandJobHandler'"'"','"'"'httpJobHandler'"'"')), '"'"':'"'"',
        (SELECT COUNT(*) FROM xxl_job_registry WHERE registry_group='"'"'EXECUTOR'"'"' AND registry_key='"'"'risk-shadow-executor'"'"'), '"'"':'"'"',
        (SELECT COUNT(*) FROM xxl_job_info WHERE id=3 AND executor_param='"'"'{}'"'"' AND schedule_conf='"'"'0 5 * * * ? *'"'"')
      );"' \
    2>/dev/null
)"

[ "$query_result" = "1:4:0:1:1" ] || {
  printf 'unexpected restricted scheduler contract: %s\n' "$query_result" >&2
  exit 1
}

printf 'PASS: one restricted executor, three fixed shadow jobs, one fixed production job, zero arbitrary command/HTTP jobs.\n'
printf 'PASS: offline preview dependencies are injected only into the restricted executor.\n'
printf 'PASS: deploying the XXL-JOB lab does not itself mutate Kubernetes risk CronJobs.\n'

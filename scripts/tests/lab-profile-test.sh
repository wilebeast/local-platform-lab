#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local output="$1"
  local expected="$2"
  grep -Fqx "$expected" <<<"$output" || fail "expected line: $expected"
}

assert_not_contains() {
  local output="$1"
  local unexpected="$2"
  if grep -Fqx "$unexpected" <<<"$output"; then
    fail "unexpected line: $unexpected"
  fi
}

plan() {
  make --no-print-directory -C "$ROOT_DIR" lab-plan PROFILE="$1"
}

light_plan="$(plan light)"
assert_contains "$light_plan" "enabled postgres"
assert_contains "$light_plan" "enabled etcd"
assert_contains "$light_plan" "enabled nacos"
assert_contains "$light_plan" "enabled api-bff"
assert_contains "$light_plan" "enabled user-service"
assert_not_contains "$light_plan" "enabled kafka"
assert_not_contains "$light_plan" "enabled risk-control-lab"
assert_not_contains "$light_plan" "enabled kubesphere-core"

card_plan="$(plan card)"
assert_contains "$card_plan" "enabled postgres"
assert_contains "$card_plan" "enabled apisix"
assert_contains "$card_plan" "enabled enterprise-card-lab"
assert_not_contains "$card_plan" "enabled risk-control-lab"
assert_not_contains "$card_plan" "enabled risk-airflow-lab"

risk_plan="$(plan risk-warehouse)"
assert_contains "$risk_plan" "enabled postgres"
assert_contains "$risk_plan" "enabled kafka"
assert_contains "$risk_plan" "enabled risk-control-lab"
assert_contains "$risk_plan" "enabled risk-airflow-lab"
assert_not_contains "$risk_plan" "enabled redis"
assert_not_contains "$risk_plan" "enabled kubesphere-core"
assert_not_contains "$risk_plan" "enabled enterprise-card-lab"

scheduler_plan="$(plan scheduler)"
assert_contains "$scheduler_plan" "enabled xxl-job-lab"
assert_contains "$scheduler_plan" "enabled postgres"
assert_contains "$scheduler_plan" "preserved risk-control-lab"
assert_not_contains "$scheduler_plan" "enabled risk-control-lab"
assert_not_contains "$scheduler_plan" "enabled risk-airflow-lab"
assert_not_contains "$scheduler_plan" "enabled kubesphere-core"

full_plan="$(plan full)"
assert_contains "$full_plan" "enabled kubesphere-core"
assert_contains "$full_plan" "enabled enterprise-card-lab"
assert_contains "$full_plan" "enabled risk-airflow-lab"
assert_contains "$full_plan" "enabled webhook-lab"

if make --no-print-directory -C "$ROOT_DIR" lab-plan PROFILE=unknown >/tmp/lab-profile-unknown.out 2>&1; then
  fail "unknown profile unexpectedly succeeded"
fi
grep -Fq "unknown lab profile: unknown" /tmp/lab-profile-unknown.out ||
  fail "unknown profile did not explain the error"

card_up="$(LAB_PROFILE_DRY_RUN=1 make --no-print-directory -C "$ROOT_DIR" lab-up PROFILE=card)"
assert_contains "$card_up" "prepare ensure-postgres-secrets.sh"
assert_contains "$card_up" "prepare ensure-enterprise-card-secrets.sh"
assert_contains "$card_up" "apply postgres"
assert_contains "$card_up" "apply enterprise-card-lab"
assert_contains "$card_up" "suspend risk-control-lab"
assert_not_contains "$card_up" "apply risk-control-lab"
if grep -Eq 'delete[[:space:]]+(persistentvolumeclaim|pvc)' <<<"$card_up"; then
  fail "profile switch must not delete persistent volumes"
fi

risk_up="$(LAB_PROFILE_DRY_RUN=1 make --no-print-directory -C "$ROOT_DIR" lab-up PROFILE=risk-warehouse)"
assert_contains "$risk_up" "prepare ensure-risk-control-secrets.sh"
assert_contains "$risk_up" "prepare ensure-risk-airflow-secrets.sh"
assert_contains "$risk_up" "apply kafka"
assert_contains "$risk_up" "apply risk-control-lab"
assert_contains "$risk_up" "apply risk-airflow-lab"
assert_not_contains "$risk_up" "apply kubesphere-core"

scheduler_up="$(LAB_PROFILE_DRY_RUN=1 make --no-print-directory -C "$ROOT_DIR" lab-up PROFILE=scheduler)"
assert_contains "$scheduler_up" "prepare ensure-postgres-secrets.sh"
assert_contains "$scheduler_up" "prepare ensure-xxl-job-secrets.sh"
assert_contains "$scheduler_up" "apply postgres"
assert_contains "$scheduler_up" "apply xxl-job-lab"
assert_contains "$scheduler_up" "preserve risk-control-lab"
assert_not_contains "$scheduler_up" "suspend risk-control-lab"
assert_not_contains "$scheduler_up" "apply risk-control-lab"
assert_not_contains "$scheduler_up" "apply risk-airflow-lab"

down_output="$(LAB_PROFILE_DRY_RUN=1 make --no-print-directory -C "$ROOT_DIR" lab-down)"
assert_contains "$down_output" "suspend postgres"
assert_contains "$down_output" "suspend risk-control-lab"
assert_contains "$down_output" "stop colima"
if grep -Eq 'delete[[:space:]]+(persistentvolumeclaim|pvc)' <<<"$down_output"; then
  fail "lab-down must preserve persistent volumes"
fi

printf 'PASS: lab profile public contract\n'

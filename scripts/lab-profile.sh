#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"
DRY_RUN="${LAB_PROFILE_DRY_RUN:-0}"

all_apps() {
  cat <<'EOF'
account-service
acquiring-lab
api-bff
apisix
enterprise-card-lab
etcd
inbound-webhook-lab
kafka
kubesphere-core
local-alb
matching-service
nacos
notification-lab
platform-demo
postgres
redis
risk-airflow-lab
risk-control-lab
user-service
virtual-account-lab
webhook-lab
EOF
}

profile_apps() {
  case "${1:-}" in
    light)
      cat <<'EOF'
postgres
redis
etcd
nacos
apisix
local-alb
platform-demo
api-bff
user-service
account-service
EOF
      ;;
    card)
      cat <<'EOF'
postgres
apisix
local-alb
enterprise-card-lab
EOF
      ;;
    risk-warehouse)
      cat <<'EOF'
postgres
kafka
risk-control-lab
risk-airflow-lab
EOF
      ;;
    full)
      all_apps
      ;;
    *)
      printf 'unknown lab profile: %s\n' "${1:-}" >&2
      printf 'available profiles: light card risk-warehouse full\n' >&2
      return 2
      ;;
  esac
}

manifest_for_app() {
  local app="$1"
  if [ "$app" = "platform-demo" ]; then
    printf '%s/argocd/application.yaml\n' "$GITOPS_DIR"
  else
    printf '%s/argocd/%s.yaml\n' "$GITOPS_DIR" "$app"
  fi
}

validate_profile() {
  local profile="$1"
  local app manifest
  while IFS= read -r app; do
    manifest="$(manifest_for_app "$app")"
    if [ ! -f "$manifest" ]; then
      printf 'missing Argo CD application manifest for %s: %s\n' "$app" "$manifest" >&2
      return 1
    fi
  done < <(profile_apps "$profile")
}

print_plan() {
  local profile="$1"
  local app
  local enabled

  validate_profile "$profile"
  enabled="$(profile_apps "$profile")"

  printf 'profile %s\n' "$profile"
  while IFS= read -r app; do
    printf 'enabled %s\n' "$app"
  done <<<"$enabled"

  while IFS= read -r app; do
    if ! grep -Fqx "$app" <<<"$enabled"; then
      printf 'suspended %s\n' "$app"
    fi
  done < <(all_apps)
}

list_profiles() {
  printf '%-16s %s\n' PROFILE PURPOSE
  printf '%-16s %s\n' light 'API gateway and core account services'
  printf '%-16s %s\n' card 'enterprise card practice'
  printf '%-16s %s\n' risk-warehouse 'Kafka, risk service, Flink, ClickHouse and Airflow'
  printf '%-16s %s\n' full 'all local applications (resource intensive)'
}

run_prepare_script() {
  local script="$1"
  printf 'prepare %s\n' "$script"
  if [ "$DRY_RUN" != "1" ]; then
    "$ROOT_DIR/scripts/$script"
  fi
}

prepare_profile() {
  local profile="$1"
  local enabled
  enabled="$(profile_apps "$profile")"

  if grep -Fqx postgres <<<"$enabled"; then
    run_prepare_script ensure-postgres-secrets.sh
  fi

  if grep -Eq '^(api-bff|apisix|local-alb|platform-demo|user-service)$' <<<"$enabled"; then
    run_prepare_script ensure-auth-secrets.sh
  fi

  if grep -Fqx enterprise-card-lab <<<"$enabled"; then
    run_prepare_script ensure-enterprise-card-secrets.sh
  fi

  if grep -Fqx inbound-webhook-lab <<<"$enabled"; then
    run_prepare_script ensure-inbound-webhook-secrets.sh
  fi

  if grep -Fqx notification-lab <<<"$enabled"; then
    run_prepare_script ensure-notification-secrets.sh
  fi

  if grep -Fqx risk-control-lab <<<"$enabled"; then
    run_prepare_script ensure-risk-control-secrets.sh
  fi

  if grep -Fqx risk-airflow-lab <<<"$enabled"; then
    run_prepare_script ensure-risk-airflow-secrets.sh
  fi

  if grep -Fqx webhook-lab <<<"$enabled"; then
    run_prepare_script ensure-webhook-secrets.sh
  fi
}

application_exists() {
  kubectl --context "$KUBE_CONTEXT" -n argocd get application "$1" >/dev/null 2>&1
}

suspend_application() {
  local app="$1"
  local resources

  printf 'suspend %s\n' "$app"
  if [ "$DRY_RUN" = "1" ] || ! application_exists "$app"; then
    return 0
  fi

  kubectl --context "$KUBE_CONTEXT" -n argocd patch application "$app" \
    --type merge -p '{"spec":{"syncPolicy":{"automated":null}}}' >/dev/null
  kubectl --context "$KUBE_CONTEXT" -n argocd annotate application "$app" \
    local-platform.wilebeast.dev/suspended-by-profile=true --overwrite >/dev/null

  resources="$(kubectl --context "$KUBE_CONTEXT" -n argocd get application "$app" -o json)"
  jq -r '.status.resources[]? |
    select(.kind == "Deployment" or .kind == "StatefulSet") |
    [.namespace, (.kind | ascii_downcase), .name] | @tsv' <<<"$resources" |
    while IFS=$'\t' read -r namespace kind name; do
      kubectl --context "$KUBE_CONTEXT" -n "$namespace" scale "$kind/$name" --replicas=0 >/dev/null
    done

  jq -r '.status.resources[]? |
    select(.kind == "CronJob") |
    [.namespace, .name] | @tsv' <<<"$resources" |
    while IFS=$'\t' read -r namespace name; do
      kubectl --context "$KUBE_CONTEXT" -n "$namespace" patch "cronjob/$name" \
        --type merge -p '{"spec":{"suspend":true}}' >/dev/null
    done
}

apply_application() {
  local app="$1"
  local manifest
  manifest="$(manifest_for_app "$app")"

  printf 'apply %s\n' "$app"
  if [ "$DRY_RUN" = "1" ]; then
    return 0
  fi

  kubectl --context "$KUBE_CONTEXT" apply -f "$manifest" >/dev/null
  kubectl --context "$KUBE_CONTEXT" -n argocd annotate application "$app" \
    local-platform.wilebeast.dev/suspended-by-profile- >/dev/null 2>&1 || true
  kubectl --context "$KUBE_CONTEXT" -n argocd annotate application "$app" \
    argocd.argoproj.io/refresh=hard --overwrite >/dev/null
}

apply_profile() {
  local profile="$1"
  local enabled app

  validate_profile "$profile"
  enabled="$(profile_apps "$profile")"

  if [ "$DRY_RUN" != "1" ]; then
    kubectl --context "$KUBE_CONTEXT" get namespace argocd >/dev/null
  fi

  prepare_profile "$profile"

  while IFS= read -r app; do
    if ! grep -Fqx "$app" <<<"$enabled"; then
      suspend_application "$app"
    fi
  done < <(all_apps)

  while IFS= read -r app; do
    apply_application "$app"
  done <<<"$enabled"

  printf 'active profile %s\n' "$profile"
}

down_lab() {
  local app
  if [ "$DRY_RUN" != "1" ] && ! colima status >/dev/null 2>&1; then
    printf 'runtime already stopped\n'
    return 0
  fi

  while IFS= read -r app; do
    suspend_application "$app"
  done < <(all_apps)

  printf 'stop colima\n'
  if [ "$DRY_RUN" != "1" ] && colima status >/dev/null 2>&1; then
    colima stop
  fi
}

show_status() {
  if ! colima status >/dev/null 2>&1; then
    printf 'runtime stopped\n'
    return 0
  fi
  if ! kubectl --context "$KUBE_CONTEXT" get namespace argocd >/dev/null 2>&1; then
    printf 'runtime running; cluster unavailable\n'
    return 1
  fi

  printf '%-28s %-12s %-12s %s\n' APP SYNC HEALTH SUSPENDED
  kubectl --context "$KUBE_CONTEXT" -n argocd get applications -o json |
    jq -r '.items[] | [
      .metadata.name,
      (.status.sync.status // "-"),
      (.status.health.status // "-"),
      (.metadata.annotations["local-platform.wilebeast.dev/suspended-by-profile"] // "false")
    ] | @tsv' |
    sort |
    while IFS=$'\t' read -r app sync health suspended; do
      printf '%-28s %-12s %-12s %s\n' "$app" "$sync" "$health" "$suspended"
    done
}

usage() {
  cat <<'EOF'
Usage: lab-profile.sh <list|plan|apply|down|status> [profile]
EOF
}

command="${1:-}"
case "$command" in
  list)
    list_profiles
    ;;
  plan)
    print_plan "${2:-light}"
    ;;
  apply)
    apply_profile "${2:-light}"
    ;;
  down)
    down_lab
    ;;
  status)
    show_status
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

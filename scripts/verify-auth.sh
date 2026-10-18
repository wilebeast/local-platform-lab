#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
LOCAL_PORT="${AUTH_HTTPS_PORT:-18443}"

kubectl --context "$KUBE_CONTEXT" -n redis rollout status deployment/redis --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n user-service rollout status deployment/user-service --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n api-bff rollout status deployment/api-bff --timeout=180s
kubectl --context "$KUBE_CONTEXT" -n apisix rollout status deployment/apisix --timeout=180s

cookie_dir="$(mktemp -d)"
cookie_jar="$cookie_dir/cookies.txt"
cleanup() {
  if [ -n "${forward_pid:-}" ]; then
    kill "$forward_pid" >/dev/null 2>&1 || true
    wait "$forward_pid" 2>/dev/null || true
  fi
  rm -rf "$cookie_dir"
}
trap cleanup EXIT

kubectl --context "$KUBE_CONTEXT" -n local-alb port-forward service/local-alb "$LOCAL_PORT:443" >/tmp/local-platform-auth-port-forward.log 2>&1 &
forward_pid=$!
sleep 2

base_url="https://localhost:$LOCAL_PORT"
origin="https://localhost:18443"
unauthenticated_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' "$base_url/api/v3/profile")"
test "$unauthenticated_status" = "401"

sleep 11
login_response="$(curl --insecure --fail --silent --show-error -c "$cookie_jar" -X POST "$base_url/api/v3/auth/login" -H "Origin: $origin" -H 'Content-Type: application/json' --data '{"email":"demo@local.test","password":"local-demo-password"}')"
test "$(printf '%s' "$login_response" | sed -n 's/.*"authenticated":\(true\).*/\1/p')" = "true"
if printf '%s' "$login_response" | grep -Eq 'accessToken|refreshToken'; then echo "Token leaked in login JSON" >&2; exit 1; fi
old_refresh_token="$(awk '$6 == "refresh_token" {print $7}' "$cookie_jar")"
csrf_token="$(awk '$6 == "csrf_token" {print $7}' "$cookie_jar")"
test -n "$old_refresh_token"
test -n "$csrf_token"

curl --insecure --fail --silent --show-error -b "$cookie_jar" "$base_url/api/v3/profile"
printf '\n'

missing_csrf_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' -b "$cookie_jar" -X POST "$base_url/api/v3/auth/refresh" -H "Origin: $origin")"
test "$missing_csrf_status" = "403"
wrong_origin_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' -b "$cookie_jar" -X POST "$base_url/api/v3/auth/refresh" -H 'Origin: https://evil.example' -H "X-CSRF-Token: $csrf_token")"
test "$wrong_origin_status" = "403"

curl --insecure --fail --silent --show-error -b "$cookie_jar" -c "$cookie_jar" -X POST "$base_url/api/v3/auth/refresh" -H "Origin: $origin" -H "X-CSRF-Token: $csrf_token" >/dev/null
new_refresh_token="$(awk '$6 == "refresh_token" {print $7}' "$cookie_jar")"
new_csrf_token="$(awk '$6 == "csrf_token" {print $7}' "$cookie_jar")"
test "$new_refresh_token" != "$old_refresh_token"

replay_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' -X POST "$base_url/api/v3/auth/refresh" -H "Origin: $origin" -H "X-CSRF-Token: $new_csrf_token" -b "refresh_token=$old_refresh_token; csrf_token=$new_csrf_token")"
test "$replay_status" = "401"

for attempt in 1 2 3 4; do
  status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' -X POST "$base_url/api/v3/auth/login" -H "Origin: $origin" -H 'Content-Type: application/json' --data '{"email":"demo@local.test","password":"wrong"}')"
  test "$status" = "401"
done
rate_limited_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' -X POST "$base_url/api/v3/auth/login" -H "Origin: $origin" -H 'Content-Type: application/json' --data '{"email":"demo@local.test","password":"wrong"}')"
test "$rate_limited_status" = "429"

curl --insecure --fail --silent --show-error -b "$cookie_jar" -X POST "$base_url/api/v3/auth/logout" -H "Origin: $origin" -H "X-CSRF-Token: $new_csrf_token"
printf '\nAuthentication chain verified.\n'

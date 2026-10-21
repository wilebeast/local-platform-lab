#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
HTTPS_PORT="${AUTH_HTTPS_PORT:-18443}"
BFF_PORT="${BFF_DIRECT_PORT:-18888}"
cookie_dir="$(mktemp -d)"
cookie_jar="$cookie_dir/cookies.txt"

cleanup() {
  for pid in ${forward_pids:-}; do
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$cookie_dir"
}
trap cleanup EXIT

decode_base64url() {
  local value="$1"
  case $((${#value} % 4)) in
    2) value="${value}==" ;;
    3) value="${value}=" ;;
  esac
  printf '%s' "$value" | tr '_-' '/+' | openssl base64 -d -A
}

login() {
  curl --insecure --fail --silent --show-error -c "$cookie_jar" \
    -H 'Origin: https://localhost:18443' \
    -H 'Content-Type: application/json' \
    --data '{"email":"demo@local.test","password":"local-demo-password"}' \
    "https://localhost:$HTTPS_PORT/api/v3/auth/login" >/dev/null
}

access_token() {
  awk '$6 == "access_token" {print $7}' "$cookie_jar"
}

claims() {
  local token segment
  token="$(access_token)"
  segment="$(printf '%s' "$token" | cut -d. -f2)"
  decode_base64url "$segment"
}

kubectl --context "$KUBE_CONTEXT" -n redis exec deploy/redis -- redis-cli SET 'auth:user-version:local-user' 1 >/dev/null
kubectl --context "$KUBE_CONTEXT" -n local-alb port-forward service/local-alb "$HTTPS_PORT:443" >/tmp/local-platform-session-https.log 2>&1 &
forward_pids="$!"
kubectl --context "$KUBE_CONTEXT" -n api-bff port-forward service/api-bff "$BFF_PORT:80" >/tmp/local-platform-session-bff.log 2>&1 &
forward_pids="$forward_pids $!"
sleep 2

jwks="$(curl --insecure --fail --silent --show-error "https://localhost:$HTTPS_PORT/.well-known/jwks.json")"
test "$(printf '%s' "$jwks" | jq -r '.keys[0].alg')" = "EdDSA"
test "$(printf '%s' "$jwks" | jq -r '.keys[0].crv')" = "Ed25519"
test "$(printf '%s' "$jwks" | jq -r '.keys[0].d // empty')" = ""

sleep 11
login
first_token="$(access_token)"
first_header_segment="$(printf '%s' "$first_token" | cut -d. -f1)"
first_header="$(decode_base64url "$first_header_segment")"
first_claims="$(claims)"
test "$(printf '%s' "$first_header" | jq -r .alg)" = "EdDSA"
test "$(printf '%s' "$first_header" | jq -r .kid)" = "local-auth-1"
test "$(printf '%s' "$first_claims" | jq -r .sub)" = "local-user"
first_sid="$(printf '%s' "$first_claims" | jq -r .sid)"
first_jti="$(printf '%s' "$first_claims" | jq -r .jti)"
test -n "$first_sid"
test -n "$first_jti"
test "$(printf '%s' "$first_claims" | jq -r .ver)" = "1"

session_json="$(kubectl --context "$KUBE_CONTEXT" -n redis exec deploy/redis -- redis-cli --raw GET "auth:session:$first_sid")"
test "$(printf '%s' "$session_json" | jq -r .Status)" = "active"
test "$(printf '%s' "$session_json" | jq -r .UserID)" = "local-user"
curl --insecure --fail --silent --show-error -b "$cookie_jar" "https://localhost:$HTTPS_PORT/api/v3/profile" >/dev/null

forged_status="$(curl --silent --output /dev/null --write-out '%{http_code}' -H 'X-User-ID: administrator' -H 'X-Authenticated: true' "http://localhost:$BFF_PORT/api/v3/profile")"
test "$forged_status" = "401"

csrf_token="$(awk '$6 == "csrf_token" {print $7}' "$cookie_jar")"
curl --insecure --fail --silent --show-error -b "$cookie_jar" -c "$cookie_jar" \
  -X POST -H 'Origin: https://localhost:18443' -H "X-CSRF-Token: $csrf_token" \
  "https://localhost:$HTTPS_PORT/api/v3/auth/refresh" >/dev/null
second_claims="$(claims)"
second_sid="$(printf '%s' "$second_claims" | jq -r .sid)"
second_jti="$(printf '%s' "$second_claims" | jq -r .jti)"
test "$second_sid" = "$first_sid"
test "$second_jti" != "$first_jti"

revoked_json="$(printf '%s' "$session_json" | jq -c '.Status="revoked"')"
kubectl --context "$KUBE_CONTEXT" -n redis exec deploy/redis -- redis-cli SET "auth:session:$first_sid" "$revoked_json" KEEPTTL >/dev/null
revoked_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' -b "$cookie_jar" "https://localhost:$HTTPS_PORT/api/v3/profile")"
test "$revoked_status" = "401"

login
version_claims="$(claims)"
version_sid="$(printf '%s' "$version_claims" | jq -r .sid)"
kubectl --context "$KUBE_CONTEXT" -n redis exec deploy/redis -- redis-cli INCR 'auth:user-version:local-user' >/dev/null
version_status="$(curl --insecure --silent --output /dev/null --write-out '%{http_code}' -b "$cookie_jar" "https://localhost:$HTTPS_PORT/api/v3/profile")"
test "$version_status" = "401"

version_session="$(kubectl --context "$KUBE_CONTEXT" -n redis exec deploy/redis -- redis-cli --raw GET "auth:session:$version_sid")"
version_revoked="$(printf '%s' "$version_session" | jq -c '.Status="revoked"')"
kubectl --context "$KUBE_CONTEXT" -n redis exec deploy/redis -- redis-cli SET "auth:session:$version_sid" "$version_revoked" KEEPTTL >/dev/null
kubectl --context "$KUBE_CONTEXT" -n redis exec deploy/redis -- redis-cli SET 'auth:user-version:local-user' 1 >/dev/null

printf '%s\n' "JWKS: EdDSA / Ed25519 / no private key"
printf '%s\n' "JWT: sub, sid, jti, ver verified"
printf '%s\n' "Refresh: sid unchanged, jti rotated"
printf '%s\n' "Forged identity header: rejected with 401"
printf '%s\n' "Revoked sid: rejected with 401"
printf '%s\n' "Outdated ver: rejected with 401"
printf '%s\n' "BFF session lab verified."

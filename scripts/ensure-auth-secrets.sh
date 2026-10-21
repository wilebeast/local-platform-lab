#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"

for namespace in api-bff user-service apisix redis local-alb; do
  kubectl --context "$KUBE_CONTEXT" create namespace "$namespace" --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null
done

if ! kubectl --context "$KUBE_CONTEXT" -n local-alb get secret local-platform-tls >/dev/null 2>&1; then
  cert_dir="$(mktemp -d)"
  trap 'rm -rf "$cert_dir"' EXIT
  openssl req -x509 -nodes -newkey rsa:2048 -days 365 \
    -keyout "$cert_dir/tls.key" -out "$cert_dir/tls.crt" \
    -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1
  kubectl --context "$KUBE_CONTEXT" -n local-alb create secret tls local-platform-tls \
    --cert="$cert_dir/tls.crt" --key="$cert_dir/tls.key" >/dev/null
fi

if kubectl --context "$KUBE_CONTEXT" -n api-bff get secret platform-auth >/dev/null 2>&1; then
  gateway_key="$(kubectl --context "$KUBE_CONTEXT" -n api-bff get secret platform-auth -o jsonpath='{.data.gateway-signature-key}' | base64 --decode)"
else
  gateway_key="$(openssl rand -hex 32)"
fi

jwt_private_key_b64="$(kubectl --context "$KUBE_CONTEXT" -n user-service get secret platform-auth -o jsonpath='{.data.jwt-private-key-b64}' 2>/dev/null | base64 --decode || true)"
jwt_public_key_b64="$(kubectl --context "$KUBE_CONTEXT" -n api-bff get secret platform-auth -o jsonpath='{.data.jwt-public-key-b64}' 2>/dev/null | base64 --decode || true)"
jwt_key_id="$(kubectl --context "$KUBE_CONTEXT" -n api-bff get secret platform-auth -o jsonpath='{.data.jwt-key-id}' 2>/dev/null | base64 --decode || true)"
if [ -z "$jwt_private_key_b64" ] || [ -z "$jwt_public_key_b64" ]; then
  jwt_dir="$(mktemp -d)"
  trap 'rm -rf "${cert_dir:-}" "${jwt_dir:-}"' EXIT
  openssl genpkey -algorithm ED25519 -out "$jwt_dir/private.pem" >/dev/null 2>&1
  openssl pkey -in "$jwt_dir/private.pem" -outform DER | base64 | tr -d '\n' > "$jwt_dir/private.b64"
  openssl pkey -in "$jwt_dir/private.pem" -pubout -outform DER | base64 | tr -d '\n' > "$jwt_dir/public.b64"
  jwt_private_key_b64="$(<"$jwt_dir/private.b64")"
  jwt_public_key_b64="$(<"$jwt_dir/public.b64")"
fi
if [ -z "$jwt_key_id" ]; then
  jwt_key_id="local-auth-1"
fi

totp_key="$(kubectl --context "$KUBE_CONTEXT" -n user-service get secret platform-auth -o jsonpath='{.data.totp-encryption-key}' 2>/dev/null | base64 --decode || true)"
recovery_pepper="$(kubectl --context "$KUBE_CONTEXT" -n user-service get secret platform-auth -o jsonpath='{.data.recovery-code-pepper}' 2>/dev/null | base64 --decode || true)"
if [ -z "$totp_key" ]; then
  totp_key="$(openssl rand -hex 32)"
fi
if [ -z "$recovery_pepper" ]; then
  recovery_pepper="$(openssl rand -hex 32)"
fi

kubectl --context "$KUBE_CONTEXT" -n user-service create secret generic platform-auth \
  --from-literal="jwt-private-key-b64=$jwt_private_key_b64" \
  --from-literal="jwt-key-id=$jwt_key_id" \
  --from-literal="totp-encryption-key=$totp_key" \
  --from-literal="recovery-code-pepper=$recovery_pepper" \
  --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

kubectl --context "$KUBE_CONTEXT" -n api-bff create secret generic platform-auth \
  --from-literal="jwt-public-key-b64=$jwt_public_key_b64" \
  --from-literal="jwt-key-id=$jwt_key_id" \
  --from-literal="gateway-signature-key=$gateway_key" \
  --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

kubectl --context "$KUBE_CONTEXT" -n apisix create secret generic platform-auth \
  --from-literal="gateway-signature-key=$gateway_key" \
  --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null

echo "Authentication keys and the local TLS certificate are present."

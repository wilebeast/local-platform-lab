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
  jwt_key="$(kubectl --context "$KUBE_CONTEXT" -n api-bff get secret platform-auth -o jsonpath='{.data.jwt-signing-key}' | base64 --decode)"
  gateway_key="$(kubectl --context "$KUBE_CONTEXT" -n api-bff get secret platform-auth -o jsonpath='{.data.gateway-signature-key}' | base64 --decode)"
else
  jwt_key="$(openssl rand -hex 32)"
  gateway_key="$(openssl rand -hex 32)"
fi

for namespace in api-bff user-service apisix; do
  kubectl --context "$KUBE_CONTEXT" -n "$namespace" create secret generic platform-auth \
    --from-literal="jwt-signing-key=$jwt_key" \
    --from-literal="gateway-signature-key=$gateway_key" \
    --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f - >/dev/null
done

echo "Authentication keys and the local TLS certificate are present."

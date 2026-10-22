#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRIVATE_KEY="${PRIVATE_KEY:-$ROOT_DIR/.local-secrets/api-key-ed25519-private.pem}"
KEY_ID="${KEY_ID:-local-ed25519-1}"
METHOD="GET"
URI="/api/v3/open/v2/profile"
TIMESTAMP="$(date +%s)"
NONCE="$(openssl rand -base64 16 | tr '+/' '-_' | tr -d '=\n')"
DIGEST="SHA-256=47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="

if [ ! -s "$PRIVATE_KEY" ]; then
  echo "Private key not found. Run scripts/ensure-ed25519-api-key.sh first." >&2
  exit 1
fi

signing_file="$(mktemp)"
signature_file="$(mktemp)"
trap 'rm -f "$signing_file" "$signature_file"' EXIT
printf 'LOCAL-OPENAPI-V2\n%s\n%s\n%s\n%s\n%s\n%s\n' \
  "$KEY_ID" "$METHOD" "$URI" "$TIMESTAMP" "$NONCE" "$DIGEST" >"$signing_file"
openssl pkeyutl -sign -rawin -inkey "$PRIVATE_KEY" -in "$signing_file" -out "$signature_file"
SIGNATURE="$(openssl base64 -A -in "$signature_file")"

curl -ksS -w '\nHTTP %{http_code}\n' \
  -H "X-API-Key-ID: $KEY_ID" \
  -H "X-API-Timestamp: $TIMESTAMP" \
  -H "X-API-Nonce: $NONCE" \
  -H "Digest: $DIGEST" \
  -H "Authorization: Ed25519 signature=\"$SIGNATURE\"" \
  "https://localhost:18443$URI"

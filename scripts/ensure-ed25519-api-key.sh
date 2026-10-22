#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEY_DIR="$ROOT_DIR/.local-secrets"
PRIVATE_KEY="$KEY_DIR/api-key-ed25519-private.pem"
PUBLIC_KEY="$KEY_DIR/api-key-ed25519-public.pem"
KUBE_CONTEXT="${KUBE_CONTEXT:-kind-local-platform}"

mkdir -p "$KEY_DIR"
chmod 700 "$KEY_DIR"
if [ ! -s "$PRIVATE_KEY" ]; then
  openssl genpkey -algorithm ED25519 -out "$PRIVATE_KEY"
  chmod 600 "$PRIVATE_KEY"
fi
openssl pkey -in "$PRIVATE_KEY" -pubout -out "$PUBLIC_KEY"

public_hex="$(openssl pkey -pubin -in "$PUBLIC_KEY" -outform DER | tail -c 32 | xxd -p -c 64)"
fingerprint="$(printf '%s' "$public_hex" | xxd -r -p | shasum -a 256 | awk '{print $1}')"

kubectl --context "$KUBE_CONTEXT" -n postgres exec -i postgres-0 -- sh -lc \
  'PGPASSWORD="$POSTGRES_PASSWORD" psql -v ON_ERROR_STOP=1 -U local_platform -d local_platform' <<SQL
INSERT INTO developer_api_keys (
  merchant_id, key_id, permissions, ip_whitelist, status,
  algo, public_key, public_key_fingerprint
) VALUES (
  'local-org', 'local-ed25519-1', '["profile.read"]'::jsonb, '[]'::jsonb, 'active',
  'ed25519', decode('$public_hex', 'hex'), '$fingerprint'
)
ON CONFLICT (merchant_id, key_id) DO UPDATE SET
  algo = EXCLUDED.algo,
  public_key = EXCLUDED.public_key,
  public_key_fingerprint = EXCLUDED.public_key_fingerprint,
  permissions = EXCLUDED.permissions,
  status = EXCLUDED.status,
  updated_at = EXTRACT(epoch FROM CURRENT_TIMESTAMP)::bigint;
SQL

echo "Ed25519 API key is ready: key_id=local-ed25519-1 fingerprint=$fingerprint"
echo "Private key remains local at $PRIVATE_KEY and is ignored by Git."

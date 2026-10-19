#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
SCHEMA_DIR="$GITOPS_DIR/apps/api-bff/overlays/local/schema"
DESCRIPTOR="$SCHEMA_DIR/user-service-v1.pb"
METADATA="$SCHEMA_DIR/user-service-v1.yaml"
API_BFF_CONFIG="$ROOT_DIR/app/api-bff/etc/api-bff.yaml"

require_single_match() {
  local file="$1"
  local pattern="$2"
  local label="$3"
  local matches

  matches="$(grep -Ec "$pattern" "$file" || true)"
  if [[ "$matches" != "1" ]]; then
    echo "Expected exactly one $label entry in $file, found $matches" >&2
    exit 1
  fi
}

require_single_match "$METADATA" '^digest:[[:space:]].*$' 'descriptor digest'
mkdir -p "$SCHEMA_DIR"
protoc \
  --proto_path="$ROOT_DIR/api/user/v1" \
  --include_imports \
  --descriptor_set_out="$DESCRIPTOR" \
  "$ROOT_DIR/api/user/v1/user.proto"

digest="$(shasum -a 256 "$DESCRIPTOR" | awk '{print $1}')"
trap 'rm -f "${METADATA}.bak" "${API_BFF_CONFIG}.bak"' EXIT
sed -i.bak "s/^digest: .*/digest: sha256:$digest/" "$METADATA"
python3 "$ROOT_DIR/scripts/update-schema-digest.py" "$API_BFF_CONFIG" user-v1 "$digest"

grep -Fqx "digest: sha256:$digest" "$METADATA"

echo "Generated $DESCRIPTOR"
echo "SHA-256: $digest"

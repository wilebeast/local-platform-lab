#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
SCHEMA_DIR="$GITOPS_DIR/apps/api-bff/overlays/local/schema"
DESCRIPTOR="$SCHEMA_DIR/user-service-v1.pb"
METADATA="$SCHEMA_DIR/metadata.yaml"
API_BFF_CONFIG="$ROOT_DIR/app/api-bff/etc/api-bff.yaml"

mkdir -p "$SCHEMA_DIR"
protoc \
  --proto_path="$ROOT_DIR/api/user/v1" \
  --include_imports \
  --descriptor_set_out="$DESCRIPTOR" \
  "$ROOT_DIR/api/user/v1/user.proto"

digest="$(shasum -a 256 "$DESCRIPTOR" | awk '{print $1}')"
sed -i.bak "s/^digest: .*/digest: sha256:$digest/" "$METADATA"
rm -f "$METADATA.bak"
sed -i.bak "s/^  DescriptorSHA256: .*/  DescriptorSHA256: $digest/" "$API_BFF_CONFIG"
rm -f "$API_BFF_CONFIG.bak"

echo "Generated $DESCRIPTOR"
echo "SHA-256: $digest"

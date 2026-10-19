#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GITOPS_DIR="${GITOPS_DIR:-$ROOT_DIR/../local-platform-gitops}"
SCHEMA_DIR="$GITOPS_DIR/apps/api-bff/overlays/local/schema"
DESCRIPTOR="$SCHEMA_DIR/account-service-v1.pb"
METADATA="$SCHEMA_DIR/account-service-v1.yaml"
API_BFF_CONFIG="$ROOT_DIR/app/api-bff/etc/api-bff.yaml"

protoc --proto_path="$ROOT_DIR/api/account/v1" --include_imports \
  --descriptor_set_out="$DESCRIPTOR" "$ROOT_DIR/api/account/v1/account.proto"
digest="$(shasum -a 256 "$DESCRIPTOR" | awk '{print $1}')"
python3 - "$METADATA" "$digest" <<'PYCODE'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
contents = path.read_text()
updated, count = re.subn(r"(?m)^digest:\s*\S+\s*$", f"digest: sha256:{sys.argv[2]}", contents)
if count != 1:
    raise SystemExit(f"Expected exactly one digest entry in {path}, found {count}")
path.write_text(updated + ("\n" if not updated.endswith("\n") else ""))
PYCODE
python3 "$ROOT_DIR/scripts/update-schema-digest.py" "$API_BFF_CONFIG" account-v1 "$digest"
echo "Generated $DESCRIPTOR"
echo "SHA-256: $digest"

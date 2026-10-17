#!/usr/bin/env bash
set -euo pipefail

missing=0
for tool in colima docker kind kubectl helm git curl; do
  if command -v "$tool" >/dev/null 2>&1; then
    printf 'OK      %s\n' "$tool"
  else
    printf 'MISSING %s\n' "$tool"
    missing=1
  fi
done
exit "$missing"

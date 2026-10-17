#!/usr/bin/env bash
set -euo pipefail

health="$(curl --fail --silent --show-error http://localhost:18080/healthz)"
profile="$(curl --fail --silent --show-error -H 'X-User-ID: local-alb-test' http://localhost:18080/api/v3/profile)"

grep -q '"status":"ok"' <<<"$health"
grep -q 'local-alb-test' <<<"$profile"

echo "GET /healthz -> $health"
echo "GET /api/v3/profile -> $profile"

#!/usr/bin/env bash
set -euo pipefail

health="$(curl --fail --silent --show-error http://localhost:18080/healthz)"
redirect_status="$(curl --silent --output /dev/null --write-out '%{http_code}' http://localhost:18080/api/v3/profile)"

grep -q '"status":"ok"' <<<"$health"
test "$redirect_status" = "308"

echo "GET /healthz -> $health"
echo "HTTP /api/v3/profile -> $redirect_status HTTPS redirect"

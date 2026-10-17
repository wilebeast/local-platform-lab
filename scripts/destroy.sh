#!/usr/bin/env bash
set -euo pipefail

kind delete cluster --name local-platform
docker rm -f local-registry >/dev/null 2>&1 || true
echo "Deleted Kind cluster and local registry. Colima was kept running."

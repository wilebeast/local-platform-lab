#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="kind-local-platform"
password="$(kubectl --context "$KUBE_CONTEXT" -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 --decode)"
echo "Argo CD: http://localhost:8443"
echo "User: admin"
echo "Password: $password"
kubectl --context "$KUBE_CONTEXT" -n argocd port-forward service/argocd-server 8443:80

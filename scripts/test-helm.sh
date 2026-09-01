#!/usr/bin/env bash
set -euo pipefail
echo '=== Helm lint ==='
helm lint charts/diverge/
echo '=== Helm template (defaults) ==='
helm template diverge charts/diverge/ > /dev/null
echo '=== Helm template (all features) ==='
helm template diverge charts/diverge/ \
  --set database.provider=schema \
  --set deploy.provider=direct \
  --set prometheus.alerts.enabled=true \
  --set certManager.enabled=true \
  --set rbac.namespaceScoped=true \
  --set 'rbac.managedNamespaces={ns1,ns2}' \
  > /dev/null
echo '=== Helm template (namespace scoped RBAC) ==='
output=$(helm template diverge charts/diverge/ --set rbac.namespaceScoped=true --set 'rbac.managedNamespaces={test-ns}')
if echo "$output" | grep -qE '^kind: ClusterRole$'; then
  echo 'FAIL: ClusterRole found when namespaceScoped=true'
  exit 1
fi
if ! echo "$output" | grep -qE '^kind: Role$'; then
  echo 'FAIL: Role not found when namespaceScoped=true'
  exit 1
fi
if ! echo "$output" | grep -qE '^kind: RoleBinding$'; then
  echo 'FAIL: RoleBinding not found when namespaceScoped=true'
  exit 1
fi
echo '✅ All Helm tests passed'

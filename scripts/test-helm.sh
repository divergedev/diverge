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
if ! echo "$output" | grep -A4 '^kind: Role$' | grep -q 'namespace: test-ns'; then
  echo 'FAIL: Role not in expected namespace'
  exit 1
fi
if ! echo "$output" | grep -A4 '^kind: RoleBinding$' | grep -q 'namespace: test-ns'; then
  echo 'FAIL: RoleBinding not in expected namespace'
  exit 1
fi
echo '=== Helm template (server secureCookies flag) ==='
server_output=$(helm template diverge charts/diverge/ --set server.enabled=true --set server.auth.secureCookies=true)
if ! echo "$server_output" | grep -q -- '--secure-cookies=true'; then
  echo 'FAIL: --secure-cookies=true not found in server deployment args'
  exit 1
fi

echo '=== Helm template (all subcomponents: server, proxy, activatorProxy) ==='
all_components=$(helm template diverge charts/diverge/ \
  --set server.enabled=true \
  --set proxy.enabled=true \
  --set activatorProxy.enabled=true \
  --set activatorProxy.activatorUrl=http://activator.knative-serving:8012 \
  --set activatorProxy.targetSelector="app=test")

# Verify no dangling image tag like 'image: "...:"'
if echo "$all_components" | grep -E 'image:\s*".*:[[:space:]]*"'; then
  echo 'FAIL: Dangling image tag detected in rendered templates'
  exit 1
fi

echo '=== Helm template (image tag precedence) ==='
# When image.tag is overridden, controller, server, and proxy should all use that tag if component tags are not set
custom_tag=$(helm template diverge charts/diverge/ \
  --set image.tag=custom-v1 \
  --set server.enabled=true \
  --set proxy.enabled=true)

controller_matches=$(echo "$custom_tag" | grep -c 'image: "ghcr.io/divergedev/diverge:custom-v1"')
if [ "$controller_matches" -ne 3 ]; then
  echo "FAIL: Expected 3 containers with image: ghcr.io/divergedev/diverge:custom-v1, found $controller_matches"
  exit 1
fi

echo '=== Helm template (comma-separated routingProvider) ==='
multi_routing=$(helm template diverge charts/diverge/ --set 'routingProvider=gateway\,istio')
if ! echo "$multi_routing" | grep -q -- '--routing-provider=gateway,istio'; then
  echo 'FAIL: --routing-provider=gateway,istio not found in controller deployment args'
  exit 1
fi

echo '✅ All Helm tests passed'

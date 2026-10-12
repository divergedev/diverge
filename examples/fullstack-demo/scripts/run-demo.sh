#!/usr/bin/env bash
# examples/fullstack-demo/scripts/run-demo.sh
# Interactive workshop runner for the Diverge multi-service fullstack demo.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${DEMO_DIR}/../.." && pwd)"
CONFIG_PATH="${DEMO_DIR}/.diverge.yaml"

DRY_RUN="auto"
for arg in "$@"; do
  case "$arg" in
    --dry-run)
      DRY_RUN="true"
      ;;
    --live)
      DRY_RUN="false"
      ;;
    -h|--help)
      echo "Usage: $0 [--dry-run | --live]"
      exit 0
      ;;
  esac
done

if [[ "$DRY_RUN" == "auto" ]]; then
  if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
    DRY_RUN="false"
  else
    DRY_RUN="true"
  fi
fi

run_diverge() {
  if command -v diverge >/dev/null 2>&1; then
    diverge "$@"
  else
    (cd "$REPO_ROOT" && go run ./cmd/diverge "$@")
  fi
}

echo "==> [1/4] Validating .diverge.yaml against JSON Schema"
echo "    $ diverge validate -c examples/fullstack-demo/.diverge.yaml"
run_diverge validate -c "$CONFIG_PATH"

echo ""
echo "==> [2/4] Validating service dependency topology (DAG & entrypoints)"
echo "    $ diverge graph validate -c examples/fullstack-demo/.diverge.yaml"
run_diverge graph validate -n default --config "$CONFIG_PATH"

echo ""
echo "==> [3/4] Simulating header-propagated route to checkout-api"
echo "    $ diverge route checkout-api -c examples/fullstack-demo/.diverge.yaml"
run_diverge route checkout-api -n default --config "$CONFIG_PATH"

echo ""
echo "==> [4/4] Applying Kubernetes & Diverge manifests"
MANIFESTS=(
  "00-baseline.yaml"
  "01-previewgroup-pr42.yaml"
  "02-environment-scale-to-zero.yaml"
  "03-agenttask-fix-checkout.yaml"
)

for manifest in "${MANIFESTS[@]}"; do
  rel_path="examples/fullstack-demo/k8s/${manifest}"
  full_path="${DEMO_DIR}/k8s/${manifest}"
  if [[ "$DRY_RUN" == "true" ]]; then
    echo "    [dry-run] kubectl apply -f ${rel_path}"
  else
    echo "    Applying ${rel_path}..."
    kubectl apply -f "$full_path"
  fi
done

echo ""
if [[ "$DRY_RUN" == "true" ]]; then
  echo "Completed fullstack-demo workshop in --dry-run mode."
else
  echo "Completed fullstack-demo deployment. Test header routing with:"
  echo "  curl -s http://localhost:8080/api/checkout | jq ."
  echo "  curl -s -H 'x-diverge-env: pr-42' http://localhost:8080/api/checkout | jq ."
fi

#!/usr/bin/env bash
# scripts/check-pbt-coverage.sh
# Ensures every CRD types file in api/ and core domain packages have corresponding property-based tests.
set -euo pipefail

EXIT_CODE=0
for type_file in api/v1alpha1/*_types.go; do
  [[ ! -f "$type_file" ]] && continue
  base=$(basename "$type_file" _types.go)
  dir=$(dirname "$type_file")

  # Check for dedicated PBT file or matching test in shared property_test.go
  if [[ -f "${dir}/${base}_property_test.go" ]]; then
    continue
  fi
  if [[ -f "${dir}/property_test.go" ]] && grep -qi "Test${base}DeepCopyProperty" "${dir}/property_test.go"; then
    continue
  fi

  echo "⚠️  No property-based test for ${type_file}"
  echo "    Expected: ${dir}/${base}_property_test.go or Test<Kind>DeepCopyProperty in ${dir}/property_test.go"
  EXIT_CODE=1
done

REQUIRED_PBT_FILES=(
  "pkg/auth/property_test.go"
  "pkg/topology/property_test.go"
  "pkg/secrets/property_test.go"
  "pkg/features/configmap_property_test.go"
  "pkg/features/flipt_property_test.go"
  "pkg/features/flagsmith_property_test.go"
  "pkg/features/unleash_property_test.go"
  "internal/git/property_test.go"
  "internal/config/property_test.go"
  "internal/proxy/property_test.go"
  "pkg/doctor/diagnostics_test.go"
  "internal/cli/init_test.go"
)

for pbt_file in "${REQUIRED_PBT_FILES[@]}"; do
  if [[ ! -f "$pbt_file" ]]; then
    echo "⚠️  Missing required property-based test suite: ${pbt_file}"
    EXIT_CODE=1
  fi
done

if [[ $EXIT_CODE -eq 0 ]]; then
  echo "✅ All CRD types and core packages have property-based tests"
fi

exit $EXIT_CODE

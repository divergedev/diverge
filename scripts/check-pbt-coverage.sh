#!/usr/bin/env bash
# scripts/check-pbt-coverage.sh
# Ensures every CRD types file in api/ has a corresponding property-based test.
set -euo pipefail

EXIT_CODE=0
for type_file in api/v1alpha1/*_types.go; do
  [[ ! -f "$type_file" ]] && continue
  base=$(basename "$type_file" _types.go)
  dir=$(dirname "$type_file")

  # Check for dedicated PBT file or shared property_test.go
  if [[ ! -f "${dir}/${base}_property_test.go" ]] && \
     [[ ! -f "${dir}/property_test.go" ]]; then
    echo "⚠️  No property-based test for ${type_file}"
    echo "    Expected: ${dir}/${base}_property_test.go or ${dir}/property_test.go"
    EXIT_CODE=1
  fi
done

if [[ $EXIT_CODE -eq 0 ]]; then
  echo "✅ All CRD types have property-based tests"
fi

exit $EXIT_CODE

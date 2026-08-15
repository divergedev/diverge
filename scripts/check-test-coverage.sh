#!/usr/bin/env bash
# scripts/check-test-coverage.sh
# Ensures Go source files in internal/ and pkg/ have corresponding test files.
# Files listed in .testignore are exempt.
set -euo pipefail

IGNORE_FILE=".testignore"
EXIT_CODE=0

# Load ignore patterns
declare -a IGNORES=()
if [[ -f "$IGNORE_FILE" ]]; then
  while IFS= read -r line; do
    # Skip comments and empty lines
    [[ -z "$line" || "$line" == \#* ]] && continue
    IGNORES+=("$line")
  done < "$IGNORE_FILE"
fi

is_ignored() {
  local file="$1"
  for pattern in "${IGNORES[@]}"; do
    # Support both exact paths and glob patterns
    # shellcheck disable=SC2254
    if [[ "$file" == $pattern ]]; then
      return 0
    fi
  done
  return 1
}

# Check files passed as args, or scan internal/ and pkg/
if [[ $# -gt 0 ]]; then
  FILES=("$@")
else
  mapfile -t FILES < <(find internal/ pkg/ -name '*.go' \
    -not -name '*_test.go' \
    -not -name 'zz_generated*' \
    -not -name 'doc.go' \
    -not -path '*/vendor/*' \
    | sort)
fi

MISSING=()
for f in "${FILES[@]}"; do
  # Skip test files, generated files, and non-Go files
  [[ "$f" == *_test.go ]] && continue
  [[ "$f" == *zz_generated* ]] && continue
  [[ "$f" != *.go ]] && continue

  # Only check internal/ and pkg/
  [[ "$f" != internal/* && "$f" != pkg/* ]] && continue

  # Check ignore list
  if is_ignored "$f"; then
    continue
  fi

  # Look for test file
  test_f="${f%.go}_test.go"
  if [[ ! -f "$test_f" ]]; then
    MISSING+=("$f")
  fi
done

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "⚠️  Go files without corresponding test files:"
  for f in "${MISSING[@]}"; do
    echo "    ${f}"
  done
  echo ""
  echo "To exempt a file, add it to .testignore (supports glob patterns)"
  EXIT_CODE=1
else
  echo "✅ All Go source files have corresponding tests"
fi

exit $EXIT_CODE

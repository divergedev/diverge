#!/usr/bin/env bash
# Generate test coverage report
set -euo pipefail

COVER_DIR="coverage"
mkdir -p "$COVER_DIR"

echo "Running tests with coverage..."
go test ./... -coverprofile="${COVER_DIR}/coverage.out" -covermode=atomic -count=1

echo ""
echo "Coverage by package:"
go tool cover -func="${COVER_DIR}/coverage.out" | grep -E "^(total|github)" | sort -k3 -rn | head -25

echo ""
echo "Total coverage:"
go tool cover -func="${COVER_DIR}/coverage.out" | tail -1

echo ""
echo "HTML report: go tool cover -html=${COVER_DIR}/coverage.out"

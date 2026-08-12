#!/usr/bin/env bash
# Verify that generated protobuf/ConnectRPC stubs in gen/ are up-to-date.
#
# This script re-runs code generation and checks for any diff or new
# untracked files against the committed gen/ directory. If the generated
# output differs, the stubs are stale and must be regenerated with
# `make proto`.
#
# Usage:
#   ./scripts/check-gen.sh          # exits 0 if clean, 1 if stale
#
# Used by:
#   - lefthook pre-push hook
#   - GitHub Actions CI

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

# Re-exec inside nix develop if not already there
if [[ -f flake.nix ]] && ! [[ "${IN_NIX_SHELL:-}" == "1" || -n "${IN_NIX_SHELL:-}" ]]; then
    exec nix develop -c bash "$0" "$@"
fi

# Verify tools are available
for tool in buf protoc-gen-go protoc-gen-connect-go; do
    if ! command -v "$tool" &>/dev/null; then
        echo "❌ Required tool '$tool' not found. Run inside nix develop."
        exit 1
    fi
done

# Run code generation (same as `make proto`)
echo "▸ Running buf generate..."
buf generate
buf generate --template buf.gen.domain.yaml
rm -rf gen/domain/diverge/v1

# Check for modified or untracked files in gen/
DIFF_OUTPUT=$(git diff --stat gen/ 2>/dev/null || true)
UNTRACKED=$(git ls-files --others --exclude-standard -- gen/ 2>/dev/null || true)

if [[ -n "$DIFF_OUTPUT" || -n "$UNTRACKED" ]]; then
    echo ""
    echo "❌ Generated files are STALE."
    if [[ -n "$DIFF_OUTPUT" ]]; then
        echo ""
        echo "Modified files:"
        echo "$DIFF_OUTPUT"
    fi
    if [[ -n "$UNTRACKED" ]]; then
        echo ""
        echo "New untracked files:"
        echo "$UNTRACKED"
    fi
    echo ""
    echo "Run 'make proto' and commit the updated gen/ directory."
    exit 1
fi

echo "✅ Generated files are up-to-date"

#!/usr/bin/env bash
# Verify that generated protobuf/ConnectRPC stubs in gen/ are up-to-date.
#
# This script re-runs code generation and checks for any diff against the
# committed gen/ directory. If the generated output differs, the stubs are
# stale and must be regenerated with `make proto`.
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

# Save current state of gen/ to detect changes
STASH_REF=$(git stash create 2>/dev/null || true)

# Run code generation (same as `make proto`)
echo "▸ Running buf generate..."
buf generate
buf generate --template buf.gen.domain.yaml
rm -rf gen/domain/diverge/v1

# Check for diff
if git diff --quiet gen/; then
    echo "✅ Generated files are up-to-date"
    exit 0
else
    echo ""
    echo "❌ Generated files are STALE. Diff:"
    echo ""
    git diff --stat gen/
    echo ""
    echo "Run 'make proto' and commit the updated gen/ directory."
    # Restore original state
    git checkout -- gen/
    exit 1
fi

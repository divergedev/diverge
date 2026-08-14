#!/usr/bin/env bash
set -euo pipefail
echo "🧹 Tearing down demo cluster..."
k3d cluster delete diverge-demo
echo "✅ Demo cluster deleted."

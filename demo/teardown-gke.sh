#!/usr/bin/env bash
set -euo pipefail

PROJECT="${GCP_PROJECT:?Set GCP_PROJECT}"
REGION="${GCP_REGION:-us-central1}"
CLUSTER_NAME="${GKE_CLUSTER:-diverge-demo}"

echo "🧹 Tearing down GKE demo cluster..."
gcloud container clusters delete "$CLUSTER_NAME" \
  --region="$REGION" --project="$PROJECT" --quiet
echo "✅ GKE demo cluster deleted."

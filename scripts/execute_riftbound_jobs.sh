#!/bin/bash
# Executes Riftbound Cloud Run Jobs immediately.
# Executions are async — this script just fires them off, it does not wait for completion.
# Usage: bash scripts/execute_riftbound_jobs.sh

PROJECT=future-gadget-labs-483502
REGION=us-central1

# rb03 excluded — no pricecharting URLs yet
SETS=(rb01 rb02)

total=${#SETS[@]}
idx=0

for SET_ID in "${SETS[@]}"; do
  JOB_NAME="riftbound-${SET_ID}"
  idx=$(( idx + 1 ))
  echo "[$idx/$total] Executing $JOB_NAME..."
  gcloud run jobs execute "$JOB_NAME" --region="$REGION" --project="$PROJECT" --async
  sleep 10
done

echo ""
echo "All $total jobs triggered. Monitor progress at:"
echo "https://console.cloud.google.com/run/jobs?project=$PROJECT"

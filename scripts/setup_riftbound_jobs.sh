#!/bin/bash
# Creates one Cloud Run Job + Cloud Scheduler trigger per Riftbound set.
# Jobs are staggered 5 minutes apart starting at 02:00 UTC on the 15th
# (offset from Pokemon jobs which start at 00:00 UTC).
# Already-created jobs/schedulers are skipped automatically.
# Usage: bash scripts/setup_riftbound_jobs.sh

set -e

PROJECT=future-gadget-labs-483502
REGION=us-central1
IMAGE=us-central1-docker.pkg.dev/future-gadget-labs-483502/tcg/evupdate:latest
SA=evupdate-runner@future-gadget-labs-483502.iam.gserviceaccount.com

# rb03 excluded — no pricecharting URLs yet
SETS=(rb01 rb02)

# Start at 02:00 UTC on the 15th to avoid overlap with Pokemon jobs
BASE_HOUR=2
BASE_MINUTE=0

idx=0

for SET_ID in "${SETS[@]}"; do
  JOB_NAME="riftbound-${SET_ID}"
  SCHEDULER_NAME="${JOB_NAME}-monthly"

  TOTAL_MINUTES=$(( BASE_HOUR * 60 + BASE_MINUTE + idx * 5 ))
  HOUR=$(( TOTAL_MINUTES / 60 ))
  MINUTE=$(( TOTAL_MINUTES % 60 ))
  SCHEDULE="${MINUTE} ${HOUR} 15 * *"

  echo ""
  echo "=== [$idx] $SET_ID  ->  $JOB_NAME  (${HOUR}:$(printf '%02d' $MINUTE) UTC) ==="

  # Create Cloud Run Job (skip if already exists)
  if gcloud run jobs describe "$JOB_NAME" --region="$REGION" --project="$PROJECT" &>/dev/null; then
    echo "  job already exists, skipping"
  else
    gcloud run jobs create "$JOB_NAME" \
      --image="$IMAGE" \
      --region="$REGION" \
      --service-account="$SA" \
      --set-env-vars="GAME=riftbound,BQ_PROJECT=$PROJECT,BQ_DATASET=tcg_stage" \
      --memory=512Mi \
      --task-timeout=30m \
      --args="-set,$SET_ID" \
      --project="$PROJECT"
    echo "  job created"
  fi

  # Create Cloud Scheduler trigger (skip if already exists)
  if gcloud scheduler jobs describe "$SCHEDULER_NAME" --location="$REGION" --project="$PROJECT" &>/dev/null; then
    echo "  scheduler already exists, skipping"
  else
    gcloud scheduler jobs create http "$SCHEDULER_NAME" \
      --schedule="$SCHEDULE" \
      --location="$REGION" \
      --uri="https://${REGION}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/${PROJECT}/jobs/${JOB_NAME}:run" \
      --message-body="{}" \
      --oauth-service-account-email="$SA" \
      --project="$PROJECT"
    echo "  scheduler created: $SCHEDULE"
  fi

  idx=$(( idx + 1 ))
done

echo ""
echo "Done. $idx jobs configured."

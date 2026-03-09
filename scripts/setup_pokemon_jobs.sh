#!/bin/bash
# Creates one Cloud Run Job + Cloud Scheduler trigger per Pokemon set.
# Jobs are staggered 5 minutes apart starting at midnight UTC on the 15th.
# Already-created jobs/schedulers are skipped automatically.
# Usage: bash scripts/setup_pokemon_jobs.sh

set -e

PROJECT=future-gadget-labs-483502
REGION=us-central1
IMAGE=us-central1-docker.pkg.dev/future-gadget-labs-483502/tcg/evupdate:latest
SA=evupdate-runner@future-gadget-labs-483502.iam.gserviceaccount.com

SETS=(
  sv01 sv02 sv03 sv04 sv05 sv06 sv07 sv08 sv09 sv10 sv3.5 sv4.5 sv6.5 sv8.5 svbb svwf
  swsh01 swsh02 swsh03 swsh04 swsh05 swsh06 swsh07 swsh08 swsh09 swsh10 swsh10.5 swsh11 swsh12 swsh12.5 swsh3.5 swsh4.5 swsh7.5
  sm01 sm02 sm03 sm04 sm05 sm06 sm07 sm08 sm09 sm10 sm11 sm11.5 sm12 sm3.5 sm7.5 sm9.5
  xy1 xy2 xy3 xy4 xy5 xy6 xy7 xy8 xy9 xy9.5
  bw1 bw2 bw3 bw4 bw5 bw6 bw7 bw8 bw9 bw10 bw11
  hgss1 hgss2 hgss3 hgss4
  dp1 dp2 dp3 dp4 dp5 dp6 dp7
  pl1 pl2 pl3 pl4
  col1
  neo1 neo2 neo3 neo4
  ex1 ex2 ex3 ex4 ex5 ex6 ex7 ex8 ex9 ex10 ex11 ex12 ex13 ex14 ex15 ex16
  ecard1 ecard2 ecard3
  me01 me02 me2.5
  base1 base1s base1u base2
  jungle fossil teamrocket gymheroes gymchallenge
)

idx=0

for SET_ID in "${SETS[@]}"; do
  JOB_NAME="evupdate-${SET_ID//./-}"
  SCHEDULER_NAME="${JOB_NAME}-monthly"

  # Stagger: 5 minutes apart starting at 00:00 UTC on the 15th
  MINUTE=$(( (idx * 5) % 60 ))
  HOUR=$(( (idx * 5) / 60 ))
  SCHEDULE="${MINUTE} ${HOUR} 15 * *"

  echo ""
  echo "=== [$idx] $SET_ID  ->  $JOB_NAME  (${HOUR}:$(printf '%02d' $MINUTE) UTC) ==="

  # Create Cloud Run Job (skip if already exists)
  if gcloud run jobs describe "$JOB_NAME" --region="$REGION" --project="$PROJECT" &>/dev/null; then
    echo "  job already exists, skipping"
  else
    gcloud run jobs create "$JOB_NAME" --image="$IMAGE" --region="$REGION" --service-account="$SA" --set-env-vars="BQ_PROJECT=$PROJECT,BQ_DATASET=tcg_stage" --memory=512Mi --task-timeout=30m --args="-set,$SET_ID" --project="$PROJECT"
    echo "  job created"
  fi

  # Create Cloud Scheduler trigger (skip if already exists)
  if gcloud scheduler jobs describe "$SCHEDULER_NAME" --location="$REGION" --project="$PROJECT" &>/dev/null; then
    echo "  scheduler already exists, skipping"
  else
    gcloud scheduler jobs create http "$SCHEDULER_NAME" --schedule="$SCHEDULE" --location="$REGION" --uri="https://${REGION}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/${PROJECT}/jobs/${JOB_NAME}:run" --message-body="{}" --oauth-service-account-email="$SA" --project="$PROJECT"
    echo "  scheduler created: $SCHEDULE"
  fi

  idx=$(( idx + 1 ))
done

echo ""
echo "Done. $idx jobs configured."

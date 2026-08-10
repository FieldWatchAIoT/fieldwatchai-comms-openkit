#!/usr/bin/env bash
# LocalStack ready hook — creates the SQS queue used by the openkit demo.
# Runs once, after LocalStack signals it is ready.
set -euo pipefail

awslocal sqs create-queue --queue-name openkit-inbound >/dev/null
echo "[openkit] SQS queue 'openkit-inbound' ready at http://localhost:4566/000000000000/openkit-inbound"

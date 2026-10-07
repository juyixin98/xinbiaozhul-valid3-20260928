#!/usr/bin/env bash
# Local verification: build, run the test binary, then the sample job.
# Expected judgment:
#   - mink_tests prints one [TEST ...] PASS line per case and exits 0
#     with "[SUMMARY] ... failed=0".
#   - minkcli prints SUM / SUPPORT / ADDITIVITY lines; every ADDITIVITY
#     line must end with status=OK.
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/build.sh
RUN_ID="local-$(date +%Y%m%d-%H%M%S)"
./build/mink_tests --run-id "$RUN_ID"
./build/minkcli fixtures/sample_job.txt

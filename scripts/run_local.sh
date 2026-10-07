#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT_DIR}/build"
DATA_DIR="${ROOT_DIR}/build/fixtures"

cmake -S "${ROOT_DIR}" -B "${BUILD_DIR}" -DCMAKE_BUILD_TYPE=Release
cmake --build "${BUILD_DIR}" --parallel

mkdir -p "${DATA_DIR}"
"${BUILD_DIR}/generate_fixtures" \
    --sawtooth-segments 2048 \
    --output "${DATA_DIR}/synthetic_batch.csv"

"${BUILD_DIR}/nearest_projection_batch" \
    --input "${DATA_DIR}/synthetic_batch.csv" \
    --config "${ROOT_DIR}/config/default.properties" \
    --output "${DATA_DIR}/synthetic_results.csv"

printf 'Generated %s\n' "${DATA_DIR}/synthetic_results.csv"
printf 'Expected spot checks:\n'
printf '  ZERO_TIE candidate_count=2, segment indices 0 and 1\n'
printf '  SELF_INTERSECTION candidate_count=2, distance_squared=0\n'
printf '  HUGE_MIDPOINT t=0.5 and distance_squared=2\n'

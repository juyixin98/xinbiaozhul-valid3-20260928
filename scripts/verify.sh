#!/usr/bin/env bash
# Local verification entry: configure, build, generate fixtures, run tests,
# and run a small end-to-end batch demo. No containers, no extra runtimes.
set -euo pipefail

cd "$(dirname "$0")/.."
BUILD_DIR="${BUILD_DIR:-build}"

echo "== configure =="
cmake -S . -B "$BUILD_DIR" -DCMAKE_BUILD_TYPE=Release

echo "== build =="
cmake --build "$BUILD_DIR" -j

echo "== generate synthetic fixtures =="
"$BUILD_DIR/gen_fixtures" data

echo "== run test suite (ctest) =="
ctest --test-dir "$BUILD_DIR" --output-on-failure

echo "== batch demo: index engine =="
"$BUILD_DIR/mileage_batch" data/large_random.poly data/queries.txt data/results_index.txt

echo "== batch demo: full-scan engine (reference) =="
"$BUILD_DIR/mileage_batch" data/large_random.poly data/queries.txt data/results_scan.txt --scan

echo "== cross-check: index results must equal scan results =="
if diff -q data/results_index.txt data/results_scan.txt; then
  echo "OK: index and full scan produced identical result files"
else
  echo "MISMATCH between index and scan results" >&2
  exit 1
fi

echo "== degenerate-case validation demo =="
"$BUILD_DIR/mileage_validate" data/self_intersect.poly || true
"$BUILD_DIR/mileage_validate" data/zero_length.poly || true

echo "== single query demo =="
"$BUILD_DIR/mileage_query" data/simple.poly 12 4

echo "verification finished"

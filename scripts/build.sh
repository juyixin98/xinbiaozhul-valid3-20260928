#!/usr/bin/env bash
# One-off configure + build. Does not run any tests.
set -euo pipefail
cd "$(dirname "$0")/.."
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j

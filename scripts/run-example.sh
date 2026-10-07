#!/usr/bin/env bash
set -euo pipefail

BUILD_DIR="${1:-build/native}"
"$BUILD_DIR/minkowski_service" --config config/geometry-service.conf \
  < examples/batch.txt

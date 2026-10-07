#!/usr/bin/env bash
set -euo pipefail

BUILD_DIR="${1:-build/native}"
OUTPUT_DIR="${2:-examples/generated}"
"$BUILD_DIR/minkowski_fixture_generator" "$OUTPUT_DIR"

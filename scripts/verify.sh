#!/usr/bin/env bash
# Local verification harness (POSIX shell, native Node only).
# Runs the hand-computed corpus through BOTH engines via the compare CLI,
# then runs the Node built-in test suite.
set -euo pipefail

cd "$(dirname "$0")/.."

echo "== regenerate exhaustive short-string fixture (independent reference) =="
./node_modules/.bin/tsx scripts/generate-fixtures.ts \
  --grammar fixtures/grammars/leftrec.bnf \
  --start E \
  --alphabet 'id,+' \
  --max-length 5 \
  --out fixtures/expected/leftrec-exhaustive.json

echo "== cross-check hand-computed corpus =="
./node_modules/.bin/tsx src/cli/compare.ts --corpus fixtures/expected/corpus.json

echo "== unit tests =="
npm test

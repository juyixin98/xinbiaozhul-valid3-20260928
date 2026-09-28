#!/usr/bin/env bash
# Local verification from a clean checkout.
#
#   scripts/verify.sh            # full run (venv + docker DB + tests + demo)
#   scripts/verify.sh --tests    # only recreate venv and run pytest
#
# Requirements: python3.12+, docker (or a reachable PostgreSQL), network for
# the pinned wheels on first install.
set -euo pipefail
cd "$(dirname "$0")/.."

PG_HOST=${SCHED_PG_HOST:-127.0.0.1}
PG_PORT=${SCHED_PG_PORT:-5433}
DEV_URL="postgresql://scheduler:scheduler@${PG_HOST}:${PG_PORT}/scheduler"
TEST_URL="postgresql://scheduler:scheduler@${PG_HOST}:${PG_PORT}/scheduler_test"
ADMIN_URL="postgresql://scheduler:scheduler@${PG_HOST}:${PG_PORT}/postgres"

log() { printf '\n\033[1;36m== %s\033[0m\n' "$*"; }

log "1/5 Python virtualenv + pinned dependencies"
python3 -m venv .venv
.venv/bin/pip install --quiet --upgrade pip
.venv/bin/pip install --quiet -r requirements.txt
.venv/bin/pip freeze | sort > requirements.lock

if [[ "${1:-}" == "--tests" ]]; then
  export SCHED_TEST_DATABASE_URL="$TEST_URL" SCHED_TEST_ADMIN_URL="$ADMIN_URL"
  log "Running pytest"
  .venv/bin/python -m pytest tests/ "$@"
  exit 0
fi

log "2/5 PostgreSQL via docker compose (port ${PG_PORT})"
if PGPASSWORD=scheduler pg_isready -h "$PG_HOST" -p "$PG_PORT" -U scheduler >/dev/null 2>&1; then
  echo "PostgreSQL already reachable at ${PG_HOST}:${PG_PORT}; reusing it."
else
  docker compose up -d db
  for _ in $(seq 1 30); do
    PGPASSWORD=scheduler pg_isready -h "$PG_HOST" -p "$PG_PORT" -U scheduler >/dev/null 2>&1 && break
    sleep 1
  done
fi

export SCHED_DATABASE_URL="$DEV_URL"
export SCHED_TEST_DATABASE_URL="$TEST_URL"
export SCHED_TEST_ADMIN_URL="$ADMIN_URL"

log "3/5 Test suite"
.venv/bin/python -m pytest tests/ "$@"

log "4/5 End-to-end DST/downtime/rollback demo (in-process HTTP)"
.venv/bin/python scripts/demo.py

log "5/5 Done. API server: SCHED_DATABASE_URL='$DEV_URL' .venv/bin/python -m scheduler"
echo "Then: curl -s 127.0.0.1:8080/healthz"

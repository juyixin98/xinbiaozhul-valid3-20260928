#!/usr/bin/env bash
# 从干净环境完整验证：venv → 锁定依赖 → 一次性 PG → schema → 起服务 → curl 演示 → pytest。
# 用法：bash scripts/verify.sh
set -euo pipefail

cd "$(dirname "$0")/.."
PORT="${TEST_PORT:-5434}"
APIPORT="${API_PORT:-18080}"
CONTAINER="sched-postgres-verify"
export DATABASE_URL="postgresql://sched:sched@127.0.0.1:${PORT}/scheduler"
export TEST_DATABASE_URL="$DATABASE_URL"
BASE="http://127.0.0.1:${APIPORT}"

cleanup() {
  echo ">> 清理"
  [[ -n "${UVICORN_PID:-}" ]] && kill "$UVICORN_PID" 2>/dev/null || true
  if [[ "${KEEP_DB:-0}" != "1" ]]; then
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "== 1/6 虚拟环境与依赖（锁定） =="
python3 -m venv .venv
. .venv/bin/activate
pip install --quiet --upgrade pip
if [[ -f requirements.lock ]]; then
  pip install --quiet -r requirements.lock
else
  pip install --quiet -r requirements.txt
fi

echo "== 2/6 一次性 PostgreSQL (127.0.0.1:${PORT}) =="
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$CONTAINER" \
  -e POSTGRES_USER=sched -e POSTGRES_PASSWORD=sched -e POSTGRES_DB=scheduler \
  -p "127.0.0.1:${PORT}:5432" postgres:16-alpine >/dev/null
for _ in $(seq 1 60); do
  docker exec "$CONTAINER" pg_isready -U sched -d scheduler >/dev/null 2>&1 && break
  sleep 1
done

echo "== 3/6 应用 schema =="
python - <<'PY'
import os, psycopg
from app.db import apply_schema
with psycopg.connect(os.environ["DATABASE_URL"]) as conn:
    apply_schema(conn)
print("schema ok")
PY

echo "== 4/6 启动服务 (127.0.0.1:${APIPORT}, 后台 tick 关闭，用手动 tick 演示) =="
SCHEDULER_ENABLED=false uvicorn app.main:app --host 127.0.0.1 --port "$APIPORT" \
  >/tmp/sched-uvicorn.log 2>&1 &
UVICORN_PID=$!
for _ in $(seq 1 60); do
  curl -sf "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 1
done
echo "healthz: $(curl -s "$BASE/healthz")"

echo "== 5/6 curl 演示：创建(纽约每日02:30，展示 gap) 与 fold 计划(每日01:30) → 手动 tick =="
SID=$(curl -s -X POST "$BASE/api/v1/schedules" -H 'content-type: application/json' \
  -d '{
    "name":"demo-gap","timezone":"America/New_York","rrule":"FREQ=DAILY",
    "start_at":"2026-03-07T02:30:00","exceptions":{},"gap_policy":"SKIP",
    "fallback_policy":"EARLIEST","action":{"type":"log"}
  }' | python -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "gap schedule_id=$SID"
echo "-- 理论时间线：2026-03-08 02:30 是春季不存在时间 → SKIP_GAP（前后日照常） --"
curl -s "$BASE/api/v1/schedules/$SID/timeline?start=2026-03-07T00:00:00Z&end=2026-03-09T05:00:00Z" \
  | python -c 'import sys,json;[print(" ",o["local_wall"],o["kind"],o["resolution"],o["due_at_utc"]) for o in json.load(sys.stdin)["occurrences"]]'

FID=$(curl -s -X POST "$BASE/api/v1/schedules" -H 'content-type: application/json' \
  -d '{
    "name":"demo-fold","timezone":"America/New_York","rrule":"FREQ=DAILY",
    "start_at":"2026-10-30T01:30:00","exceptions":{},"gap_policy":"SKIP",
    "fallback_policy":"EARLIEST","action":{"type":"log"}
  }' | python -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "fold schedule_id=$FID"
echo "-- 时间线预览：2026-11-01 01:30 是秋季重复时间，EARLIEST 解析为 05:30Z（fold=0） --"
curl -s "$BASE/api/v1/schedules/$FID/timeline?start=2026-11-01T00:00:00Z&end=2026-11-02T05:00:00Z" \
  | python -c 'import sys,json
for o in json.load(sys.stdin)["occurrences"]:
    print(" ",o["local_wall"],o["kind"],o["resolution"],"fold=",o["resolved_fold"],o["due_at_utc"])'

echo "-- 在 05:30:00Z 手动 tick：fold 的第一次（05:30Z）准时触发，且只触发一次 --"
curl -s -X POST "$BASE/api/v1/admin/ticks" -H 'content-type: application/json' \
  -d "{\"now\":\"2026-11-01T05:30:00Z\",\"schedule_id\":\"$FID\"}" \
  | python -c 'import sys,json;d=json.load(sys.stdin);s=d["schedules"][0];print("  run",d["run_id"],d["status"],"ontime=",s["claimed_ontime"],"succeeded=",s["succeeded"])'
# 再向前走到 fold 的第二次瞬间 06:30Z：EARLIEST 策略下该墙钟已处理，不会重复执行
curl -s -X POST "$BASE/api/v1/admin/ticks" -H 'content-type: application/json' \
  -d "{\"now\":\"2026-11-01T06:30:00Z\",\"schedule_id\":\"$FID\"}" >/dev/null
echo "-- fold 触发器落库（唯一一次，05:30Z） --"
curl -s "$BASE/api/v1/schedules/$FID/triggers?limit=50" \
  | python -c 'import sys,json
for t in json.load(sys.stdin)["items"]:
    if t["local_wall"].startswith("2026-11-01"): print(" ",t["local_wall"],t["due_at_utc"],t["status"])'

echo "== 6/6 pytest =="
python -m pytest tests/unit -q

echo ""
echo "全部验证步骤完成。"

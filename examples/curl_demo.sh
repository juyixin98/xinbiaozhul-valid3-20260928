#!/usr/bin/env bash
# 针对已运行服务（默认 http://127.0.0.1:8000）的示例请求。
set -euo pipefail
BASE="${BASE:-http://127.0.0.1:8000}"

echo "== 创建纽约每日 02:30（覆盖 2026 春季 gap） =="
SID=$(curl -s -X POST "$BASE/api/v1/schedules" -H 'content-type: application/json' \
  -d @examples/create_schedule.json | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
echo "schedule_id=$SID"

echo "== 查看理论时间线（本地→UTC 判定） =="
curl -s "$BASE/api/v1/schedules/$SID/timeline?start=2026-03-07T00:00:00Z&end=2026-03-09T05:00:00Z" \
  | python3 -m json.tool

echo "== 手动跑一个 tick（注入 now） =="
curl -s -X POST "$BASE/api/v1/admin/ticks" -H 'content-type: application/json' \
  -d "{\"now\":\"2026-03-09T07:30:30Z\",\"schedule_id\":\"$SID\"}" | python3 -m json.tool

echo "== 查询触发器与审计 =="
curl -s "$BASE/api/v1/schedules/$SID/triggers?limit=20" | python3 -m json.tool
curl -s "$BASE/api/v1/audit?entity_id=$SID" | python3 -m json.tool

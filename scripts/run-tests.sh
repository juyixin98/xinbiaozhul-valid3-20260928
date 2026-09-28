#!/usr/bin/env bash
# 在一次性 PostgreSQL 中运行全部测试。
# 用法：bash scripts/run-tests.sh
# 可用环境变量：TEST_PORT（默认 5434）、KEEP_DB=1（测试后保留容器便于排查）
set -euo pipefail

cd "$(dirname "$0")/.."
PORT="${TEST_PORT:-5434}"
CONTAINER="sched-postgres-test"
IMAGE="postgres:16-alpine"
export TEST_DATABASE_URL="postgresql://sched:sched@127.0.0.1:${PORT}/scheduler"

cleanup() {
  if [[ "${KEEP_DB:-0}" != "1" ]]; then
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo ">> 启动一次性 PostgreSQL (127.0.0.1:${PORT})"
docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$CONTAINER" \
  -e POSTGRES_USER=sched -e POSTGRES_PASSWORD=sched -e POSTGRES_DB=scheduler \
  -p "127.0.0.1:${PORT}:5432" "$IMAGE" >/dev/null

echo ">> 等待数据库就绪"
for _ in $(seq 1 60); do
  if docker exec "$CONTAINER" pg_isready -U sched -d scheduler >/dev/null 2>&1; then
    echo "   ready"; break
  fi
  sleep 1
done

if [[ ! -d .venv ]]; then
  echo ">> 创建虚拟环境并安装锁定依赖"
  python3 -m venv .venv
  . .venv/bin/activate
  pip install --quiet --upgrade pip
  if [[ -f requirements.lock ]]; then pip install --quiet -r requirements.lock; else pip install --quiet -r requirements.txt; fi
else
  . .venv/bin/activate
fi

echo ">> 运行 pytest -s（打印本地时间→UTC 判定与 run/trigger 标识）"
python -m pytest tests/unit -s "$@"

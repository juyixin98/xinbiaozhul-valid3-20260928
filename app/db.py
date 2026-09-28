"""PostgreSQL 连接池与 schema 应用（psycopg3）。"""
from __future__ import annotations

from contextlib import contextmanager
from pathlib import Path
from typing import Iterator

import psycopg
from psycopg_pool import ConnectionPool

SCHEMA_PATH = Path(__file__).resolve().parent.parent / "db" / "schema.sql"

_pool: ConnectionPool | None = None


def init_pool(database_url: str, *, min_size: int = 1, max_size: int = 8) -> ConnectionPool:
    global _pool
    if _pool is None:
        _pool = ConnectionPool(
            database_url,
            min_size=min_size,
            max_size=max_size,
            # 应用/仓储显式管理事务（见 transaction()）；连接以 autocommit 打开，
            # 避免连接归还池时仍持有隐式事务/行锁。
            kwargs={"autocommit": True, "row_factory": psycopg.rows.dict_row},
            open=True,
        )
    return _pool


def get_pool() -> ConnectionPool:
    if _pool is None:
        raise RuntimeError("connection pool not initialized; call init_pool() first")
    return _pool


def close_pool() -> None:
    global _pool
    if _pool is not None:
        _pool.close()
        _pool = None


@contextmanager
def transaction() -> Iterator[psycopg.Connection]:
    """一个原子工作单元：正常退出提交，异常回滚。池连接本身 autocommit=True。"""
    pool = get_pool()
    with pool.connection() as conn:
        try:
            with conn.transaction():
                yield conn
        except Exception:
            raise


def apply_schema(conn: psycopg.Connection) -> None:
    """幂等应用 schema.sql。"""
    conn.execute(SCHEMA_PATH.read_text(encoding="utf-8"))
    if not conn.autocommit:
        conn.commit()

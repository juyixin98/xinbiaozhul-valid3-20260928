"""FastAPI 依赖：连接池 / 时钟 / 配置（测试可覆盖 app.state 注入）。"""
from __future__ import annotations

from fastapi import Request

from app.config import Settings
from app.db import get_pool


def pool(request: Request):
    return request.app.state.pool


def settings(request: Request) -> Settings:
    return request.app.state.settings


def scheduler(request: Request):
    return request.app.state.scheduler


def clock_now(request: Request):
    return request.app.state.clock

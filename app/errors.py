"""错误契约：类型化异常 + 错误码 + 统一 JSON 信封。

四类可区分：
- 非法输入 422（VALIDATION_*）
- 资源超限 422（RRULE_TOO_DENSE）
- 状态冲突 409（VERSION_CONFLICT / STATE_CONFLICT）
- 运行失败：动作失败记录在 executions；内部 PLANNER_* 返回 500，绝不包装成成功
"""
from __future__ import annotations

from fastapi import Request
from fastapi.encoders import jsonable_encoder
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse

# ---- 错误码常量 -----------------------------------------------------------

# 422 非法输入
E_VALIDATION = "VALIDATION_ERROR"
E_RRULE_PARSE = "RRULE_PARSE_ERROR"
E_RRULE_UNSUPPORTED = "UNSUPPORTED_RRULE"
E_TIMEZONE_UNKNOWN = "TIMEZONE_UNKNOWN"
E_POLICY_CONFLICT = "POLICY_CONFLICT"
E_EXCEPTION_PARSE = "INVALID_EXCEPTION_DATE"
E_EXCEPTION_CONFLICT = "EXCEPTION_CONFLICT"
E_EXCEPTION_AMBIGUOUS_FOLD = "EXCEPTION_AMBIGUOUS_FOLD"
# 422 资源超限
E_RRULE_TOO_DENSE = "RRULE_TOO_DENSE"
# 404 / 409
E_NOT_FOUND = "NOT_FOUND"
E_VERSION_CONFLICT = "VERSION_CONFLICT"
E_STATE_CONFLICT = "STATE_CONFLICT"
# 500 内部：未定义/未收敛
E_PLANNER_ERROR = "PLANNER_ERROR"
E_PLANNER_NONMONOTONIC = "PLANNER_NONMONOTONIC"
# 动作（记录在 executions.code，非 HTTP 错误）
A_HTTP_4XX = "ADAPTER_HTTP_4XX"
A_HTTP_5XX = "ADAPTER_HTTP_5XX"
A_TIMEOUT = "ADAPTER_TIMEOUT"
A_CONNECTION = "ADAPTER_CONNECTION"
A_INTERNAL = "ADAPTER_INTERNAL"


class AppError(Exception):
    http_status: int = 422
    code: str = E_VALIDATION

    def __init__(self, message: str, details: dict | None = None):
        super().__init__(message)
        self.message = message
        self.details = details or {}


class ValidationError(AppError):
    http_status = 422
    code = E_VALIDATION


class RRuleParseError(AppError):
    http_status = 422
    code = E_RRULE_PARSE


class UnsupportedRRule(AppError):
    http_status = 422
    code = E_RRULE_UNSUPPORTED


class TimezoneUnknown(AppError):
    http_status = 422
    code = E_TIMEZONE_UNKNOWN


class PolicyConflict(AppError):
    http_status = 422
    code = E_POLICY_CONFLICT


class ExceptionParseError(AppError):
    http_status = 422
    code = E_EXCEPTION_PARSE


class ExceptionConflict(AppError):
    http_status = 422
    code = E_EXCEPTION_CONFLICT


class ExceptionAmbiguousFold(AppError):
    http_status = 422
    code = E_EXCEPTION_AMBIGUOUS_FOLD


class RRuleTooDense(AppError):
    http_status = 422
    code = E_RRULE_TOO_DENSE


class NotFound(AppError):
    http_status = 404
    code = E_NOT_FOUND


class VersionConflict(AppError):
    http_status = 409
    code = E_VERSION_CONFLICT


class StateConflict(AppError):
    http_status = 409
    code = E_STATE_CONFLICT


class PlannerInternalError(AppError):
    """未定义 / 未收敛：不得包装成成功。"""

    http_status = 500
    code = E_PLANNER_ERROR


class PlannerNonMonotonic(PlannerInternalError):
    code = E_PLANNER_NONMONOTONIC


def _envelope(code: str, message: str, details: dict, request_id: str | None) -> dict:
    return {"error": {"code": code, "message": message, "details": details, "request_id": request_id}}


def register_exception_handlers(app) -> None:
    @app.exception_handler(AppError)
    async def _app_error(request: Request, exc: AppError):
        request_id = getattr(request.state, "request_id", None)
        return JSONResponse(
            status_code=exc.http_status,
            content=_envelope(exc.code, exc.message, exc.details, request_id),
        )

    @app.exception_handler(RequestValidationError)
    async def _validation(request: Request, exc: RequestValidationError):
        request_id = getattr(request.state, "request_id", None)
        return JSONResponse(
            status_code=422,
            content=_envelope(
                E_VALIDATION, "request validation failed",
                {"errors": jsonable_encoder(exc.errors())}, request_id
            ),
        )

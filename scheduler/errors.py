"""Error contract shared by every module.

The four failure classes the problem requires us to keep distinguishable each
map to their own error code prefix and HTTP status:

* invalid input      -> ValidationError     (422, code ``VAL-*``)
* state conflict     -> ConflictError       (409, code ``STATE-*``)
* resource limit     -> ResourceLimitError  (429, code ``LIMIT-*``)
* runtime failure    -> DispatchError       (502, code ``RUN-*``)

Plus ``NOT-FOUND`` (404) and ``ENGINE-DIVERGED`` (500) for results that are
undefined / did not converge — those are *never* wrapped as success.

Errors carry a machine code, a human message and an optional ``details`` dict.
Modules never raise bare ``ValueError`` across a module boundary.
"""
from __future__ import annotations

from typing import Any


class AppError(Exception):
    """Base class for all errors crossing module/HTTP boundaries."""

    http_status: int = 500
    code: str = "INTERNAL"

    def __init__(
        self,
        message: str,
        *,
        details: dict[str, Any] | None = None,
        code: str | None = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.details = details or {}
        if code:
            self.code = code

    def to_dict(self) -> dict[str, Any]:
        return {"error": {"code": self.code, "message": self.message, "details": self.details}}


class ValidationError(AppError):
    """Request/spec is malformed. Never retried as-is."""

    http_status = 422
    code = "VAL-BAD-SPEC"

    def __init__(
        self,
        message: str,
        *,
        field: str | None = None,
        code: str | None = None,
        details: dict[str, Any] | None = None,
    ) -> None:
        d = dict(details or {})
        if field:
            d.setdefault("field", field)
        super().__init__(message, details=d)
        if code:
            self.code = code


class NotFoundError(AppError):
    http_status = 404
    code = "NOT-FOUND"


class ConflictError(AppError):
    """State is incompatible with the requested transition (409)."""

    http_status = 409
    code = "STATE-CONFLICT"


class ResourceLimitError(AppError):
    """A bounded resource (catch-up budget, candidate window) was exceeded."""

    http_status = 429
    code = "LIMIT-EXCEEDED"


class DispatchError(AppError):
    """The schedule was valid and due, but delivery to the target failed."""

    http_status = 502
    code = "RUN-FAILED"


class EngineDivergenceError(AppError):
    """An internal identity/ordering invariant was violated.

    Undefined/non-converged results surface as this 500 rather than being
    reported as a successful fire.
    """

    http_status = 500
    code = "ENGINE-DIVERGED"

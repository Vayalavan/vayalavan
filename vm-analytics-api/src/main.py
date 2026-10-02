"""vm-analytics-api — read-only reports over the analytics schema (CLAUDE.md §5.4).

Connects as the analytics READER role, which can SELECT from schema `analytics`
and nothing else, so this service cannot see a customer's name, phone or
address however a query is written.

Run:  uv run python src/main.py     (port from ANALYTICS_API_PORT)
Test: uv run pytest
"""

from __future__ import annotations

import hmac
import json
import logging
import sys
import uuid
from contextlib import asynccontextmanager
from datetime import datetime, timezone

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from psycopg.rows import dict_row
from psycopg_pool import AsyncConnectionPool

from config import SERVICE_NAME, load_settings

settings = load_settings()

INTERNAL_TOKEN_HEADER = "X-Internal-Token"
# Defence in depth behind the gateway's own check (CLAUDE.md §7).
ANALYTICS_ROLES = {"admin", "analyst"}
HEALTH_PATHS = {"/healthz", "/readyz"}


class _JSON(logging.Formatter):
    """One JSON object per line, the shape the Go services emit."""

    def format(self, record: logging.LogRecord) -> str:
        return json.dumps({
            "time": datetime.fromtimestamp(record.created, tz=timezone.utc).isoformat(),
            "level": record.levelname, "msg": record.getMessage(),
            "service": SERVICE_NAME, **getattr(record, "fields", {}),
        }, default=str)


_handler = logging.StreamHandler(sys.stdout)
_handler.setFormatter(_JSON())
logging.basicConfig(handlers=[_handler], level=settings.log_level.upper().replace("WARN", "WARNING"))
logging.getLogger("uvicorn.access").disabled = True
logger = logging.getLogger(SERVICE_NAME)


def error(status: int, code: str, message: str) -> JSONResponse:
    """The platform's single error shape (CLAUDE.md rule 8)."""
    return JSONResponse(status_code=status,
                        content={"error": {"code": code, "message": message, "details": {}}})


pool = AsyncConnectionPool(
    settings.analytics_api_database_url,
    max_size=settings.db_max_conns,
    # Read-only twice over: the role defaults to it, and the session says so.
    kwargs={"options": f"-c default_transaction_read_only=on "
                       f"-c statement_timeout={settings.db_statement_timeout_ms}",
            "row_factory": dict_row},
    open=False,
)


@asynccontextmanager
async def lifespan(_: FastAPI):
    await pool.open()
    logger.info("database pool ready")
    yield
    await pool.close()


app = FastAPI(title=SERVICE_NAME, lifespan=lifespan, docs_url=None, redoc_url=None)


@app.middleware("http")
async def guard(request: Request, call_next):
    request_id = request.headers.get("X-Request-Id") or str(uuid.uuid4())
    if request.url.path not in HEALTH_PATHS:
        # CLAUDE.md rule 5: only the gateway may call this service.
        if not hmac.compare_digest(request.headers.get(INTERNAL_TOKEN_HEADER, ""),
                                   settings.internal_service_token):
            return error(401, "UNAUTHORIZED", "This endpoint is not publicly accessible.")
        if request.headers.get("X-User-Role") not in ANALYTICS_ROLES:
            return error(403, "FORBIDDEN", "Analytics is for admins and analysts.")
    try:
        response = await call_next(request)
    except Exception:
        logger.exception("unhandled error", extra={"fields": {"request_id": request_id}})
        response = error(500, "INTERNAL", "Something went wrong.")
    response.headers["X-Request-Id"] = request_id
    logger.info("request", extra={"fields": {
        "request_id": request_id, "method": request.method,
        "path": request.url.path, "status": response.status_code}})
    return response


@app.get("/healthz")
async def healthz():
    return {"status": "ok", "service": SERVICE_NAME}


@app.get("/readyz")
async def readyz():
    try:
        async with pool.connection(timeout=2) as conn:
            await conn.execute("SELECT 1")
    except Exception:
        logger.warning("readiness check failed", exc_info=True)
        return JSONResponse(status_code=503, content={
            "status": "unavailable", "service": SERVICE_NAME, "checks": {"postgres": "failed"}})
    return {"status": "ok", "service": SERVICE_NAME, "checks": {"postgres": "ok"}}


# The sample report: headline figures over everything in the analytics schema.
# "Paid" is the same set of statuses the admin dashboard counts as revenue.
SUMMARY_SQL = """
SELECT
    (SELECT count(*) FROM orders)                                    AS orders,
    (SELECT count(*) FROM orders
      WHERE status IN ('paid', 'processed', 'dispatched'))           AS paid_orders,
    (SELECT coalesce(sum(total_paise), 0) FROM orders
      WHERE status IN ('paid', 'processed', 'dispatched'))           AS gmv_paise,
    (SELECT count(*) FROM products WHERE status = 'active')          AS active_products,
    (SELECT greatest(
        (SELECT max(last_event_at) FROM orders),
        (SELECT max(last_event_at) FROM products)))                  AS last_event_at
"""


@app.get("/analytics/summary")
async def summary():
    async with pool.connection() as conn:
        row = await (await conn.execute(SUMMARY_SQL)).fetchone()
    # Money stays integer paise; the UI formats it (CLAUDE.md rule 1).
    return {
        "orders": row["orders"],
        "paid_orders": row["paid_orders"],
        "gmv_paise": int(row["gmv_paise"]),
        "active_products": row["active_products"],
        # How fresh the pipeline is: the newest event analytics has seen.
        "last_event_at": row["last_event_at"],
    }


if __name__ == "__main__":
    import uvicorn

    # Reload in development only, the way air runs the Go services.
    uvicorn.run("main:app", host="0.0.0.0", port=settings.analytics_api_port,
                app_dir="src", reload=settings.app_env == "development",
                log_config=None)

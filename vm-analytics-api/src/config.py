"""Configuration from the environment.

CLAUDE.md rule 3: everything configurable comes from env vars, and the service
fails fast with a clear message when a required one is missing — no silent
defaults for secrets or URLs. In development the repository root's .env is
read too; real environment variables always win over it, exactly as in the Go
services' config.LoadRootDotEnv.
"""

from __future__ import annotations

import sys
from functools import lru_cache
from pathlib import Path
from typing import Literal

from pydantic import Field, ValidationError
from pydantic_settings import BaseSettings, SettingsConfigDict

SERVICE_NAME = "vm-analytics-api"

# src/config.py -> repository root. Absent inside a container,
# where the platform sets real environment variables.
_ROOT_ENV = Path(__file__).resolve().parents[2] / ".env"


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=_ROOT_ENV if _ROOT_ENV.is_file() else None,
        env_file_encoding="utf-8",
        extra="ignore",
    )

    app_env: Literal["development", "staging", "production"] = "development"
    log_level: Literal["debug", "info", "warn", "error"] = "info"
    # This service's slot in the platform port map.
    analytics_api_port: int = Field(default=8085, gt=0, lt=65536)

    # The analytics READER role: SELECT on schema `analytics`, nothing else,
    # read-only by default (infra/postgres/init/03-analytics.sh).
    analytics_api_database_url: str = Field(min_length=1)
    internal_service_token: str = Field(min_length=1)

    db_max_conns: int = Field(default=10, gt=0)
    db_statement_timeout_ms: int = Field(default=15000, gt=0)


@lru_cache(maxsize=1)
def load_settings() -> Settings:
    """Settings, or exit with every problem listed at once."""
    try:
        return Settings()  # type: ignore[call-arg]
    except ValidationError as exc:
        problems = "; ".join(
            f"{'.'.join(str(p) for p in err['loc']).upper()}: {err['msg']}"
            for err in exc.errors()
        )
        print(f"{SERVICE_NAME}: invalid configuration — {problems}. "
              "Copy .env.example to .env and set it.", file=sys.stderr)
        raise SystemExit(1) from None

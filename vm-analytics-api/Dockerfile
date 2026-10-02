# vm-analytics-api — the one Python service. Built from its own directory:
#   docker build vm-analytics-api
FROM python:3.12-slim

COPY --from=ghcr.io/astral-sh/uv:0.12 /uv /usr/local/bin/uv

WORKDIR /app
# Locked dependencies first, so a code-only change reuses this layer.
COPY pyproject.toml uv.lock .python-version ./
RUN uv sync --frozen --no-dev --no-install-project
COPY src ./src

# Non-root. A container escape should not start from uid 0.
RUN useradd --system --no-create-home vayal
USER vayal

ENV PATH="/app/.venv/bin:$PATH" PYTHONUNBUFFERED=1
EXPOSE 8085

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request,os; urllib.request.urlopen(f'http://127.0.0.1:{os.environ.get(\"ANALYTICS_API_PORT\",\"8085\")}/healthz')"

CMD ["python", "src/main.py"]

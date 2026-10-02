"""The sample endpoint against the real analytics schema, as the reader role."""

import psycopg
import pytest
from fastapi.testclient import TestClient

from config import load_settings
from main import app

settings = load_settings()
TOKEN = {"X-Internal-Token": settings.internal_service_token}


@pytest.fixture(scope="module")
def client():
    try:
        psycopg.connect(settings.analytics_api_database_url, connect_timeout=2).close()
    except psycopg.OperationalError as exc:
        pytest.skip(f"analytics database unreachable: {exc}")
    with TestClient(app) as c:
        yield c


def test_summary_for_admin_and_analyst(client):
    for role in ("admin", "analyst"):
        res = client.get("/analytics/summary", headers={**TOKEN, "X-User-Role": role})
        assert res.status_code == 200, res.text
        body = res.json()
        assert body["orders"] >= body["paid_orders"] >= 0
        assert isinstance(body["gmv_paise"], int)


def test_refuses_other_roles_and_missing_token(client):
    assert client.get("/analytics/summary",
                      headers={**TOKEN, "X-User-Role": "customer"}).status_code == 403
    res = client.get("/analytics/summary", headers={"X-User-Role": "admin"})
    assert res.status_code == 401
    assert res.json()["error"]["code"] == "UNAUTHORIZED"


def test_reader_role_cannot_see_or_write_anything_else():
    with psycopg.connect(settings.analytics_api_database_url) as conn:
        for sql in ("SELECT 1 FROM orders.orders LIMIT 1",
                    "SELECT 1 FROM profile.users LIMIT 1",
                    "DELETE FROM analytics.orders WHERE false"):
            with pytest.raises(psycopg.Error):
                conn.execute(sql)
            conn.rollback()

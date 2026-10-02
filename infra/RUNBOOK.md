# Runbook

For when it is already running and something is wrong. Written to be read
under pressure: symptom first, then what to check, then what to do.

For first-time setup see [DEPLOY.md](./DEPLOY.md).

Throughout, `dc` means:

```bash
alias dc='docker compose -f infra/docker-compose.prod.yml --env-file .env.production'
```

---

## First moves, always

```bash
dc ps                                    # which containers are unhealthy
./infra/scripts/smoke.sh https://api.<domain>   # 11 checks, read-only, safe
dc logs --tail=200 gateway               # the gateway sees every request
```

Every log line is JSON with a `request_id`, propagated across service hops. If
a customer gives you one, that is the fastest way in:

```bash
dc logs --since=1h | grep '<request-id>'
```

---

## Symptom: the whole site is down

**Check in this order.**

1. `dc ps` — is anything not `healthy`?
2. `curl -sS https://api.<domain>/readyz` — this checks each Go service *and*
   its database pool, and names what is broken in the body.
3. `dc logs --tail=100 postgres` — most total outages are the database.

If Postgres is down and will not start, jump to [Restoring from
backup](#restoring-from-backup). **Do not delete the volume to make it start.**

---

## Symptom: every request from the UIs fails, but curl works

Almost always `PUBLIC_API_BASE_URL` missing its `/api` suffix.

The gateway serves the JSON API under `/api` and health probes at the root. A
UI built with `https://api.example.com` calls `/auth/login` and gets
`{"error":{"code":"NOT_FOUND","message":"No route matches this path."}}`, while
`curl https://api.example.com/api/auth/login` succeeds — because you typed the
full path yourself.

Fix, then **rebuild the UI images**. `VITE_*` values are baked in at build time;
restarting changes nothing.

```bash
dc build client-ui admin-ui supplier-ui
dc up -d client-ui admin-ui supplier-ui
```

---

## Symptom: customers see "Too many requests"

`RATE_LIMIT_MAX` is per IP, per `RATE_LIMIT_WINDOW_MS`, shared by everyone
behind one address — an office, a mobile carrier's NAT.

```bash
dc logs --since=30m gateway | grep -c TOO_MANY_REQUESTS
```

If legitimate users are hitting it, raise `RATE_LIMIT_MAX` and restart the
gateway. If one IP is responsible, block it at the TLS terminator instead —
raising the limit for everyone to accommodate one abuser is the wrong trade.

---

## Symptom: an order was paid but the supplier has no payout

This should be impossible: payouts are created in the same transaction that
marks an order paid. Verify before assuming:

```sql
SELECT o.order_number, o.status, o.total_paise,
       p.supplier_id, p.amount_paise, p.status AS payout_status
FROM orders.orders o
LEFT JOIN orders.supplier_payouts p ON p.order_id = o.id
WHERE o.order_number = 'VM-260815-0042';
```

- **Order `paid`, payout rows present** — working; the supplier is looking at
  the wrong screen or a different account.
- **Order `paid`, no payout rows** — a real bug. Capture the order id and the
  logs, and settle that supplier by hand from `order_items`:

```sql
SELECT supplier_id, sum(line_total_paise) FROM orders.order_items
WHERE order_id = '<id>' GROUP BY supplier_id;
```

---

## Symptom: stock is wrong — sold out when it should not be

Stock lives in **two** places by design (`catalog.daily_availability` holds the
grams, `orders.stock_reservations` mirrors the holds), because no transaction
may span two schemas. They are reconciled by a sweeper.

Stock is held per GRADE (`size_code_id`), not per product. The catalogue has
no expiry pass of its own: only the orders sweeper releases holds, and only by
telling the catalogue over HTTP. A hold that is still `held` after it lapsed
got stuck in one of four ways, and each needs a different fix. Find out which:

```sql
SELECT h.id AS hold_id, o.order_number, o.status AS order_status,
       r.status AS orders_side, h.grams, h.available_on, h.expires_at,
       EXISTS (
         SELECT 1 FROM orders.webhook_events w
         WHERE w.event_type = 'payment.captured'
           AND w.payload -> 'payload' -> 'payment' -> 'entity' ->> 'order_id' = o.razorpay_order_id
       ) AS payment_captured
FROM catalog.stock_holds h
LEFT JOIN orders.orders o ON o.id = h.order_ref
LEFT JOIN orders.stock_reservations r ON r.catalog_reservation_id = h.id
WHERE h.status = 'held' AND h.expires_at < now() - interval '2 minutes'
ORDER BY h.expires_at;
```

| What the row shows | What happened | Fix |
|---|---|---|
| `payment_captured` true, order `pending_payment` | **Deliberate.** Razorpay took the money but processing the capture failed, so the sweeper is keeping the customer's produce (CLAUDE.md §6.3 step 4) | **Do not release.** Settle the ORDER: "record payment" in admin, or refund. Either settles the hold |
| `orders_side` = `held` | The sweeper has not reached it — not running, or failing | Fix the sweeper: `dc logs orders-api \| grep sweep`. Do not touch the hold; the sweeper settles it once it runs |
| `orders_side` = `released` | The sweeper released it, then could not reach the catalogue. Nothing retries that | **Release** it by hand (below) |
| `orders_side` = `committed` | The order was PAID, then the catalogue could not be told. Nothing retries that either | **Commit** it by hand (below). Releasing it would put sold produce back on sale |

Both fixes take the grade, date and grams from the hold row itself, so there is
nothing to type except the hold id. `AND status = 'held'` makes a repeat a
no-op: `UPDATE 0` on the second statement means nothing was changed.

Release (grams go back on sale):

```sql
BEGIN;
WITH h AS (
  UPDATE catalog.stock_holds SET status = 'released', settled_at = now()
   WHERE id = '<hold-id>' AND status = 'held'
  RETURNING size_code_id, available_on, grams
)
UPDATE catalog.daily_availability a
   SET reserved_grams = GREATEST(0, a.reserved_grams - h.grams)
  FROM h
 WHERE a.size_code_id = h.size_code_id AND a.available_on = h.available_on;
COMMIT;
```

Commit (grams move from reserved to sold):

```sql
BEGIN;
WITH h AS (
  UPDATE catalog.stock_holds SET status = 'committed', settled_at = now()
   WHERE id = '<hold-id>' AND status = 'held'
  RETURNING size_code_id, available_on, grams
)
UPDATE catalog.daily_availability a
   SET reserved_grams = GREATEST(0, a.reserved_grams - h.grams),
       sold_grams     = a.sold_grams + h.grams
  FROM h
 WHERE a.size_code_id = h.size_code_id AND a.available_on = h.available_on;
COMMIT;
```

Expect `UPDATE 1`. Re-run the diagnostic afterwards and re-read that grade's
`daily_availability` row. Getting this wrong oversells a grower's produce.

---

## Symptom: Razorpay webhooks are failing signature verification

The signature is HMAC-SHA256 over the **raw, unparsed body**. Any proxy that
rewrites, re-encodes, buffers-and-reserialises or pretty-prints JSON breaks it,
and the failure looks exactly like a wrong secret.

1. Confirm `RAZORPAY_WEBHOOK_SECRET` matches the dashboard (it is the webhook's
   own secret, not the API key secret — a common mix-up).
2. Confirm the TLS terminator passes the body through unchanged.
3. Check for delivery attempts that never arrived:

```sql
SELECT provider_event_id, event_type, received_at, processed_at, error
FROM orders.webhook_events ORDER BY received_at DESC LIMIT 20;
```

Razorpay retries failed deliveries. Once the secret is right, re-drive them
from the dashboard rather than editing rows by hand — the unique constraint on
`provider_event_id` makes replay safe.

---

## Symptom: everything is slow

```bash
curl -sS http://127.0.0.1:8081/metrics | grep duration_ms_max
```

Each Go service exposes `/metrics` on the internal network: request counts by
route and status, in-flight requests, and mean/max latency per route. Compare
routes — one slow route is a query; everything slow is the database or the
host.

Then look at the database:

```sql
SELECT pid, state, wait_event_type, wait_event,
       now() - query_start AS running_for, left(query, 120)
FROM pg_stat_activity
WHERE state <> 'idle' AND backend_type = 'client backend'
ORDER BY query_start;
```

Queries are capped by `DB_STATEMENT_TIMEOUT_MS` (15s), lock waits by
`DB_LOCK_TIMEOUT_MS` (5s), and abandoned open transactions by
`DB_IDLE_TX_TIMEOUT_MS` (30s), so a single runaway query cannot hold a pooled
connection forever. If you see something exceed those, the settings are not
being applied — check the pool logs at startup.

To kill one query (not the connection):

```sql
SELECT pg_cancel_backend(<pid>);
```

---

## Backups

**Nothing in this repo takes backups automatically. Set this up before launch.**

### Taking one

```bash
dc exec -T postgres pg_dump -U "$POSTGRES_USER" -d vayal --format=custom \
    > "vayal-$(date +%Y%m%d-%H%M%S).dump"
```

`--format=custom` is compressed and allows selective restore. Store it **off
the host** — a backup on the same disk does not survive the failure you are
most likely to have.

A cron entry, keeping 14 days:

```cron
0 2 * * * cd /srv/vayal && docker compose -f infra/docker-compose.prod.yml \
  --env-file .env.production exec -T postgres \
  pg_dump -U vayal_admin -d vayal --format=custom \
  > /backups/vayal-$(date +\%Y\%m\%d).dump 2>>/var/log/vayal-backup.log \
  && find /backups -name 'vayal-*.dump' -mtime +14 -delete
```

MinIO holds product images in the `minio-data` volume. Back it up too, or
accept that images are re-uploadable and the database is not:

```bash
docker run --rm -v vayal-prod_minio-data:/data -v "$PWD":/backup alpine \
    tar czf /backup/minio-$(date +%Y%m%d).tar.gz -C /data .
```

### Restoring from backup

**A backup you have never restored is a hope, not a backup.** Practise this on
a scratch host before you need it.

```bash
# 1. Stop everything that writes.
dc stop gateway profile-api catalog-api orders-api

# 2. Restore into a CLEAN database. --clean drops objects first; without it you
#    get confusing constraint errors on top of existing rows.
dc exec -T postgres pg_restore -U "$POSTGRES_USER" -d vayal \
    --clean --if-exists < vayal-20260815-020000.dump

# 3. Bring the services back. They re-run migrations at startup, which is
#    harmless if the dump is already current.
dc up -d
./infra/scripts/smoke.sh https://api.<domain>
```

Then check the most recent order in the admin UI and confirm it matches what
you expect from the backup's timestamp. **Anything placed after the dump is
gone** — reconcile against Razorpay's dashboard (or your payment records) for
the gap, because money may have moved for orders the database no longer knows
about.

---

## Rotating a leaked secret

| Leaked | Do this | Effect |
|---|---|---|
| `JWT_SECRET` | Change it, restart `gateway` and `profile-api` | Everyone is signed out. Correct and cheap. |
| `INTERNAL_SERVICE_TOKEN` | Change it, restart **all four** services together | Brief 401s between services during the rolling restart |
| `RAZORPAY_KEY_SECRET` | Regenerate in the Razorpay dashboard first, then update and restart `orders-api` | Payments fail in between — do it in a quiet window |
| Database password | Change with `ALTER ROLE`, update the `*_DATABASE_URL`, restart that service | That service is down until restarted |

Rotate one at a time and run the smoke test after each. Changing several at
once makes the failure ambiguous.

---

## Index and query review

Reviewed against the live schema. Every foreign key that is *queried* has a
supporting index. Four foreign keys deliberately do not:

| Column | Why no index |
|---|---|
| `profile.suppliers.approved_by` | Only read when displaying one supplier, already fetched by primary key |
| `profile.refresh_tokens.replaced_by` | Walked only during reuse detection, on a handful of rows |
| `catalog.stock_holds.product_id` | Queried by `order_ref` and `expires_at`, both indexed — never by product alone. `stock_holds` is the hottest write path in checkout, and an unused index there costs every insert. |
| `orders.order_idempotency.order_id` | Looked up by key, which is the primary key |

Re-run the check after adding queries:

```sql
SELECT c.conrelid::regclass AS tbl, a.attname AS col
FROM pg_constraint c
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY(c.conkey)
WHERE c.contype = 'f'
  AND NOT EXISTS (SELECT 1 FROM pg_index i
                  WHERE i.indrelid = c.conrelid AND a.attnum = i.indkey[0]);
```

Note that on small tables Postgres will correctly choose a sequential scan even
where an index exists. That is not a missing index; check the plan again once
the table is large.

---

## Escalation

Before asking for help, collect:

- output of `dc ps`
- output of `./infra/scripts/smoke.sh <url>`
- `dc logs --tail=200` for the failing service
- the `request_id` of one failing request
- what changed most recently (a deploy, a config edit, a migration)

Support address on every page and in every email: `vayal.mikrogreenz@gmail.com`.

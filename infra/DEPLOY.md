# Deploying Vayalavan

How to get this from a git checkout to a running production system. Written for
someone doing it for the first time, at 6am, without the person who built it.

For what to do when it is already running and something is wrong, see
[RUNBOOK.md](./RUNBOOK.md).

---

## What you are deploying

Seven containers on two networks:

```
                    internet
                       │
                  TLS terminator          (Caddy / nginx / cloud LB)
                       │
     ┌─────────────────┼──────────────────┬──────────────┐
     │                 │                  │              │
  client-ui         admin-ui         supplier-ui      gateway     ← edge
   (nginx)           (nginx)           (nginx)        (node)
                                                          │
     ┌────────────────────────────────────────────────────┤
     │                    │                    │          │      ← internal
 profile-api         catalog-api          orders-api      │        (no route
    (go)                 (go)                 (go)        │         to the
     └────────────────────┴────────────────────┴──────────┘         internet)
                          │
                    postgres + minio
```

**Only the gateway serves the API.** The three Go services sit on a Docker
network declared `internal: true`, so they have no route to or from the
internet — CLAUDE.md rule 5 is enforced by the network, not by convention. They
additionally require an `INTERNAL_SERVICE_TOKEN` header, so a mistake in the
network config still does not expose them.

---

## Before you start

You need:

- A host with Docker and Docker Compose v2 (2 vCPU / 4 GB is ample — see the
  capacity note at the end).
- Three DNS names pointing at it, e.g. `vayalmikrogreenz.com`,
  `admin.…`, `supplier.…`, plus `api.…`.
- A TLS certificate for each. Nothing in this repo terminates TLS.

You do **not** need Razorpay yet. Checkout is not switched on; orders rest in
`pending_payment` and an admin records payments by hand. See
[Enabling Razorpay](#enabling-razorpay-later) when you are ready.

---

## 1. Configure

```bash
cp .env.production.example .env.production
```

Then edit it. Every `CHANGE_ME` must be replaced. Generate each secret
separately — never reuse one value across two variables:

```bash
openssl rand -base64 48
```

The values that will bite you if you get them wrong:

| Variable | Why it matters |
|---|---|
| `PUBLIC_API_BASE_URL` | **Must end in `/api`.** The gateway serves the API under `/api` and health probes at the root. Omit it and every request from all three UIs 404s while `curl` against the same host looks fine. This exact mistake has already happened once. |
| `CORS_ALLOWED_ORIGINS` | Exact origins, comma-separated. Never `*` — these requests carry credentials. |
| `INTERNAL_SERVICE_TOKEN` | The proof a caller is inside the network. If this leaks, anyone who can reach a Go service can act as any user. |
| `JWT_SECRET` | Signs access tokens. Rotating it signs everyone out, which is the correct response to a suspected leak. |
| `*_DB_USER` / `*_DB_PASSWORD` | One pair per database role: `PROFILE_`, `CATALOG_`, `ORDERS_`, `ANALYTICS_`, `ANALYTICS_READER_`, `EVENTS_CDC_`. Postgres creates each role from them on first boot, **and** the compose file builds each service's connection string from the same pair, so a password is written once and cannot drift. Use `openssl rand -hex 32` for these: they go inside a URL, where base64's `+ / =` would need escaping. |
| `*_DATABASE_URL` | Leave unset on the bundled Postgres; they are built for you. Set them only for a managed database (§3). Keep `search_path=<schema>,public` — dropping `public` breaks `citext` and `pgcrypto`, and migrations fail with a confusing "type does not exist". |

`.env.production` is gitignored. Keep the real one in a password manager or a
secrets store, not in the repo and not in a chat message.

---

## 2. Build the images

```bash
cd infra
docker compose -f docker-compose.prod.yml --env-file ../.env.production build
```

This takes a few minutes the first time.

**The UI images bake `VITE_API_BASE_URL` into the JavaScript at build time.**
Vite substitutes it into the bundle; it is not read at runtime. Pointing a UI
at a different API means *rebuilding* that image, not restarting it. This also
means no secret may ever be a `VITE_` variable — they ship to every browser in
plain text.

---

## 3. Start the database and run migrations

Bring up Postgres alone first, so migrations run against a database that is
definitely ready:

```bash
docker compose -f docker-compose.prod.yml --env-file ../.env.production up -d postgres
docker compose -f docker-compose.prod.yml --env-file ../.env.production logs -f postgres
```

Wait for `database system is ready to accept connections`.

On first start, `postgres/init/` runs automatically, in order:

```
[init] done: schemas profile, catalog, orders created with owner roles
[verify] ok: each service role can reach only its own schema
[analytics] done: schema analytics, roles vm_analytics, vm_analytics_reader, vm_events_cdc
```

`02-verify-isolation.sh` fails startup if any role can reach another role's
schema. If Postgres refuses to start with a message about schema access, that
script is doing its job; do not bypass it. The init scripts run only on an
EMPTY data volume: a volume from a failed first boot must be removed
(`down -v`) before trying again.

Then apply migrations. Services do **not** migrate at boot — each one does it
on request, as its own role, in dependency order:

```bash
C="docker compose -f docker-compose.prod.yml --env-file ../.env.production"
for svc in profile-api catalog-api orders-api events-consumer; do
    $C run --rm "$svc" -migrate up || break
done
```

Then create the analytics publication and replication slot (once;
idempotent). They need the outbox tables the migrations just made, and the
slot must exist BEFORE the services start, or the first events are lost:

```bash
$C exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -v cdc="$EVENTS_CDC_DB_USER" -v publication=vayal_analytics -v slot=vayal_analytics' \
    < postgres/cdc-setup.sql
```

Now start everything:

```bash
$C up -d
```

**Managed database (RDS, Cloud SQL, Neon):** delete the `postgres` service,
run the SQL from `postgres/init/*.sh` against it once as an admin role
(logical replication must be enabled on the instance), and set the full
`*_DATABASE_URL` / `EVENTS_CONSUMER_REPLICATION_URL` variables — each replaces
the URL the compose file would otherwise build, and its `*_DB_*` parts are
then not needed.

Verify each service reached `http://<container>:<port>/readyz` — the next step
checks this for you.

### Seeding an admin

Admins are never self-signup (CLAUDE.md §5.1). Create the first one directly:

```bash
docker compose -f docker-compose.prod.yml --env-file ../.env.production \
    exec -T postgres psql -U "$POSTGRES_USER" -d vayal \
    -v admin_email="'you@example.com'" \
    -v admin_phone="'9800000000'" \
    -v admin_password_hash="'<bcrypt hash>'" \
    < seed/02_identity.sql
```

Generate the hash with cost 12 — do not paste a plaintext password into SQL.
Every subsequent admin and supplier is created through the admin UI.

### Analytics (CDC) and analyst accounts

The publication and the replication slot are created in §3 above. To fill the
analytics schema with history, run the three `-emit-snapshots` backfills (profile, catalog, orders)
(CLAUDE.md §5.4). An analyst is inserted like the admin above, with
`role = 'analyst'`: they can sign in to the admin console and see only
Analytics.

---

## 4. Start everything

```bash
docker compose -f docker-compose.prod.yml --env-file ../.env.production up -d
docker compose -f docker-compose.prod.yml --env-file ../.env.production ps
```

Every service should show `healthy`. The Go services and the gateway each have
a `HEALTHCHECK`, so `ps` tells the truth rather than just "running".

---

## 5. Put TLS in front

The gateway and the four UIs bind to **loopback only** (`127.0.0.1:8080`,
`:8090`, `:8091`, `:8092`, `:8093`). They are not reachable from outside the host until
you proxy to them. A minimal Caddyfile:

```caddy
vayalmikrogreenz.com          { reverse_proxy 127.0.0.1:8090 }
admin.vayalmikrogreenz.com    { reverse_proxy 127.0.0.1:8091 }
supplier.vayalmikrogreenz.com { reverse_proxy 127.0.0.1:8092 }
analytics.vayalmikrogreenz.com { reverse_proxy 127.0.0.1:8093 }

api.vayalmikrogreenz.com {
    reverse_proxy 127.0.0.1:8080

    # The Razorpay webhook signature is computed over the RAW body
    # (CLAUDE.md §6.4). Any proxy that rewrites, re-encodes or buffers-and-
    # reserialises the body will break verification in a way that looks like
    # a wrong secret. Caddy passes bytes through unchanged; if you use
    # something else, verify this before enabling payments.
}
```

Caddy obtains certificates automatically. With nginx, add HSTS yourself — the
smoke test checks for it.

Consider restricting `admin.` to your office IP range or a VPN. It is the only
surface where bank details are visible unmasked. `analytics.` serves only
code — the Analytics section's JavaScript, which admin-ui loads at runtime and
which fetches data with the admin's own token — so it may sit behind the same
restriction or not, but every admin's browser must be able to reach it. Its
hostname must match `ANALYTICS_UI_PUBLIC_URL`.

---

## 6. Verify

```bash
./infra/scripts/smoke.sh https://api.vayalmikrogreenz.com
```

Eleven checks: health, readiness, the `/api` mount, the IST cutoff, that
unauthenticated requests are refused, that internal routes are not exposed, and
the security headers. **Exit code 0 means safe to route traffic.**

It is read-only and safe to run against production at any time.

Then, by hand:

1. Open the customer site. Today's produce should list, or say nothing is
   available — both are correct answers depending on whether suppliers have
   declared stock.
2. Sign in to the admin UI. The dashboard should load.
3. Sign in as a supplier. Declare 1 kg of something.
4. Reload the customer site. It should appear.

Do **not** run `make test-e2e` against production. It places real orders.

---

## Deploying an update

```bash
git pull
cd infra
docker compose -f docker-compose.prod.yml --env-file ../.env.production build
docker compose -f docker-compose.prod.yml --env-file ../.env.production up -d
./scripts/smoke.sh https://api.vayalmikrogreenz.com
```

Compose replaces containers one service at a time. There is a brief gap per
service; with a single host there is no zero-downtime path without a second
replica behind the proxy.

**Migrations run automatically at service startup and are forward-only in
practice.** Before deploying a migration that drops or renames a column, take a
backup (see RUNBOOK) — goose can roll back, but a rollback cannot recreate data
the migration deleted.

---

## Enabling Razorpay later

Checkout is deliberately off. To switch it on:

1. Set `RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET` (live keys) in
   `.env.production`.
2. In the Razorpay dashboard, add a webhook pointing at
   `https://api.<your-domain>/webhooks/razorpay`, subscribed to
   `payment.captured`, `payment.failed` and `refund.processed`.
3. Put the webhook's signing secret in `RAZORPAY_WEBHOOK_SECRET`.
4. Rebuild the **client UI** — `VITE_RAZORPAY_KEY_ID` is baked in at build time.
5. Restart `orders-api` and `gateway`.

The webhook is the source of truth, not the browser callback. Test with a real
₹1 payment before announcing it, and confirm in the admin UI that the order
moved to `paid` and a supplier payout row appeared.

Razorpay also requires your Terms, Privacy, Shipping and Refund pages to be
publicly reachable before activating a live account. They are already routed
and linked in every footer, **but they contain `[TO BE COMPLETED BEFORE
LAUNCH]` markers** — a lawyer should review them first.

---

## Capacity

Measured on a development laptop running Postgres, all four services and three
UIs simultaneously, against `/api/catalog` (the uncached endpoint every visitor
hits):

```
20 concurrent readers, 10s
  3,721 req/s sustained
  p50 5ms · p95 7ms · p99 9ms · max 50ms
  0 failures
```

Reproduce with `node infra/scripts/loadcheck.mjs 20 10` (temporarily raise
`RATE_LIMIT_MAX`, or the limiter caps the measurement rather than the service).

This is not a capacity plan — real data volumes and a dedicated database will
change it. It does establish that a modest single host is not the constraint at
launch scale, and that a 2 vCPU box is a reasonable starting point.

`RATE_LIMIT_MAX` is **per IP**, shared by everyone behind one address — a
household, an office, a mobile carrier's NAT. 600/minute is the current
default. Watch for 429s in the logs after launch and raise it if legitimate
users are hitting it.

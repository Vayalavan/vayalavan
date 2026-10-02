# Vayalavan

B2C marketplace for fresh fruits, vegetables and microgreens. Suppliers declare
what they have each morning, customers order against that day's availability,
and we settle suppliers manually by NEFT.

*Vayal* (வயல்) is Tamil for a farming field.

> **Read [CLAUDE.md](./CLAUDE.md) before writing code.** It is the source of
> truth for architecture, domain rules and conventions. This README covers how
> to run things; CLAUDE.md covers what to build and why.

**Status: foundation.** Infrastructure, shared packages, service skeletons and
the local development environment are in place and verified. There are no
domain endpoints or tables yet — see [What exists today](#what-exists-today).

---

## First five minutes

All the commands a person runs live in `scripts/`. One set for macOS, and an
`-ubuntu` set for Ubuntu:

| | macOS | Ubuntu |
|---|---|---|
| Set up, once (and after pulling) | `./scripts/dev-setup.sh` | `./scripts/dev-setup-ubuntu.sh` |
| Run everything | `./scripts/start.sh` | `./scripts/start-ubuntu.sh` |
| Run a mobile app's dev server | `./scripts/start-mobile.sh [customer]` | `./scripts/start-mobile-ubuntu.sh [customer]` |

```bash
git clone <repo> && cd vayal-mikrogreenz
./scripts/dev-setup.sh        # once: prerequisites, .env, every app's deps,
                              # Docker infra, migrations, seed, analytics backfill
./scripts/start.sh            # every time: runs all 10 processes, prints the URLs
```

**Prerequisites:** Go 1.25+, Node 20.12+, Docker (running) and uv. Each setup
script lists them at the top, with the exact install commands for that OS, and
stops with the fix if one is missing.

**Ubuntu differences:**
- **Install commands:** Go comes from snap and Node from NodeSource; Ubuntu's own
  packages of both are too old.
- **Docker without sudo:** add yourself to the `docker` group, then log out and
  back in.
- **File-watch limit:** raise it, or ten dev servers crash with ENOSPC.
- **How it works:** the Ubuntu scripts check these things, then run the same
  shared scripts with an `ss`-based `lsof` (`infra/scripts/ubuntu/`) and the
  detected LAN address. A fix to the shared scripts applies to both systems.

**`.env` comes from `test.env`,** the team's committed development settings:
- **No secrets:** mail goes to MailHog and the Razorpay keys are placeholders.
  Real keys belong only in your own `.env`, which stays gitignored.
- **Copied once:** setup never modifies `test.env` and never overwrites an
  existing `.env`.
- **Network address swapped:** while copying, setup replaces the LAN address
  `test.env` was made on (used by `S3_ENDPOINT` and `MOBILE_API_BASE_URL`) with
  this machine's own, so product images and the mobile apps work here too.
  `IP=192.168.1.40 ./scripts/dev-setup.sh` chooses the address yourself;
  `make -C infra mobile-ip` repoints an existing `.env` after a network change.

Re-running setup is safe: migrations and seed are idempotent. `--with-mobile`
also installs the Expo apps, and `--reset` rebuilds the database from scratch.

The pieces it is made of still work on their own:

```bash
cp .env.example .env          # one env file for the whole platform
make -C infra doctor          # checks tooling, ports and .env — fix anything it flags
make -C infra dev             # infra + migrations + seed + all 10 processes
```

`make dev` is the only command you need. From a clean clone it installs Go and
npm dependencies, installs `air` and `goose` into `.tools/bin`, starts
Postgres/MinIO/MailHog, waits for them to be genuinely healthy, runs each
service's migrations in dependency order, seeds, and then runs all ten
application processes in **one terminal** with prefixed, colour-coded,
interleaved logs:

```
[gateway]     {"level":"info","service":"vm-gateway-api","msg":"http server listening"}
[profile]     building...
[catalog]     {"level":"info","service":"vm-catalog-api","msg":"database pool ready"}
[client-ui]   VITE v6.4.3  ready in 412 ms
```

Ctrl-C stops everything. Everything hot-reloads on save — `air` for the Go
services, `tsx watch` for the gateway, `vite` for the UIs, uvicorn's reloader
for the one Python service (`vm-analytics-api`, run through `uv`).

Then open:

| URL | What |
|---|---|
| <http://localhost:5173> | Customer storefront |
| <http://localhost:5174> | Admin console (admin, or an analyst who sees only Analytics) |
| <http://localhost:5175> | Supplier portal |
| <http://localhost:5176> | `vm-analytics-ui` — the admin Analytics remote, standalone (admins use it at Admin → Analytics) |
| <http://localhost:9001> | MinIO console (`vayal_minio_admin` / `local_dev_minio_pw`) |
| <http://localhost:8025> | MailHog — every outbound email lands here |

The storefront home page calls the gateway's `/healthz` through the shared API
client, so a green **"Connected to vm-gateway-api"** card confirms the whole
frontend chain — env loaded, CORS allowed, gateway reachable — at a glance.

> **Logging in:** `make seed` creates development accounts from the `SEED_*`
> values in `.env`: an admin (`SEED_ADMIN_EMAIL`), an **analyst**
> (`SEED_ANALYST_EMAIL`, who signs in to the admin console and sees only the
> Analytics tab), two approved suppliers and a customer.

Confirm everything is up at any time:

```bash
make -C infra status
```

```
Containers
mailhog     Up 6 minutes
minio       Up 6 minutes (healthy)
postgres    Up 6 minutes (healthy)

Application processes
  SERVICE      PORT   STATE      ENDPOINT
  gateway      8080   healthy    http://localhost:8080/healthz
  profile      8081   healthy    http://localhost:8081/healthz
  catalog      8082   healthy    http://localhost:8082/healthz
  orders       8083   healthy    http://localhost:8083/healthz
  events       8084   healthy    http://localhost:8084/healthz
  analytics    8085   healthy    http://localhost:8085/healthz
  client-ui    5173   up         http://localhost:5173
  admin-ui     5174   up         http://localhost:5174
  analytics-ui 5176   up         http://localhost:5176
  supplier-ui  5175   up         http://localhost:5175
```

### Run only what you're working on

Ten processes to change one file is a bad trade. Pick a slice:

| Command | Processes | Use when |
|---|---|---|
| `make -C infra dev` | 10 | full stack |
| `make -C infra dev-api` | 6 — gateway + 3 Go APIs + analytics pipeline | backend work, no UI |
| `make -C infra dev-client` | 4 — gateway, catalog, orders, client UI | storefront work |

All three start infra, migrate and seed first; they differ only in which
application processes they run. `dev-client` omits `vm-profile-api`, so the
gateway's `/readyz` reports `profile_api: failed` — expected, not a fault.

---

## Port map

| Port | Service | Notes |
|---|---|---|
| **8080** | `vm-gateway-api` | **The only public entry point** |
| 8081 | `vm-profile-api` | internal only |
| 8082 | `vm-catalog-api` | internal only |
| 8083 | `vm-orders-api` | internal only |
| 8084 | `vm-events-consumer-api` | internal only — CDC consumer for analytics |
| 8085 | `vm-analytics-api` | internal only — Python, read-only analytics reports |
| 5173 | `vm-client-ui` | customer storefront |
| 5174 | `vm-admin-ui` | admin console |
| 5175 | `vm-supplier-ui` | supplier portal |
| 5176 | `vm-analytics-ui` | admin Analytics, a Module Federation remote loaded by `vm-admin-ui` |
| 8090 | Expo dev server (Metro) | `vm-supplier-mobile-ui`; started separately with `make -C infra mobile` |
| 8091 | Expo dev server (Metro) | `vm-client-mobile-ui`; started separately with `make -C infra mobile-client` |
| 5432 | PostgreSQL 16 | database `vayal` |
| 9000 | MinIO S3 API | bucket `mikrogreenz` |
| 9001 | MinIO console | web UI |
| 1025 | MailHog SMTP | captures all outbound mail |
| 8025 | MailHog web UI | read captured mail here |

Every port comes from `.env` (`GATEWAY_PORT`, `CLIENT_UI_PORT`, …). Change a UI
port and you must change `CORS_ALLOWED_ORIGINS` to match; `make doctor` checks
the ports you actually configured, not the defaults.

---

## Configuration: one file

There is exactly **one** env file, at the repository root:

```
.env.example    committed, documents every variable
.env            gitignored, yours
```

No per-service `.env`. A value that appears in two places is a value that will
disagree in two places. Everything reads from this one file:

| Consumer | How |
|---|---|
| Go services | walk up from cwd to the repo root, load `.env` |
| Gateway (Node) | `process.loadEnvFile` on the resolved root |
| Vite (3 UIs) | `envDir` pointed at the root; `loadEnv` for the dev-server port |
| docker compose | `--env-file ../.env` |

Variables are prefixed by their owner (`PROFILE_PORT`, `CATALOG_DATABASE_URL`)
because ten processes sharing one file cannot each own a bare `PORT`.
Genuinely shared values (`APP_ENV`, `LOG_LEVEL`, `INTERNAL_SERVICE_TOKEN`) stay
unprefixed.

Services fail at startup, before opening a listener, and report *every* problem
at once:

```
vm-orders-api: invalid configuration:
  - RAZORPAY_KEY_SECRET is required but not set
  - ORDER_CUTOFF_HOUR_IST must be between 0 and 23, got 25
```

Two things to keep straight:

- `INTERNAL_SERVICE_TOKEN` must be identical across the gateway and all three
  Go services, or every proxied call returns 401.
- Anything prefixed `VITE_` is compiled into the browser bundle and is
  therefore **public**. The Razorpay key *id* belongs there; the key *secret*
  never does.

---

## Why one MinIO endpoint

`S3_ENDPOINT=http://localhost:9000` is used for **both** signing URLs
server-side and fetching them from the browser. There is deliberately no
second "public base URL" setting.

An S3 presigned signature covers the `Host` header. If the service signed for
one host and the browser fetched from another, the signature would be invalid —
the classic *"works from the server, 403 from the browser"* MinIO bug. The
usual workaround is to rewrite the host after signing, which does not work,
and then to bolt on a second endpoint variable, which encodes the bug as
configuration.

This setup avoids the problem entirely: because the application services run
**natively on the host** rather than in Docker, `localhost:9000` means the
same thing to `vm-catalog-api` and to the browser. One value, no rewriting.

That is a direct consequence of the compose file running only backing
services. If the Go services are ever containerised, they would resolve
`minio:9000` while the browser still needs `localhost:9000`, and this
constraint has to be solved properly — with a shared hostname resolvable from
both sides, not a rewrite.

### Testing the mobile apps: the endpoint has to move

A handset is a second machine, and `localhost` there is the handset. Leave
`S3_ENDPOINT` on `localhost:9000` while `MOBILE_API_BASE_URL` points at the LAN
and the apps *almost* work, which is what makes it hard to spot: the customer
app lists today's produce but shows the placeholder on every card, and the
supplier app's presigned upload fails, so a product saves with no photo and the
customer app then legitimately has nothing to show.

`make -C infra mobile-ip` moves both variables to this machine's LAN address
together. That keeps the one-endpoint rule intact — the LAN address is
reachable from the laptop's browser as well — and `make -C infra doctor` warns
if the two ever drift apart. The endpoint is read once at startup, so restart
`vm-catalog-api` afterwards; the app URL is baked in at build time, so restart
Expo too.

Prove it works, any time:

```bash
make -C infra verify-storage
```

```
1/5 presigned PUT ok
2/5 uploaded via presigned URL ok            bytes=50
3/5 presigned GET ok
4/5 downloaded via presigned URL, bytes match
5/5 cleaned up test object
storage selftest PASSED — presigned URLs work from the browser
```

Steps 2 and 4 use plain `net/http`, not the AWS SDK, precisely because the SDK
would re-sign the request and hide the failure this test exists to catch.

The bucket is created by `vm-catalog-api` on startup (CLAUDE.md §6.6) — there
is no bootstrap container, so exactly one component owns that decision.

### Size codes, packs and galleries

A product is a **tree**, not a flat list:

```
Tomato
  M2   150 g - 200 g     <- gallery and daily stock live HERE
    1 kg     Rs 1,000
    1.5 kg   Rs 2,500
  XL   260 g +           <- its own gallery, its own crate
    1 kg     Rs 1,400
```

Because a grade is what distinguishes two otherwise identical lines, every
surface that names one names its grade: cart, checkout, order history, the
confirmation email, the admin order view, the supplier's sales list, and the
courier sheet the packer works from ("Pomegranate (L1) 3 Kg Box x1"). The rule
for hiding the two cases that are not a choice — an empty code, and the
implicit `STD` — is written once per language: `produce.GradedName` in
vm-go-common, `gradedName` in @vayal/ui-kit, and a tested copy in each mobile
app.

Both levels carry an optional line of detail. A size code's says what the
GRADE is — "260 g +"; a pack's says what is in the BOX — "6-8 fruit,
ventilated carton". Same field, same 80-character cap, same
"Detail (optional)" input in the supplier form, because a label short enough
to be a chip in a selector cannot also be a description. The product page
prints each under the thing it describes.

A **size code** is a grade the grower sells. Three things hang off it, each for
its own reason:

- **Packs**, because a price belongs to (size, weight) and not to weight alone.
  1 kg of XL is not 1 kg of M, and the schema lets both exist at once.
- **Media**, because the photographs are of that grade. Switching size on the
  shop page swaps the gallery, with no round trip — the whole tree comes down
  in one response.
- **Daily stock**, because the crates are physically separate. Selling out M
  leaves L untouched. One shared pool would let the shop sell an XL box against
  tomatoes that are really M.

Every product has at least one size code. A grower who does not grade simply
has one, and the storefront hides the selector when there is nothing to select
— a second "flat product" code path would double every read, write and screen
for a case the single-grade one already covers.

Galleries hold several images and videos in an order the grower chooses. They
live in `catalog.product_media`, one row per file, keyed by MinIO object key —
never a URL, so changing bucket or host never breaks a listing.

On the product page that gallery is a slide: the pictures sit in one row and
the row moves, so the next photograph arrives from the side it is on rather
than replacing the last one in place. The browser gets large arrows over the
picture — disabled at the ends, because wrapping would have to sweep the whole
strip backwards to land on the first slide — and the app gets the gesture
instead, since arrows over a phone-sized image cover the produce. Both keep the
thumbnail strip, both show "2 / 5", and both stop a video that slides out of
view. A video that the customer SETTLES on — a second on the same slide —
starts playing itself, muted; flicking past one on the way to a photograph
cancels the timer, and a browser asking for reduced motion gets the first frame
and a play button instead.

The app does not pause on the way out, and that is deliberate: `useVideoPlayer`
returns a RELEASING shared object, so it drops the native player whenever the
source changes — which a swipe does. Pausing afterwards reaches for a native
object that is already gone and throws `NotFoundException`, crashing the
screen. Releasing stops playback on its own. The web has no equivalent hazard:
it holds plain `HTMLVideoElement` refs, `pause()` on a detached element is a
no-op, and the ref callbacks drop entries as elements unmount. The animation is dropped for a viewer whose system asks for less motion;
the move still happens.

Some pictures are not of a grade at all: the field being harvested, the packing
shed, the grower turning a crate over. Those go in the PRODUCT's own gallery —
a row with a NULL `size_code_id` — and a customer sees them after the pictures
of whichever grade they are looking at. The grower uploads them once instead of
once per crate, in the supplier form's "Photos and videos for all sizes". The
concatenation happens server-side, so a phone and a browser cannot disagree
about the order, and a grade with no photograph of its own takes its cover from
that bucket rather than showing a placeholder.

Two rules explain most of the behaviour:

- **The cover is the first IMAGE in a grade's gallery.** Not a flag, not the
  first item — ordering alone decides it, because the grower already expresses
  that by dragging. Reorder the gallery and the cover follows. A product's own
  thumbnail is the cover of the first grade that has one.
- **A video is never a cover.** Catalogue cards, order thumbnails, the
  availability sheet and the admin table all need a still they can paint at
  once, and nothing here decodes video. A product carrying only video shows the
  placeholder, exactly as one with no media does — the supplier UIs say so
  rather than letting it happen quietly.

So API responses keep `image_url` (the presigned cover) everywhere a single
thumbnail is drawn, and nest `media[]` under each size code where a gallery is
actually rendered. A catalogue card OPENS on one grade — the first with
something buyable, so a card never leads with a sold-out M while the XL beside
it is on sale — but carries every grade's cover, packs and stock in
`size_codes[]`, so the size chips on the card switch the picture and the prices
with no round trip. What it does not carry is each grade's whole gallery: a
listing must not presign pictures no card draws. The detail page carries those.

| | limit | env var |
|---|---|---|
| One image | 5 MB, JPEG/PNG/WebP | `MAX_IMAGE_BYTES` |
| One video | 50 MB, MP4/MOV/WebM | `MAX_VIDEO_BYTES` |
| Per size code | 8 files | `MAX_MEDIA_PER_SIZE_CODE` |

Nothing transcodes video. The supplier apps downscale photos before uploading
(1600px JPEG, which also strips the EXIF GPS of the farm) and cap recording at
30 seconds to stay inside the video limit. A longer file picked from the
library is refused with its size named, which is the only actionable thing to
say about it.

In a CSV import, rows collapse twice: by `name`+`grade` into a product, then by
`size_code` into a grade. Repeat a row and vary `image_url` / `video_url` to
build that grade's gallery — every distinct URL joins it, deduplicated.
`size_code` is optional and defaults to `STD`, so a file written before grades
existed still imports.

To verify:

```bash
make -C infra test-e2e-catalog
```

which presigns an image and a video, proves the size cap differs per kind,
PUTs real bytes to MinIO and reads them back through the presigned GET the
browser would use.

---

## Architecture

Only the gateway is publicly reachable. The three Go services sit behind it on
the internal network and require a shared token; each owns exactly one Postgres
schema and cannot read the others.

```
                      ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
   browsers           │ vm-client-ui │  │  vm-admin-ui │  │vm-supplier-ui│
                      │  (customer)  │  │    (admin)   │  │  (supplier)  │
                      │    :5173     │  │    :5174     │  │    :5175     │
                      └──────┬───────┘  └──────┬───────┘  └──────┬───────┘
                             │                 │                 │
                      ┌──────────────────────┐ │                 │
   Android / iOS      │vm-supplier-mobile-ui │ │                 │
                      │   (supplier, Expo)   │ │                 │
                      └──────┬───────────────┘ │                 │
                             │                 │                 │
                             └─────────────────┼─────────────────┘
                                               │  HTTPS + JWT
                                               ▼
    ══════════════════════════════ public boundary ══════════════════════════
                                    ┌──────────────────┐
                                    │  vm-gateway-api  │   Node + TypeScript
                                    │       :8080      │   BFF / API gateway
                                    └────────┬─────────┘   rate limit, CORS,
                                             │             auth, request-id
    ─────────────────────────── internal network ────────────────────────────
                     X-Internal-Token on every call
              ┌──────────────────────┼──────────────────────┐
              ▼                      ▼                      ▼
     ┌─────────────────┐   ┌──────────────────┐   ┌─────────────────┐
     │ vm-profile-api  │   │  vm-catalog-api  │   │  vm-orders-api  │   Go
     │      :8081      │   │      :8082       │   │      :8083      │
     ├─────────────────┤   ├──────────────────┤   ├─────────────────┤
     │ identity        │   │ products, units  │   │ cart, orders    │
     │ customers       │   │ images, CSV      │   │ timeline        │
     │ addresses       │   │ daily availab.   │   │ payments        │
     │ suppliers       │   │                  │   │ payouts, outbox │
     └────────┬────────┘   └────────┬─────────┘   └────────┬────────┘
              │                     │                      │
              ▼                     ▼                      ▼
     ┌ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┐
       schema: profile      schema: catalog       schema: orders
     │ role: vm_profile     role: vm_catalog      role: vm_orders  │
       ╰──────────────── PostgreSQL 16 · db "vayal" ─────────────╯
     └ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ┘
                                    │
                     ┌──────────────┴───────────────┐
                     ▼                              ▼
              ┌─────────────┐              ┌────────────────┐
              │    MinIO    │              │    MailHog     │
              │ :9000/:9001 │              │  :1025/:8025   │
              │ product img │              │  local SMTP    │
              └─────────────┘              └────────────────┘

  Docker runs ONLY the bottom row: postgres, minio, mailhog.
  Everything above it runs natively on the host, with hot reload.
```

### Analytics: outbox + CDC

Supplier, product and order changes are also written, in the same
transaction, to insert-only outbox tables. Postgres logical replication streams them to
`vm-events-consumer-api` (Go, :8084). It dedupes on event id
(`processed_events`) and projects them into the `analytics` schema.
`vm-analytics-api` (Python, :8085) reads that schema as a read-only role and
the gateway serves it at `/api/analytics/*` to admins and analysts.

```
profile/catalog/orders ─┬─ same txn ─▶ *_events_outbox ─▶ WAL (publication vayal_analytics)
                        │                                   │ logical replication
                        ▼                                   ▼
                 (business tables)              vm-events-consumer-api
                                                            │ processed_events (dedupe)
                                                            ▼
                    analytics.suppliers · orders · order_items · products · product_packs
                                                            │ SELECT only
                                                            ▼
                                  vm-analytics-api ─▶ gateway /api/analytics ─▶ Analytics tab
```

Full walkthrough, including why `processed_events` exists, which roles can
see what, backfill and operations: **[docs/analytics.md](docs/analytics.md)**.

Schema isolation is enforced by Postgres grants, not convention — each schema
is `AUTHORIZATION`-owned by its role and `PUBLIC` grants are revoked. Every
fresh stack asserts it:

```
[verify] ok: each service role can reach only its own schema
```

### Shared packages

| Package | Consumed by | Provides |
|---|---|---|
| `packages/vm-go-common` | the 3 Go services | config, logging, errors, pgx pool, migrations, `money`, `isttime` |
| `packages/vm-ui-kit` | the 3 UIs | Vayal logo, Tailwind preset, layout shell, typed API client |

Two rules from CLAUDE.md live in code here:

- **`money.Paise` is `int64`.** Never a float, never a decimal string. Rupees
  exist only at the UI edge, via `FormatRupees`.
- **`isttime` owns every date decision.** Timestamps are stored UTC; the 4pm
  cutoff and delivery days are computed in `Asia/Kolkata`.
- **Rarity is a client rule, not a server one.** `product_size_codes.
  harvest_share_pct` stores the grower's estimate of what share of the harvest
  a grade is; `harvestTier` in vm-ui-kit (and its tested copy in
  vm-client-mobile-ui's `lib/harvest.ts`) turns that into gold / bronze /
  silver on the storefront's size selector. Nothing on the server names a tier,
  and nothing that touches money or stock reads the column.

  Verify: `go test ./internal/api/ -run TestHarvestShare` in vm-catalog-api,
  and `npm test` in vm-client-mobile-ui. By hand, give one grade a share of 3
  and another 55 in the supplier product form, then open that product in the
  storefront — the 3% chip is gold and sweeps, the 55% one is silver.

- **`serviceable` owns the delivery area.** We deliver to Bengaluru (560xxx)
  and all of Tamil Nadu (600xxx-643xxx), and nowhere else yet. vm-profile-api
  refuses to save a customer address outside it; vm-orders-api checks again at
  checkout, which catches an address saved before the area was narrowed. The
  two client copies — `@vayal/ui-kit`'s `isServiceablePincode` and the customer
  app's `src/lib/pincode.ts` — only save the customer a round trip, and the
  app's `pincode.test.ts` asserts all three agree.

  Verify: `go test ./serviceable/` in `packages/vm-go-common`, and
  `npm test` in `vm-client-mobile-ui`. By hand, add an address with PIN code
  `110001` (Delhi) in the customer UI or app — the field rejects it before the
  form will submit, and `POST /api/addresses` returns a 422 against `pincode`
  if you skip the UI.

---

## Everyday commands

| Command | Does |
|---|---|
| `make -C infra doctor` | tooling, ports and `.env` completeness — run this first |
| `make -C infra dev` | full stack, 10 processes, one terminal |
| `make -C infra dev-api` | gateway + 3 Go APIs + analytics pipeline |
| `make -C infra dev-client` | gateway, catalog, orders, storefront |
| `make -C infra status` | health of containers and processes |
| `make -C infra down` | stop containers (Ctrl-C stops the app processes) |
| `make -C infra reset` | **destroys volumes**, rebuilds the database — ~10s |
| `make -C infra migrate-up` | analytics roles → migrations (profile → catalog → orders → analytics) → CDC publication |
| `make -C infra cdc-setup` | (re)create the logical-replication publication — idempotent |
| `make -C infra migrate-down` | roll back one migration each, reverse order |
| `make -C infra seed` | load `infra/seed/*.sql` |
| `make -C infra verify-storage` | prove presigned MinIO upload/download works |
| `make -C infra test` | Go + Python + JS tests (includes both mobile apps) |
| `make -C infra test-py` | vm-analytics-api's pytest suite, via uv |
| `make -C infra lint` | gofmt, go vet, typecheck every TS package |
| `make -C infra logs` | tail container logs |
| `make -C infra mobile` | Expo dev server for the supplier app, on :8090 |
| `make -C infra mobile-client` | Expo dev server for the customer app, on :8091 |
| `make -C infra mobile-ip` | point `MOBILE_API_BASE_URL` at this machine (both apps) |
| `make -C infra mobile-android` | native debug build of the supplier app |
| `make -C infra mobile-client-android` | native debug build of the customer app |
| `make -C infra mobile-ios` / `mobile-client-ios` | the same, onto a simulator or device |
| `make -C infra mobile-doctor` | check both mobile projects against their Expo SDK |

The mobile targets are deliberately outside `make dev`. The other ten
processes are servers that either start or fail; a mobile app needs a simulator
or a paired handset, and blocking the whole platform on that would stop anyone
without either from booting it.

The scripts split the same way, and neither touches the other's processes:

```bash
./scripts/start.sh                  # terminal 1 — backend + the web UIs
./scripts/start-mobile.sh           # terminal 2 — the SUPPLIER app's Expo server
./scripts/start-mobile.sh customer  # terminal 3 — the CUSTOMER app's, on its own port
# on Ubuntu: start-ubuntu.sh / start-mobile-ubuntu.sh, same arguments
```

`./scripts/start-mobile.sh` re-points `MOBILE_API_BASE_URL` at this machine's current
LAN address before it starts, which is the thing that goes stale every time the
laptop changes network — on a handset, `localhost` is the handset. Both apps
read that one variable, so fixing it for one fixes it for both; everything else,
including the Metro port, is per app so the two can run side by side.
`./scripts/start-mobile.sh --stop` frees both.

### Running one service by hand

```bash
cd vm-orders-api
go run ./cmd/api                # serve
go run ./cmd/api -migrate up    # migrate, then exit
go run ./cmd/api -migrate status
air -c .air.toml                # with hot reload
```

Each Go service finds the root `.env` by walking up, migrates its own schema
with its own role, and embeds its migrations in the binary — so the migrations
that run always match the code that was deployed.

New migrations are authored with the `goose` CLI (`make tools` installs it):

```bash
cd vm-catalog-api/migrations
../../.tools/bin/goose -s create add_products sql
```

---

## What exists today

Verified working:

- Single root `.env` consumed by every process, Vite and compose
- `make dev` / `dev-api` / `dev-client`, one terminal, prefixed logs
- Hot reload on save: air (Go), tsx watch (gateway), vite (UIs)
- `make doctor` — catches missing tools, taken ports, and `.env` drift
- `make reset` — full database rebuild in ~10s
- Postgres schemas, per-service roles, cross-schema isolation asserted
- Migrations embedded per service, run in dependency order
- MinIO presigned PUT/GET round trip through the browser-facing endpoint
- Size codes — graded produce (M/L/XL), each grade with its own packs, prices,
  gallery and daily stock (see "Size codes, packs and galleries" below)
- `/healthz` and `/readyz` on all four APIs, JSON logs with `request_id`
- CORS allow-list enforced (unlisted origins are not reflected)
- `vm-supplier-mobile-ui` — the supplier portal as an Expo app for Android and
  iOS, at parity with the web version, against the same gateway. Its own
  `node_modules` (React 19.1 vs the web UIs' 18.3), its own test suite, folded
  into `make test` and `make lint`. `make -C infra mobile-doctor` passes 18/18.
  Pinned to Expo SDK 57 — whatever the store build of Expo Go is,
  and therefore the newest that runs on a real iPhone without a paid Apple
  Developer account. See its README, "Why the SDK is pinned".
- `vm-client-mobile-ui` — the storefront as an Expo app, same SDK pin, same
  tooling, same gateway: browse today's produce, cart, checkout, orders and the
  three-milestone timeline. Its own Metro port (8091) so both mobile apps can
  run at once. Browsing works signed out; sign-in is a modal at the point of
  ordering. `expo-doctor` passes 18/18, and `./scripts/start-mobile.sh customer` starts
  it.

### Analytics pipeline and the analyst role

Supplier, product and order events flow through a transactional outbox and
Postgres logical replication into an `analytics` schema, read by a Python API.
See [docs/analytics.md](docs/analytics.md) for the flow and
[docs/analytics-metrics.md](docs/analytics-metrics.md) for which admin and
supplier metrics it supports and where each comes from. The Admin → Analytics tab is a Module
Federation remote (`vm-analytics-ui`). Its first endpoint,
`GET /api/analytics/summary`, is open to admins and to the new **analyst**
role, which can reach nothing else.

### The admin dashboard's breakdowns

`GET /admin/analytics` (admin only, through the gateway) backs everything under
"Breakdowns" on the operations overview: revenue by supplier and by produce,
the day-by-day column chart, basket and customer mix, the split either side of
the 4pm cutoff, and where the parcels went.

It is a SEPARATE request from `/admin/dashboard` on purpose — the headline
tiles refresh every minute and must stay cheap, these are aggregates over every
order line in the window, and a slow one must not take the tiles off the
screen. It takes the same `?range=` vocabulary and resolves it with the same
server-side IST resolver, so the two can never disagree about what "past 7
days" means.

One panel deliberately ignores the picker: "Sales, last 7 days" is always today
and the six days before it, so the week is still readable while someone is
digging through a custom range above it.

To verify: sign in to the admin console, place and pay for an order (or run the
seed), then open the dashboard and switch the period — every breakdown must
move with it, and the seven-day line must not.

### The customer storefront

The shop front is a photograph, the categories are photographs, and the case
for buying here is stated on the page rather than assumed: a hero, a picture
tile per category, and a "Why choose Vayalavan?" band at the foot of
the catalogue. Both customer clients carry the same three, in the same words —
web and app are one shop.

Nothing about the shopping changed with it. The category tiles set the same
single piece of state the pill buttons did, the grade filter is the same
select, and the cart, cutoff and stock rules are untouched; what changed is
that produce is chosen by eye, which is how produce is chosen everywhere else.

The four reasons in that band are each something the platform actually
enforces — the grower declares stock every morning, the farm is named on every
listing, the grade and its weight range are on the pack you pick, and the
cutoff and delivery date are shown before you pay. A reason to buy that the
product does not back up is a slogan, and a customer finds out on the first
order.

The wordmark is **Vayalavan** throughout, and the two apps are "Vayalavan" and
"Vayalavan Supplier". The rename covered every string a customer, grower or
admin can read — UI copy, page titles, manifests, store names, transactional
emails, iOS permission prompts, the docs — and deliberately stopped at the
addresses underneath: Go module paths, npm package names, the `vayal`
database, docker containers, bundle ids and folder names all still say
`vayal-mikrogreenz`. Those are not the name; renaming them would break the
apps' upgrade path and force a database migration to change nothing anyone can
see. The support inbox is unchanged for the same reason — a live address beats
an on-brand one.

Photography lives with the logo in `packages/vm-ui-kit/src/brand/photos/` and
is copied into each client at the size it needs by `make -C infra sync-brand` —
1600px for the web hero, 420px for a phone's category tile. All of it is CC0,
recorded in `CREDITS.md` beside the files: a shop front is the last place to
inherit a licence that obliges a credit line on every screen. Each was opened
and looked at before being committed, which is how one carrying a stock-library
watermark was caught, and how another filed as "heirloom tomatoes" turned out
to be peppers.

To verify: open the customer web app at `/`, and the app's Shop tab.

### The daily sheet, grouped by produce

`GET /supplier/availability` returns one row per SIZE CODE, because a
declaration is per grade — 40 kg of M is a different crate from 15 kg of XL.
Both supplier clients now group those rows into one card per PRODUCT, with a
number box per grade inside it: a grower with three grades of pomegranate was
reading three near-identical cards and had to compare size chips to tell them
apart. What is saved is unchanged — the payload is still per size code, and
still only the boxes actually edited.

A grower who does not grade sees exactly what they saw before: the produce, one
box, Save. The size chip and the per-grade thumbnail appear only when there is
more than one grade, the same rule the storefront's size selector follows.

To verify: sign in to the supplier portal, open Today, and compare a product
with several size codes against one with a single `STD` code.

### The supplier's Insights tab

`GET /supplier/analytics` backs a tab of the same shape in **both** supplier
clients — vm-supplier-ui and vm-supplier-mobile-ui, at parity as CLAUDE.md §1
requires. It answers a different question from the Sales screen: Sales is a
reconciliation document covering all time, Insights is a period view that
defaults to the past week and shows what sold, in which pack sizes, in which
GRADES, day by day, plus the settlement split. Every amount is the grower's
own — their listed price with our markup already subtracted, the same
subtraction the Sales screen makes, so the two can never disagree.

The three breakdowns answer three different questions and none substitutes for
another: "what sold" is per produce, "which packs sell" is per box size, and
"which sizes sell" is per (produce, grade) — the one that says whether picking
XL2 paid for itself. That last is keyed by produce AND grade, never grade
alone, because one grower's M2 pomegranate and M2 tomato are different crates.
Ungraded lines are excluded from it rather than lumped together
(`size_code_snapshot` is NULL on an ungraded listing and on any order predating
size codes), so a grower who does not grade simply sees no such card. Each list
is the top five with its total alongside, as on the admin dashboard.

It is scoped to the calling supplier in SQL; there is no supplier_id parameter
to tamper with (CLAUDE.md §7). It deliberately returns nothing about customers:
cities, repeat rates and basket mix are on the admin dashboard because they are
ours to see.

The chart marks themselves live in `packages/vm-ui-kit/src/components/Charts.tsx`
and are shared by the admin dashboard and the supplier web page. The mobile app
carries its own copy — it sits outside the npm workspaces and cannot import the
kit — drawing the same forms. The line uses `react-native-svg`, added with
`expo install` so it is pinned to the version SDK 57 expects; it ships inside
Expo Go, so it still runs on a real handset without an EAS development build.
`expo-doctor` still passes 18/18. Tapping a point opens its figures in a
callout, which is the touch equivalent of the web's hover tooltip.

Note that the two supplier screens offer DIFFERENT period lists on purpose:
Sales defaults to and allows all time, Insights does not, because
/supplier/analytics refuses `range=all`. `TREND_RANGES` in the mobile app and
`DEFAULT_RANGES` in vm-ui-kit are the lists that match that endpoint.

To verify: sign in as a supplier on either client, open Insights, and switch the
period — everything moves with it except "last 7 days", which must not.

### Order confirmations actually send

Payment writes an `order.confirmed` row to `outbox` in the same transaction as
the state change (CLAUDE.md §5.3). The dispatcher in
`vm-orders-api/internal/api/outbox.go` is the other half of that pattern, and
until it existed the rows had **no consumer at all** — every customer who paid
received nothing.

It claims a batch by stamping `published_at` (so two replicas cannot take the
same row), sends outside the transaction, and releases a row back with the
error recorded if the send fails, so a relay outage delays a confirmation
rather than losing it. Addresses come from vm-profile-api's new
`POST /internal/customers/contacts`, since the orders schema may not read them.
It uses the `SMTP_*` relay — MailHog locally — never the report's Gmail account.

`go run ./internal/devtools -outbox` runs a single tick.

**Before pointing `SMTP_*` at a real relay for the first time**, check what the
outbox is holding: `go run ./internal/devtools -suppress-outbox` counts the
unsent rows, and `-suppress-outbox -yes` marks them published WITHOUT sending.
A queue that predates the dispatcher is full of confirmations for orders that
were delivered, cancelled or expired weeks ago, and draining it at a real relay
would mail every one of those customers. Suppressed rows are stamped
`last_error = 'suppressed: ...'` so it is clear later that nobody was emailed.

### Two things the clock now does on its own

**Dispatch on the delivery day.** The processor tick that promotes `paid ->
processed` at each order's 4pm cutoff also promotes `processed -> dispatched`
on that order's delivery day, from `DISPATCH_HOUR_IST` onwards. Before this,
dispatch happened only when an admin pressed the button, so a customer's screen
could still read "Being prepared" days after the parcel went out. An admin can
still dispatch earlier by hand; the clock only catches what nobody got to.

`DISPATCH_HOUR_IST` defaults to `0` — dispatch as soon as the delivery day
begins. Set it to the hour the courier actually collects (`9`, say) so the
customer is not told their food is on its way while the box is still on the
floor. It is deliberately a separate variable from `ORDER_CUTOFF_HOUR_IST`:
one is an afternoon deadline for taking orders, the other a morning collection.

**The daily courier report.** Once the 4pm run has decided what is being packed,
the dispatch QUEUE — every order sitting in `processed`, which is exactly what
the admin's fulfilment screen lists — is emailed as a CSV attachment to
`MAIL_TO`, using the same writer as the admin export. A queue rather than a
date window on purpose: an order processed early, or one placed yesterday
evening and packed today, belongs on tonight's sheet, and both fall through a
window on `placed_at` or `processing_at`. Sent once per IST day, guarded by a row in
`orders.daily_report_sends`: the job polls on the processor's tick rather than
firing on a cron, so a restart at 16:00 cannot lose the day, and the primary key
on the date is what stops sixty sends an hour. A failed send releases its claim
so the next tick retries.

`go run ./internal/devtools [YYYY-MM-DD]` prints what the report would contain
without sending; add `-send` to actually mail it, through the same job the
scheduler calls.

Both are paced by `ORDER_PROCESSOR_INTERVAL_SECONDS`, and the report's earliest
send time is `ORDER_CUTOFF_HOUR_IST` — the same variable that decides each
order's processing date. One setting rather than two: the sheet describes what
the cutoff decided, so moving the cutoff moves the report with it. The report's SMTP settings
(`MAIL_*`) are deliberately separate from the transactional `SMTP_*` relay: that
one points at MailHog locally so no order confirmation escapes a laptop, and one
relay cannot be both. Leave `MAIL_TO` empty and the report does not run.

### Payments are wired end to end

A customer now pays. `POST /orders` reserves stock, registers the order with
Razorpay and returns `razorpay_order_id` + `razorpay_key_id`; the storefront
opens Standard Checkout on top of that (a script in vm-client-ui, a modal
WebView in vm-client-mobile-ui — the native SDK is not in Expo Go). All four
surfaces of the flow were already built server-side; what landed is the screens.

**The browser is never believed.** `POST /orders/{id}/verify-payment` checks the
callback signature and returns "keep waiting" — it cannot mark an order paid,
because anyone holding a valid signature for a one-rupee order could otherwise
replay it. The **webhook** moves an order to `paid`, commits the reservation,
creates the supplier payouts and writes the confirmation email, in one
transaction (CLAUDE.md §6.4 step 6). The clients poll their own order for up to
40 seconds after Checkout succeeds and say "your payment went through, the
confirmation is still coming" if it takes longer — never a tick for an order the
server has not moved.

**A dismissed sheet is not a failure.** The order keeps its reservation for
`RESERVATION_TTL_MINUTES`, and `GET /orders/{id}` keeps returning the provider
ids while the order is `pending_payment` so the order page can offer Pay now.
Once it is paid those fields are gone from the response.

#### Testing payments locally

Test mode needs **no KYC**. `rzp_test_*` keys work from the moment the account
exists; activation gates only live keys and real settlement.

1. Put the test key id and secret in the root `.env`. `RAZORPAY_KEY_ID` and
   `VITE_RAZORPAY_KEY_ID` must be **the same value** — the order is created
   under the server's key and Checkout must open under that same one. (The
   clients prefer the id the server returns for exactly this reason, but a
   mismatched `VITE_` value still means the two disagree, so fix it rather than
   relying on the fallback.) Verify a pair actually authenticates:

   ```sh
   curl -s -o /dev/null -w '%{http_code}\n' \
     -u "$RAZORPAY_KEY_ID:$RAZORPAY_KEY_SECRET" \
     'https://api.razorpay.com/v1/orders?count=1'   # 200 = good, 401 = wrong pair
   ```

2. **Razorpay cannot call `localhost`.** Without a public URL the webhook never
   arrives and every test order sits in `pending_payment` looking like a bug in
   the app. Start the stack with a tunnel in front of the gateway:

   ```sh
   ./scripts/start.sh --tunnel
   ```

   That runs `cloudflared` (or `ngrok`, whichever is installed — `brew install
   cloudflared` if neither), waits until the public hostname actually answers
   `/healthz`, and prints the webhook URL to register. `./scripts/start.sh --stop`
   takes it down again. To run one by hand instead:

   ```sh
   cloudflared tunnel --url http://localhost:8080     # or: ngrok http 8080
   ```

   In the Razorpay dashboard (test mode) → Settings → Webhooks, add
   `https://<public-host>/webhooks/razorpay`, subscribe to `payment.captured`,
   `payment.failed` and `refund.processed`, and set the secret to
   `RAZORPAY_WEBHOOK_SECRET`.

   The tunnel **survives `./scripts/start.sh` restarts** on purpose — it is bound to
   the gateway's port, not to its processes, so it reconnects on its own and
   the registered URL keeps working. `./scripts/start.sh --stop` is what takes it down,
   and a quick tunnel's hostname dies with the process: the next one is
   different and the dashboard needs re-pointing. Setting `DEV_TUNNEL_NAME` (a
   cloudflared named tunnel) or `DEV_TUNNEL_HOSTNAME` (an ngrok reserved
   domain) in `.env` pins a hostname you register once — see the comments in
   `.env.example`.

   If an order sits at "Confirming your payment with the bank…" and then falls
   back to the "taking a little longer" copy, the webhook did not arrive. Two
   causes, in order of likelihood:

   - **The tunnel died but its process did not.** Cloudflare deregisters a
     quick tunnel whose control stream keeps failing, and cloudflared then
     retries forever against a hostname that no longer resolves — so `pgrep`
     shows it running while Razorpay rejects the URL with `no such host`.
     Re-run `./scripts/start.sh --tunnel`: it probes the running tunnel, replaces it if
     it has stopped answering, and prints the new URL to register. The
     transport defaults to `http2` (`DEV_TUNNEL_PROTOCOL`) because QUIC is what
     collapses on networks that idle-drop UDP.
   - **The dashboard points at an older hostname.** Only the URL that
     `./scripts/start.sh --tunnel` last printed is live; delete the stale entries. The signature is verified over the **raw** body,
   so the webhook route must keep its raw-body handling.

3. Pay with a test card: **4111 1111 1111 1111**, any future expiry, any CVV,
   OTP `1234` (Razorpay's test-mode 3-D Secure page has a success button). Test
   UPI and netbanking give a success/failure simulator instead of a real bank.

4. The order should reach `paid` within a couple of seconds, with a
   `supplier_payouts` row per supplier and a confirmation in MailHog.

Not built yet: every domain table and endpoint (CLAUDE.md §5, §6), auth and
seeded accounts, and the tests CLAUDE.md requires for stock reservation
concurrency, CSV import and webhook signatures.

Test coverage worth knowing about:

- markup (both services) — that a flat per-product markup moves every pack size
  by the same amount, that the grower is paid on THEIR price and never on ours,
  and that the markup is counted once in earnings rather than also falling
  inside the commission figure. `make -C infra test-e2e-markup` proves the same
  four things end to end against a running stack.
- `stockcheck` (vm-orders-api) — that a cart of three 5 kg packs against 5 kg of
  stock is caught in the CART, not at checkout, and that the per-line "you may
  keep N" ceilings apportioned across two pack sizes of one product never add
  up to more than exists.
- `grams` (vm-go-common) — the weight wording both services quote back to a
  customer, including the tenth that carries into a whole kilo.
- the reporting period (vm-orders-api) — that every preset ends today in IST
  from the SERVER's clock, that the supplier screen still defaults to all time,
  and that an order placed at 23:30 on the last day of a custom range falls
  inside it.
- the dashboard breakdowns (vm-orders-api) — that a day nobody ordered on still
  appears on the chart as a zero rather than vanishing from the axis, and that a
  one-day period still plots.

- `money` — half-up rounding, basis-point fees, and the malformed rupee strings
  a supplier CSV can contain. `"1.-5"` must be rejected, not silently priced at
  95 paise.
- `isttime` — the exact cutoff table from CLAUDE.md §6.1, including the
  10:30 UTC row that catches a naive implementation comparing the wrong hour.
- `httpx` — that a 500 never leaks a connection string into the response body.
- `vm-catalog-api` catalogue — that produce which has sold out, or that a
  grower closed, stays on the storefront as an unbuyable card instead of
  disappearing, sorts below what is still for sale, and is excluded from the
  "N items available today" count. Needs the dev database (`make -C infra up`);
  skips without it.
- product media (vm-catalog-api) — that the upload allow-list maps every
  accepted content type to the right kind and extension and refuses everything
  else (an SVG is scriptable and would be served from our own origin), that
  gallery order comes from array position, and that a repeated object key or a
  gallery past the cap is refused with the offending item named.
- grade naming (vm-go-common, both mobile apps) — that an empty code and the
  implicit `STD` are hidden while everything else survives verbatim, asserted
  once per language so the web, the phones and the courier sheet cannot drift
  into showing a customer a grade they were never offered.
- product-level media (vm-catalog-api) — that a grade's gallery is its own
  pictures followed by the product's common ones, that building one grade's
  gallery does not mutate the tree the next grade reads, and that the common
  bucket can supply the cover when a grade has only video or nothing.
- admin size breakdown (vm-orders-api) — that "Revenue by produce" is grouped
  by grade rather than summing crates that are separate goods, and that the
  platform-wide grade list counts the suppliers behind each one.
- pack detail (vm-catalog-api) — that a pack's optional detail line is
  trimmed, comes back null rather than empty, and truncates at 80 exactly as a
  size code's does, so the same field at two levels cannot behave differently.
- catalogue cards (vm-catalog-api) — that a card ships every grade the grower
  declared today, each with its own packs, prices, cover and stock, in the
  grower's order; that the flat fields still describe the grade the card opens
  on; and that a sold-out or closed grade leaves its siblings buyable on the
  same card. Needs the dev database; skips without it.
- size-code stock isolation (vm-catalog-api, vm-orders-api) — the property size
  codes exist for. Selling out one grade must leave its siblings buyable, in
  the locked reserve path AND in the unlocked cart check; and a shortfall in
  one grade must name that grade, because "Tomato has sold out" is wrong when
  only the XL has.
- CSV galleries (vm-catalog-api) — that rows collapsing into one grade
  contribute every distinct `image_url` and `video_url` between them,
  deduplicated, with images kept ahead of videos so the cover is an image
  whatever order the file was written in; and that a file with no `video_url`
  column still parses.
- each service — that `/healthz` needs no database and no internal token.

---

## Repository layout

```
vayal-mikrogreenz/
├── CLAUDE.md                 # architecture, domain rules, conventions
├── .env.example              # THE env file — one for the whole platform
├── go.work                   # ties the Go modules together
├── package.json              # npm workspaces root
├── infra/
│   ├── Makefile              # the developer entrypoint
│   ├── docker-compose.yml    # postgres, minio, mailhog — nothing else
│   ├── postgres/init/        # database, schemas, per-service roles
│   ├── scripts/              # doctor, run, wait, seed, status; ubuntu/ helpers
│   └── seed/                 # development seed data
├── scripts/                  # what people run: dev-setup, start, start-mobile (+ -ubuntu)
├── packages/
│   ├── vm-go-common/         # config, logging, httpx, db, migrate, money, isttime
│   └── vm-ui-kit/            # logo, Tailwind preset, shell, API client
├── vm-gateway-api/           # Node + TypeScript BFF        :8080
├── vm-profile-api/           # Go — schema: profile         :8081
├── vm-catalog-api/           # Go — schema: catalog         :8082
├── vm-orders-api/            # Go — schema: orders          :8083
├── vm-events-consumer-api/   # Go — schema: analytics       :8084
├── vm-analytics-api/         # Python — reads analytics     :8085
├── vm-client-ui/             # React — customer             :5173
├── vm-admin-ui/              # React — admin                :5174
├── vm-supplier-ui/           # React — supplier             :5175
├── vm-analytics-ui/          # React — admin Analytics remote :5176
├── vm-supplier-mobile-ui/    # React Native (Expo) — supplier, Android + iOS
└── vm-client-mobile-ui/      # React Native (Expo) — customer, Android + iOS
```

The two mobile apps are the packages with their own `node_modules` and
lockfiles, outside the npm workspaces. Expo SDK 57 pins React 19.2 and the three
web UIs are on React 18.3; hoisting both into one root tree makes npm pick, and
it picks differently per machine. See their READMEs.

---

## Conventions

Non-negotiables, in full, in [CLAUDE.md §4](./CLAUDE.md). The ones that bite
soonest:

1. Money is `int64` paise. Format to rupees only at the UI edge.
2. Timestamps stored UTC; business-day logic computed in `Asia/Kolkata`.
3. No hardcoded URLs, ports, fees or secrets. Everything from `.env`.
4. No secrets in the repo — not in tests, not in seed data.
5. Only the gateway is public.
6. Writes that move money or stock are idempotent.
7. Parameterised SQL only.
8. Every service: `/healthz`, `/readyz`, JSON logs with `request_id`, and one
   error shape:
   ```json
   { "error": { "code": "SNAKE_CASE_CODE", "message": "human readable", "details": {} } }
   ```

Support: <vayal.mikrogreenz@gmail.com>

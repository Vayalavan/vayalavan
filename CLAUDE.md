# Vayalavan — Project Context

Read this file at the start of every session. It is the source of truth for
architecture, domain rules, and conventions. If a request conflicts with this
file, say so before writing code.

## 1. What we're building

Vayalavan ("vayal" = farming field in Tamil) is a B2C e-commerce
platform for fresh fruits, vegetables and microgreens. Suppliers list produce
and declare availability every day; customers order against that day's
availability; we collect payment, hand parcels to third-party couriers, and
settle suppliers manually by NEFT.

Three personas, three separate web UIs:
- Customer -> vm-client-ui
- Admin    -> vm-admin-ui
- Supplier -> vm-supplier-ui

Two of the three also get a native app, each at feature parity with its web
counterpart:
- Supplier -> **vm-supplier-mobile-ui** ("Vayalavan Supplier"). Availability is
  declared from a field at 6am, not a desk.
- Customer -> **vm-client-mobile-ui** ("Vayalavan"). Buying groceries is
  a phone habit, and a 4pm cutoff rewards someone who can order from wherever
  they happen to be.

The web UIs stay — they are the only version on a laptop, and a change must land
in both or in neither. Admin has no app and is not getting one: it is a desk job
with tables in it.

Target: production launch in ~4 weeks. Prioritise correctness of money,
stock and date logic over feature breadth. Anything marked P1 must not be
started until every P0 item is done and tested.

## 2. Repository layout

```
vayal-mikrogreenz/
  README.md
  scripts/                # the commands people run: dev-setup, start, start-mobile
                          # (macOS) and their -ubuntu wrappers
  mocks/                  # development seed data SQL cannot hold: supplier catalogues
                          # + their photos/videos (vm-catalog-api -seed-mocks)
  infra/                  # docker-compose, migrations runner, seed data, Makefile
  packages/
    vm-go-common/         # Go: config, logging, errors, auth middleware, db, money, ist-time
    vm-ui-kit/            # React: shared components, theme tokens, Vayal logo, API client
  vm-gateway-api/         # Node (TypeScript) — BFF / API gateway. The ONLY service exposed publicly.
  vm-profile-api/         # Go — identity, customers, addresses, suppliers
  vm-catalog-api/         # Go — products, units, images, CSV import, daily availability
  vm-orders-api/          # Go — cart, orders, timeline, payments, supplier payouts
  vm-events-consumer-api/ # Go — CDC consumer: supplier/product/order events -> analytics schema
  vm-analytics-api/       # Python (FastAPI) — read-only reports over the analytics schema
  vm-client-ui/           # React (Vite + TS)
  vm-admin-ui/            # React (Vite + TS)
  vm-supplier-ui/         # React (Vite + TS)
  vm-analytics-ui/        # React (Vite + TS) — admin Analytics, a Module Federation remote
  vm-supplier-mobile-ui/  # React Native (Expo + TS) — supplier, Android + iOS
  vm-client-mobile-ui/    # React Native (Expo + TS) — customer, Android + iOS
```

Do not add any other top-level service without asking first.

**vm-analytics-ui is not a fourth persona.** It is the admin console's
Analytics section, built and deployed on its own and loaded by vm-admin-ui at
runtime through Module Federation (`@module-federation/vite`; host `vm_admin`,
remote `vm_analytics`, exposed `./Analytics`). It never signs in: the host passes
`apiBaseUrl` and `getAccessToken`, so there is one admin session and the token
stays in the console's memory. React, React DOM, React Query and
`@vayal/ui-kit` are shared singletons — the two `shared` lists must agree.
The props are a contract between two deploys, written twice
(`vm-analytics-ui/src/Analytics.tsx`, `vm-admin-ui/src/remotes.d.ts`); a
breaking change ships both. The remote emits no Tailwind base layer, because
the host page already has one. An unreachable remote is contained to the
Analytics page by an error boundary.

The two mobile apps are the packages OUTSIDE the npm workspaces, each with its
own `node_modules` and lockfile: Expo SDK 57 pins React 19.2 and the web UIs
are on React 18.3, and hoisting both leaves npm to pick. They therefore cannot
import `@vayal/ui-kit` (which is React DOM anyway). Where that forces a
duplicate — the colour ramps, the money helpers, the catalogue count copy — a
test in the mobile app asserts the copies agree, rather than leaving them to
drift.

The two apps are deliberately near-identical in structure and tooling: same SDK
pin, same `Screen`/`Card`/`Button` primitives, same API client, same auth model.
A fix to one is usually a fix to both. They share `MOBILE_API_BASE_URL` — one
gateway — and nothing else: separate bundle ids, Metro ports, EAS projects and
keystore keys, because both can be installed on one handset.

## 3. Tech stack (fixed — do not substitute)

- Gateway: Node 20 + TypeScript + Express (or Fastify), zod for validation
- Services: Go 1.22+, chi router, pgx/v5, sqlc for queries, goose for migrations
- The ONE exception, decided with the product owner on 2026-10-02:
  vm-analytics-api is Python 3.12 + FastAPI + psycopg 3, managed by uv (flat
  `src/`, `uv run python src/main.py`, `uv run pytest`). It only ever READS
  the analytics schema. Every other service stays Go.
- DB: PostgreSQL 16. One instance, one database `vayal`, one schema per
  service: `profile`, `catalog`, `orders`. A service may only read/write its
  own schema. No cross-schema foreign keys — reference by ID and validate via
  API calls or accept eventual consistency.
- Object storage: MinIO (S3-compatible) via aws-sdk-go-v2 with path-style
  addressing. Must work unchanged against real S3 by changing env vars only.
- Payments: Razorpay (Standard Checkout + webhooks)
- Frontend: React 18 + TypeScript + Vite, React Router, TanStack Query,
  Tailwind CSS + shadcn/ui components. Responsive: mobile-first, must work
  well from 360px up to desktop.
- Email: SMTP via env config (MailHog locally). Templates in Go html/template.
- Mobile (both apps): React Native via Expo, TypeScript, React Navigation,
  TanStack Query. No Tailwind — styles come from `src/theme/tokens.ts`, which is
  bound to vm-ui-kit's Tailwind preset by a test. Never hardcode a hex value in
  a component, exactly as on the web.
  **Pinned to Expo SDK 57, and do not upgrade it casually.** The pin tracks
  whatever the App Store build of Expo Go is, for one reason: iOS cannot
  sideload an older Expo Go, so a project behind the store version cannot be
  opened on a real iPhone at all without a paid Apple Developer account and an
  EAS development build. That is what forced the move off SDK 54 — the store
  build became 57 and the phone simply refused the project. Expect the same
  again; when it happens, upgrade rather than pin harder.
  Change versions only as a set (`npm install expo@^<major>` then
  `npx expo install --fix`), verify with `npx expo-doctor` (21/21), and move
  BOTH apps together: one Expo Go install has to run both. `--fix` does not
  touch dev dependencies — `babel-preset-expo`, `@types/react` and
  `typescript` need setting by hand, and doctor is what catches them.
  What the 54 -> 57 move actually broke, as a guide to what to look for next
  time: `StyleSheet.absoluteFillObject` was removed from React Native (a
  silent no-op when spread, not a crash), `expo-video`'s `allowsFullscreen`
  became `fullscreenOptions={{ enable: true }}`, `android.edgeToEdgeEnabled`
  disappeared because edge-to-edge is now the default, and `expo-status-bar`
  and `expo-sharing` became config plugins that must be listed.
  Consequence to know: `expo-file-system` has no `UploadTask` in its modern
  API, so file transfers use `expo-file-system/legacy` (supplier app only —
  the customer app uploads nothing). That subpath still exists in 57; check it
  before any future upgrade, because the supplier app's uploads ride on it.
  Both apps move to a new SDK together: one Expo Go install has to run both.
  Android and iOS only — `platforms` excludes web on purpose; the web versions
  are vm-supplier-ui and vm-client-ui.
  Anything a browser gives free has to be wired by hand and is easy to forget:
  online/offline and focus for TanStack Query (`lib/queryClient.ts`),
  `crypto.randomUUID` (absent in Hermes), `AbortSignal.timeout`, and `Intl`
  timezone support, which is why `lib/datetime.ts` does IST by arithmetic.

Out of scope entirely: an admin mobile app, real-time courier tracking,
coupons/discounts, ratings/reviews, multi-language.

(Subscriptions were on this list until 2026-09-26. Scheduled and repeat
orders, paid from a prepaid wallet, are now IN scope — see §6.7.)

## 4. Non-negotiable engineering rules

1. **Money is `int64` paise. Never float, never decimal strings in logic.**
   Format to rupees only at the UI edge. All DB money columns are `BIGINT`
   and named with a `_paise` suffix.
2. **All timestamps stored as `TIMESTAMPTZ` in UTC.** All business-day logic
   (the 4pm cutoff, delivery days) is computed in `Asia/Kolkata`. Never use
   the server's local timezone. `vm-go-common/isttime` owns this.
3. **Everything configurable comes from env vars.** No hardcoded URLs, ports,
   hosts, buckets, fees, or secrets anywhere in source. Every service ships a
   committed `.env.example` with every variable documented and safe defaults.
   Real `.env` files are gitignored.
4. **No secrets in the repo.** Ever. Not in tests, not in seed data, not in
   docker-compose defaults beyond local-only dev creds.
5. **Only the gateway is publicly reachable.** Go services bind to the
   internal network and require a shared `INTERNAL_SERVICE_TOKEN` header.
6. **Every write endpoint that creates money or stock movement is
   idempotent.** Accept an `Idempotency-Key` header on order creation; store
   and dedupe webhook events by provider event ID.
7. **Parameterised SQL only** (sqlc-generated). No string concatenation.
8. **Every service exposes** `GET /healthz` (liveness) and `GET /readyz`
   (checks DB + dependencies), emits structured JSON logs with a `request_id`
   propagated from the gateway via `X-Request-Id`, and returns errors in a
   single shape:
   `{"error": {"code": "SNAKE_CASE_CODE", "message": "human readable", "details": {}}}`
9. **Tests are required for**: money math, the cutoff/timeline calculator,
   stock reservation concurrency, CSV import validation, and webhook
   signature verification. Table-driven tests in Go.
10. When you're unsure about a business rule, **stop and ask** rather than
    inventing one.

## 5. Domain model

### 5.1 profile schema

**users** — single identity table for all personas
  id UUID PK, email CITEXT UNIQUE NULL, phone TEXT UNIQUE NULL,
  password_hash TEXT, role TEXT CHECK (role IN ('customer','supplier','admin','analyst')),
  status TEXT ('active','pending','suspended'), email_verified BOOL,
  created_at, updated_at
  - Customers sign up with phone + email + password. Password hashed with
    bcrypt (cost 12) or argon2id.
  - Admins are seeded, never self-signup.

**customer_profiles** — user_id PK/FK, name

**addresses** — id, user_id FK, label, recipient_name, phone, line1, line2,
  landmark, city, state, pincode, is_default BOOL, deleted_at
  - Soft delete only. Orders snapshot the address at placement time.

**suppliers** — id UUID PK, user_id FK UNIQUE, business_name, contact_name,
  phone, email, gstin NULL, pan NULL, address fields, bank_account_name,
  bank_account_number, bank_ifsc, status ('pending','approved','suspended'),
  approved_by, approved_at, created_at
  - Bank details are needed for manual NEFT. Mask them in all API responses
    except the admin payout screen (show last 4 of account number elsewhere).

**supplier_events_outbox** — the profile part of §5.4. Insert-only, written
  in the same transaction as every supplier change (applied, created, updated,
  approved, rejected, suspended). Carries business name, status, town and
  commission — never contact, tax or bank details.

**refresh_tokens** — id, user_id, token_hash, expires_at, revoked_at,
  user_agent, created_at

### 5.2 catalog schema

**products** — id UUID PK, supplier_id, name, type
  ('fruit','vegetable','microgreen','other'), grade (e.g. 'A','B','Premium'),
  description, status ('draft','active','archived'), created_at, updated_at
  - Media lives in `product_media`, not on this row. There is no `image_key`
    column: it was replaced by the gallery in migration 00008.

**product_media** — id UUID PK, size_code_id FK NULL, product_id,
  kind ('image','video'), object_key TEXT, content_type TEXT NULL,
  sort_order INT, created_at
  - Hangs off the SIZE CODE, not the product: the photographs are of a grade.
    Switching size code on the storefront swaps the gallery.
  - **A NULL `size_code_id` means the whole product** — the field, the packing
    shed, the grower. Those pictures are the same whichever crate you buy, and
    a grower with four grades was uploading them four times. The customer sees
    one gallery per grade: **that grade's own rows first, the common ones
    after**, concatenated server-side so no two clients can disagree about the
    order. The composite FK is MATCH SIMPLE, so a NULL row is not cascaded
    when a grade is deleted — which is what lets the farm photographs survive
    the delete-and-replace a product save performs.
  - UNIQUE (size_code_id, object_key) — the same file attached twice is a
    double-submit, not a second photograph. Postgres treats NULLs as distinct,
    so the common bucket has its own partial unique index on
    (product_id, object_key) WHERE size_code_id IS NULL.
  - Store the MinIO **object key**, not a full URL. URLs are generated at
    read time so changing bucket/host never breaks existing rows.
  - **A product may have several images and videos.** Produce is sold on how
    it looks, and one still cannot show a grower turning a crate over.
  - **Ordering decides the cover, not a flag.** `sort_order` is the supplier's
    own arrangement, and the COVER is the first row of kind `'image'`. An
    `is_primary` boolean would be a second thing to keep consistent for a
    choice the supplier already makes by dragging.
  - **A video is never a cover.** Catalogue cards, order thumbnails, the
    availability sheet and the admin table all need a still they can paint
    immediately, and nothing here decodes video. A product carrying only
    video therefore has no cover and renders the placeholder, exactly as a
    product with no media does.
  - **The cover falls back to the common bucket.** A grade's own first image
    wins; failing that the product-level gallery's. A grower who photographed
    the field but not the crates gets a thumbnail rather than a placeholder,
    and every surface that paints one — card, detail page, availability sheet,
    order thumbnail, admin table — applies the same fallback.
  - The SUPPLIER's product response keeps the two buckets separate
    (`media[]` on the product, `media[]` on each size code) because the form
    edits them separately. Only the customer-facing catalogue concatenates.
  - API responses keep `image_url` (the presigned cover) for every surface
    that shows one thumbnail — on a product that is the first GRADE with one —
    and nest `media[]` under each size code where a gallery is actually
    rendered. A catalogue listing does not ship every grade's whole gallery for
    pictures nothing on that screen draws.

**product_size_codes** — id UUID PK, product_id FK, code (e.g. 'M','XL','M2'),
  meta NULL (e.g. '150 g - 200 g'), is_active BOOL, sort_order INT,
  created_at, updated_at
  - UNIQUE (product_id, lower(btrim(code))), plus UNIQUE (id, product_id) as
    the target of the child tables' composite FKs.
  - **A grading level between the product and its packs.** Produce is sold by
    grade: a grower has 40 kg of M tomatoes and 15 kg of XL, in separate
    crates, at different prices, photographed separately.
  - **Three things hang off the size code, each for its own reason:** the
    PACKS, because a price belongs to (size, weight) and not weight alone;
    the MEDIA, because the photographs are of that grade; and the daily
    STOCK, because the crates are physically separate.
  - **Every product has at least one.** A grower who does not grade simply has
    one, and the storefront hides the selector when there is nothing to
    select. A second "flat product" code path would double every read, write
    and screen for a case the single-grade one already covers.
  - `meta` is free text, never parsed. The weight that matters is on the pack.
  - **`harvest_share_pct` is roughly what percent of the harvest this grade
    is** — 55 for the M that is most of the field, 3 for the XL2 that is a
    handful of fruit a tree. A GENERAL property of the grade, not a daily one:
    today's grams are stock and live on `daily_availability`. Nullable, because
    a grower who has not estimated their split is the normal starting state and
    must not be made to invent a number. Whole percent, not basis points — this
    is an estimate, not money, and nothing adds it to a total. The stated
    shares across one product may not exceed 100; under is fine and common.
    **Display only.** The storefront's size selector draws each grade from it —
    gold for rare, bronze for the middle, silver for the grade there is most of
    — and nothing in pricing, reservation, payout or fulfilment reads it. The
    threshold rule is a CLIENT rule and lives in two places, one per language:
    `harvestTier` in @vayal/ui-kit and a tested copy in
    vm-client-mobile-ui's `lib/harvest.ts`. No Go equivalent — the server
    stores the number and never names a tier.

**product_pack_options** — id UUID PK, size_code_id FK, product_id,
  label (e.g. "1 kg box"), meta NULL (e.g. '6-8 fruit'), weight_grams INT,
  price_paise BIGINT, is_active BOOL, sort_order INT
  - Replaces `product_units`. Suppliers define whatever packs they want
    (1kg, 1.5kg, 3kg, 250g...) **per grade**.
  - `price_paise` is the price of that whole pack, not per kg.
  - UNIQUE (size_code_id, weight_grams) — the same weight under a DIFFERENT
    grade is fine, and is the point: 1 kg of M and 1 kg of XL are different
    goods at different prices.
  - `product_id` is denormalised and held honest by a COMPOSITE foreign key
    against `product_size_codes (id, product_id)`, so a pack whose product
    disagrees with its size code's is unwritable rather than merely wrong.
  - `meta` is the same field a size code has, one level down: free text,
    never parsed, capped at 80. The LABEL has to stay short enough to be a
    chip in a selector, so the sentence that would not fit in it — "6-8
    fruit", "ventilated carton" — goes here and is printed under the label on
    the product page. One idea, one name, one cap at both levels.

**daily_availability** — id UUID PK, supplier_id, product_id,
  size_code_id FK, available_on DATE, total_grams INT,
  reserved_grams INT DEFAULT 0, sold_grams INT DEFAULT 0,
  status ('open','closed'), updated_at
  - UNIQUE (size_code_id, available_on)
  - **Availability is tracked in grams per SIZE CODE**, not per product and
    not per pack. A supplier saying "40 kg of M tomatoes today" means any mix
    of that grade's 1/1.5/3 kg packs can be sold until the grams run out.
    Buying one 1.5 kg pack of M consumes 1500 g of M.
  - **The grades do not share.** Selling out M must leave L untouched — they
    are separate crates, and one pool would let the shop sell an XL box
    against tomatoes that are really M.
  - `total_grams - reserved_grams - sold_grams` = sellable now, for that
    grade. When that hits 0, or `status='closed'`, or
    `available_on != today (IST)`, that GRADE is not purchasable and the
    client UI shows it as sold out. A product is sold out only when every one
    of its grades is.
  - Availability does NOT carry over. Each day is a fresh row.

**csv_imports** — id, supplier_id, filename, status
  ('validating','preview','committed','failed'), total_rows, valid_rows,
  error_rows, report JSONB, created_at

### 5.3 orders schema

**carts / cart_items** — server-side cart keyed by customer_id.
  cart_items: id, cart_id, product_unit_id, product_id, supplier_id, qty
  - `product_unit_id` references a PACK OPTION, which belongs to one size
    code. A cart can hold two lines of one produce at different grades: they
    are different goods at different prices, drawing on different crates.
  - Prices are NOT stored in the cart. Always re-priced from catalog at
    checkout. Show the customer a diff if anything changed.

**orders**
  id UUID PK, order_number TEXT UNIQUE (human friendly, e.g. VM-260814-0042),
  customer_id, status, address_snapshot JSONB,
  subtotal_paise, platform_fee_paise, delivery_fee_paise, total_paise,
  placed_at TIMESTAMPTZ, processing_at TIMESTAMPTZ, delivery_day DATE,
  expected_delivery_date DATE,
  payment_status, razorpay_order_id, razorpay_payment_id,
  cancelled_at, cancel_reason, created_at, updated_at

  Status enum: `pending_payment` -> `paid` -> `processed` -> `dispatched`,
  plus terminal `payment_failed`, `expired`, `cancelled`, `refunded`.

**order_items** — id, order_id, supplier_id, product_id, product_unit_id,
  size_code_id, size_code_snapshot, size_meta_snapshot,
  product_name_snapshot, unit_label_snapshot, grade_snapshot, weight_grams,
  unit_price_paise, qty, line_total_paise
  - The SIZE CODE is snapshotted for the same reason the price is: it decides
    the price, and it lives in the catalog schema this service may not read.
    Left NULL on ungraded listings and on orders placed before size codes
    existed — writing the implicit code would invent a grade the customer
    never chose.
  - **Every surface that names a line names its grade.** "Pomegranate · 1 Kg
    Box" is not a description of anything when the grower sells eleven grades
    out of separate crates: two lines can read identically and be different
    goods at different prices. That covers the cart, checkout, order history,
    the confirmation email, the admin order view, the supplier's sales list
    and the COURIER SHEET a packer works from. The rule for hiding the two
    non-choices — an empty code, and the implicit `STD` — lives in exactly
    three places, one per language: `produce.GradedName` in vm-go-common,
    `gradedName` in @vayal/ui-kit, and a tested copy in each mobile app's
    `lib/grade.ts`.
  - Snapshot everything. An order must render correctly even if the product
    is later edited or deleted.
  - A single order MAY contain items from multiple suppliers.

**stock_reservations** — id, order_id, product_id, available_on,
  grams, status ('held','committed','released'), expires_at, created_at

**payments** — id, order_id, provider, provider_order_id,
  provider_payment_id, amount_paise, status, method, raw_payload JSONB,
  created_at

**webhook_events** — id, provider, provider_event_id TEXT UNIQUE,
  event_type, payload JSONB, received_at, processed_at, error
  - Unique constraint on provider_event_id is the idempotency guard.

**supplier_payouts** — id, supplier_id, order_id, amount_paise,
  status ('pending','paid'), marked_paid_by (admin user id),
  marked_paid_at, reference_no (NEFT UTR), notes, created_at
  - One row per (order, supplier). Created when an order becomes `paid`.

**outbox** — id, aggregate_type, aggregate_id, event_type, payload JSONB,
  created_at, published_at
  - Emails and downstream side effects are written to outbox in the same
    transaction as the state change, then dispatched by a worker.

**admin_audit_log** — id, admin_user_id, action, entity_type, entity_id,
  before JSONB, after JSONB, created_at
  - Mandatory for: supplier approval, marking a payout paid, order
    cancellation, refunds — including wallet refunds (`wallet.refund`).

**wallets / wallet_transactions / wallet_topups / wallet_refunds** — the
  prepaid wallet (§6.7). `wallets.balance_paise` is a cache of the ledger,
  only changed under `SELECT … FOR UPDATE` in the same transaction as a
  `wallet_transactions` row (signed amount + balance after); a CHECK refuses
  a negative balance. Unique indexes make a top-up credit once and an order
  debit once, whatever replays.

**order_events_outbox** — the orders half of §5.4. Insert-only, written in
  the same transaction as every order transition (placed, paid, expired,
  cancelled, processed, dispatched; `order.snapshot` for backfill). Distinct
  from `outbox`, which is the email/alert work queue.

**schedules / schedule_items / schedule_occurrences** — scheduled and
  repeat orders (§6.7). Items are priced live at each run, never stored
  prices. `schedule_occurrences` UNIQUE (schedule_id, delivery_date) is the
  charging run's idempotency guard. `orders.schedule_id` links the orders a
  schedule placed.

### 5.4 Analytics — outbox + CDC (decided 2026-10-02)

Suppliers, products and orders (suppliers added 2026-10-02). The pipeline:

```
profile.supplier_events_outbox ─┐
catalog.product_events_outbox ──┤   publication vayal_analytics (INSERTs only)
orders.order_events_outbox ─────┴─> logical replication (pgoutput, slot vayal_analytics)
                                   └─> vm-events-consumer-api ─> analytics.*  ─> vm-analytics-api
```

- **Each event is written in the same transaction as the change it
  describes**, by `internal/analyticsevents` in vm-profile-api,
  vm-catalog-api and vm-orders-api. A transition that commits always has its event; one that
  rolls back never does. Every path that changes an order's status or a
  product must emit — `applyOrderPaid` covers every way of paying.
- **Payloads carry the whole aggregate AFTER the change** (event-carried
  state), so the consumer upserts and a missed or repeated event converges.
- **Payloads are typed structs with no field for personal data**: no names,
  phones, emails, street addresses, payment/bank references, descriptions or
  cancel or rejection reasons; no supplier contact, PAN, GSTIN or bank
  details (a `gst_registered` flag instead). Delivery location is
  city/state/pincode only. Tests hold the order and supplier payloads' keys
  to an allow-list; adding a field is a decision
  about what the analytics role may see.
- **The consumer confirms a WAL position only after the analytics
  transaction commits**, and dedupes on event id (`processed_events`), so
  delivery is at-least-once and effect is exactly-once. Unparseable events go
  to `dead_letter_events` rather than blocking the stream.
- analytics tables: `suppliers` (one per supplier: name, status, town,
  commission), `orders` (one per order, lifecycle timestamps),
  `order_items` (one per line), `products` (one per product), `product_packs`
  (SCD type 2 — a product save replaces every pack id, so old order lines
  resolve against the version they sold at).
- Roles: `ANALYTICS_DB_USER` owns schema `analytics` (consumer);
  `ANALYTICS_READER_DB_USER` can only SELECT it (vm-analytics-api);
  `EVENTS_CDC_DB_USER` is a REPLICATION login with no table grants at all.
  None of the three can reach profile, catalog or orders, and the init and
  cdc-setup scripts assert it.
- `make migrate-up` provisions roles, migrates, then creates the publication
  and the replication slot (before any service runs, so no event is missed).
  Backfill existing rows: `go run ./cmd/api -emit-snapshots` (profile, catalog) and
  `go run ./internal/devtools -emit-snapshots` (orders) — they flow through
  the same CDC path. Outbox rows are pruned after `OUTBOX_RETENTION_DAYS`.
- Postgres runs with `wal_level=logical` and `max_slot_wal_keep_size`, so a
  consumer that is gone for good cannot fill the disk with retained WAL.

## 6. Core business logic (get this exactly right)

### 6.1 Order cutoff and timeline

Constants: `ORDER_CUTOFF_HOUR_IST=16` (4:00 PM), timezone `Asia/Kolkata`.

Computed **once, deterministically, at order placement** and stored on the
order. Never recomputed later.

```
placed_at_ist = placed_at in Asia/Kolkata
D = placed_at_ist.date

if placed_at_ist.time < 16:00:
    processing_date = D
else:
    processing_date = D + 1 day

processing_at           = processing_date at 16:00 IST
delivery_day            = processing_date + 1 day
expected_delivery_date  = delivery_day + 1 day
```

The customer-facing timeline has exactly **three** milestones:

| Milestone       | Timestamp        | Marked complete when              |
|-----------------|------------------|-----------------------------------|
| Order received  | `placed_at`      | immediately on successful payment |
| Order processed | `processing_at`  | `now() >= processing_at`          |
| Delivery day    | `delivery_day`   | `now() >= delivery_day 00:00 IST` |

Display copy under the timeline:
- "Expected delivery: {expected_delivery_date, formatted DD MMM YYYY}"
- "We hand your order to professional courier partners after processing, so
  live tracking isn't available. Dates shown are estimates."
- "Any issues? Write to us at vayal.mikrogreenz@gmail.com"

Milestone completion is **derived from timestamps at read time** — do not
depend on a cron job for correctness of the display. A scheduled job only
handles side effects (status column update, notification emails).

**Terminal orders show NO timeline at all.** `cancelled`, `refunded`,
`expired` and `payment_failed` return an empty milestone list; the status
carries the outcome and the clients hide the block. Derivation from timestamps
is right for an order still moving, but on its own it announced "Order
processed" and "Delivery day" for an order that expired unpaid — telling a
customer who was never charged that food had been packed for them. Time decides
completion; status decides whether there is anything to complete.

**Required test cases** (assert exact values):

| Placed at (IST)       | processing_at        | delivery_day | expected_delivery |
|-----------------------|----------------------|--------------|-------------------|
| 2026-06-15 09:00      | 2026-06-15 16:00     | 2026-06-16   | 2026-06-17        |
| 2026-06-15 15:59:59   | 2026-06-15 16:00     | 2026-06-16   | 2026-06-17        |
| 2026-06-15 16:00:00   | 2026-06-16 16:00     | 2026-06-17   | 2026-06-18        |
| 2026-06-15 23:45      | 2026-06-16 16:00     | 2026-06-17   | 2026-06-18        |
| 2026-06-15 10:30 UTC  | 2026-06-16 16:00 IST | 2026-06-17   | 2026-06-18        |

(The last row is 16:00 IST exactly — it verifies UTC input is converted, not
compared naively.)

### 6.2 Pricing

Env-driven constants with these defaults:

```
PLATFORM_FEE_BPS=300      # 3.00% expressed in basis points
DELIVERY_FEE_PAISE=1500   # flat Rs. 15
```

```
subtotal_paise      = sum(order_items.line_total_paise)
line_total_paise    = unit_price_paise * qty
platform_fee_paise  = round_half_up(subtotal_paise * PLATFORM_FEE_BPS / 10000)
delivery_fee_paise  = DELIVERY_FEE_PAISE
total_paise         = subtotal + platform_fee + delivery_fee
```

Rounding: integer arithmetic, half-up. `(subtotal*bps + 5000) / 10000`.
Test with subtotals that land on .5 paise.

**Supplier payable for an order** = sum of that supplier's
`line_total_paise`. The platform fee and delivery fee are ours; suppliers
receive their listed prices in full. Delivery and platform charges are added
**on top** of the subtotal and paid by the customer.

Show the customer a clear breakdown at checkout: Subtotal, Platform fee (3%),
Delivery charge, Total.

### 6.3 Stock reservation (prevents overselling)

Stock belongs to a SIZE CODE, so every step below is per grade. Two customers
buying different grades of one produce take different locks and never wait on
each other; two buying the same grade still serialise.

1. On checkout, in ONE transaction:
   - `SELECT ... FOR UPDATE` the relevant `daily_availability` rows, ordered
     by size_code_id to avoid deadlocks
   - verify `total_grams - reserved_grams - sold_grams >= requested_grams`
     for every line, against that line's GRADE
   - verify `available_on = today (IST)` and `status = 'open'`
   - increment `reserved_grams`, insert `stock_reservations` rows with
     `status='held'`, `expires_at = now() + RESERVATION_TTL_MINUTES (default 15)`
   - create the order with `status='pending_payment'`
2. On payment success: move `reserved_grams -> sold_grams`, reservations
   `-> 'committed'`.
3. On payment failure/cancel/TTL expiry: decrement `reserved_grams`,
   reservations `-> 'released'`, order `-> 'expired'` or `'payment_failed'`.
4. A sweeper job releases expired reservations every minute — **except**
   where money may have been taken. A paying customer never loses their
   produce (decided with the product owner on 2026-10-02):
   - a `payment.captured` row in `webhook_events` whose processing failed →
     held until an admin records the payment or refunds it;
   - an order that went to Razorpay → released only after the sweeper asks
     Razorpay (`GET /v1/orders/{id}/payments`) and hears no payment is
     captured or authorized. Money taken with no webhook yet → held, and the
     late webhook completes the order. Razorpay unreachable → held, asked
     again next tick. The calls are made outside the claim transaction.

Write a concurrency test: N goroutines checking out the last available pack;
exactly one must succeed. And a second one for the property size codes exist
for: selling out one grade must leave its siblings untouched.

### 6.4 Razorpay payment flow

1. Client calls `POST /api/orders` -> we reserve stock, create our order, call
   Razorpay Orders API for `amount = total_paise`, currency INR,
   `receipt = order_number`, and return `razorpay_order_id` + public key id.
2. Client UI opens Razorpay Checkout.
3. On checkout callback, client posts `razorpay_order_id`,
   `razorpay_payment_id`, `razorpay_signature` to
   `POST /api/orders/{id}/verify-payment`. Verify:
   `HMAC_SHA256(razorpay_order_id + "|" + razorpay_payment_id, RAZORPAY_KEY_SECRET) == razorpay_signature`
4. **The webhook is the source of truth**, not the browser callback. Handle
   `payment.captured`, `payment.failed`, `refund.processed`. Verify
   `X-Razorpay-Signature` = `HMAC_SHA256(raw_request_body, RAZORPAY_WEBHOOK_SECRET)`
   using a **constant-time compare** against the **raw, unparsed body**.
   Configure the Node gateway to preserve the raw body on this route, or
   route the webhook directly to orders-api.
5. Dedupe by `x-razorpay-event-id` into `webhook_events`. Always return 200
   for events already processed. **An event is processed once it SUCCEEDED,
   not once it was received** (2026-10-02): a TEMPORARY failure (a database
   blip, a restart) leaves it open (`processed_at` NULL) and answers 5xx, so
   Razorpay's redelivery runs it again — safe because every handler is
   idempotent. A PERMANENT failure (amount or currency mismatch, no matching
   order) closes it with the error and answers 200: no retry can fix it, and
   Razorpay disables the whole webhook after 24 h of failures. A temporary
   failure that repeats `WEBHOOK_MAX_ATTEMPTS` times is closed the same way,
   with a `webhook.gave_up` alert.
6. On captured: order -> `paid`, commit reservations, create
   `supplier_payouts` rows (status `pending`), write order-confirmation email
   to outbox. All in one transaction.
7. Amounts must be validated against our own order total before marking paid.

Razorpay test-mode keys go in `.env`, never committed.

### 6.5 CSV product upload

Two-step: **validate/preview**, then **commit**.

- `POST /api/supplier/products/import` (multipart) -> parses, validates every
  row, returns `import_id` + a per-row report `{row, status, errors[], parsed}`.
  Nothing is written to `products` yet.
- `POST /api/supplier/products/import/{id}/commit` -> writes all valid rows
  in one transaction.

Columns (header row required, order-independent, case-insensitive):

```
name,type,grade,description,size_code,size_meta,unit_label,weight_grams,price_rupees,image_url,video_url
```

- Multiple rows with the same `name` + `grade` collapse into one product;
  within it, rows sharing a `size_code` collapse again into one grade with
  several packs. `size_code` is optional and defaults to `STD`, so a file
  written before grades existed still imports as a single-grade product.
- **Media collapses the same way, onto the GRADE.** Every distinct
  `image_url` and `video_url` across the rows of one size code joins that
  grade's gallery, deduplicated by URL — which is how a spreadsheet expresses
  several pictures without inventing a column syntax: repeat the row and vary
  the URL. Images are kept ahead of videos so the first entry is the cover
  whatever order the rows were written in. Each gallery is capped at
  `MAX_MEDIA_PER_SIZE_CODE`.
- Validate: name non-empty & <=120 chars; type in the enum; weight_grams a
  positive integer; price_rupees a positive number with <=2 decimals
  (converted to paise); image_url and video_url optional and each must be a
  valid http(s) URL (fetched and re-uploaded to MinIO server-side, with
  size/content-type limits). The SERVED content type decides whether a fetched
  file is an image or a video — not the column it came from and not the URL's
  extension — against the same allow-list a direct upload is held to, so a CSV
  cannot smuggle in a file type the presign endpoint would refuse.
- Limits: max 5 MB, max 2000 rows. Reject with a clear error beyond that.
- Provide a downloadable template CSV from the supplier UI.

### 6.6 Media upload (MinIO)

- Supplier UI requests `POST /api/uploads/presign` with content type and size,
  once per file.
- Server validates against one allow-list covering both kinds —
  `image/jpeg|png|webp` up to `MAX_IMAGE_BYTES` (5 MB), and
  `video/mp4|quicktime|webm` up to `MAX_VIDEO_BYTES` (50 MB) — generates a key
  like `products/{supplier_id}/{uuid}.{ext}`, and returns a **presigned PUT
  URL** (15 min TTL) plus the `kind` it resolved the content type to.
- The size cap depends on the kind, so it can only be applied once the content
  type is known; an unrecognised type is held to the image limit, the stricter
  of the two.
- Nothing transcodes video. The supplier apps downscale photos before
  uploading (1600px JPEG, which also strips the EXIF GPS of the farm) and cap
  recording at 30 seconds to stay inside the video limit.
- UI uploads directly to MinIO, then sends the object key back with the
  product payload.
- Reads: serve via presigned GET (1 hour TTL) generated at response time, or
  a public-read bucket behind a CDN later. Decide once and stay consistent.
- Bucket is created automatically on service startup if missing.

### 6.7 Wallet and scheduled / repeat orders

Decided with the product owner on 2026-09-26:

- **A customer picks DATES, never times.** An expected delivery date E is
  processed on E-2 and charged on E-2 at `cutoff - SCHEDULE_CHARGE_LEAD_MINUTES`
  (15:30 IST by default), i.e. before that day's cutoff, so the order is
  processed that day and draws on that day's stock like any checkout order.
  The order's timeline is computed from E, not from the clock (§6.1 read
  backwards: processing E-2 16:00, delivery_day E-1, expected E).
- **Earliest first date** = the expected date of an order placed now, + 1 day.
  Latest first date = today + 60. A repeat may run up to a year.
- **Repeats:** once, daily, weekly (chosen weekdays), monthly (day of month;
  29-31 fall on a shorter month's last day). Logic: `internal/recurrence`.
- **Charged from the prepaid wallet only.** Prices are TODAY's at the run
  (re-priced, as the cart is). Nothing is charged when the schedule is made.
- **Stock short at the run → deliver what is there.** Each line is filled as
  far as that grade's remaining grams allow; lines with nothing are dropped;
  the customer is emailed what was left out. Nothing at all → the date is
  skipped (`skipped_no_stock`) and nothing is charged. Logic:
  `internal/scheduling`.
- **Wallet can't cover it → skip and notify** (`skipped_low_balance`). After
  `SCHEDULE_LOW_BALANCE_PAUSE_AFTER` (3) in a row the schedule PAUSES.
  No partial charge from a short wallet; no stock is reserved.
- **Skip / edit / pause / cancel** are allowed until a date's charging time.
  After it the date is locked. Cancelling never touches placed orders.
- **The wallet is closed-loop.** In only via a Razorpay top-up credited by
  the capture WEBHOOK (never the browser, §6.4); out only as a delivery or an
  ADMIN refund to the payment it came from (split over top-ups newest first,
  audited). No customer withdrawal, no transfers. A refund Razorpay refuses is
  returned to the wallet.
- **One transaction per charged delivery:** order written, wallet debited,
  `applyOrderPaid` (the same transition a card capture runs: reservations
  committed, supplier payouts, confirmation email), occurrence recorded,
  schedule advanced. Stock is committed in the catalogue after commit.
- Limits (env): `WALLET_MIN_TOPUP_PAISE`, `WALLET_MAX_TOPUP_PAISE`,
  `WALLET_MAX_BALANCE_PAISE`.

**Required tests** (exist; keep them green): recurrence dates incl. month
ends and the 4 pm boundary; the planner's partial fills and grade isolation;
top-up credited once under replay and refused on an amount mismatch; a run
that delivers part of a schedule and charges once; a low-balance skip that
pauses and reserves nothing.

## 7. Roles and access control

| Endpoint group              | customer | supplier | admin | analyst |
|-----------------------------|----------|----------|-------|---------|
| Browse today's catalog      | yes      | yes      | yes   | no      |
| Cart, checkout, own orders  | yes      | no       | no    | no      |
| Own products & availability | no       | yes      | no    | no      |
| Any product / any order     | no       | no       | yes   | no      |
| Supplier approval, payouts  | no       | no       | yes   | no      |
| Analytics (`/api/analytics`) | no      | no       | yes   | yes     |

An **analyst** signs in to the admin console and sees only its Analytics
section. Seeded like admins (`SEED_ANALYST_*`), never self-signup. Not a kind
of admin: every admin route names `Role.Admin` alone at the gateway, so a new
admin route is closed to analysts without anyone remembering to close it.

Enforce ownership checks in the service layer, not just the UI. A supplier
must never be able to read or mutate another supplier's products,
availability, orders or payouts. Write a test for this.

## 8. Environment variables (single consolidated list)

Shared:
  APP_ENV, LOG_LEVEL, TZ=Asia/Kolkata

Gateway (vm-gateway-api):
  PORT, CORS_ALLOWED_ORIGINS,
  PROFILE_API_URL, CATALOG_API_URL, ORDERS_API_URL,
  JWT_PUBLIC_KEY (or JWT_SECRET), INTERNAL_SERVICE_TOKEN,
  RATE_LIMIT_WINDOW_MS, RATE_LIMIT_MAX

All Go services:
  PORT, DATABASE_URL, DB_MAX_CONNS, INTERNAL_SERVICE_TOKEN

vm-profile-api:
  JWT_SECRET, JWT_ACCESS_TTL_MIN, JWT_REFRESH_TTL_DAYS, BCRYPT_COST

vm-catalog-api:
  STORAGE_PROVIDER=minio, S3_ENDPOINT, S3_REGION, S3_BUCKET,
  S3_ACCESS_KEY, S3_SECRET_KEY, S3_FORCE_PATH_STYLE=true,
  S3_PUBLIC_BASE_URL, MAX_IMAGE_BYTES, MAX_VIDEO_BYTES,
  MAX_MEDIA_PER_SIZE_CODE, MAX_SIZE_CODES_PER_PRODUCT,
  MAX_CSV_BYTES, MAX_CSV_ROWS

Analytics (§5.4):
  EVENTS_CONSUMER_PORT=8084, EVENTS_CONSUMER_DATABASE_URL,
  EVENTS_CONSUMER_REPLICATION_URL, CDC_SLOT_NAME, CDC_PUBLICATION,
  CDC_STATUS_INTERVAL_SECONDS=10, ANALYTICS_API_PORT=8085,
  ANALYTICS_API_DATABASE_URL, ANALYTICS_API_URL (gateway),
  OUTBOX_RETENTION_DAYS=7 (catalog, orders),
  ANALYTICS_DB_USER/PASSWORD, ANALYTICS_READER_DB_USER/PASSWORD,
  EVENTS_CDC_DB_USER/PASSWORD

vm-orders-api:
  PLATFORM_FEE_BPS=300, DELIVERY_FEE_PAISE=1500,
  ORDER_CUTOFF_HOUR_IST=16, RESERVATION_TTL_MINUTES=15,
  WALLET_MIN_TOPUP_PAISE=10000, WALLET_MAX_TOPUP_PAISE=1000000,
  WALLET_MAX_BALANCE_PAISE=2000000, SCHEDULE_CHARGE_LEAD_MINUTES=30,
  SCHEDULE_LOW_BALANCE_PAUSE_AFTER=3, SCHEDULE_RUN_INTERVAL_SECONDS=60,
  RAZORPAY_KEY_ID, RAZORPAY_KEY_SECRET, RAZORPAY_WEBHOOK_SECRET,
  WEBHOOK_MAX_ATTEMPTS=10,
  SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASSWORD, SMTP_FROM,
  SUPPORT_EMAIL=vayal.mikrogreenz@gmail.com

Frontends (VITE_ prefixed):
  VITE_API_BASE_URL, VITE_RAZORPAY_KEY_ID, VITE_SUPPORT_EMAIL,
  VITE_APP_NAME
  vm-admin-ui also: VITE_ANALYTICS_REMOTE_URL (the remote's remoteEntry.js)
  vm-analytics-ui:  ANALYTICS_UI_PORT=5176, ANALYTICS_UI_PUBLIC_URL (its own
                    origin with a trailing "/"; unset locally = localhost)

The mobile apps (MOBILE_ prefixed; read by each app.config.ts at BUILD time
and baked into the binary, so they cannot be changed after a build):
  shared by both:  MOBILE_API_BASE_URL
  vm-supplier-mobile-ui: MOBILE_METRO_PORT=8090, MOBILE_EAS_PROJECT_ID
  vm-client-mobile-ui:   MOBILE_CLIENT_METRO_PORT=8091,
                         MOBILE_CLIENT_EAS_PROJECT_ID,
                         MOBILE_STOREFRONT_URL (optional)
  Both also read the shared SUPPORT_EMAIL and APP_ENV.
  MOBILE_API_BASE_URL must be the gateway as the PHONE sees it — "localhost"
  on a handset is the handset, so a device needs the machine's LAN address.
  It is shared because there is one gateway; `make -C infra mobile-ip` sets it.
  The Metro ports are not Metro's default 8081 (that is vm-profile-api's), and
  are one each so both dev servers can run at the same time.
  MOBILE_STOREFRONT_URL is where the web storefront is published; the customer
  app links its policy pages there rather than shipping a second copy of the
  legal text. Unset simply hides the links.
  Everything in an app's Expo `extra` block ships inside the binary and is
  PUBLIC. Never put a secret there.

Every one of these must appear in the relevant `.env.example` with a comment.
The app must fail fast on startup with a clear message if a required env var
is missing — no silent defaults for secrets or URLs.

## 9. Branding

- Name: **Vayalavan**. "Vayal" is the Tamil word for a farming field; a
  *vayalavan* is the one who works it. The shop is named for the grower, not
  the produce — which is why "Mikrogreenz" was dropped: it described one line
  in a catalogue that is mostly fruit and vegetables.
- Logo: an SVG in `packages/vm-ui-kit/src/logo/` — a stylised leaf/sprout over
  furrowed field lines. Must work as a full lockup (mark + wordmark), a
  standalone mark, and a favicon. Single-colour version required.
- Palette: deep field green primary, warm earth/soil secondary, off-white
  surface, amber accent for "available today" badges. Define as CSS custom
  properties / Tailwind theme tokens in vm-ui-kit — never hardcode hex values
  in components.
- Support email `vayal.mikrogreenz@gmail.com` appears in the footer of all
  three UIs, on both apps' sign-in and Account screens, on the order timeline,
  and in transactional emails.
- The mobile apps cannot import the Tailwind preset (they are outside the npm
  workspaces). Each `src/theme/tokens.ts` transcribes the ramps and its
  `tokens.test.ts` asserts every value still matches the preset — so a brand
  change made in vm-ui-kit and not there fails the build rather than shipping.
  Their app icons, adaptive icons, splash artwork, in-app lockups and the
  customer app's produce placeholder all come from the same masters via
  `make -C infra sync-brand`.
- The two apps carry the same artwork and differ by NAME: the customer app is
  "Vayalavan", the supplier app is "Vayalavan Supplier". Two store listings
  of one brand, not two brands.

## 10. Definition of done for any phase

- Compiles and runs via `make dev` / `docker compose up` from a clean checkout
- Migrations run forward and roll back cleanly
- `.env.example` updated
- Tests written for the logic listed in rule 9 and passing
- No secrets, no hardcoded URLs, no TODO stubs left in P0 paths
- README section updated with how to run and how to verify the new capability

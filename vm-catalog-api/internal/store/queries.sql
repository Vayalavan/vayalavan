-- Queries for vm-catalog-api. Generated into Go by sqlc (CLAUDE.md §3).
--
-- Ownership rule: every supplier-facing query takes supplier_id and filters on
-- it IN SQL. A supplier can therefore never receive another supplier's row,
-- even if a handler forgets a check — the wrong row is never fetched.

-- ===========================================================================
-- products
-- ===========================================================================

-- markup_bps is passed in rather than left to the column default: the
-- platform's default rate is an env var (PRODUCT_MARKUP_DEFAULT_BPS), and a
-- second copy of it in the schema is a second thing to change.
-- name: CreateProduct :one
INSERT INTO products (
    supplier_id, name, type, grade, description, status, markup_bps
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- Admin-only: no supplier filter.
-- name: GetProduct :one
SELECT * FROM products WHERE id = $1;

-- Supplier-scoped read. Ownership is the WHERE clause, not a later check.
-- name: GetProductForSupplier :one
SELECT * FROM products WHERE id = $1 AND supplier_id = $2;

-- name: UpdateProduct :one
UPDATE products SET
    name        = $3,
    type        = $4,
    grade       = $5,
    description = $6,
    status      = $7
WHERE id = $1 AND supplier_id = $2
RETURNING *;

-- Archive rather than delete: orders snapshot product details, but the
-- supplier's own history should still resolve (CLAUDE.md §5.3).
-- name: ArchiveProductForSupplier :one
UPDATE products SET status = 'archived'
WHERE id = $1 AND supplier_id = $2 AND status <> 'archived'
RETURNING *;

-- name: ArchiveProductAsAdmin :one
UPDATE products SET status = 'archived'
WHERE id = $1 AND status <> 'archived'
RETURNING *;

-- Sets the admin markup on a product. Admin only — a supplier must never be
-- able to reach this, and never sees the column it writes.
--
-- updated_at is deliberately NOT touched: it is the supplier's own signal for
-- when their listing last changed, and moving it because we repriced would
-- show a grower an edit they did not make.
-- name: SetProductMarkup :one
UPDATE products SET markup_bps = $2
WHERE id = $1
RETURNING *;

-- Filters are all optional (sqlc.narg): a NULL means "no filter". Written as
-- one query rather than assembled in Go, so there is no string concatenation
-- anywhere near the SQL (rule 7).
-- name: ListProducts :many
SELECT * FROM products
WHERE (sqlc.narg(supplier_id)::uuid IS NULL OR supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (sqlc.narg(type)::text   IS NULL OR type   = sqlc.narg(type)::text)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(search)::text IS NULL OR name ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountProducts :one
SELECT count(*) FROM products
WHERE (sqlc.narg(supplier_id)::uuid IS NULL OR supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (sqlc.narg(type)::text   IS NULL OR type   = sqlc.narg(type)::text)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(search)::text IS NULL OR name ILIKE '%' || sqlc.narg(search)::text || '%');

-- ===========================================================================
-- product_units
-- ===========================================================================

-- ===========================================================================
-- product_size_codes
-- ===========================================================================

-- name: CreateSizeCode :one
INSERT INTO product_size_codes (
    product_id, code, meta, is_active, sort_order, harvest_share_pct
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- Ownership check for the availability screen: resolves a size code only if it
-- belongs to a product of this supplier, and returns the product detail the
-- caller needs alongside — so a foreign id is a miss rather than a later
-- constraint violation.
-- name: GetSizeCodeForSupplier :one
SELECT
    sc.*,
    p.name  AS product_name,
    p.type  AS product_type,
    p.grade AS product_grade
FROM product_size_codes sc
JOIN products p ON p.id = sc.product_id
WHERE sc.id = $1 AND p.supplier_id = $2;

-- Admin/internal read by id, no supplier filter. Used to name a grade in an
-- error message on the reserve path.
-- name: GetSizeCode :one
SELECT * FROM product_size_codes WHERE id = $1;

-- name: ListSizeCodesForProduct :many
SELECT * FROM product_size_codes
WHERE product_id = $1
ORDER BY sort_order, created_at;

-- Bulk fetch for the list view: one query for a whole page of products rather
-- than N+1 round trips.
-- name: ListSizeCodesForProducts :many
SELECT * FROM product_size_codes
WHERE product_id = ANY($1::uuid[])
ORDER BY product_id, sort_order, created_at;

-- The atomic half of "replace the size-code tree": delete all, then
-- re-insert, inside one transaction. Pack options and media cascade with it.
-- name: DeleteSizeCodesForProduct :execrows
DELETE FROM product_size_codes WHERE product_id = $1;

-- ===========================================================================
-- product_pack_options
-- ===========================================================================

-- name: CreatePackOption :one
INSERT INTO product_pack_options (
    size_code_id, product_id, label, meta, weight_grams, price_paise, is_active, sort_order
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListPackOptionsForProduct :many
SELECT * FROM product_pack_options
WHERE product_id = $1
ORDER BY sort_order, weight_grams;

-- Every pack across a page of products, ordered so a caller can group by size
-- code without a second sort.
-- name: ListPackOptionsForProducts :many
SELECT p.* FROM product_pack_options p
JOIN product_size_codes s ON s.id = p.size_code_id
WHERE p.product_id = ANY($1::uuid[])
ORDER BY p.product_id, s.sort_order, s.created_at, p.sort_order, p.weight_grams;

-- ===========================================================================
-- product_media
-- ===========================================================================

-- The cover photograph for products referenced by an order.
--
-- Takes the FIRST size code's first image: an order line snapshots the pack it
-- bought but not which grade's gallery to show, and the default grade is the
-- one the storefront opens on.
--
-- No status filter: an order placed months ago must still show its produce,
-- and by then the product may well be archived. Order lines snapshot the name
-- and price for exactly this reason (CLAUDE.md §5.3); the image is the one
-- thing they cannot snapshot, because what is stored is an object key whose
-- URL is minted at read time.
-- name: ProductImagesForIDs :many
SELECT DISTINCT ON (m.product_id)
    m.product_id AS id,
    m.object_key AS image_key
FROM product_media m
LEFT JOIN product_size_codes s ON s.id = m.size_code_id
WHERE m.product_id = ANY($1::uuid[]) AND m.kind = 'image'
-- A grade's own picture wins; the product-level gallery is the fallback, so a
-- grower who only uploaded photographs of the farm still gets a thumbnail.
ORDER BY m.product_id, (m.size_code_id IS NULL), s.sort_order, s.created_at,
         m.sort_order, m.created_at;

-- name: ListMediaForSizeCode :many
SELECT * FROM product_media
WHERE size_code_id = $1
ORDER BY sort_order, created_at;

-- The galleries for a page of products, in one round trip. Ordered by product
-- then size code so a caller can walk the rows and group them without a
-- second sort.
--
-- LEFT JOIN, because a row with a NULL size_code_id belongs to the whole
-- product rather than to one grade. Those sort last within their product, so
-- a caller appending them to each grade's gallery gets the grower's order for
-- free — a grade's own pictures, then the farm.
-- name: ListMediaForProducts :many
SELECT m.* FROM product_media m
LEFT JOIN product_size_codes s ON s.id = m.size_code_id
WHERE m.product_id = ANY($1::uuid[])
ORDER BY m.product_id, (m.size_code_id IS NULL), s.sort_order, s.created_at,
         m.sort_order, m.created_at;

-- The product-level bucket alone: the pictures that are of the FARM, not of a
-- grade. Replaced as a whole on save, exactly as each grade's gallery is.
-- name: DeleteCommonMediaForProduct :execrows
DELETE FROM product_media WHERE product_id = $1 AND size_code_id IS NULL;

-- name: InsertProductMedia :one
INSERT INTO product_media (size_code_id, product_id, kind, object_key, content_type, sort_order)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- ===========================================================================
-- csv_imports
-- ===========================================================================

-- name: CreateCSVImport :one
INSERT INTO csv_imports (
    supplier_id, filename, status, total_rows, valid_rows, error_rows, report
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- Supplier-scoped: one supplier must not be able to commit another's import.
-- name: GetCSVImport :one
SELECT * FROM csv_imports WHERE id = $1 AND supplier_id = $2;

-- Filtered on status so a double-submit commits exactly once.
-- name: MarkCSVImportCommitted :one
UPDATE csv_imports
SET status = 'committed', committed_at = now()
WHERE id = $1 AND supplier_id = $2 AND status = 'preview'
RETURNING *;

-- name: ListCSVImports :many
SELECT * FROM csv_imports
WHERE supplier_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- ===========================================================================
-- daily_availability
-- ===========================================================================

-- The supplier's daily sheet: EVERY active size code, whether or not it has
-- been declared for this date. A LEFT JOIN rather than an inner one, because
-- "not declared today" is the state the screen most needs to show.
--
-- One row per SIZE CODE, not per product: the grades are separate crates with
-- separate gram pools, so they are declared separately. Ordered by product
-- then the grower's own grade order, so the client can group without sorting.
-- name: ListAvailabilitySheet :many
SELECT
    p.id           AS product_id,
    p.name         AS product_name,
    p.type         AS product_type,
    p.grade        AS product_grade,
    sc.id          AS size_code_id,
    sc.code        AS size_code,
    sc.meta        AS size_meta,
    sc.sort_order  AS size_sort_order,
    -- The cover: first image of THIS grade's gallery, or of the product-level
    -- one when the grade has no picture of its own. COALESCE'd to '' because
    -- sqlc infers a scalar subquery as NOT NULL, and a grade with no
    -- photograph would otherwise fail to scan. Empty string is "no cover".
    coalesce((SELECT m.object_key
                FROM product_media m
               WHERE m.kind = 'image'
                 AND (m.size_code_id = sc.id
                      OR (m.size_code_id IS NULL AND m.product_id = p.id))
               ORDER BY (m.size_code_id IS NULL), m.sort_order, m.created_at
               LIMIT 1), '')::text AS product_image_key,
    a.id           AS availability_id,
    a.total_grams,
    a.reserved_grams,
    a.sold_grams,
    a.status       AS availability_status
FROM products p
JOIN product_size_codes sc ON sc.product_id = p.id AND sc.is_active
LEFT JOIN daily_availability a
       ON a.size_code_id = sc.id AND a.available_on = $2
WHERE p.supplier_id = $1
  AND p.status = 'active'
ORDER BY p.name, sc.sort_order, sc.created_at;

-- name: GetAvailabilityForSizeCode :one
SELECT * FROM daily_availability
WHERE size_code_id = $1 AND available_on = $2;

-- Supplier-scoped by id, so one supplier cannot close another's row.
-- name: GetAvailabilityForSupplier :one
SELECT * FROM daily_availability WHERE id = $1 AND supplier_id = $2;

-- Upsert: re-declaring a product for the same day updates it rather than
-- failing on the (product_id, available_on) unique constraint.
-- Reserved and sold are never touched here — they belong to the order flow.
-- Updating the declared quantity must NOT reopen a closed product.
--
-- This previously forced status='open' on every conflict. Because the supplier
-- screen submits every row it has a number for, editing one product silently
-- reopened every other product the supplier had closed that day — stock they
-- had deliberately stopped selling went back on sale. Reopening is now its own
-- explicit action below. New rows still start open.
-- name: UpsertAvailability :one
INSERT INTO daily_availability (
    supplier_id, product_id, size_code_id, available_on, total_grams, status
)
VALUES ($1, $2, $3, $4, $5, 'open')
ON CONFLICT (size_code_id, available_on) DO UPDATE
SET total_grams = EXCLUDED.total_grams
RETURNING *;

-- name: CloseAvailability :one
UPDATE daily_availability
SET status = 'closed'
WHERE id = $1 AND supplier_id = $2 AND status = 'open'
RETURNING *;

-- The deliberate counterpart to closing. Scoped to the owning supplier, and to
-- rows that are actually closed, so a replay is a no-op rather than a silent
-- success.
-- name: ReopenAvailability :one
UPDATE daily_availability
SET status = 'open'
WHERE id = $1 AND supplier_id = $2 AND status = 'closed'
RETURNING *;

-- Source rows for copy-from-yesterday. Only products still active today are
-- worth copying, so the join filters them.
-- name: ListAvailabilityForDate :many
SELECT a.*
FROM daily_availability a
JOIN products p ON p.id = a.product_id AND p.status = 'active'
JOIN product_size_codes sc ON sc.id = a.size_code_id AND sc.is_active
WHERE a.supplier_id = $1 AND a.available_on = $2
ORDER BY p.name, sc.sort_order, sc.created_at;

-- ===========================================================================
-- customer catalogue
-- ===========================================================================

-- Everything a customer sees on a given business day, sellable or not.
--
-- The date is passed in by the CALLER, which always computes it server-side in
-- IST (CLAUDE.md rule 2) — a client-supplied date must never reach this query.
-- supplier_ids is the approved-supplier allow-list resolved from
-- vm-profile-api, so a suspended supplier's produce disappears from the
-- storefront without any catalog-side state.
--
-- Sold-out and closed produce is deliberately NOT filtered out. A product
-- declared for today that has since run out stays on the storefront as a
-- disabled card, so a customer who came looking for it learns it was listed
-- and went, rather than concluding we never sell it. Purchasability is decided
-- by the handler from status and the joined grams; the join to
-- daily_availability still bounds the page to what was declared for TODAY, so
-- produce nobody listed this morning is absent as before.
--
-- One row per PRODUCT even though stock is now per size code: the storefront
-- shows one CARD per product, and paging must count products, not grades.
-- The grams are therefore summed across the grades declared today, and
-- `any_sellable` is true if ANY grade can still be bought — a product whose M
-- has gone but whose XL has not is still on sale.
--
-- The per-grade rows the card needs to actually price something come from
-- ListSizeCodeAvailability, called with this page's product ids.
-- name: ListCatalog :many
SELECT
    p.*,
    sum(a.total_grams)::integer    AS total_grams,
    sum(a.reserved_grams)::integer AS reserved_grams,
    sum(a.sold_grams)::integer     AS sold_grams,
    bool_or(a.status = 'open'
            AND a.total_grams > a.reserved_grams + a.sold_grams)::boolean AS any_sellable
FROM products p
JOIN daily_availability a ON a.product_id = p.id
JOIN product_size_codes sc ON sc.id = a.size_code_id AND sc.is_active
WHERE a.available_on = sqlc.arg(available_on)::date
  AND p.status = 'active'
  AND p.supplier_id = ANY(sqlc.arg(supplier_ids)::uuid[])
  AND (sqlc.narg(type)::text  IS NULL OR p.type  = sqlc.narg(type)::text)
  AND (sqlc.narg(grade)::text IS NULL OR p.grade = sqlc.narg(grade)::text)
  AND (sqlc.narg(search)::text IS NULL OR p.name ILIKE '%' || sqlc.narg(search)::text || '%')
GROUP BY p.id
-- Sellable first, sold out last, alphabetical within each group: the shop
-- still leads with what can be bought, and paging stays stable because the
-- sort key is a function of the row, not of the request.
ORDER BY bool_or(a.status = 'open'
                 AND a.total_grams > a.reserved_grams + a.sold_grams) DESC,
         p.name
LIMIT $1 OFFSET $2;

-- Today's grades and their stock, for a page of products.
--
-- The companion to ListCatalog: that query pages over products, this one fills
-- in the grades each card and detail page offers. Ordered by the grower's own
-- grade order so the client groups without sorting.
-- name: ListSizeCodeAvailability :many
SELECT
    sc.id          AS size_code_id,
    sc.product_id,
    sc.code,
    sc.meta,
    sc.sort_order,
    -- Display only: decides how the grade's chip is drawn on the storefront.
    sc.harvest_share_pct,
    a.id           AS availability_id,
    a.total_grams,
    a.reserved_grams,
    a.sold_grams,
    a.status       AS availability_status
FROM product_size_codes sc
JOIN daily_availability a
     ON a.size_code_id = sc.id AND a.available_on = sqlc.arg(available_on)::date
WHERE sc.product_id = ANY(sqlc.arg(product_ids)::uuid[])
  AND sc.is_active
ORDER BY sc.product_id, sc.sort_order, sc.created_at;

-- DISTINCT because a product now joins one availability row per grade, and the
-- storefront counts cards.
-- name: CountCatalog :one
SELECT count(DISTINCT p.id)
FROM products p
JOIN daily_availability a ON a.product_id = p.id
JOIN product_size_codes sc ON sc.id = a.size_code_id AND sc.is_active
WHERE a.available_on = sqlc.arg(available_on)::date
  AND p.status = 'active'
  AND p.supplier_id = ANY(sqlc.arg(supplier_ids)::uuid[])
  AND (sqlc.narg(type)::text  IS NULL OR p.type  = sqlc.narg(type)::text)
  AND (sqlc.narg(grade)::text IS NULL OR p.grade = sqlc.narg(grade)::text)
  AND (sqlc.narg(search)::text IS NULL OR p.name ILIKE '%' || sqlc.narg(search)::text || '%');

-- How many of today's products can still be bought.
--
-- Counted separately from CountCatalog because the storefront now lists sold-
-- out cards too: "12 items available today" must mean twelve buyable items,
-- not twelve cards of which four are greyed out.
-- A product counts once if ANY of its grades can still be bought.
-- name: CountSellableCatalog :one
SELECT count(DISTINCT p.id)
FROM products p
JOIN daily_availability a ON a.product_id = p.id
JOIN product_size_codes sc ON sc.id = a.size_code_id AND sc.is_active
WHERE a.available_on = sqlc.arg(available_on)::date
  AND a.status = 'open'
  AND a.total_grams > a.reserved_grams + a.sold_grams
  AND p.status = 'active'
  AND p.supplier_id = ANY(sqlc.arg(supplier_ids)::uuid[])
  AND (sqlc.narg(type)::text  IS NULL OR p.type  = sqlc.narg(type)::text)
  AND (sqlc.narg(grade)::text IS NULL OR p.grade = sqlc.narg(grade)::text)
  AND (sqlc.narg(search)::text IS NULL OR p.name ILIKE '%' || sqlc.narg(search)::text || '%');

-- Distinct grades listed today, for the storefront's filter chips.
--
-- Sold-out rows count here too, or picking "Grade A" would empty a page that
-- was showing Grade A cards a moment earlier.
-- name: ListCatalogGrades :many
SELECT DISTINCT p.grade
FROM products p
JOIN daily_availability a ON a.product_id = p.id
JOIN product_size_codes sc ON sc.id = a.size_code_id AND sc.is_active
WHERE a.available_on = sqlc.arg(available_on)::date
  AND p.status = 'active'
  AND p.grade IS NOT NULL
  AND p.supplier_id = ANY(sqlc.arg(supplier_ids)::uuid[])
ORDER BY p.grade;

-- ===========================================================================
-- stock holds — CLAUDE.md §6.3
-- ===========================================================================

-- Everything vm-orders-api needs to price and validate a cart, for one day.
--
-- Deliberately its own query rather than reusing ListCatalog. orders-api used
-- to read the customer-facing /catalog endpoint, which was fine until prices
-- split in two: that endpoint returns the CUSTOMER price and must never
-- return the markup beside it, and orders-api needs both — the customer price
-- to charge, the markup to know which part of it is not the grower's. Reading
-- both from one row is also what stops the two drifting between calls.
--
-- No paging: this is one day's produce from a handful of growers, and a cart
-- can reference any of it.
-- name: ListInternalUnitsForDate :many
SELECT
    u.id            AS unit_id,
    u.label,
    u.weight_grams,
    u.price_paise,
    p.id            AS product_id,
    p.supplier_id,
    p.name          AS product_name,
    -- Type and grade travel to vm-orders-api so they can be SNAPSHOTTED onto
    -- the order line (CLAUDE.md §5.3). Without them an order cannot say what
    -- kind of produce it was for, and no report downstream can either — the
    -- orders schema has no way to look either one up later.
    p.type          AS product_type,
    p.grade,
    p.markup_bps,
    -- The SIZE CODE the pack belongs to. It travels for the same reason type
    -- and grade do: an order line has to be able to say it was 'M2, 150 g -
    -- 200 g', and the size code id is what the stock hold is taken against.
    sc.id           AS size_code_id,
    sc.code         AS size_code,
    sc.meta         AS size_meta,
    a.total_grams,
    a.reserved_grams,
    a.sold_grams,
    a.status        AS availability_status
FROM product_pack_options u
JOIN product_size_codes sc ON sc.id = u.size_code_id
JOIN products p ON p.id = u.product_id
JOIN daily_availability a ON a.size_code_id = sc.id
    AND a.available_on = sqlc.arg(available_on)::date
WHERE u.is_active
  AND sc.is_active
  AND p.status = 'active'
  AND p.supplier_id = ANY(sqlc.arg(supplier_ids)::uuid[])
ORDER BY p.id, sc.sort_order, sc.created_at, u.sort_order, u.weight_grams;

-- Today's declared stock for a specific set of products, unlocked.
--
-- Feeds the cart's "you have asked for more than is left" check, which runs on
-- every cart read and must not take row locks or serialise against checkouts.
-- The numbers are therefore advisory: the authoritative check is the locked
-- one in LockAvailabilityForUpdate at reservation time. A product with no row
-- for the day simply does not come back, and the caller reads that as none
-- available.
-- name: AvailabilityForSizeCodes :many
SELECT
    a.size_code_id,
    a.product_id,
    p.name  AS product_name,
    sc.code AS size_code,
    a.total_grams,
    a.reserved_grams,
    a.sold_grams,
    a.status
FROM daily_availability a
JOIN product_size_codes sc ON sc.id = a.size_code_id
JOIN products p ON p.id = a.product_id
WHERE a.available_on = $1 AND a.size_code_id = ANY($2::uuid[]);

-- Locks one availability row for update.
--
-- The lock is now per SIZE CODE, which is what a gram pool belongs to: two
-- customers buying different grades of the same produce no longer serialise
-- against each other, and two buying the same grade still do.
--
-- Callers MUST take these in a deterministic order (by size_code_id) across
-- the whole request, or two concurrent checkouts touching the same two grades
-- can deadlock by grabbing them in opposite orders.
-- name: LockAvailabilityForUpdate :one
SELECT * FROM daily_availability
WHERE size_code_id = $1 AND available_on = $2
FOR UPDATE;

-- name: IncrementReservedGrams :one
UPDATE daily_availability
SET reserved_grams = reserved_grams + $3
WHERE size_code_id = $1 AND available_on = $2
RETURNING *;

-- Releasing returns grams to the pool. GREATEST guards against a double
-- release ever driving the counter negative.
-- name: DecrementReservedGrams :one
UPDATE daily_availability
SET reserved_grams = GREATEST(0, reserved_grams - $3)
WHERE size_code_id = $1 AND available_on = $2
RETURNING *;

-- Committing moves grams from reserved to sold; the total is unchanged.
-- name: CommitReservedGrams :one
UPDATE daily_availability
SET reserved_grams = GREATEST(0, reserved_grams - $3),
    sold_grams     = sold_grams + $3
WHERE size_code_id = $1 AND available_on = $2
RETURNING *;

-- name: CreateStockHold :one
INSERT INTO stock_holds (
    order_ref, product_id, size_code_id, available_on, grams, expires_at
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- Filtered on status so a double release or commit is a no-op rather than
-- double-counting grams.
-- name: SettleStockHold :one
UPDATE stock_holds
SET status = $2, settled_at = now()
WHERE id = $1 AND status = 'held'
RETURNING *;

-- name: ListHoldsForOrder :many
SELECT * FROM stock_holds WHERE order_ref = $1 ORDER BY created_at;

-- The sweeper's claim query. FOR UPDATE SKIP LOCKED lets several replicas
-- sweep concurrently without fighting over the same rows.
-- name: ClaimExpiredHolds :many
SELECT * FROM stock_holds
WHERE status = 'held' AND expires_at < now()
ORDER BY expires_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- One product for the customer-facing detail page.
--
-- LEFT JOIN, unlike ListCatalog's inner join, and no sold-out filter: someone
-- following a shared link to produce that has since sold out should reach the
-- page and be told so, not a 404 that reads as "this never existed". The
-- handler decides purchasability from the joined numbers; NULLs mean nothing
-- is declared for today.
--
-- The supplier and status guards stay: a suspended grower's produce must not
-- be reachable by URL just because it is not reachable by browsing.
-- The product itself. Its grades and their stock come from
-- ListSizeCodeAvailability, so this stays one row however many grades there
-- are — no aggregate, no LEFT JOIN fan-out.
-- name: GetCatalogProduct :one
SELECT p.*
FROM products p
WHERE p.id = sqlc.arg(id)
  AND p.status = 'active'
  AND p.supplier_id = ANY(sqlc.arg(supplier_ids)::uuid[]);

-- ===========================================================================
-- analytics product events — CLAUDE.md §5.4
-- ===========================================================================

-- Written in the caller's transaction, next to the change it describes.
-- name: InsertProductEvent :one
INSERT INTO product_events_outbox (
    aggregate_id, event_type, schema_version, occurred_at, actor_role, request_id, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- Retention. CDC has already read these from the WAL.
-- name: PruneProductEvents :execrows
DELETE FROM product_events_outbox WHERE created_at < $1;

-- The backfill's cursor: every product, oldest first, a page at a time.
-- name: ListProductIDsAfter :many
SELECT id, created_at FROM products
WHERE (created_at, id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY created_at, id
LIMIT sqlc.arg(page_size);

-- ===========================================================================
-- development seed (mocks/)
-- ===========================================================================

-- The seed's idempotency check, in the same terms as the unique index
-- products_supplier_name_grade_key: a live product of this name and grade.
-- name: ProductExistsForSupplier :one
SELECT EXISTS (
    SELECT 1 FROM products
    WHERE supplier_id = $1
      AND lower(btrim(name)) = lower(btrim(sqlc.arg(name)::text))
      AND lower(coalesce(btrim(grade), '')) = lower(coalesce(btrim(sqlc.narg(grade)::text), ''))
      AND status <> 'archived'
) AS exists;

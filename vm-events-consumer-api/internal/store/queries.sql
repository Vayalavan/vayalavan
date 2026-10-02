-- Queries for the analytics projections (CLAUDE.md §5.4).
--
-- Every write here is idempotent under replay: an upsert keyed on the
-- aggregate, guarded so a STALE event (older than what the row already
-- reflects) cannot roll it back.

-- ===========================================================================
-- bookkeeping
-- ===========================================================================

-- Returns a row only the FIRST time an event id is seen.
-- name: ClaimEvent :one
INSERT INTO processed_events (event_id, source_table, lsn)
VALUES ($1, $2, $3)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id;

-- name: DeadLetter :exec
INSERT INTO dead_letter_events (event_id, source_table, event_type, payload, error, lsn)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING;

-- name: PruneProcessedEvents :execrows
DELETE FROM processed_events WHERE processed_at < $1;

-- ===========================================================================
-- orders
-- ===========================================================================

-- Lifecycle timestamps are never cleared and never moved: COALESCE keeps the
-- first time a transition was seen. The WHERE makes a stale event a no-op.
-- name: UpsertOrder :execrows
INSERT INTO orders (
    order_id, order_number, customer_id, schedule_id, source, status,
    payment_method, paid_via,
    subtotal_paise, platform_fee_paise, delivery_fee_paise, total_paise,
    markup_paise, supplier_payable_paise, item_count, total_qty, total_grams,
    placed_at, paid_at, processed_at, dispatched_at, cancelled_at, expired_at,
    refunded_at, cancelled_from_status,
    placed_date_ist, placed_hour_ist, placed_before_cutoff, processing_at,
    delivery_day, expected_delivery_date, ship_city, ship_state, ship_pincode,
    last_event_id, last_event_type, last_event_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32,
    $33, $34, $35, $36, $37, now()
)
ON CONFLICT (order_id) DO UPDATE SET
    schedule_id            = EXCLUDED.schedule_id,
    status                 = EXCLUDED.status,
    payment_method         = COALESCE(EXCLUDED.payment_method, orders.payment_method),
    paid_via               = COALESCE(orders.paid_via, EXCLUDED.paid_via),
    subtotal_paise         = EXCLUDED.subtotal_paise,
    platform_fee_paise     = EXCLUDED.platform_fee_paise,
    delivery_fee_paise     = EXCLUDED.delivery_fee_paise,
    total_paise            = EXCLUDED.total_paise,
    markup_paise           = EXCLUDED.markup_paise,
    supplier_payable_paise = COALESCE(EXCLUDED.supplier_payable_paise, orders.supplier_payable_paise),
    item_count             = EXCLUDED.item_count,
    total_qty              = EXCLUDED.total_qty,
    total_grams            = EXCLUDED.total_grams,
    paid_at                = COALESCE(orders.paid_at, EXCLUDED.paid_at),
    processed_at           = COALESCE(orders.processed_at, EXCLUDED.processed_at),
    dispatched_at          = COALESCE(orders.dispatched_at, EXCLUDED.dispatched_at),
    cancelled_at           = COALESCE(orders.cancelled_at, EXCLUDED.cancelled_at),
    expired_at             = COALESCE(orders.expired_at, EXCLUDED.expired_at),
    refunded_at            = COALESCE(orders.refunded_at, EXCLUDED.refunded_at),
    cancelled_from_status  = COALESCE(orders.cancelled_from_status, EXCLUDED.cancelled_from_status),
    ship_city              = EXCLUDED.ship_city,
    ship_state             = EXCLUDED.ship_state,
    ship_pincode           = EXCLUDED.ship_pincode,
    last_event_id          = EXCLUDED.last_event_id,
    last_event_type        = EXCLUDED.last_event_type,
    last_event_at          = EXCLUDED.last_event_at,
    updated_at             = now()
WHERE orders.last_event_at <= EXCLUDED.last_event_at;

-- Lines never change after placement, but an upsert costs nothing and means a
-- snapshot can repair a line written by an older consumer.
-- name: UpsertOrderItem :exec
INSERT INTO order_items (
    order_item_id, order_id, supplier_id, product_id, size_code_id,
    pack_option_id, product_name, product_type, grade, size_code, pack_label,
    weight_grams, qty, unit_price_paise, line_total_paise, line_markup_paise,
    line_grams
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (order_item_id) DO UPDATE SET
    product_name      = EXCLUDED.product_name,
    product_type      = EXCLUDED.product_type,
    grade             = EXCLUDED.grade,
    size_code         = EXCLUDED.size_code,
    pack_label        = EXCLUDED.pack_label,
    weight_grams      = EXCLUDED.weight_grams,
    qty               = EXCLUDED.qty,
    unit_price_paise  = EXCLUDED.unit_price_paise,
    line_total_paise  = EXCLUDED.line_total_paise,
    line_markup_paise = EXCLUDED.line_markup_paise,
    line_grams        = EXCLUDED.line_grams;

-- ===========================================================================
-- products
-- ===========================================================================

-- name: UpsertProduct :execrows
INSERT INTO products (
    product_id, supplier_id, name, type, grade, status, markup_bps, source,
    size_code_count, pack_count, active_pack_count, min_price_paise,
    max_price_paise, image_count, video_count, created_at, first_active_at,
    archived_at, last_event_id, last_event_type, last_event_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21, now()
)
ON CONFLICT (product_id) DO UPDATE SET
    name              = EXCLUDED.name,
    type              = EXCLUDED.type,
    grade             = EXCLUDED.grade,
    status            = EXCLUDED.status,
    markup_bps        = EXCLUDED.markup_bps,
    -- How it was CREATED; a later edit through the form does not change that.
    source            = products.source,
    size_code_count   = EXCLUDED.size_code_count,
    pack_count        = EXCLUDED.pack_count,
    active_pack_count = EXCLUDED.active_pack_count,
    min_price_paise   = EXCLUDED.min_price_paise,
    max_price_paise   = EXCLUDED.max_price_paise,
    image_count       = EXCLUDED.image_count,
    video_count       = EXCLUDED.video_count,
    first_active_at   = COALESCE(products.first_active_at, EXCLUDED.first_active_at),
    -- Set while archived, cleared if it comes back.
    archived_at       = CASE WHEN EXCLUDED.status = 'archived'
                             THEN COALESCE(products.archived_at, EXCLUDED.archived_at)
                             END,
    last_event_id     = EXCLUDED.last_event_id,
    last_event_type   = EXCLUDED.last_event_type,
    last_event_at     = EXCLUDED.last_event_at,
    updated_at        = now()
WHERE products.last_event_at <= EXCLUDED.last_event_at;

-- name: CurrentPacksForProduct :many
SELECT * FROM product_packs
WHERE product_id = $1 AND valid_to IS NULL;

-- name: ClosePack :exec
UPDATE product_packs SET valid_to = $3
WHERE pack_option_id = $1 AND valid_from = $2 AND valid_to IS NULL;

-- Two versions of one pack at the same instant (two events in one source
-- transaction) collapse into the later one rather than violating the key.
-- name: OpenPack :exec
INSERT INTO product_packs (
    pack_option_id, valid_from, product_id, size_code_id, size_code, size_meta,
    harvest_share_pct, size_code_active, label, weight_grams, price_paise,
    markup_bps, customer_price_paise, is_active
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (pack_option_id, valid_from) DO UPDATE SET
    valid_to             = NULL,
    size_code            = EXCLUDED.size_code,
    size_meta            = EXCLUDED.size_meta,
    harvest_share_pct    = EXCLUDED.harvest_share_pct,
    size_code_active     = EXCLUDED.size_code_active,
    label                = EXCLUDED.label,
    weight_grams         = EXCLUDED.weight_grams,
    price_paise          = EXCLUDED.price_paise,
    markup_bps           = EXCLUDED.markup_bps,
    customer_price_paise = EXCLUDED.customer_price_paise,
    is_active            = EXCLUDED.is_active;

-- ===========================================================================
-- suppliers
-- ===========================================================================

-- approved_at and suspended_at keep the first time each was seen; a later
-- event never clears them. The WHERE makes a stale event a no-op.
-- name: UpsertSupplier :execrows
INSERT INTO suppliers (
    supplier_id, business_name, status, city, state, pincode, gst_registered,
    commission_bps, created_at, approved_at, suspended_at,
    last_event_id, last_event_type, last_event_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
ON CONFLICT (supplier_id) DO UPDATE SET
    business_name   = EXCLUDED.business_name,
    status          = EXCLUDED.status,
    city            = EXCLUDED.city,
    state           = EXCLUDED.state,
    pincode         = EXCLUDED.pincode,
    gst_registered  = EXCLUDED.gst_registered,
    commission_bps  = EXCLUDED.commission_bps,
    approved_at     = COALESCE(suppliers.approved_at, EXCLUDED.approved_at),
    suspended_at    = COALESCE(suppliers.suspended_at, EXCLUDED.suspended_at),
    last_event_id   = EXCLUDED.last_event_id,
    last_event_type = EXCLUDED.last_event_type,
    last_event_at   = EXCLUDED.last_event_at,
    updated_at      = now()
WHERE suppliers.last_event_at <= EXCLUDED.last_event_at;

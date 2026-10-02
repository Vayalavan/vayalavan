-- Queries for vm-orders-api. Generated into Go by sqlc (CLAUDE.md §3).
--
-- Ownership rule: every customer-facing query takes customer_id and filters on
-- it IN SQL, so one customer's cart or order is never returned to another.

-- ===========================================================================
-- carts
-- ===========================================================================

-- One live cart per customer; the upsert makes "get or create" a single round
-- trip with no race between two tabs.
-- name: GetOrCreateCart :one
INSERT INTO carts (customer_id) VALUES ($1)
ON CONFLICT (customer_id) DO UPDATE SET customer_id = EXCLUDED.customer_id
RETURNING *;

-- name: ListCartItems :many
SELECT * FROM cart_items WHERE cart_id = $1 ORDER BY created_at;

-- Adding the same pack again bumps quantity rather than creating a duplicate
-- line, which is what a customer means by pressing Add twice.
-- name: UpsertCartItem :one
INSERT INTO cart_items (cart_id, product_unit_id, product_id, supplier_id, qty)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (cart_id, product_unit_id) DO UPDATE
SET qty = LEAST(99, cart_items.qty + EXCLUDED.qty)
RETURNING *;

-- name: SetCartItemQty :one
UPDATE cart_items SET qty = $3 WHERE cart_id = $1 AND id = $2
RETURNING *;

-- name: DeleteCartItem :execrows
DELETE FROM cart_items WHERE cart_id = $1 AND id = $2;

-- name: ClearCart :execrows
DELETE FROM cart_items WHERE cart_id = $1;

-- ===========================================================================
-- orders
-- ===========================================================================

-- name: CreateOrder :one
INSERT INTO orders (
    order_number, customer_id, status, address_snapshot,
    subtotal_paise, platform_fee_paise, delivery_fee_paise, total_paise,
    placed_at, processing_at, delivery_day, expected_delivery_date
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- Ownership is the WHERE clause, not a later check.
-- name: GetOrderForCustomer :one
SELECT * FROM orders WHERE id = $1 AND customer_id = $2;

-- name: ListOrdersForCustomer :many
SELECT * FROM orders WHERE customer_id = $1
ORDER BY placed_at DESC
LIMIT $2 OFFSET $3;

-- name: CountOrdersForCustomer :one
SELECT count(*) FROM orders WHERE customer_id = $1;

-- name: SetOrderStatus :one
UPDATE orders SET status = $2 WHERE id = $1 RETURNING *;

-- Expires an order whose held stock has lapsed.
--
-- Filtered on pending_payment, and that filter is the whole point. The sweeper
-- used to call SetOrderStatus, which has no guard, so a payment that committed
-- while the sweep was in flight could be overwritten with 'expired' — money
-- captured, stock released, order dead. Every other transition in this file is
-- filtered on its only legal predecessor (see MarkOrderPaid); this one was the
-- exception.
--
-- Zero rows updated means the order moved on while we were working, which is
-- not an error: the reservation rows have already been released, and an order
-- that reached 'paid' has its own committed reservations.
-- name: ExpireOrderIfUnpaid :execrows
UPDATE orders SET status = 'expired'
WHERE id = $1 AND status = 'pending_payment';

-- Serial per IST day, for the human-friendly VM-YYMMDD-NNNN number.
-- name: CountOrdersPlacedOn :one
SELECT count(*) FROM orders
WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date = $1;

-- unit_price_paise and line_total_paise are what the CUSTOMER paid; the markup
-- columns record which part of that was ours, and at what rate. Subtracting is
-- what keeps a grower's payout on their own price (CLAUDE.md §6.2).
-- name: CreateOrderItem :one
INSERT INTO order_items (
    order_id, supplier_id, product_id, product_unit_id,
    size_code_id, size_code_snapshot, size_meta_snapshot,
    product_name_snapshot, unit_label_snapshot, grade_snapshot,
    product_type_snapshot, weight_grams,
    unit_price_paise, qty, line_total_paise,
    markup_bps, markup_paise, line_markup_paise
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
RETURNING *;

-- name: ListOrderItems :many
SELECT * FROM order_items WHERE order_id = $1 ORDER BY created_at;

-- name: ListOrderItemsForOrders :many
SELECT * FROM order_items WHERE order_id = ANY($1::uuid[]) ORDER BY order_id, created_at;

-- ===========================================================================
-- idempotency — CLAUDE.md rule 6
-- ===========================================================================

-- The unique constraint IS the guard. A replayed key collides, and the handler
-- returns the original order instead of charging twice.
-- name: ClaimIdempotencyKey :one
INSERT INTO order_idempotency (key, customer_id, order_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: LookupIdempotencyKey :one
SELECT * FROM order_idempotency WHERE customer_id = $1 AND key = $2;

-- ===========================================================================
-- stock_reservations (orders-side mirror of catalog holds)
-- ===========================================================================

-- name: CreateStockReservation :one
INSERT INTO stock_reservations (
    order_id, product_id, catalog_reservation_id, available_on, grams, expires_at
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListReservationsForOrder :many
SELECT * FROM stock_reservations WHERE order_id = $1 ORDER BY created_at;

-- Filtered on 'held' so a retried settle is a no-op rather than double-counting.
-- name: SettleReservation :one
UPDATE stock_reservations SET status = $2
WHERE id = $1 AND status = 'held'
RETURNING *;

-- The sweeper's claim. SKIP LOCKED lets replicas sweep in parallel without
-- contending for the same rows.
--
-- A hold whose order Razorpay has reported CAPTURED is never claimed, however
-- long it has lapsed. The webhook event is recorded before it is processed, so
-- a capture whose processing failed (a database blip, an amount mismatch) still
-- shows here while the order sits in pending_payment. Releasing that stock
-- would sell a paying customer's produce to someone else; holding it until an
-- admin records the payment or refunds it costs only grams.
--
-- And a hold whose order went to Razorpay is claimed only once the sweeper has
-- ASKED Razorpay and been told no money was taken (`cleared`, from
-- ListLapsedRazorpayOrders). That covers the capture whose webhook has not
-- arrived at all yet — an outage, a backlog — which leaves no row above to
-- find. An order that never reached Razorpay has nothing to ask about.
-- name: ClaimExpiredReservations :many
SELECT r.* FROM stock_reservations r
JOIN orders o ON o.id = r.order_id
WHERE r.status = 'held' AND r.expires_at < now()
  AND (o.razorpay_order_id IS NULL OR r.order_id = ANY(sqlc.arg(cleared)::uuid[]))
  AND NOT EXISTS (
      SELECT 1 FROM webhook_events w
      WHERE w.event_type = 'payment.captured'
        AND w.payload -> 'payload' -> 'payment' -> 'entity' ->> 'order_id' = o.razorpay_order_id
  )
ORDER BY r.expires_at
LIMIT sqlc.arg(row_limit)
FOR UPDATE OF r SKIP LOCKED;

-- The orders the sweeper must check with Razorpay before it may claim their
-- lapsed holds: those that went to Razorpay and have no recorded capture
-- (one with a capture is held regardless, so asking would be a wasted call).
-- Oldest first, the order the claim works in.
-- name: ListLapsedRazorpayOrders :many
SELECT o.id, o.razorpay_order_id::text AS razorpay_order_id
FROM orders o
JOIN stock_reservations r ON r.order_id = o.id
WHERE r.status = 'held' AND r.expires_at < now()
  AND o.razorpay_order_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM webhook_events w
      WHERE w.event_type = 'payment.captured'
        AND w.payload -> 'payload' -> 'payment' -> 'entity' ->> 'order_id' = o.razorpay_order_id
  )
GROUP BY o.id, o.razorpay_order_id
ORDER BY min(r.expires_at)
LIMIT $1;

-- ===========================================================================
-- outbox
-- ===========================================================================

-- name: EnqueueOutbox :one
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- ===========================================================================
-- payments, webhook events, payouts — CLAUDE.md §6.4
-- ===========================================================================

-- name: CreatePayment :one
INSERT INTO payments (order_id, provider_order_id, amount_paise, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPaymentForOrder :one
SELECT * FROM payments WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: MarkPaymentCaptured :one
UPDATE payments
SET status = 'captured', provider_payment_id = $2, method = $3, raw_payload = $4
WHERE order_id = $1
RETURNING *;

-- name: MarkPaymentFailed :one
UPDATE payments
SET status = 'failed', provider_payment_id = $2, raw_payload = $3
WHERE order_id = $1
RETURNING *;

-- name: SetOrderRazorpayOrderID :one
UPDATE orders SET razorpay_order_id = $2 WHERE id = $1 RETURNING *;

-- Marks an order paid. Filtered on pending_payment so a replayed capture
-- cannot re-run the transition, and a cancelled order cannot be resurrected.
-- name: MarkOrderPaid :one
UPDATE orders
SET status = 'paid', payment_status = 'paid', razorpay_payment_id = $2
WHERE id = $1 AND status = 'pending_payment'
RETURNING *;

-- name: MarkOrderPaymentFailed :one
UPDATE orders
SET status = $2, payment_status = 'failed'
WHERE id = $1 AND status = 'pending_payment'
RETURNING *;

-- name: GetOrderByRazorpayOrderID :one
SELECT * FROM orders WHERE razorpay_order_id = $1;

-- name: GetOrderByID :one
SELECT * FROM orders WHERE id = $1;

-- The dedupe guard: the unique constraint on provider_event_id means a
-- replayed delivery collides here and is never processed twice.
-- Records a delivery and claims it for processing, in one statement.
--
-- A first delivery inserts. A redelivery of an event still open (processed_at
-- NULL: its last attempt failed for a temporary reason, or is running now)
-- counts another attempt and returns the row, so it is processed again —
-- safe, because every handler is idempotent. A redelivery of a CLOSED event
-- matches the WHERE on the conflict update, updates nothing, and returns no
-- row: pgx.ErrNoRows means "already done, answer 200".
-- name: RecordWebhookEvent :one
INSERT INTO webhook_events (provider_event_id, event_type, payload)
VALUES ($1, $2, $3)
ON CONFLICT (provider_event_id) DO UPDATE
    SET attempts = webhook_events.attempts + 1
    WHERE webhook_events.processed_at IS NULL
RETURNING *;

-- name: GetWebhookEvent :one
SELECT * FROM webhook_events WHERE provider_event_id = $1;

-- Closes an event: processed successfully (error NULL), or failed in a way
-- no retry can fix (error set). A closed event is never processed again.
-- Keyed by provider_event_id, which is what the handler has in hand.
-- name: MarkWebhookProcessed :one
UPDATE webhook_events SET processed_at = now(), error = $2
WHERE provider_event_id = $1
RETURNING *;

-- Records a TEMPORARY failure and leaves the event open, so Razorpay's next
-- redelivery processes it again.
-- name: MarkWebhookRetryable :one
UPDATE webhook_events SET error = $2
WHERE provider_event_id = $1 AND processed_at IS NULL
RETURNING *;

-- One row per (order, supplier). The unique constraint makes payout creation
-- idempotent: a replayed capture cannot double-pay a supplier.
-- name: CreateSupplierPayout :one
INSERT INTO supplier_payouts (supplier_id, order_id, amount_paise)
VALUES ($1, $2, $3)
ON CONFLICT (order_id, supplier_id) DO NOTHING
RETURNING *;

-- name: ListPayoutsForOrder :many
SELECT * FROM supplier_payouts WHERE order_id = $1 ORDER BY created_at;

-- Sums each supplier's lines on an order, for the payout rows.
--
-- MINUS the markup. line_total_paise is what the customer paid, which includes
-- what we added on top of the grower's price; paying that out would hand every
-- supplier our own margin. The markup is snapshotted on the line, so this
-- figure never moves when an admin reprices the product tomorrow.
-- name: SumOrderItemsBySupplier :many
SELECT
    supplier_id,
    sum(line_total_paise - line_markup_paise)::bigint AS amount_paise
FROM order_items WHERE order_id = $1
GROUP BY supplier_id
ORDER BY supplier_id;

-- Held reservations for an order, LOCKED.
--
-- FOR UPDATE so the payment transaction owns these rows before it commits them.
-- The sweeper claims expired reservations with SKIP LOCKED, so it steps around
-- an order being paid for rather than racing it to the order's status column.
-- name: ListHeldReservationsForOrder :many
SELECT * FROM stock_reservations WHERE order_id = $1 AND status = 'held'
FOR UPDATE;

-- ===========================================================================
-- outbox dispatcher
-- ===========================================================================

-- SKIP LOCKED so several dispatcher replicas can drain the queue in parallel.
-- name: ClaimOutboxBatch :many
SELECT * FROM outbox
WHERE published_at IS NULL
ORDER BY created_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxPublished :one
UPDATE outbox SET published_at = now() WHERE id = $1 RETURNING *;

-- name: MarkOutboxFailed :one
UPDATE outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1 RETURNING *;

-- Puts a claimed row back after a failed send.
--
-- Claim-then-send, released on failure — the same shape as the daily report,
-- and for the same reason: an SMTP round trip inside the claiming transaction
-- would hold a pooled connection open across a network call, and a slow relay
-- would drain the pool while Postgres sat idle.
--
-- attempts is incremented here too, so a row that keeps failing is visible
-- without joining anything.
-- name: ReleaseOutbox :execrows
UPDATE outbox
SET published_at = NULL, attempts = attempts + 1, last_error = $2
WHERE id = $1;

-- One order with everything an order-confirmation email needs.
--
-- The customer's ADDRESS is not here: it lives in the profile schema, which
-- this service may not read (CLAUDE.md §3). The dispatcher fetches it over
-- HTTP from vm-profile-api.
-- name: OrderForEmail :one
SELECT * FROM orders WHERE id = $1;

-- ===========================================================================
-- admin: orders
-- ===========================================================================

-- Every filter is optional (sqlc.narg). Written as one statement rather than
-- assembled in Go, so no string concatenation goes near the SQL (rule 7).
-- name: AdminListOrders :many
SELECT o.* FROM orders o
WHERE (sqlc.narg(status)::text IS NULL OR o.status = sqlc.narg(status)::text)
  AND (sqlc.narg(customer_id)::uuid IS NULL OR o.customer_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(order_number)::text IS NULL
       OR o.order_number ILIKE '%' || sqlc.narg(order_number)::text || '%')
  AND (sqlc.narg(placed_from)::timestamptz IS NULL OR o.placed_at >= sqlc.narg(placed_from)::timestamptz)
  AND (sqlc.narg(placed_to)::timestamptz   IS NULL OR o.placed_at <  sqlc.narg(placed_to)::timestamptz)
  -- Supplier filter is an EXISTS over the order's lines, so an order shows up
  -- if ANY of its items came from that supplier.
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM order_items oi
        WHERE oi.order_id = o.id AND oi.supplier_id = sqlc.narg(supplier_id)::uuid))
ORDER BY o.placed_at DESC
LIMIT $1 OFFSET $2;

-- name: AdminCountOrders :one
SELECT count(*) FROM orders o
WHERE (sqlc.narg(status)::text IS NULL OR o.status = sqlc.narg(status)::text)
  AND (sqlc.narg(customer_id)::uuid IS NULL OR o.customer_id = sqlc.narg(customer_id)::uuid)
  AND (sqlc.narg(order_number)::text IS NULL
       OR o.order_number ILIKE '%' || sqlc.narg(order_number)::text || '%')
  AND (sqlc.narg(placed_from)::timestamptz IS NULL OR o.placed_at >= sqlc.narg(placed_from)::timestamptz)
  AND (sqlc.narg(placed_to)::timestamptz   IS NULL OR o.placed_at <  sqlc.narg(placed_to)::timestamptz)
  AND (sqlc.narg(supplier_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM order_items oi
        WHERE oi.order_id = o.id AND oi.supplier_id = sqlc.narg(supplier_id)::uuid));

-- Manual transition to dispatched. Filtered on the only legal predecessor, so
-- an unpaid or cancelled order cannot be dispatched.
-- name: AdminMarkOrderDispatched :one
UPDATE orders SET status = 'dispatched'
WHERE id = $1 AND status IN ('paid', 'processed')
RETURNING *;

-- name: AdminCancelOrder :one
UPDATE orders
SET status = 'cancelled', cancelled_at = now(), cancel_reason = $2
WHERE id = $1 AND status NOT IN ('cancelled', 'refunded', 'dispatched')
RETURNING *;

-- ===========================================================================
-- admin: payouts — CLAUDE.md §5.3
-- ===========================================================================

-- Per-supplier settlement summary: what we owe, over how many orders, and how
-- long the oldest has been waiting.
-- name: PayoutSummary :many
SELECT
    supplier_id,
    sum(amount_paise)::bigint AS pending_paise,
    count(*)::bigint          AS pending_orders,
    min(created_at)::timestamptz AS oldest_pending_at
FROM supplier_payouts
WHERE status = 'pending'
GROUP BY supplier_id
ORDER BY sum(amount_paise) DESC;

-- name: ListPayouts :many
SELECT p.*, o.order_number, o.placed_at
FROM supplier_payouts p
JOIN orders o ON o.id = p.order_id
WHERE (sqlc.narg(supplier_id)::uuid IS NULL OR p.supplier_id = sqlc.narg(supplier_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
ORDER BY p.created_at
LIMIT $1 OFFSET $2;

-- Locks the requested payouts so two admins cannot settle the same rows at
-- once. Ordered by id for a deterministic lock order.
-- name: LockPayoutsForUpdate :many
SELECT * FROM supplier_payouts
WHERE id = ANY($1::uuid[])
ORDER BY id
FOR UPDATE;

-- Filtered on status='pending', so a replayed mark-paid updates zero rows
-- rather than overwriting who settled it and when.
-- name: MarkPayoutPaid :one
UPDATE supplier_payouts
SET status = 'paid', marked_paid_by = $2, marked_paid_at = now(),
    reference_no = $3, notes = $4
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- Reconciliation: what a supplier is owed on an order according to the LINE
-- ITEMS, independent of the payout row. Used by the test that proves the two
-- agree.
-- name: PayableFromOrderItems :many
SELECT
    supplier_id,
    sum(line_total_paise - line_markup_paise)::bigint AS amount_paise
FROM order_items
WHERE order_id = ANY($1::uuid[])
GROUP BY supplier_id
ORDER BY supplier_id;

-- ===========================================================================
-- admin: dashboard
-- ===========================================================================

-- Today's trading figures, in IST. GMV counts only orders that were actually
-- paid — a pending_payment order is not revenue.
-- Trading figures over an IST date range, inclusive of both ends.
--
-- The range is compared against placed_at converted to Asia/Kolkata, not to
-- the server's zone: a business day here is an IST calendar day (CLAUDE.md
-- rule 2), and an order placed at 11pm IST belongs to that day even though it
-- is already tomorrow in UTC.
--
-- Note this is `BETWEEN $1 AND $2` — passing the same date twice gives a
-- single day, which is how "Today" is served without a second query.
-- name: DashboardRange :one
SELECT
    count(*) FILTER (
        WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2)::bigint
        AS orders_today,
    coalesce(sum(total_paise) FILTER (
        WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
          AND status IN ('paid','processed','dispatched')), 0)::bigint
        AS gmv_today_paise,
    coalesce(sum(platform_fee_paise) FILTER (
        WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
          AND status IN ('paid','processed','dispatched')), 0)::bigint
        AS platform_fees_today_paise,
    -- The subtotal is what supplier commission is charged on, so it is summed
    -- here rather than derived from GMV — GMV includes our own fees, and
    -- charging commission on our own fees would be wrong.
    coalesce(sum(subtotal_paise) FILTER (
        WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
          AND status IN ('paid','processed','dispatched')), 0)::bigint
        AS subtotal_today_paise,
    -- Counted separately from orders_today: the delivery margin is earned per
    -- PAID order, and orders_today includes ones still awaiting payment.
    count(*) FILTER (
        WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
          AND status IN ('paid','processed','dispatched'))::bigint
        AS paid_orders_today,
    -- Deliberately NOT filtered by the range. "Awaiting processing" is a
    -- queue depth right now, not something that happened during a period —
    -- scoping it to last month would report a backlog that has since cleared.
    count(*) FILTER (WHERE status = 'paid')::bigint
        AS awaiting_processing
FROM orders;

-- name: DashboardOutstanding :one
SELECT coalesce(sum(amount_paise), 0)::bigint AS outstanding_paise
FROM supplier_payouts WHERE status = 'pending';

-- ===========================================================================
-- admin audit — CLAUDE.md §5.3
-- ===========================================================================

-- name: InsertAuditLog :one
INSERT INTO admin_audit_log (admin_user_id, action, entity_type, entity_id, before, after)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAuditLog :many
SELECT * FROM admin_audit_log
WHERE (sqlc.narg(entity_type)::text IS NULL OR entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- ---------------------------------------------------------------------------
-- Supplier's own sales.
--
-- Every query below is scoped to one supplier_id and that scope is not
-- optional (CLAUDE.md §7): a supplier must never be able to see another
-- supplier's lines, orders or amounts.
-- ---------------------------------------------------------------------------

-- Money totals come from supplier_payouts, not from order_items, deliberately:
-- payouts are exactly what the admin settlement screen pays against, so the
-- supplier's "owed" figure and the admin's "owing" figure are the same number
-- from the same row. Deriving one from items and the other from payouts is how
-- the two screens end up disagreeing at settlement time.
-- Every supplier sales query below takes the SAME optional window, as two
-- nullable timestamptz bounds: NULL and NULL mean all time.
--
-- The bounds are instants, not dates, and the caller computes them from an IST
-- calendar day with isttime (CLAUDE.md rule 2). Doing the conversion in SQL —
-- `(placed_at AT TIME ZONE 'Asia/Kolkata')::date` — would put a second copy of
-- the timezone rule in a place no Go test covers, and would depend on the
-- database's own tzdata. The upper bound is EXCLUSIVE: it is midnight at the
-- start of the day after the range, so the last day is included whole without
-- anyone having to write 23:59:59.999.
--
-- placed_at is the basis throughout, so the summary, the produce breakdown and
-- the order list can never describe different sets of orders.
-- name: SupplierSalesSummary :one
SELECT
    count(*)::bigint                                                          AS order_count,
    coalesce(sum(p.amount_paise), 0)::bigint                                  AS gross_paise,
    coalesce(sum(p.amount_paise) FILTER (WHERE p.status = 'pending'), 0)::bigint AS pending_paise,
    coalesce(sum(p.amount_paise) FILTER (WHERE p.status = 'paid'), 0)::bigint    AS settled_paise
FROM supplier_payouts p
JOIN orders o ON o.id = p.order_id
WHERE p.supplier_id = $1
  AND (sqlc.narg('placed_from')::timestamptz IS NULL OR o.placed_at >= sqlc.narg('placed_from'))
  AND (sqlc.narg('placed_to')::timestamptz IS NULL OR o.placed_at < sqlc.narg('placed_to'));

-- What was actually sold, by produce. Snapshots, so an edited or deleted
-- product still reports correctly.
-- name: SupplierSalesByProduct :many
SELECT
    i.product_name_snapshot                     AS product_name,
    i.grade_snapshot                            AS grade,
    sum(i.qty)::bigint                          AS units,
    sum(i.weight_grams::bigint * i.qty)::bigint AS grams,
    sum(i.line_total_paise - i.line_markup_paise)::bigint AS amount_paise
FROM order_items i
JOIN orders o ON o.id = i.order_id
WHERE i.supplier_id = $1
  AND o.status IN ('paid', 'processed', 'dispatched')
  AND (sqlc.narg('placed_from')::timestamptz IS NULL OR o.placed_at >= sqlc.narg('placed_from'))
  AND (sqlc.narg('placed_to')::timestamptz IS NULL OR o.placed_at < sqlc.narg('placed_to'))
GROUP BY i.product_name_snapshot, i.grade_snapshot
ORDER BY sum(i.line_total_paise - i.line_markup_paise) DESC;

-- One row per order this supplier has produce in. The payout status is joined
-- so the supplier can see which orders they have already been paid for.
-- name: SupplierOrders :many
SELECT
    o.id,
    o.order_number,
    o.placed_at,
    o.delivery_day,
    o.status,
    sum(i.line_total_paise - i.line_markup_paise)::bigint AS amount_paise,
    sum(i.qty)::bigint              AS units,
    -- coalesced, not just cast. These come from a LEFT JOIN, so they are NULL
    -- for an order whose payout row does not exist yet; the cast alone told
    -- sqlc they were non-nullable and scanning blew up on the first unsettled
    -- order. Empty string means "not settled", which the handler reads.
    coalesce(max(p.status), 'pending')::text AS payout_status,
    coalesce(max(p.reference_no), '')::text  AS payout_reference,
    coalesce(
        to_char(max(p.marked_paid_at) AT TIME ZONE 'UTC',
                'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')::text AS payout_paid_at
FROM orders o
JOIN order_items i ON i.order_id = o.id AND i.supplier_id = $1
LEFT JOIN supplier_payouts p ON p.order_id = o.id AND p.supplier_id = $1
WHERE o.status IN ('paid', 'processed', 'dispatched')
  AND (sqlc.narg('placed_from')::timestamptz IS NULL OR o.placed_at >= sqlc.narg('placed_from'))
  AND (sqlc.narg('placed_to')::timestamptz IS NULL OR o.placed_at < sqlc.narg('placed_to'))
GROUP BY o.id, o.order_number, o.placed_at, o.delivery_day, o.status
ORDER BY o.placed_at DESC
LIMIT $2 OFFSET $3;

-- The count behind the pager. Same JOIN as above so the two can never disagree
-- about which orders belong to this supplier.
-- name: SupplierOrdersCount :one
SELECT count(DISTINCT o.id) FROM orders o
JOIN order_items i ON i.order_id = o.id AND i.supplier_id = $1
WHERE o.status IN ('paid', 'processed', 'dispatched')
  AND (sqlc.narg('placed_from')::timestamptz IS NULL OR o.placed_at >= sqlc.narg('placed_from'))
  AND (sqlc.narg('placed_to')::timestamptz IS NULL OR o.placed_at < sqlc.narg('placed_to'));

-- The supplier's OWN lines on those orders. Filtered by supplier_id, so a
-- multi-supplier order never reveals what another grower supplied or charged.
-- name: SupplierOrderItems :many
SELECT
    order_id,
    product_name_snapshot AS product_name,
    unit_label_snapshot   AS unit_label,
    grade_snapshot        AS grade,
    -- The GRADE the pack was sold at. A grower picking eleven grades of one
    -- fruit out of separate crates cannot pack an order that only says
    -- "Pomegranate 1 Kg Box" (CLAUDE.md §5.2).
    size_code_snapshot    AS size_code,
    size_meta_snapshot    AS size_meta,
    weight_grams,
    qty,
    -- The SUPPLIER's price, not the customer's. What we add on top is ours
    -- and is never shown to the grower who set the price underneath it.
    (unit_price_paise - markup_paise)::bigint      AS unit_price_paise,
    (line_total_paise - line_markup_paise)::bigint AS line_total_paise
FROM order_items
WHERE supplier_id = $1 AND order_id = ANY($2::uuid[])
ORDER BY order_id, product_name_snapshot;

-- ---------------------------------------------------------------------------
-- Fulfilment transitions: paid -> processed -> dispatched.
--
-- Every one filters on its only legal predecessor inside the UPDATE. That is
-- the concurrency guard as well as the state machine: two admins clicking at
-- once cannot both "win", and a bulk action cannot skip a step by including an
-- id that is not ready.
-- ---------------------------------------------------------------------------

-- name: AdminMarkOrderProcessed :one
UPDATE orders SET status = 'processed'
WHERE id = $1 AND status = 'paid'
RETURNING *;

-- Bulk processing. Returns only the rows it actually moved, so the caller can
-- report "12 of 15 processed" rather than claiming all of them.
-- name: AdminBulkMarkProcessed :many
UPDATE orders SET status = 'processed'
WHERE id = ANY($1::uuid[]) AND status = 'paid'
RETURNING *;

-- Every paid order in a window. Used by "process all" and by the scheduled
-- job; both mean the same thing, one is a click and one is a clock.
-- name: AdminProcessAllPaid :many
UPDATE orders SET status = 'processed'
WHERE status = 'paid'
  AND (sqlc.narg(placed_from)::timestamptz IS NULL OR placed_at >= sqlc.narg(placed_from)::timestamptz)
  AND (sqlc.narg(placed_to)::timestamptz   IS NULL OR placed_at <  sqlc.narg(placed_to)::timestamptz)
RETURNING *;

-- The scheduled job's claim. An order becomes due at its OWN processing_at,
-- which is 4pm IST of its processing date (CLAUDE.md §6.1) — so this is what
-- "process everything at 4pm" actually means, and it self-heals: an order that
-- became due while the service was restarting is picked up on the next tick
-- rather than missed forever.
-- name: ClaimOrdersDueForProcessing :many
UPDATE orders SET status = 'processed'
WHERE id IN (
    SELECT id FROM orders
    WHERE status = 'paid' AND processing_at <= now()
    ORDER BY processing_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- The dispatch counterpart of ClaimOrdersDueForProcessing: everything whose
-- delivery day has arrived and which nobody dispatched by hand.
--
-- `delivery_day` is a DATE, and today's IST date arrives as a PARAMETER rather
-- than being computed here with `AT TIME ZONE`. That keeps the one definition
-- of "today" in Go, where isttime owns it and tests cover it (CLAUDE.md rule 2)
-- — a second copy in SQL would also depend on the database's own tzdata.
--
-- `<=` rather than `=` so an order whose day passed while the service was down
-- is still picked up, exactly as the processing job self-heals.
--
-- Filtered to 'processed': an order still awaiting payment, or cancelled, must
-- never be dispatched by a clock.
-- name: ClaimOrdersDueForDispatch :many
UPDATE orders SET status = 'dispatched'
WHERE id IN (
    SELECT id FROM orders
    WHERE status = 'processed' AND delivery_day <= $2::date
    ORDER BY delivery_day
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: AdminBulkMarkDispatched :many
UPDATE orders SET status = 'dispatched'
WHERE id = ANY($1::uuid[]) AND status = 'processed'
RETURNING *;

-- name: AdminDispatchAllProcessed :many
UPDATE orders SET status = 'dispatched'
WHERE status = 'processed'
  AND (sqlc.narg(placed_from)::timestamptz IS NULL OR placed_at >= sqlc.narg(placed_from)::timestamptz)
  AND (sqlc.narg(placed_to)::timestamptz   IS NULL OR placed_at <  sqlc.narg(placed_to)::timestamptz)
RETURNING *;

-- The courier handover sheet.
--
-- Everything needed to put a parcel on a van, in one row per order: who it
-- goes to, where, and what is in it. The address is the SNAPSHOT taken at
-- placement, never the customer's current address book — a customer editing
-- their address after ordering must not silently redirect a parcel already
-- being packed.
-- name: AdminOrdersForExport :many
SELECT
    o.id,
    o.order_number,
    o.status,
    o.placed_at,
    o.delivery_day,
    o.expected_delivery_date,
    o.total_paise,
    o.address_snapshot,
    (SELECT coalesce(sum(oi.qty), 0) FROM order_items oi WHERE oi.order_id = o.id)::bigint
        AS item_count,
    (SELECT coalesce(sum(oi.weight_grams::bigint * oi.qty), 0) FROM order_items oi
      WHERE oi.order_id = o.id)::bigint AS total_grams,
    -- The packing list, as a person reads it: produce, GRADE, pack, quantity.
    --
    -- The grade is not decoration here. A grower sells eleven grades of one
    -- fruit out of separate crates (CLAUDE.md §5.2), so "Pomegranate 1 Kg Box
    -- x1" twice on one sheet is two different boxes and the packer has no way
    -- to tell which. Hidden for an ungraded listing and for the implicit 'STD'
    -- code, the same two cases gradedName() hides in Go.
    (SELECT string_agg(
                oi.product_name_snapshot ||
                CASE
                    WHEN oi.size_code_snapshot IS NULL
                      OR btrim(oi.size_code_snapshot) = ''
                      OR upper(btrim(oi.size_code_snapshot)) = 'STD' THEN ''
                    ELSE ' (' || btrim(oi.size_code_snapshot) || ')'
                END ||
                ' ' || oi.unit_label_snapshot || ' x' || oi.qty,
                '; ' ORDER BY oi.product_name_snapshot, oi.size_code_snapshot)
       FROM order_items oi WHERE oi.order_id = o.id)::text AS contents
FROM orders o
WHERE o.status = ANY($1::text[])
  AND (sqlc.narg(placed_from)::timestamptz IS NULL OR o.placed_at >= sqlc.narg(placed_from)::timestamptz)
  AND (sqlc.narg(placed_to)::timestamptz   IS NULL OR o.placed_at <  sqlc.narg(placed_to)::timestamptz)
  -- The same rows, windowed on when they were PROCESSED instead. The admin
  -- download asks "what was ordered in this period"; the daily courier sheet
  -- asks "what is being packed tonight", and those are different sets: an
  -- order placed at 6pm yesterday is processed today, and filtering it by
  -- placed_at would leave that parcel off today's sheet entirely.
  AND (sqlc.narg(processed_from)::timestamptz IS NULL OR o.processing_at >= sqlc.narg(processed_from)::timestamptz)
  AND (sqlc.narg(processed_to)::timestamptz   IS NULL OR o.processing_at <  sqlc.narg(processed_to)::timestamptz)
ORDER BY o.placed_at;

-- Commission actually collected in a period.
--
-- Derived from what happened, not recomputed from a rate. Supplier commission
-- is now per-supplier, so applying the platform rate to the total subtotal
-- gives a number that is wrong for every grower on a negotiated rate — and
-- wrong in a way nobody would notice, because it still looks plausible.
--
-- supplier_payouts stores the NET amount a grower receives, so the difference
-- between their line totals and their payout IS the commission, whatever rate
-- produced it. It also stays correct if a rate changes after the fact, because
-- the payout row was written at the rate that applied at the time.
-- Supplier gross is the ORDER SUBTOTAL MINUS THE MARKUP, not the subtotal.
--
-- The subtotal is what the customer paid, and part of that is our markup — it
-- was never the grower's to be commissioned on. Left as subtotal_paise, this
-- reported commission = gross - payouts, and the markup fell inside that
-- difference: every rupee of markup would have been counted a second time,
-- once here and once as markup revenue.
-- name: CommissionCollectedInRange :one
WITH paid AS (
    SELECT id, subtotal_paise FROM orders
    WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
      AND status IN ('paid', 'processed', 'dispatched')
)
SELECT
    -- Cast around the whole subtraction, not each half: sqlc types the
    -- expression, and casting only the operands left it inferring int32 for
    -- the result — a paise total that overflows at ₹21 lakh.
    (coalesce((SELECT sum(subtotal_paise) FROM paid), 0)
     - coalesce((SELECT sum(line_markup_paise) FROM order_items
                  WHERE order_id IN (SELECT id FROM paid)), 0))::bigint
        AS supplier_gross_paise,
    coalesce((SELECT sum(amount_paise) FROM supplier_payouts
               WHERE order_id IN (SELECT id FROM paid)), 0)::bigint
        AS supplier_net_paise;

-- What we earned from markup in a period: the fourth source of revenue,
-- alongside the platform fee, supplier commission and delivery margin.
--
-- Read from what actually happened — the markup snapshotted on each line —
-- and never recomputed from today's product markup, which an admin may have
-- changed since.
-- name: MarkupCollectedInRange :one
SELECT coalesce(sum(i.line_markup_paise), 0)::bigint AS markup_paise
FROM order_items i
JOIN orders o ON o.id = i.order_id
WHERE (o.placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
  AND o.status IN ('paid', 'processed', 'dispatched');

-- ===========================================================================
-- admin analytics — the dashboard's breakdowns
--
-- Every query below is scoped by the SAME window the dashboard tiles use:
-- `(o.placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2`, on
-- placed_at, over the paid statuses. Sharing one basis is what lets the
-- screen claim its totals reconcile with each other — a breakdown filtered on
-- a different column, or a different status set, would add up to a number the
-- tile above it does not show.
--
-- "Revenue" here is what the CUSTOMER paid for those lines (line_total_paise,
-- which includes our markup), not what the grower is owed. The grower's share
-- and our margin are reported beside it rather than folded into one figure.
-- ===========================================================================

-- Revenue by supplier, with what we actually kept from each.
--
-- margin = what customers paid for their lines − what their payout pays them.
-- Read from the payout rows rather than recomputed from a commission rate:
-- rates are per-supplier and negotiable, so a rate-based figure would be
-- describing a policy rather than what happened (the same reasoning as
-- CommissionCollectedInRange).
-- name: AnalyticsRevenueBySupplier :many
WITH paid AS (
    SELECT id FROM orders
    WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
      AND status IN ('paid', 'processed', 'dispatched')
), lines AS (
    SELECT
        i.supplier_id,
        sum(i.line_total_paise)::bigint                  AS sales_paise,
        sum(i.line_markup_paise)::bigint                 AS markup_paise,
        sum(i.qty)::bigint                               AS units,
        sum(i.weight_grams::bigint * i.qty)::bigint      AS grams,
        count(DISTINCT i.order_id)::bigint               AS orders
    FROM order_items i
    WHERE i.order_id IN (SELECT id FROM paid)
    GROUP BY i.supplier_id
), payouts AS (
    SELECT supplier_id, sum(amount_paise)::bigint AS payable_paise
    FROM supplier_payouts
    WHERE order_id IN (SELECT id FROM paid)
    GROUP BY supplier_id
)
SELECT
    l.supplier_id,
    l.sales_paise,
    l.markup_paise,
    l.units,
    l.grams,
    l.orders,
    coalesce(p.payable_paise, 0)::bigint AS payable_paise
FROM lines l
LEFT JOIN payouts p ON p.supplier_id = l.supplier_id
ORDER BY l.sales_paise DESC;

-- Revenue by produce. Snapshots, so an edited, archived or deleted product
-- still reports under the name it was sold as.
-- name: AnalyticsRevenueByProduce :many
SELECT
    i.product_name_snapshot                          AS product_name,
    i.grade_snapshot                                 AS grade,
    -- The SIZE CODE, so a row names the crate it sold out of. Without it this
    -- breakdown summed eleven grades of pomegranate into one line and called
    -- it "Pomegranate", which is the number nobody can act on: the grades are
    -- separate crates at separate prices (CLAUDE.md §5.2).
    coalesce(i.size_code_snapshot, '')::text         AS size_code,
    sum(i.line_total_paise)::bigint                  AS sales_paise,
    sum(i.line_markup_paise)::bigint                 AS markup_paise,
    sum(i.qty)::bigint                               AS units,
    sum(i.weight_grams::bigint * i.qty)::bigint      AS grams,
    count(DISTINCT i.order_id)::bigint               AS orders
FROM order_items i
JOIN orders o ON o.id = i.order_id
WHERE (o.placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
  AND o.status IN ('paid', 'processed', 'dispatched')
GROUP BY i.product_name_snapshot, i.grade_snapshot, coalesce(i.size_code_snapshot, '')
ORDER BY sum(i.line_total_paise) DESC;

-- Which GRADES the platform actually sells, across every supplier.
--
-- The supplier's own Insights tab answers this for one grower; this is the
-- same question for the shop. It is what says whether the market wants XL2 or
-- whether growers are picking a grade nobody buys — and neither the produce
-- breakdown (which sums the grades) nor the pack breakdown (box sizes, the
-- same across grades) can answer it.
--
-- Grouped by produce AND grade, never grade alone: one supplier's M2
-- pomegranate and another's M2 tomato are different goods, and a row reading
-- "M2" that summed them would be worse than no row. Ungraded lines are
-- excluded rather than bucketed — size_code_snapshot is NULL on an ungraded
-- listing and on any order predating size codes (§5.3).
-- name: AnalyticsRevenueBySizeCode :many
SELECT
    i.product_name_snapshot                          AS product_name,
    i.size_code_snapshot                             AS size_code,
    coalesce(max(i.size_meta_snapshot), '')::text    AS size_meta,
    count(DISTINCT i.supplier_id)::bigint            AS suppliers,
    sum(i.line_total_paise)::bigint                  AS sales_paise,
    sum(i.line_markup_paise)::bigint                 AS markup_paise,
    sum(i.qty)::bigint                               AS units,
    sum(i.weight_grams::bigint * i.qty)::bigint      AS grams,
    count(DISTINCT i.order_id)::bigint               AS orders
FROM order_items i
JOIN orders o ON o.id = i.order_id
WHERE (o.placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
  AND o.status IN ('paid', 'processed', 'dispatched')
  AND i.size_code_snapshot IS NOT NULL
  AND btrim(i.size_code_snapshot) <> ''
GROUP BY i.product_name_snapshot, i.size_code_snapshot
ORDER BY sum(i.line_total_paise) DESC;

-- One row per calendar day in the window that had any order at all.
--
-- Days with no orders are absent rather than zero: filling the gaps needs a
-- date series, and the caller already knows the window's bounds, so it can do
-- that without a second table scan.
-- name: AnalyticsDailyTrend :many
SELECT
    (placed_at AT TIME ZONE 'Asia/Kolkata')::date AS day,
    count(*)::bigint AS orders,
    count(*) FILTER (WHERE status IN ('paid', 'processed', 'dispatched'))::bigint
        AS paid_orders,
    coalesce(sum(total_paise) FILTER (
        WHERE status IN ('paid', 'processed', 'dispatched')), 0)::bigint
        AS gmv_paise
FROM orders
WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
GROUP BY 1
ORDER BY 1;

-- What happened to the orders placed in the window, by outcome.
--
-- Counts every order PLACED in the period whatever its state, unlike the
-- other analytics queries: the whole point is the ones that did not become
-- revenue. `expired` and `payment_failed` are checkout falling over;
-- `cancelled` is a decision someone made afterwards.
-- name: AnalyticsOrderOutcomes :many
SELECT status, count(*)::bigint AS orders,
       coalesce(sum(total_paise), 0)::bigint AS value_paise
FROM orders
WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
GROUP BY status
ORDER BY count(*) DESC;

-- Basket shape and who is buying: the two figures that say whether a period
-- grew because more people came or because they bought more.
--
-- `returning_customers` counts customers with an EARLIER paid order outside
-- the window, so "new" means new to us, not new to this period.
-- name: AnalyticsBasketAndCustomers :one
WITH paid AS (
    SELECT o.id, o.customer_id, o.total_paise, o.placed_at FROM orders o
    WHERE (o.placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
      AND o.status IN ('paid', 'processed', 'dispatched')
), items AS (
    SELECT coalesce(sum(qty), 0)::bigint AS units
    FROM order_items WHERE order_id IN (SELECT id FROM paid)
), first_seen AS (
    -- Each customer's first paid order INSIDE the window, so "returning" can
    -- be asked as: did anything of theirs come before this?
    SELECT customer_id, min(placed_at) AS first_in_window
    FROM paid GROUP BY customer_id
), returning_customers AS (
    SELECT count(*)::bigint AS customers
    FROM first_seen f
    WHERE EXISTS (
        SELECT 1 FROM orders o
        WHERE o.customer_id = f.customer_id
          AND o.status IN ('paid', 'processed', 'dispatched')
          AND o.placed_at < f.first_in_window
    )
)
SELECT
    (SELECT count(*) FROM paid)::bigint                        AS paid_orders,
    (SELECT coalesce(sum(total_paise), 0) FROM paid)::bigint   AS gmv_paise,
    (SELECT count(DISTINCT customer_id) FROM paid)::bigint     AS customers,
    (SELECT customers FROM returning_customers)::bigint        AS returning_customers,
    (SELECT units FROM items)::bigint                          AS units;

-- Orders either side of the 4pm IST cutoff (CLAUDE.md §6.1).
--
-- The hour is a parameter rather than a literal 16 so this reads whatever
-- ORDER_CUTOFF_HOUR_IST is configured to — a query that hardcoded 16 would
-- quietly start lying the day the cutoff moves.
-- name: AnalyticsCutoffSplit :one
SELECT
    count(*) FILTER (
        WHERE extract(hour FROM (placed_at AT TIME ZONE 'Asia/Kolkata')) < $3::int)::bigint
        AS before_cutoff,
    count(*) FILTER (
        WHERE extract(hour FROM (placed_at AT TIME ZONE 'Asia/Kolkata')) >= $3::int)::bigint
        AS after_cutoff
FROM orders
WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
  AND status IN ('paid', 'processed', 'dispatched');

-- Where the parcels are going, from the address SNAPSHOT on the order — the
-- address as it was when placed, which is where it was actually sent, even if
-- the customer has since edited or deleted it.
-- name: AnalyticsTopDestinations :many
SELECT
    coalesce(nullif(trim(address_snapshot->>'city'), ''), 'Unknown')::text AS city,
    coalesce(nullif(trim(address_snapshot->>'pincode'), ''), '—')::text    AS pincode,
    count(*)::bigint AS orders,
    coalesce(sum(total_paise), 0)::bigint AS gmv_paise
FROM orders
WHERE (placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1 AND $2
  AND status IN ('paid', 'processed', 'dispatched')
GROUP BY 1, 2
ORDER BY count(*) DESC, sum(total_paise) DESC
LIMIT $3;


-- ===========================================================================
-- supplier analytics — the grower's own trend screen
--
-- Scoped by supplier_id IN SQL, like every other supplier query: a grower must
-- never be able to read another's figures, and the WHERE clause is the
-- enforcement rather than a check the handler might forget (CLAUDE.md §7).
--
-- Money here is the GROWER's, not the customer's: line_total minus the markup
-- that was ours. The same subtraction as SupplierSalesByProduct, so the two
-- screens cannot quote different totals for one period.
-- ===========================================================================

-- Day by day, in IST. Bounded by dates rather than instants because the
-- grouping is by calendar day, and both bounds must describe the same thing.
-- name: SupplierAnalyticsDaily :many
SELECT
    (o.placed_at AT TIME ZONE 'Asia/Kolkata')::date              AS day,
    count(DISTINCT o.id)::bigint                                 AS orders,
    sum(i.qty)::bigint                                           AS units,
    sum(i.weight_grams::bigint * i.qty)::bigint                  AS grams,
    sum(i.line_total_paise - i.line_markup_paise)::bigint        AS amount_paise
FROM order_items i
JOIN orders o ON o.id = i.order_id
WHERE i.supplier_id = $1
  AND o.status IN ('paid', 'processed', 'dispatched')
  AND (o.placed_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $2 AND $3
GROUP BY 1
ORDER BY 1;

-- Which pack SIZES actually sell.
--
-- The one breakdown a grower cannot get anywhere else on the platform: they
-- choose their own units (CLAUDE.md §5.2), and nothing tells them whether the
-- 10 kg is worth listing. Instants rather than dates, matching the other
-- supplier queries this screen reuses.
-- name: SupplierAnalyticsByPack :many
SELECT
    i.unit_label_snapshot                                 AS unit_label,
    i.weight_grams,
    sum(i.qty)::bigint                                    AS units,
    sum(i.weight_grams::bigint * i.qty)::bigint           AS grams,
    sum(i.line_total_paise - i.line_markup_paise)::bigint AS amount_paise,
    count(DISTINCT i.order_id)::bigint                    AS orders
FROM order_items i
JOIN orders o ON o.id = i.order_id
WHERE i.supplier_id = $1
  AND o.status IN ('paid', 'processed', 'dispatched')
  AND (sqlc.narg('placed_from')::timestamptz IS NULL OR o.placed_at >= sqlc.narg('placed_from'))
  AND (sqlc.narg('placed_to')::timestamptz IS NULL OR o.placed_at < sqlc.narg('placed_to'))
GROUP BY i.unit_label_snapshot, i.weight_grams
ORDER BY sum(i.line_total_paise - i.line_markup_paise) DESC;

-- Which GRADES actually sell.
--
-- A grower grades their produce into separate crates at separate prices
-- (CLAUDE.md §5.2) and then has to decide, next season, which grades are worth
-- picking for. Nothing else on the platform answers that: "what sold" is by
-- produce, "which packs sell" is by box size, and neither can tell an XL2 from
-- a T1.
--
-- Grouped by PRODUCE AND grade, never by grade alone: one grower's M2
-- pomegranate and M2 tomato are different crates, and a row reading "M2" that
-- silently summed them would be worse than no row.
--
-- Ungraded lines are excluded rather than bucketed. size_code_snapshot is NULL
-- on an ungraded listing and on any order placed before size codes existed
-- (§5.3), and an "ungraded" row would be a lump of exactly the produce this
-- breakdown has nothing to say about.
-- name: SupplierAnalyticsBySizeCode :many
SELECT
    i.product_name_snapshot                               AS product_name,
    -- COALESCE'd only so the generated type is a plain string: the WHERE
    -- clause below has already excluded every NULL.
    coalesce(i.size_code_snapshot, '')::text              AS size_code,
    -- One meta per grade, so max() is a pick rather than an aggregate. It can
    -- differ across a period if the grower reworded it; the latest wording
    -- wins, which is the one they would recognise.
    coalesce(max(i.size_meta_snapshot), '')::text         AS size_meta,
    sum(i.qty)::bigint                                    AS units,
    sum(i.weight_grams::bigint * i.qty)::bigint           AS grams,
    sum(i.line_total_paise - i.line_markup_paise)::bigint AS amount_paise,
    count(DISTINCT i.order_id)::bigint                    AS orders
FROM order_items i
JOIN orders o ON o.id = i.order_id
WHERE i.supplier_id = $1
  AND o.status IN ('paid', 'processed', 'dispatched')
  AND i.size_code_snapshot IS NOT NULL
  AND btrim(i.size_code_snapshot) <> ''
  AND (sqlc.narg('placed_from')::timestamptz IS NULL OR o.placed_at >= sqlc.narg('placed_from'))
  AND (sqlc.narg('placed_to')::timestamptz IS NULL OR o.placed_at < sqlc.narg('placed_to'))
GROUP BY i.product_name_snapshot, coalesce(i.size_code_snapshot, '')
ORDER BY sum(i.line_total_paise - i.line_markup_paise) DESC;

-- ===========================================================================
-- daily courier report
-- ===========================================================================

-- Claims the right to send today's report.
--
-- ON CONFLICT DO NOTHING means the second caller gets no row back, which is
-- how a replica — or the next tick a minute later — learns that someone else
-- already sent it. The same shape as the webhook dedupe: the constraint does
-- the thinking, not the application.
-- name: ClaimDailyReport :one
INSERT INTO daily_report_sends (report_date, order_count, recipients)
VALUES ($1, $2, $3)
ON CONFLICT (report_date) DO NOTHING
RETURNING *;

-- Releases the claim so a later tick can retry.
--
-- Sending happens AFTER the claim — an SMTP call inside the transaction would
-- hold a pooled connection open across a network round trip — so a failed send
-- must undo its claim or the day is silently lost.
-- name: ReleaseDailyReport :execrows
DELETE FROM daily_report_sends WHERE report_date = $1;

-- Marks the pre-existing outbox backlog published WITHOUT sending it.
--
-- A maintenance action, run once by hand from internal/devtools. The
-- dispatcher was added long after these rows were written, so the queue held
-- months of confirmations for orders that have already been delivered,
-- cancelled or expired. Draining it against a real relay would mail every one
-- of those customers about an order they finished with weeks ago.
--
-- Bounded by a timestamp the caller passes, not `now()` inside the statement,
-- so a confirmation written WHILE this runs is not swallowed by it. The reason
-- is stamped on last_error rather than left blank: a row marked published that
-- nobody ever sent should say so.
-- name: SuppressOutboxBacklog :execrows
UPDATE outbox
SET published_at = now(),
    last_error = 'suppressed: pre-existing backlog, never sent'
WHERE published_at IS NULL AND created_at < $1;

-- How many rows the suppression above would affect. Run first, and read it.
-- name: CountUnsentOutbox :one
SELECT count(*)::bigint FROM outbox
WHERE published_at IS NULL AND created_at < $1;

-- ===========================================================================
-- Wallet — CLAUDE.md §6.7
-- ===========================================================================

-- Creates the wallet on first touch. DO UPDATE rather than DO NOTHING so the
-- row comes back either way.
-- name: EnsureWallet :one
INSERT INTO wallets (customer_id) VALUES ($1)
ON CONFLICT (customer_id) DO UPDATE SET customer_id = EXCLUDED.customer_id
RETURNING *;

-- The row lock every balance change takes, so two movements serialise.
-- name: LockWallet :one
SELECT * FROM wallets WHERE customer_id = $1 FOR UPDATE;

-- name: GetWallet :one
SELECT * FROM wallets WHERE customer_id = $1;

-- Callers hold LockWallet and write the matching ledger row in the same
-- transaction. The CHECK on balance_paise refuses an overdraft regardless.
-- name: SetWalletBalance :one
UPDATE wallets SET balance_paise = $2 WHERE customer_id = $1 RETURNING *;

-- name: InsertWalletTransaction :one
INSERT INTO wallet_transactions (
    customer_id, kind, amount_paise, balance_after_paise, topup_id, order_id, refund_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListWalletTransactions :many
SELECT t.*, o.order_number
FROM wallet_transactions t
LEFT JOIN orders o ON o.id = t.order_id
WHERE t.customer_id = $1
ORDER BY t.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountWalletTransactions :one
SELECT count(*) FROM wallet_transactions WHERE customer_id = $1;

-- name: CreateWalletTopup :one
INSERT INTO wallet_topups (customer_id, amount_paise, idempotency_key)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetWalletTopupByKey :one
SELECT * FROM wallet_topups WHERE customer_id = $1 AND idempotency_key = $2;

-- name: GetWalletTopupForCustomer :one
SELECT * FROM wallet_topups WHERE id = $1 AND customer_id = $2;

-- name: SetWalletTopupRazorpayOrder :one
UPDATE wallet_topups SET razorpay_order_id = $2 WHERE id = $1 RETURNING *;

-- name: GetWalletTopupByRazorpayOrderID :one
SELECT * FROM wallet_topups WHERE razorpay_order_id = $1;

-- Filtered on status='created' inside the UPDATE, so it is the replay guard as
-- well as the transition: two captures of one top-up cannot both credit.
-- name: CaptureWalletTopup :one
UPDATE wallet_topups
SET status = 'captured', razorpay_payment_id = $2, captured_at = now()
WHERE id = $1 AND status = 'created'
RETURNING *;

-- Newest first: a refund goes back against the most recent money in, which
-- is the payment the customer is most likely to recognise on a statement.
-- name: LockRefundableTopups :many
SELECT * FROM wallet_topups
WHERE customer_id = $1 AND status = 'captured' AND refunded_paise < amount_paise
ORDER BY captured_at DESC
FOR UPDATE;

-- name: AddTopupRefunded :one
UPDATE wallet_topups SET refunded_paise = refunded_paise + $2 WHERE id = $1 RETURNING *;

-- name: CreateWalletRefund :one
INSERT INTO wallet_refunds (customer_id, topup_id, amount_paise, admin_user_id, notes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: MarkWalletRefundIssued :one
UPDATE wallet_refunds SET status = 'issued', razorpay_refund_id = $2
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: MarkWalletRefundFailed :one
UPDATE wallet_refunds SET status = 'failed', error = $2
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: ListWalletRefundsForCustomer :many
SELECT * FROM wallet_refunds WHERE customer_id = $1 ORDER BY created_at DESC LIMIT 50;

-- Admin: every wallet holding money, largest first.
-- name: AdminListWallets :many
SELECT w.*,
       (SELECT count(*) FROM schedules s
        WHERE s.customer_id = w.customer_id AND s.status = 'active') AS active_schedules
FROM wallets w
WHERE w.balance_paise > 0 OR sqlc.arg(include_empty)::boolean
ORDER BY w.balance_paise DESC
LIMIT $1 OFFSET $2;

-- name: AdminWalletTotals :one
SELECT count(*) FILTER (WHERE balance_paise > 0) AS funded_wallets,
       COALESCE(sum(balance_paise), 0)::bigint AS total_balance_paise
FROM wallets;

-- A wallet-paid order's payment row. provider = 'wallet' is how every
-- report tells it apart from a card payment.
-- name: CreateWalletPayment :one
INSERT INTO payments (order_id, provider, amount_paise, status)
VALUES ($1, 'wallet', $2, 'created')
RETURNING *;

-- name: SetOrderSchedule :exec
UPDATE orders SET schedule_id = $2 WHERE id = $1;

-- ===========================================================================
-- Schedules — CLAUDE.md §6.7
-- ===========================================================================

-- name: CreateSchedule :one
INSERT INTO schedules (
    customer_id, address_id, address_snapshot, frequency, weekdays,
    day_of_month, start_date, end_date, next_delivery_date
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: CreateScheduleItem :one
INSERT INTO schedule_items (
    schedule_id, product_id, product_unit_id, qty,
    product_name_snapshot, unit_label_snapshot, size_code_snapshot
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: DeleteScheduleItems :exec
DELETE FROM schedule_items WHERE schedule_id = $1;

-- name: ListSchedulesForCustomer :many
SELECT * FROM schedules
WHERE customer_id = $1 AND status <> 'cancelled'
ORDER BY status = 'active' DESC, next_delivery_date NULLS LAST, created_at DESC;

-- Ownership is the WHERE clause.
-- name: GetScheduleForCustomer :one
SELECT * FROM schedules WHERE id = $1 AND customer_id = $2;

-- name: LockSchedule :one
SELECT * FROM schedules WHERE id = $1 FOR UPDATE;

-- name: ListScheduleItems :many
SELECT * FROM schedule_items WHERE schedule_id = $1 ORDER BY created_at, id;

-- name: ListScheduleItemsForSchedules :many
SELECT * FROM schedule_items WHERE schedule_id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY created_at, id;

-- name: UpdateScheduleAddress :one
UPDATE schedules SET address_id = $2, address_snapshot = $3
WHERE id = $1 RETURNING *;

-- name: SetScheduleStatus :one
UPDATE schedules SET status = $2, next_delivery_date = $3, low_balance_skips = $4
WHERE id = $1 RETURNING *;

-- Everything the charging run must look at today: the date being charged,
-- and any earlier date it never reached. customer_id narrows it to one
-- customer — for tests against a shared database, and for an operator
-- re-running one account by hand.
-- name: ListDueSchedules :many
SELECT * FROM schedules
WHERE status = 'active' AND next_delivery_date IS NOT NULL
  AND next_delivery_date <= sqlc.arg(horizon)::date
  AND (sqlc.narg(customer_id)::uuid IS NULL OR customer_id = sqlc.narg(customer_id)::uuid)
ORDER BY next_delivery_date, created_at
LIMIT sqlc.arg(max_rows);

-- name: GetOccurrence :one
SELECT * FROM schedule_occurrences WHERE schedule_id = $1 AND delivery_date = $2;

-- name: CreateOccurrence :one
INSERT INTO schedule_occurrences (schedule_id, delivery_date, status, order_id, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- Only a customer's own skip can be undone.
-- name: DeleteCustomerSkip :execrows
DELETE FROM schedule_occurrences
WHERE schedule_id = $1 AND delivery_date = $2 AND status = 'skipped_by_customer';

-- name: ListOccurrencesForSchedule :many
SELECT so.*, o.order_number, o.total_paise
FROM schedule_occurrences so
LEFT JOIN orders o ON o.id = so.order_id
WHERE so.schedule_id = $1
ORDER BY so.delivery_date DESC
LIMIT 60;

-- The customer's future skips, to mark on the upcoming-dates list.
-- name: ListFutureSkips :many
SELECT delivery_date FROM schedule_occurrences
WHERE schedule_id = $1 AND delivery_date >= $2 AND status = 'skipped_by_customer';

-- For the outbox, which has only the schedule id.
-- name: GetScheduleByID :one
SELECT * FROM schedules WHERE id = $1;

-- ===========================================================================
-- analytics order events — CLAUDE.md §5.4
-- ===========================================================================

-- Written in the caller's transaction, next to the transition it describes.
-- name: InsertOrderEvent :one
INSERT INTO order_events_outbox (
    aggregate_id, event_type, schema_version, occurred_at, actor_role, request_id, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- Retention. CDC has already read these from the WAL; the table only keeps a
-- replay window.
-- name: PruneOrderEvents :execrows
DELETE FROM order_events_outbox WHERE created_at < $1;

-- The backfill's cursor: every order, oldest first, a page at a time.
-- name: ListOrderIDsAfter :many
SELECT id, placed_at FROM orders
WHERE (placed_at, id) > (sqlc.arg(after_placed_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY placed_at, id
LIMIT sqlc.arg(page_size);

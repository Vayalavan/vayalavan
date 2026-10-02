-- Carts, orders and reservations — CLAUDE.md §5.3.
--
-- customer_id and product/unit ids reference other schemas but carry NO
-- foreign keys: CLAUDE.md §3 forbids cross-schema FKs, and the vm_orders role
-- cannot see profile or catalog anyway.

-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- carts
-- ---------------------------------------------------------------------------

CREATE TABLE carts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- profile.users(id). One live cart per customer.
    customer_id UUID NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER carts_set_updated_at
    BEFORE UPDATE ON carts FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE cart_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id         UUID NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    -- catalog.product_units(id) and catalog.products(id).
    product_unit_id UUID NOT NULL,
    product_id      UUID NOT NULL,
    supplier_id     UUID NOT NULL,
    qty             INTEGER NOT NULL CHECK (qty > 0 AND qty <= 99),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Adding the same pack twice bumps qty rather than creating a second line.
    CONSTRAINT cart_items_one_per_unit UNIQUE (cart_id, product_unit_id)
);

-- NOTE: cart_items deliberately stores NO price. CLAUDE.md §5.3 requires the
-- cart to be re-priced from the catalogue on every read, so a stored price
-- could only ever be a stale second opinion.

CREATE TRIGGER cart_items_set_updated_at
    BEFORE UPDATE ON cart_items FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX cart_items_cart_idx ON cart_items (cart_id);

-- ---------------------------------------------------------------------------
-- orders
-- ---------------------------------------------------------------------------

CREATE TABLE orders (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Human-friendly, e.g. VM-260814-0042. What a customer quotes to support.
    order_number  TEXT NOT NULL UNIQUE,
    customer_id   UUID NOT NULL,

    status        TEXT NOT NULL DEFAULT 'pending_payment' CHECK (status IN (
        'pending_payment', 'paid', 'processed', 'dispatched',
        'payment_failed', 'expired', 'cancelled', 'refunded'
    )),

    -- The address as it was at placement. An order must render correctly even
    -- if the customer later edits or deletes the address (CLAUDE.md §5.3).
    address_snapshot   JSONB NOT NULL,

    -- All money is int64 paise (CLAUDE.md rule 1).
    subtotal_paise     BIGINT NOT NULL CHECK (subtotal_paise >= 0),
    platform_fee_paise BIGINT NOT NULL CHECK (platform_fee_paise >= 0),
    delivery_fee_paise BIGINT NOT NULL CHECK (delivery_fee_paise >= 0),
    total_paise        BIGINT NOT NULL CHECK (total_paise >= 0),

    -- The §6.1 timeline, computed ONCE at placement and never recomputed, so
    -- a change to the cutoff configuration cannot retroactively move a date a
    -- customer has already been shown.
    placed_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    processing_at          TIMESTAMPTZ NOT NULL,
    delivery_day           DATE NOT NULL,
    expected_delivery_date DATE NOT NULL,

    payment_status     TEXT NOT NULL DEFAULT 'pending'
                            CHECK (payment_status IN ('pending', 'paid', 'failed', 'refunded')),
    razorpay_order_id  TEXT,
    razorpay_payment_id TEXT,

    cancelled_at  TIMESTAMPTZ,
    cancel_reason TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The arithmetic must reconcile. A row that fails this is a bug in the
    -- pricing calculator, and it should never reach storage.
    CONSTRAINT orders_total_reconciles CHECK (
        total_paise = subtotal_paise + platform_fee_paise + delivery_fee_paise
    )
);

CREATE TRIGGER orders_set_updated_at
    BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX orders_customer_idx ON orders (customer_id, placed_at DESC);
CREATE INDEX orders_status_idx ON orders (status, placed_at DESC);

-- Idempotency for order creation (CLAUDE.md rule 6). The unique constraint IS
-- the guard: a replayed Idempotency-Key collides and the handler returns the
-- original order instead of charging twice.
CREATE TABLE order_idempotency (
    key         TEXT NOT NULL,
    customer_id UUID NOT NULL,
    order_id    UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Scoped per customer so two customers cannot collide on a client-chosen
    -- key, and one cannot probe another's keys.
    PRIMARY KEY (customer_id, key)
);

CREATE TABLE order_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    supplier_id     UUID NOT NULL,
    product_id      UUID NOT NULL,
    product_unit_id UUID NOT NULL,

    -- Snapshot everything (CLAUDE.md §5.3): the order must render correctly
    -- even after the product is edited, archived or deleted.
    product_name_snapshot TEXT NOT NULL,
    unit_label_snapshot   TEXT NOT NULL,
    grade_snapshot        TEXT,
    weight_grams          INTEGER NOT NULL CHECK (weight_grams > 0),

    unit_price_paise BIGINT NOT NULL CHECK (unit_price_paise >= 0),
    qty              INTEGER NOT NULL CHECK (qty > 0),
    line_total_paise BIGINT NOT NULL CHECK (line_total_paise >= 0),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT order_items_line_total_reconciles
        CHECK (line_total_paise = unit_price_paise * qty)
);

CREATE INDEX order_items_order_idx ON order_items (order_id);
CREATE INDEX order_items_supplier_idx ON order_items (supplier_id);

-- ---------------------------------------------------------------------------
-- stock_reservations
--
-- The orders-side record of grams held in vm-catalog-api.
--
-- DEVIATION from CLAUDE.md §6.3, which describes reserving inside the same
-- transaction that writes these rows. daily_availability lives in the catalog
-- schema and §3 forbids this service touching it — an isolation the vm_orders
-- role physically cannot violate. vm-catalog-api therefore performs the
-- FOR UPDATE and gram decrement atomically on its side, and this table records
-- what was held so it can be released or committed later.
--
-- The gap between the two writes is bounded by expires_at: if this service
-- dies after catalog reserved, the sweeper releases the grams. That is what
-- the TTL is for.
-- ---------------------------------------------------------------------------

CREATE TABLE stock_reservations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id     UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    product_id   UUID NOT NULL,
    -- The catalog-side reservation this mirrors.
    catalog_reservation_id UUID NOT NULL,
    available_on DATE NOT NULL,
    grams        INTEGER NOT NULL CHECK (grams > 0),
    status       TEXT NOT NULL DEFAULT 'held'
                      CHECK (status IN ('held', 'committed', 'released')),
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX stock_reservations_order_idx ON stock_reservations (order_id);
-- Drives the sweeper: only held rows can expire.
CREATE INDEX stock_reservations_expiry_idx ON stock_reservations (expires_at)
    WHERE status = 'held';

-- ---------------------------------------------------------------------------
-- outbox
-- ---------------------------------------------------------------------------

CREATE TABLE outbox (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL,
    payload        JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at   TIMESTAMPTZ,
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT
);

-- The dispatcher's queue: unpublished rows, oldest first.
CREATE INDEX outbox_unpublished_idx ON outbox (created_at)
    WHERE published_at IS NULL;

-- +goose Down

DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS stock_reservations;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS order_idempotency;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS cart_items;
DROP TABLE IF EXISTS carts;
DROP FUNCTION IF EXISTS set_updated_at();

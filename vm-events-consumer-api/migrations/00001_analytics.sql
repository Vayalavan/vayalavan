-- The analytics schema — the read model of CLAUDE.md §5.4.
--
-- Written ONLY by vm-events-consumer-api, from the product and order events
-- that Postgres logical replication streams out of catalog.product_events_outbox
-- and orders.order_events_outbox. Read ONLY by the analytics reader role
-- (vm-analytics-api), which can see this schema and nothing else.
--
-- Nothing here is a source of truth. Every row can be rebuilt by replaying
-- events (or re-running the snapshot backfills), which is why there are no
-- foreign keys back into catalog or orders and why money and timestamps are
-- copied rather than referenced.
--
-- Shapes chosen for the queries an analytics page actually runs:
--   orders          one row per order, current state, lifecycle timestamps
--   order_items     one row per line — revenue by product, grade, supplier
--   products        one row per product, current state
--   product_packs   one row per pack VERSION (slowly changing, type 2): price
--                   history, and the way an old order line's pack id still
--                   resolves after the grower's next save replaced it

-- +goose Up

-- ---------------------------------------------------------------------------
-- orders
-- ---------------------------------------------------------------------------
CREATE TABLE orders (
    order_id                UUID PRIMARY KEY,
    order_number            TEXT NOT NULL,
    customer_id             UUID NOT NULL,
    schedule_id             UUID,
    source                  TEXT NOT NULL,          -- checkout | schedule
    status                  TEXT NOT NULL,
    payment_method          TEXT,
    paid_via                TEXT,                   -- razorpay_webhook | admin_manual | wallet

    -- Money, int64 paise (CLAUDE.md rule 1).
    subtotal_paise          BIGINT NOT NULL,
    platform_fee_paise      BIGINT NOT NULL,
    delivery_fee_paise      BIGINT NOT NULL,
    total_paise             BIGINT NOT NULL,
    markup_paise            BIGINT NOT NULL DEFAULT 0,
    -- What growers are owed; NULL until paid. subtotal − this = our margin
    -- on the goods, before fees.
    supplier_payable_paise  BIGINT,

    item_count              INT NOT NULL,
    total_qty               INT NOT NULL,
    total_grams             BIGINT NOT NULL,

    -- Lifecycle. placed_at is the order's own; the rest are when the event
    -- that moved it there happened. NULL on orders that came in by backfill
    -- for transitions that pre-date the outbox.
    placed_at               TIMESTAMPTZ NOT NULL,
    paid_at                 TIMESTAMPTZ,
    processed_at            TIMESTAMPTZ,
    dispatched_at           TIMESTAMPTZ,
    cancelled_at            TIMESTAMPTZ,
    expired_at              TIMESTAMPTZ,
    refunded_at             TIMESTAMPTZ,
    cancelled_from_status   TEXT,

    -- The business day, in IST (CLAUDE.md rule 2), precomputed by
    -- vm-orders-api so no query here has to know the timezone or the cutoff.
    placed_date_ist         DATE NOT NULL,
    placed_hour_ist         SMALLINT NOT NULL,
    placed_before_cutoff    BOOLEAN NOT NULL,
    processing_at           TIMESTAMPTZ NOT NULL,
    delivery_day            DATE NOT NULL,
    expected_delivery_date  DATE NOT NULL,

    ship_city               TEXT,
    ship_state              TEXT,
    ship_pincode            TEXT,

    last_event_id           UUID NOT NULL,
    last_event_type         TEXT NOT NULL,
    last_event_at           TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX orders_placed_date_idx  ON orders (placed_date_ist);
CREATE INDEX orders_status_idx       ON orders (status);
CREATE INDEX orders_customer_idx     ON orders (customer_id, placed_at);
CREATE INDEX orders_pincode_idx      ON orders (ship_pincode);

-- ---------------------------------------------------------------------------
-- order_items
-- ---------------------------------------------------------------------------
CREATE TABLE order_items (
    order_item_id       UUID PRIMARY KEY,
    order_id            UUID NOT NULL REFERENCES orders (order_id) ON DELETE CASCADE,
    supplier_id         UUID NOT NULL,
    product_id          UUID NOT NULL,
    size_code_id        UUID,
    -- The pack as sold. Resolves against product_packs at any version.
    pack_option_id      UUID NOT NULL,
    product_name        TEXT NOT NULL,
    product_type        TEXT,
    grade               TEXT,
    size_code           TEXT,
    pack_label          TEXT NOT NULL,
    weight_grams        INT NOT NULL,
    qty                 INT NOT NULL,
    unit_price_paise    BIGINT NOT NULL,
    line_total_paise    BIGINT NOT NULL,
    line_markup_paise   BIGINT NOT NULL DEFAULT 0,
    line_grams          BIGINT NOT NULL
);

CREATE INDEX order_items_order_idx    ON order_items (order_id);
CREATE INDEX order_items_product_idx  ON order_items (product_id);
CREATE INDEX order_items_supplier_idx ON order_items (supplier_id);

-- ---------------------------------------------------------------------------
-- products
-- ---------------------------------------------------------------------------
CREATE TABLE products (
    product_id          UUID PRIMARY KEY,
    supplier_id         UUID NOT NULL,
    name                TEXT NOT NULL,
    type                TEXT NOT NULL,
    grade               TEXT,
    status              TEXT NOT NULL,
    markup_bps          INT NOT NULL,
    source              TEXT NOT NULL,             -- form | csv_import | admin | backfill
    size_code_count     INT NOT NULL,
    pack_count          INT NOT NULL,
    active_pack_count   INT NOT NULL,
    -- Customer-facing price range across ACTIVE packs; NULL when none.
    min_price_paise     BIGINT,
    max_price_paise     BIGINT,
    image_count         INT NOT NULL,
    video_count         INT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    first_active_at     TIMESTAMPTZ,
    archived_at         TIMESTAMPTZ,
    last_event_id       UUID NOT NULL,
    last_event_type     TEXT NOT NULL,
    last_event_at       TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX products_supplier_idx ON products (supplier_id);
CREATE INDEX products_status_idx   ON products (status);

-- ---------------------------------------------------------------------------
-- product_packs — slowly changing dimension, type 2
-- ---------------------------------------------------------------------------
CREATE TABLE product_packs (
    pack_option_id        UUID NOT NULL,
    valid_from            TIMESTAMPTZ NOT NULL,
    -- NULL = the version in force now.
    valid_to              TIMESTAMPTZ,
    product_id            UUID NOT NULL,
    size_code_id          UUID NOT NULL,
    size_code             TEXT NOT NULL,
    size_meta             TEXT,
    harvest_share_pct     SMALLINT,
    size_code_active      BOOLEAN NOT NULL,
    label                 TEXT NOT NULL,
    weight_grams          INT NOT NULL,
    price_paise           BIGINT NOT NULL,          -- what the grower charges
    markup_bps            INT NOT NULL,
    customer_price_paise  BIGINT NOT NULL,          -- what the customer pays
    is_active             BOOLEAN NOT NULL,
    PRIMARY KEY (pack_option_id, valid_from),
    CHECK (valid_to IS NULL OR valid_to >= valid_from)
);

-- At most one current version per pack.
CREATE UNIQUE INDEX product_packs_current_idx ON product_packs (pack_option_id)
    WHERE valid_to IS NULL;
CREATE INDEX product_packs_product_current_idx ON product_packs (product_id)
    WHERE valid_to IS NULL;

-- ---------------------------------------------------------------------------
-- pipeline bookkeeping
-- ---------------------------------------------------------------------------

-- Dedupe. Postgres resends everything after the last confirmed position on a
-- reconnect, so an event can arrive twice; its id makes the second a no-op.
CREATE TABLE processed_events (
    event_id      UUID PRIMARY KEY,
    source_table  TEXT NOT NULL,
    lsn           TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX processed_events_at_idx ON processed_events (processed_at);

-- Events the consumer could not understand (an unknown schema_version, a
-- payload that does not parse). Parked rather than retried: retrying cannot
-- fix them, and a stuck event would stop every event behind it.
CREATE TABLE dead_letter_events (
    event_id      UUID PRIMARY KEY,
    source_table  TEXT NOT NULL,
    event_type    TEXT,
    payload       JSONB,
    error         TEXT NOT NULL,
    lsn           TEXT NOT NULL,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- convenience views
-- ---------------------------------------------------------------------------

-- Every line with its order's state and business day — the usual starting
-- point for revenue by product, grade or supplier.
CREATE VIEW v_order_lines AS
SELECT i.*, o.order_number, o.customer_id, o.status, o.source, o.payment_method,
       o.placed_at, o.paid_at, o.placed_date_ist, o.placed_before_cutoff,
       o.ship_city, o.ship_state, o.ship_pincode
FROM order_items i
JOIN orders o USING (order_id);

-- The packs on sale now.
CREATE VIEW v_current_packs AS
SELECT * FROM product_packs WHERE valid_to IS NULL;

-- +goose Down
DROP VIEW IF EXISTS v_current_packs;
DROP VIEW IF EXISTS v_order_lines;
DROP TABLE IF EXISTS dead_letter_events;
DROP TABLE IF EXISTS processed_events;
DROP TABLE IF EXISTS product_packs;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;

-- analytics.suppliers — who the supplier_id on every order line and product
-- is (CLAUDE.md §5.4), from profile.supplier_events_outbox.
--
-- Business name, status, town and commission only: the payload has no field
-- for the contact person, phone, email, tax ids, street address or bank
-- details, so neither does this table.

-- +goose Up
CREATE TABLE suppliers (
    supplier_id      UUID PRIMARY KEY,
    business_name    TEXT NOT NULL,
    status           TEXT NOT NULL,          -- pending | approved | rejected | suspended
    city             TEXT,
    state            TEXT,
    pincode          TEXT,
    gst_registered   BOOLEAN NOT NULL,
    -- Negotiated commission in basis points; NULL = the platform default.
    commission_bps   INT,
    created_at       TIMESTAMPTZ NOT NULL,
    approved_at      TIMESTAMPTZ,
    suspended_at     TIMESTAMPTZ,
    last_event_id    UUID NOT NULL,
    last_event_type  TEXT NOT NULL,
    last_event_at    TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX suppliers_status_idx ON suppliers (status);

-- Lines now say WHO sold them. A LEFT JOIN: a line whose supplier has not
-- reached analytics yet (or predates the supplier backfill) still counts.
CREATE OR REPLACE VIEW v_order_lines AS
SELECT i.*, o.order_number, o.customer_id, o.status, o.source, o.payment_method,
       o.placed_at, o.paid_at, o.placed_date_ist, o.placed_before_cutoff,
       o.ship_city, o.ship_state, o.ship_pincode,
       s.business_name AS supplier_name
FROM order_items i
JOIN orders o USING (order_id)
LEFT JOIN suppliers s USING (supplier_id);

-- +goose Down
-- A view cannot drop a column in place.
DROP VIEW IF EXISTS v_order_lines;
CREATE VIEW v_order_lines AS
SELECT i.*, o.order_number, o.customer_id, o.status, o.source, o.payment_method,
       o.placed_at, o.paid_at, o.placed_date_ist, o.placed_before_cutoff,
       o.ship_city, o.ship_state, o.ship_pincode
FROM order_items i
JOIN orders o USING (order_id);
DROP TABLE IF EXISTS suppliers;

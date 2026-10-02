-- Stock holds — the catalog side of CLAUDE.md §6.3.
--
-- Grams are held here, next to the daily_availability rows they decrement, so
-- the FOR UPDATE and the increment are ONE transaction. vm-orders-api mirrors
-- these rows in its own schema and refers to them by id; it cannot touch this
-- table directly (CLAUDE.md §3).

-- +goose Up

CREATE TABLE stock_holds (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- orders.orders(id). No FK: different schema, different owner.
    order_ref    UUID NOT NULL,
    product_id   UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    available_on DATE NOT NULL,
    grams        INTEGER NOT NULL CHECK (grams > 0),
    status       TEXT NOT NULL DEFAULT 'held'
                      CHECK (status IN ('held', 'committed', 'released')),
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at   TIMESTAMPTZ
);

CREATE INDEX stock_holds_order_idx ON stock_holds (order_ref);
-- The sweeper's index: only held rows can expire.
CREATE INDEX stock_holds_expiry_idx ON stock_holds (expires_at) WHERE status = 'held';

-- +goose Down

DROP TABLE IF EXISTS stock_holds;
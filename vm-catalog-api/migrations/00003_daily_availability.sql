-- Daily availability — CLAUDE.md §5.2.
--
-- The core stock model: a supplier declares how many GRAMS of a product they
-- have on a given day, and any mix of unit sizes sells from that one pool
-- until it runs out. Availability does not carry over; each day is a fresh row.

-- +goose Up

CREATE TABLE daily_availability (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Denormalised from products.supplier_id so the supplier's daily sheet is
    -- one indexed read, without joining products on every query.
    supplier_id    UUID NOT NULL,
    product_id     UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,

    -- A DATE, not a timestamp: "today" is a business day in Asia/Kolkata
    -- (CLAUDE.md rule 2), and storing an instant would invite comparing it in
    -- whatever timezone the server happens to run in.
    available_on   DATE NOT NULL,

    -- Grams at the PRODUCT level, not per unit. "40 kg of tomatoes today"
    -- sells as any mix of 1/3/5 kg packs until the grams run out.
    total_grams    INTEGER NOT NULL DEFAULT 0 CHECK (total_grams >= 0),
    -- Held by carts that have not paid yet (CLAUDE.md §6.3).
    reserved_grams INTEGER NOT NULL DEFAULT 0 CHECK (reserved_grams >= 0),
    -- Committed by paid orders.
    sold_grams     INTEGER NOT NULL DEFAULT 0 CHECK (sold_grams >= 0),

    status         TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- CLAUDE.md §5.2. One declaration per product per day; a second row would
    -- make "remaining" ambiguous.
    CONSTRAINT daily_availability_one_per_day UNIQUE (product_id, available_on),

    -- The invariant that makes overselling structurally impossible: a
    -- supplier cannot declare less than what customers have already reserved
    -- or bought. Enforced here rather than only in the API, so no code path —
    -- including a future bulk job — can violate it.
    CONSTRAINT daily_availability_not_below_committed
        CHECK (total_grams >= reserved_grams + sold_grams)
);

CREATE TRIGGER daily_availability_set_updated_at
    BEFORE UPDATE ON daily_availability
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The supplier's daily sheet: "everything I declared for this date".
CREATE INDEX daily_availability_supplier_date_idx
    ON daily_availability (supplier_id, available_on DESC);

-- The customer catalogue: "what is sellable today". Partial, because rows for
-- closed products and past days are never part of that query.
CREATE INDEX daily_availability_sellable_idx
    ON daily_availability (available_on, product_id)
    WHERE status = 'open' AND total_grams > reserved_grams + sold_grams;

-- +goose Down

DROP TABLE IF EXISTS daily_availability;

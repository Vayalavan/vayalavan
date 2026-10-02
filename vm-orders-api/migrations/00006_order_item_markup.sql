-- The markup, snapshotted onto every order line.
--
-- unit_price_paise stays what the CUSTOMER paid, so every total already stored
-- on an order remains true and no order history changes meaning. What was
-- missing is which part of that price was ours: without it, supplier payouts
-- are derived from the customer price and every grower is quietly paid our
-- markup as well.
--
--   customer paid for a line = line_total_paise
--   supplier is owed         = line_total_paise - line_markup_paise
--   we earned                = line_markup_paise
--
-- Snapshotted rather than looked up, for the same reason as the name and the
-- price (CLAUDE.md §5.3): an admin changing a markup tomorrow must not restate
-- what a supplier was owed for an order placed today.
--
-- Both columns default to 0, so every order placed before this migration reads
-- as "no markup" — which is exactly what it was.

-- +goose Up

ALTER TABLE order_items
    ADD COLUMN markup_paise BIGINT NOT NULL DEFAULT 0
        CHECK (markup_paise >= 0),
    -- markup_paise * qty, stored rather than derived so the aggregate queries
    -- that reconcile payouts read one column instead of multiplying, exactly
    -- as line_total_paise does beside unit_price_paise.
    ADD COLUMN line_markup_paise BIGINT NOT NULL DEFAULT 0
        CHECK (line_markup_paise >= 0);

COMMENT ON COLUMN order_items.markup_paise IS
    'Admin markup per pack, included in unit_price_paise. Never paid to the supplier.';
COMMENT ON COLUMN order_items.line_markup_paise IS
    'markup_paise * qty. Subtract from line_total_paise for the supplier''s share.';

-- +goose Down

ALTER TABLE order_items
    DROP COLUMN IF EXISTS markup_paise,
    DROP COLUMN IF EXISTS line_markup_paise;

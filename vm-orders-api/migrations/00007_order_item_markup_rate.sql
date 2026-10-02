-- The RATE behind each line's markup, for the record.
--
-- markup_paise and line_markup_paise are unchanged and remain the authority on
-- money: they are what was actually added and what the grower is not owed. The
-- rate is stored beside them so a line can be explained months later — "why
-- ₹600 on this one and ₹300 on that one?" is answered by 30% of two different
-- pack prices, not by two different decisions.
--
-- Snapshotted like everything else on an order line (CLAUDE.md §5.3): the
-- product's markup today says nothing about what was charged in June.
--
-- Existing lines default to 0, which is what they were charged at.

-- +goose Up

ALTER TABLE order_items
    ADD COLUMN markup_bps INTEGER NOT NULL DEFAULT 0
        CHECK (markup_bps >= 0 AND markup_bps <= 10000);

COMMENT ON COLUMN order_items.markup_bps IS
    'The markup rate applied to this line, in basis points. markup_paise is the amount it produced.';

-- +goose Down

ALTER TABLE order_items DROP COLUMN IF EXISTS markup_bps;

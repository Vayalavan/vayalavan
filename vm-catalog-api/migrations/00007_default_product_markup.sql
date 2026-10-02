-- The platform's default markup: 5%.
--
-- Two parts, and they are deliberately different in scope.
--
-- 1. The column default becomes 500 bps. It is a backstop only — every INSERT
--    in this service passes markup_bps explicitly from
--    PRODUCT_MARKUP_DEFAULT_BPS, because a commercial rate belongs in the
--    environment (CLAUDE.md rule 3) and not in two places. The column default
--    exists so a row inserted by hand, by a fixture or by a future service
--    does not silently land at zero.
--
-- 2. Products still on 0 are moved to 500. Every one of them predates the
--    markup feature, so their zero is "nobody has set one yet", not a decision
--    to sell at cost — which is exactly what a default is for.
--
--    This REPRICES the live storefront: every affected product gets 5% dearer
--    the moment this runs. It is one UPDATE away from being undone
--    (SET markup_bps = 0), and the admin products screen shows the resulting
--    customer price per pack.
--
--    Products with a markup already set are untouched. A deliberate 0% would
--    be indistinguishable from an unset one here, which is a real limitation
--    of running this backfill once, at the point where no such choice has yet
--    been made — after today, "0%" is a choice and nothing rewrites it.

-- +goose Up

ALTER TABLE products ALTER COLUMN markup_bps SET DEFAULT 500;

UPDATE products SET markup_bps = 500 WHERE markup_bps = 0;

-- +goose Down

ALTER TABLE products ALTER COLUMN markup_bps SET DEFAULT 0;

-- Deliberately NOT reversing the backfill: by the time this runs down, a 5%
-- markup may have been kept on purpose, and resetting those to 0 would give
-- away margin on produce someone chose to price that way.

-- Markup becomes a PERCENTAGE of the grower's price, not a flat amount.
--
-- A flat markup priced a 5 kg pack and a 10 kg pack identically: ₹300 on both,
-- which is 30% of one and 15% of the other. As a percentage the margin holds
-- its shape across pack sizes — 30% is ₹300 on a ₹1,000 pack and ₹600 on a
-- ₹2,000 one.
--
-- Stored in BASIS POINTS, like every other rate on the platform
-- (PLATFORM_FEE_BPS, SUPPLIER_COMMISSION_BPS, suppliers.commission_bps): 3000
-- is 30.00%. An integer, because a rate that decides money must not be a float
-- (CLAUDE.md rule 1), and basis points keep two decimal places of a percent
-- exact without one.
--
-- The rupee amount is still what gets snapshotted onto an order line — the
-- percentage is how it is DECIDED, and the amount is what was actually taken.
--
-- Migrating existing values: a flat markup carries across as the percentage it
-- represented on that product's cheapest active pack. That is the pack an
-- admin was almost certainly looking at when they typed the amount, and it
-- reproduces their intent exactly where every pack was priced proportionally.
-- Products with no markup, or no priced pack to measure against, become 0.

-- +goose Up

ALTER TABLE products
    ADD COLUMN markup_bps INTEGER NOT NULL DEFAULT 0
        CHECK (markup_bps >= 0 AND markup_bps <= 10000);

UPDATE products p
SET markup_bps = sub.bps
FROM (
    SELECT
        u.product_id,
        -- Half-up in integers: +5000 before dividing by the price is the same
        -- rounding rule the fee arithmetic uses.
        ((p2.markup_paise * 10000 + u.price_paise / 2) / u.price_paise)::int AS bps
    FROM products p2
    JOIN LATERAL (
        SELECT price_paise, product_id
        FROM product_units
        WHERE product_id = p2.id AND is_active AND price_paise > 0
        ORDER BY price_paise
        LIMIT 1
    ) u ON true
    WHERE p2.markup_paise > 0
) AS sub
WHERE p.id = sub.product_id
  -- A flat markup larger than the pack it sat on would exceed 100%, which the
  -- CHECK refuses. Those stay 0 and are re-entered by hand.
  AND sub.bps <= 10000;

ALTER TABLE products DROP COLUMN markup_paise;

COMMENT ON COLUMN products.markup_bps IS
    'Admin markup in basis points of the supplier price (3000 = 30%). Never shown to the supplier.';

-- +goose Down

ALTER TABLE products
    ADD COLUMN markup_paise BIGINT NOT NULL DEFAULT 0
        CHECK (markup_paise >= 0);

-- Back to a flat amount, measured against the same cheapest active pack.
UPDATE products p
SET markup_paise = sub.paise
FROM (
    SELECT
        u.product_id,
        ((p2.markup_bps::bigint * u.price_paise + 5000) / 10000) AS paise
    FROM products p2
    JOIN LATERAL (
        SELECT price_paise, product_id
        FROM product_units
        WHERE product_id = p2.id AND is_active AND price_paise > 0
        ORDER BY price_paise
        LIMIT 1
    ) u ON true
    WHERE p2.markup_bps > 0
) AS sub
WHERE p.id = sub.product_id;

ALTER TABLE products DROP COLUMN markup_bps;

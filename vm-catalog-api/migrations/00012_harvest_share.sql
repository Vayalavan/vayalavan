-- What proportion of a product's harvest each GRADE is.
--
-- A grower's crates are not equal piles. A season of pomegranate might run
-- 55% M, 30% L, 12% XL and 3% XL2 — the XL2 exists because a handful of fruit
-- on each tree happened to size up, and there will never be much of it. That
-- fact is invisible on the storefront today: four chips in a size selector,
-- all drawn identically, and nothing tells a customer that one of them is the
-- pick of the field and another is most of what came off it.
--
--     size code   M     harvest_share_pct 55   <- most of the field
--     size code   L     harvest_share_pct 30
--     size code   XL    harvest_share_pct 12
--     size code   XL2   harvest_share_pct  3   <- rare
--
-- A GENERAL property of the grade, not a daily one. It describes how the
-- grower's trees size up season after season, which is why it lives here and
-- not on daily_availability: today's 40 kg of M is stock, and stock already
-- has a home. Nothing in pricing, reservation or fulfilment reads this column
-- — it decides how a chip is DRAWN and nothing else.
--
-- Nullable, because a grower who has not estimated their split is the normal
-- starting state and must not be forced to invent a number. NULL renders no
-- rarity treatment at all, exactly as a product with no photograph renders the
-- placeholder.
--
-- Whole percent rather than basis points: a grower eyeing four crates in a
-- packing shed is not working to a hundredth of a percent, and storing more
-- precision than the estimate has would be inventing it. This is not money —
-- CLAUDE.md rule 1 governs paise, and nothing here is ever added to a total.

-- +goose Up

ALTER TABLE product_size_codes
    ADD COLUMN harvest_share_pct SMALLINT
        CHECK (harvest_share_pct IS NULL
               OR (harvest_share_pct >= 1 AND harvest_share_pct <= 100));

COMMENT ON COLUMN product_size_codes.harvest_share_pct IS
    'Roughly what percent of this product''s harvest comes off as this grade. NULL means the grower has not said. Display only: no price, stock or fulfilment logic reads it.';

-- +goose Down

ALTER TABLE product_size_codes DROP COLUMN harvest_share_pct;

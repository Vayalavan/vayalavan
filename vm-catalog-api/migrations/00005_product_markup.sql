-- Admin markup — the difference between what a grower charges and what a
-- customer pays.
--
-- One flat amount per PRODUCT, added to every pack of it regardless of size:
-- a ₹10 markup makes a 1 kg pack ₹10 dearer and a 10 kg pack ₹10 dearer. That
-- is what "markup in rupees" means to the person typing it, and it keeps the
-- number they enter equal to the number every price moves by.
--
-- It lives on the product, not the unit, so adding a pack size cannot
-- accidentally ship at the supplier's price.
--
-- IMPORTANT: this column is never returned to the supplier who owns the row.
-- A grower sets and sees their own prices; what we add on top is ours, and
-- exposing it would turn every listing into a negotiation. The supplier
-- endpoints in vm-catalog-api select it into no response, and vm-orders-api
-- subtracts it from every figure on the supplier's sales screen.

-- +goose Up

ALTER TABLE products
    ADD COLUMN markup_paise BIGINT NOT NULL DEFAULT 0
        CHECK (markup_paise >= 0);

COMMENT ON COLUMN products.markup_paise IS
    'Flat admin markup in paise, added to every pack of this product. Never shown to the supplier.';

-- +goose Down

ALTER TABLE products DROP COLUMN IF EXISTS markup_paise;

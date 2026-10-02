-- Media that belongs to the PRODUCT rather than to one grade.
--
-- The gallery moved onto the size code in 00009 because the photographs are of
-- a grade: a customer switching from M to XL should see XL's fruit. That is
-- still true of the pictures that sell the produce. It is not true of the
-- pictures that sell the FARM — the field being harvested, the packing shed,
-- the grower turning a crate over. Those are the same whichever crate you
-- buy, and a grower with four grades was uploading them four times.
--
--     product   Pomegranate
--       common media                 <- the farm, the packing, the grower
--       size code   M2
--         media                      <- M2's own fruit
--       size code   L2
--         media                      <- L2's own fruit
--
-- A NULL size_code_id is what "belongs to the whole product" means. Three
-- things follow from that, and each is the reason it is modelled this way
-- rather than as a second table:
--
--   * The composite foreign key is MATCH SIMPLE, so a row with a NULL
--     size_code_id is simply not checked against product_size_codes — and,
--     more importantly, is NOT cascaded when a grade is deleted. Saving a
--     product replaces its whole grade tree (see replaceSizeCodes), and the
--     farm photographs have to survive that.
--   * product_id already carries its own foreign key to products, so common
--     media still disappears with the product it belongs to.
--   * Every read path already selects from one table, and keeps doing so.
--
-- The customer sees one gallery per grade: that grade's own pictures first,
-- the common ones after. Ordering is what expresses "and then the farm" —
-- there is no flag for it, exactly as the cover is the first image and not a
-- boolean.

-- +goose Up

ALTER TABLE product_media ALTER COLUMN size_code_id DROP NOT NULL;

-- The "same file twice" guard, for the common bucket. The existing unique
-- index is on (size_code_id, object_key) and Postgres treats NULLs as
-- distinct, so without this a double-submit would insert the same key twice.
CREATE UNIQUE INDEX product_media_product_common_key_key
    ON product_media (product_id, object_key)
    WHERE size_code_id IS NULL;

-- Listing the common bucket for a product, in the grower's order.
CREATE INDEX product_media_product_common_idx
    ON product_media (product_id, sort_order, created_at)
    WHERE size_code_id IS NULL;

COMMENT ON COLUMN product_media.size_code_id IS
    'The grade these pictures are of, or NULL for media that belongs to the whole product and is shown after every grade''s own.';

-- +goose Down

-- Common media has nowhere to go in a schema where every row is a grade's:
-- attaching it to an arbitrary size code would put the farm photographs in one
-- crate's gallery and leave the others short.
DELETE FROM product_media WHERE size_code_id IS NULL;

DROP INDEX IF EXISTS product_media_product_common_idx;
DROP INDEX IF EXISTS product_media_product_common_key_key;

ALTER TABLE product_media ALTER COLUMN size_code_id SET NOT NULL;

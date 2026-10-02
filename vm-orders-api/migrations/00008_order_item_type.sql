-- The produce CATEGORY on each order line — fruit, vegetable, microgreen, other.
--
-- Snapshotted like the rest of the line (CLAUDE.md §5.3), and for a reason
-- that is structural rather than stylistic: the type lives on catalog.products,
-- and this service may not read the catalog schema (CLAUDE.md §3). Without a
-- copy here, no report built on orders can ever say what kind of produce was
-- sold — not by joining, not later, not at all.
--
-- NULLABLE on purpose. Lines written before this column existed have no
-- category and cannot be given one: recovering it would mean reading
-- catalog.products, which is exactly what this service cannot do, and guessing
-- from the product name would be inventing data. Reports show those lines as
-- "Unrecorded" rather than folding them into 'other', which is a real category
-- a grower can choose.
--
-- No CHECK constraint against the enum: catalog owns that vocabulary, and a
-- constraint here would be a second copy of it that has to be migrated in
-- lockstep with another service's migrations.

-- +goose Up

ALTER TABLE order_items ADD COLUMN product_type_snapshot TEXT;

COMMENT ON COLUMN order_items.product_type_snapshot IS
    'Produce category as it was when sold (fruit/vegetable/microgreen/other). NULL on lines written before the column existed.';

-- +goose Down

ALTER TABLE order_items DROP COLUMN IF EXISTS product_type_snapshot;

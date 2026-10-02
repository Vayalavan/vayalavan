-- The GRADE an order line was bought at — CLAUDE.md §5.3.
--
-- Order lines snapshot everything, because a product can be edited or archived
-- and the order must still render exactly what was sold. Size codes made that
-- list longer: "1 kg of Tomato at Rs 100" is now ambiguous between grades that
-- have different prices, and the orders schema cannot look the grade up later
-- — it lives in the catalog schema this service may not read (CLAUDE.md §3).
--
-- Nullable, and left NULL on every line placed before grades existed. Those
-- orders were genuinely ungraded; writing 'STD' into them would invent a fact,
-- and the clients render a missing grade as no grade at all.
--
-- size_code_id is NOT a foreign key, for the same reason product_id is not:
-- different schema, different owner.

-- +goose Up

ALTER TABLE order_items
    ADD COLUMN size_code_id      UUID,
    ADD COLUMN size_code_snapshot TEXT,
    ADD COLUMN size_meta_snapshot TEXT;

COMMENT ON COLUMN order_items.size_code_id IS
    'catalog.product_size_codes(id). No FK by design — different schema.';
COMMENT ON COLUMN order_items.size_code_snapshot IS
    'The grade code as it read when the order was placed, e.g. ''M2''. NULL for pre-grade orders.';
COMMENT ON COLUMN order_items.size_meta_snapshot IS
    'The grade''s detail line as it read then, e.g. ''150 g - 200 g''.';

-- +goose Down

ALTER TABLE order_items
    DROP COLUMN IF EXISTS size_meta_snapshot,
    DROP COLUMN IF EXISTS size_code_snapshot,
    DROP COLUMN IF EXISTS size_code_id;

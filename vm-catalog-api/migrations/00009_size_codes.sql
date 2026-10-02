-- Size codes — a grading level between a product and the packs it sells in.
--
-- Before this, a product had a flat set of units ("1 kg", "3 kg") and one pool
-- of grams a day. That cannot express how graded produce is actually sold: a
-- grower has 40 kg of M tomatoes (150-200 g each) and 15 kg of XL, in separate
-- crates, at different prices, photographed separately. The flat model would
-- sell an XL box out of a pool that is really M.
--
--     product            Tomato
--       size code        M2   meta "150 g - 200 g"   <- media and STOCK here
--         pack option    1 kg    Rs 1000
--         pack option    1.5 kg  Rs 2500
--       size code        XL   meta "260 g +"
--         pack option    1 kg    Rs 1400
--
-- Three things move down onto the size code, and each for its own reason:
--
--   * PACKS, because a price belongs to (size, weight) and not to weight
--     alone — 1 kg of XL is not 1 kg of M.
--   * MEDIA, because the photographs are of that grade. The storefront swaps
--     the gallery when the customer changes size code.
--   * STOCK, because the crates are physically separate. Selling out M must
--     leave L untouched, which one shared pool cannot express.
--
-- product_id stays denormalised on the child tables, and is held honest by a
-- COMPOSITE foreign key against product_size_codes (id, product_id) rather
-- than by convention — a pack option whose product_id disagrees with its size
-- code's is then not merely wrong, it is unwritable.
--
-- Existing rows are carried across under one implicit size code per product
-- (code 'STD'), which keeps every unit, photograph and declared gram exactly
-- where it was. A product with a single size code renders no size selector,
-- so nothing a supplier already listed changes shape on screen.

-- +goose Up

-- ---------------------------------------------------------------------------
-- product_size_codes
-- ---------------------------------------------------------------------------

CREATE TABLE product_size_codes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    -- The grade label the supplier types: 'M', 'L', 'XL', 'M2'. Short by
    -- intent — it is a chip in a size selector, not a sentence.
    code       TEXT NOT NULL CHECK (length(btrim(code)) BETWEEN 1 AND 20),
    -- Optional detail for the customer: "150 g - 200 g", "6-8 per kg". Free
    -- text, never parsed — the weight that matters is on the pack option.
    meta       TEXT CHECK (meta IS NULL OR length(btrim(meta)) <= 80),
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The target of the composite FKs below. Redundant with the primary key,
    -- and there so a child row cannot claim a different product than its
    -- parent does.
    CONSTRAINT product_size_codes_id_product_key UNIQUE (id, product_id)
);

CREATE TRIGGER product_size_codes_set_updated_at
    BEFORE UPDATE ON product_size_codes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX product_size_codes_product_idx
    ON product_size_codes (product_id, sort_order, created_at);

-- One 'M' per product. Case- and space-insensitive, because a supplier typing
-- 'm ' means the grade they already created.
CREATE UNIQUE INDEX product_size_codes_product_code_key
    ON product_size_codes (product_id, lower(btrim(code)));

COMMENT ON TABLE product_size_codes IS
    'Grading level between a product and its packs. Carries the media and the daily stock.';

-- Every existing product gets one implicit size code holding everything it
-- already had.
INSERT INTO product_size_codes (product_id, code, meta, sort_order)
SELECT id, 'STD', NULL, 0 FROM products;

-- ---------------------------------------------------------------------------
-- product_pack_options  (replaces product_units)
-- ---------------------------------------------------------------------------

CREATE TABLE product_pack_options (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    size_code_id UUID NOT NULL,
    -- Denormalised from the size code so the cart and the reserve path can
    -- reach the product without a join. Kept honest by the composite FK.
    product_id   UUID NOT NULL,
    -- Free text as the supplier types it: "1 kg", "1.5 kg box", "250 g".
    label        TEXT NOT NULL CHECK (length(btrim(label)) BETWEEN 1 AND 40),
    -- Stock is grams against the SIZE CODE (see daily_availability below), so
    -- every pack must declare what it consumes from that pool.
    weight_grams INTEGER NOT NULL CHECK (weight_grams > 0 AND weight_grams <= 1000000),
    -- The price of the WHOLE pack, not per kg. int64 paise, never a float
    -- (CLAUDE.md rule 1).
    price_paise  BIGINT NOT NULL CHECK (price_paise >= 0),
    is_active    BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Two packs of the same weight under one size code would be
    -- indistinguishable when decrementing that size code's gram pool. The
    -- SAME weight under a DIFFERENT size code is fine, and is the point of
    -- this table: 1 kg of M and 1 kg of XL are different goods at different
    -- prices.
    CONSTRAINT product_pack_options_unique_weight UNIQUE (size_code_id, weight_grams),

    CONSTRAINT product_pack_options_size_code_fk
        FOREIGN KEY (size_code_id, product_id)
        REFERENCES product_size_codes (id, product_id) ON DELETE CASCADE
);

CREATE TRIGGER product_pack_options_set_updated_at
    BEFORE UPDATE ON product_pack_options
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX product_pack_options_size_code_idx
    ON product_pack_options (size_code_id, sort_order);
CREATE INDEX product_pack_options_product_idx
    ON product_pack_options (product_id);

-- Carry every unit across as a pack option of its product's 'STD' size code.
INSERT INTO product_pack_options (
    id, size_code_id, product_id, label, weight_grams, price_paise,
    is_active, sort_order, created_at, updated_at
)
SELECT
    u.id,                -- keep the id: carts and orders already reference it
    s.id,
    u.product_id,
    u.label,
    u.weight_grams,
    u.price_paise,
    u.is_active,
    u.sort_order,
    u.created_at,
    u.updated_at
FROM product_units u
JOIN product_size_codes s ON s.product_id = u.product_id AND s.code = 'STD';

DROP TABLE product_units;

-- ---------------------------------------------------------------------------
-- product_media moves onto the size code
-- ---------------------------------------------------------------------------
--
-- The photographs are of a grade, not of a product. A customer switching from
-- M to XL sees XL's pictures.

ALTER TABLE product_media ADD COLUMN size_code_id UUID;

UPDATE product_media m
SET size_code_id = s.id
FROM product_size_codes s
WHERE s.product_id = m.product_id AND s.code = 'STD';

-- Any row whose product vanished mid-migration has nowhere to go; there is no
-- such row, and this makes that explicit rather than leaving a NULL behind.
DELETE FROM product_media WHERE size_code_id IS NULL;

ALTER TABLE product_media
    ALTER COLUMN size_code_id SET NOT NULL,
    ADD CONSTRAINT product_media_size_code_fk
        FOREIGN KEY (size_code_id, product_id)
        REFERENCES product_size_codes (id, product_id) ON DELETE CASCADE;

-- The gallery is now per size code, so the "same file twice" guard is too.
DROP INDEX product_media_product_key_key;
CREATE UNIQUE INDEX product_media_size_code_key_key
    ON product_media (size_code_id, object_key);

DROP INDEX product_media_product_idx;
CREATE INDEX product_media_size_code_idx
    ON product_media (size_code_id, sort_order, created_at);

COMMENT ON TABLE product_media IS
    'Images and videos for one SIZE CODE. Cover = first row of kind ''image'' by sort_order.';

-- ---------------------------------------------------------------------------
-- daily_availability moves onto the size code
-- ---------------------------------------------------------------------------
--
-- The crates are physically separate, so the gram pool is too. Selling out M
-- must leave L untouched.

ALTER TABLE daily_availability ADD COLUMN size_code_id UUID;

UPDATE daily_availability a
SET size_code_id = s.id
FROM product_size_codes s
WHERE s.product_id = a.product_id AND s.code = 'STD';

DELETE FROM daily_availability WHERE size_code_id IS NULL;

ALTER TABLE daily_availability
    ALTER COLUMN size_code_id SET NOT NULL,
    ADD CONSTRAINT daily_availability_size_code_fk
        FOREIGN KEY (size_code_id, product_id)
        REFERENCES product_size_codes (id, product_id) ON DELETE CASCADE;

-- One declaration per SIZE CODE per day. A product now has as many rows a day
-- as it has grades.
ALTER TABLE daily_availability
    DROP CONSTRAINT daily_availability_one_per_day,
    ADD CONSTRAINT daily_availability_one_per_day UNIQUE (size_code_id, available_on);

DROP INDEX daily_availability_sellable_idx;
CREATE INDEX daily_availability_sellable_idx
    ON daily_availability (available_on, size_code_id)
    WHERE status = 'open' AND total_grams > reserved_grams + sold_grams;

-- The catalogue joins a page of size codes to their declarations for today.
CREATE INDEX daily_availability_date_size_code_idx
    ON daily_availability (available_on, size_code_id);

-- ---------------------------------------------------------------------------
-- stock_holds move onto the size code
-- ---------------------------------------------------------------------------

ALTER TABLE stock_holds ADD COLUMN size_code_id UUID;

UPDATE stock_holds h
SET size_code_id = s.id
FROM product_size_codes s
WHERE s.product_id = h.product_id AND s.code = 'STD';

DELETE FROM stock_holds WHERE size_code_id IS NULL;

ALTER TABLE stock_holds
    ALTER COLUMN size_code_id SET NOT NULL,
    ADD CONSTRAINT stock_holds_size_code_fk
        FOREIGN KEY (size_code_id, product_id)
        REFERENCES product_size_codes (id, product_id) ON DELETE CASCADE;

CREATE INDEX stock_holds_size_code_idx ON stock_holds (size_code_id, available_on);

-- +goose Down

-- Rolling back COLLAPSES every size code of a product back into one. That is
-- lossy by nature: a product graded M/L/XL had three galleries, three gram
-- pools and three price lists, and the flat model holds one of each. The rules
-- below are chosen so what survives is the FIRST size code's — the same one
-- the storefront selects by default.

DROP INDEX IF EXISTS stock_holds_size_code_idx;
ALTER TABLE stock_holds
    DROP CONSTRAINT IF EXISTS stock_holds_size_code_fk,
    DROP COLUMN IF EXISTS size_code_id;

DROP INDEX IF EXISTS daily_availability_date_size_code_idx;
DROP INDEX IF EXISTS daily_availability_sellable_idx;

-- Sum the grades back into one pool per product per day, keeping one row.
DELETE FROM daily_availability a
USING daily_availability b
WHERE a.product_id = b.product_id
  AND a.available_on = b.available_on
  AND a.ctid > b.ctid;

ALTER TABLE daily_availability
    DROP CONSTRAINT IF EXISTS daily_availability_size_code_fk,
    DROP CONSTRAINT IF EXISTS daily_availability_one_per_day,
    ADD CONSTRAINT daily_availability_one_per_day UNIQUE (product_id, available_on);
ALTER TABLE daily_availability DROP COLUMN IF EXISTS size_code_id;

CREATE INDEX daily_availability_sellable_idx
    ON daily_availability (available_on, product_id)
    WHERE status = 'open' AND total_grams > reserved_grams + sold_grams;

-- Keep only the first size code's gallery, so the surviving pictures are the
-- ones the storefront was showing.
DELETE FROM product_media m
USING product_size_codes s, product_size_codes keep
WHERE m.size_code_id = s.id
  AND keep.product_id = s.product_id
  AND keep.id <> s.id
  AND (keep.sort_order, keep.created_at) < (s.sort_order, s.created_at);

DROP INDEX IF EXISTS product_media_size_code_idx;
DROP INDEX IF EXISTS product_media_size_code_key_key;
ALTER TABLE product_media
    DROP CONSTRAINT IF EXISTS product_media_size_code_fk,
    DROP COLUMN IF EXISTS size_code_id;

CREATE INDEX product_media_product_idx
    ON product_media (product_id, sort_order, created_at);
CREATE UNIQUE INDEX product_media_product_key_key
    ON product_media (product_id, object_key);

CREATE TABLE product_units (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id   UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    label        TEXT NOT NULL CHECK (length(btrim(label)) BETWEEN 1 AND 40),
    weight_grams INTEGER NOT NULL CHECK (weight_grams > 0 AND weight_grams <= 1000000),
    price_paise  BIGINT NOT NULL CHECK (price_paise >= 0),
    is_active    BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT product_units_unique_weight UNIQUE (product_id, weight_grams)
);

CREATE TRIGGER product_units_set_updated_at
    BEFORE UPDATE ON product_units
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX product_units_product_idx ON product_units (product_id, sort_order);

-- One unit per (product, weight): where two grades sold the same weight, the
-- first size code's price is the one that survives.
INSERT INTO product_units (
    id, product_id, label, weight_grams, price_paise, is_active, sort_order,
    created_at, updated_at
)
SELECT DISTINCT ON (p.product_id, p.weight_grams)
    p.id, p.product_id, p.label, p.weight_grams, p.price_paise,
    p.is_active, p.sort_order, p.created_at, p.updated_at
FROM product_pack_options p
JOIN product_size_codes s ON s.id = p.size_code_id
ORDER BY p.product_id, p.weight_grams, s.sort_order, s.created_at;

DROP TABLE product_pack_options;
DROP TABLE product_size_codes;

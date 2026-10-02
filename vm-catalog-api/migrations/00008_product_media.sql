-- Multiple images and videos per product — a gallery, not a single photograph.
--
-- Supersedes products.image_key, which held exactly one object key. Produce is
-- sold on how it looks, and one still cannot show a grower turning a crate of
-- tomatoes over in their hands. Suppliers asked for several shots and a short
-- clip per listing, so media becomes a child table.
--
-- Ordering, not a flag, decides the cover. `sort_order` is the supplier's own
-- arrangement, and the COVER is simply the first row of kind 'image'. A
-- separate is_primary boolean would be a second thing to keep consistent (and
-- a partial unique index to enforce), for a choice the supplier already makes
-- by dragging. A product whose media are all videos therefore has no cover and
-- renders the placeholder, exactly as a product with no media does today.
--
-- Videos are never a cover: every list surface in the product — catalogue
-- cards, order thumbnails, the availability sheet, the admin table — needs a
-- still it can paint immediately, and a poster frame would mean decoding video
-- server-side, which this service does not do.

-- +goose Up

CREATE TABLE product_media (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('image', 'video')),
    -- The MinIO object KEY, never a full URL — same rule as the column it
    -- replaces: URLs are minted at read time (CLAUDE.md §5.2).
    object_key TEXT NOT NULL CHECK (length(btrim(object_key)) BETWEEN 1 AND 400),
    -- What the upload was presigned as. Kept so a <video> tag can be given a
    -- correct type attribute without guessing from the extension.
    content_type TEXT,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The gallery read: every media row for a product, in the supplier's order.
-- created_at breaks ties so the order is total and a page never reshuffles
-- between requests.
CREATE INDEX product_media_product_idx
    ON product_media (product_id, sort_order, created_at);

-- The same file attached to one product twice is a double-submit, not a
-- second photograph.
CREATE UNIQUE INDEX product_media_product_key_key
    ON product_media (product_id, object_key);

COMMENT ON TABLE product_media IS
    'Images and videos for a product. Cover = first row of kind ''image'' by sort_order.';

-- Carry every existing photograph across as the first media row, so no
-- listing loses its picture. content_type is left NULL: what these were
-- uploaded as was never recorded, and the clients fall back to the extension.
INSERT INTO product_media (product_id, kind, object_key, sort_order)
SELECT id, 'image', btrim(image_key), 0
FROM products
WHERE image_key IS NOT NULL AND btrim(image_key) <> '';

-- Dropped rather than left in place: two sources of truth for "the product's
-- picture" is precisely the bug this table exists to prevent.
ALTER TABLE products DROP COLUMN image_key;

-- +goose Down

ALTER TABLE products ADD COLUMN image_key TEXT;

-- Restore the cover — the first image by the supplier's own ordering. Media
-- beyond the first, and every video, cannot survive a column that holds one
-- key; rolling back is lossy by nature.
UPDATE products p
SET image_key = m.object_key
FROM (
    SELECT DISTINCT ON (product_id) product_id, object_key
    FROM product_media
    WHERE kind = 'image'
    ORDER BY product_id, sort_order, created_at
) m
WHERE m.product_id = p.id;

DROP TABLE product_media;

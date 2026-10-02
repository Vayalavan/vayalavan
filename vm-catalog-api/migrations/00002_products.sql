-- Products, units and CSV imports — CLAUDE.md §5.2.
--
-- supplier_id references profile.suppliers(id) but carries NO foreign key:
-- CLAUDE.md §3 forbids cross-schema FKs, and the vm_catalog role cannot see
-- the profile schema anyway. Ownership is validated from the authenticated
-- identity on every request instead.

-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- products
-- ---------------------------------------------------------------------------

CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- profile.suppliers(id). No FK by design; see the header.
    supplier_id UUID NOT NULL,
    name        TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
    type        TEXT NOT NULL CHECK (type IN ('fruit', 'vegetable', 'microgreen', 'other')),
    grade       TEXT,
    description TEXT,
    -- The MinIO object KEY, never a full URL: URLs are generated at read time
    -- so changing bucket or host never breaks existing rows (CLAUDE.md §5.2).
    image_key   TEXT,
    status      TEXT NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft', 'active', 'archived')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER products_set_updated_at
    BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The supplier's own product list, newest first — the query behind every
-- page of the supplier UI.
CREATE INDEX products_supplier_idx ON products (supplier_id, created_at DESC);
CREATE INDEX products_status_idx ON products (status) WHERE status <> 'archived';

-- Case-insensitive substring search on name. pg_trgm makes ILIKE '%term%'
-- index-backed; without it every search is a sequential scan. The extension
-- itself is installed by infra/postgres/init/01-init.sh, which runs as the
-- superuser — a service role may not create extensions.
CREATE INDEX products_name_trgm_idx ON products USING gin (name gin_trgm_ops);

-- A supplier listing the same produce at the same grade twice is a data
-- entry mistake, and it makes the CSV importer's name+grade collapsing
-- ambiguous. Grade is nullable, so COALESCE gives NULL a stable identity.
CREATE UNIQUE INDEX products_supplier_name_grade_key
    ON products (supplier_id, lower(btrim(name)), lower(coalesce(btrim(grade), '')))
    WHERE status <> 'archived';

-- ---------------------------------------------------------------------------
-- product_units
-- ---------------------------------------------------------------------------

CREATE TABLE product_units (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id   UUID NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    -- Free text as the supplier types it: "3 kg", "250 g", "1 bunch".
    label        TEXT NOT NULL CHECK (length(btrim(label)) BETWEEN 1 AND 40),
    -- Availability is tracked in grams at the product level (CLAUDE.md §5.2),
    -- so every unit must declare its weight to consume from that pool.
    weight_grams INTEGER NOT NULL CHECK (weight_grams > 0 AND weight_grams <= 1000000),
    -- The price of the WHOLE unit, not per kg. int64 paise, never a float
    -- (CLAUDE.md rule 1).
    price_paise  BIGINT NOT NULL CHECK (price_paise >= 0),
    is_active    BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- CLAUDE.md §5.2. Two units of the same weight would be indistinguishable
    -- when decrementing the gram pool.
    CONSTRAINT product_units_unique_weight UNIQUE (product_id, weight_grams)
);

CREATE TRIGGER product_units_set_updated_at
    BEFORE UPDATE ON product_units
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX product_units_product_idx ON product_units (product_id, sort_order);

-- ---------------------------------------------------------------------------
-- csv_imports
-- ---------------------------------------------------------------------------

CREATE TABLE csv_imports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    supplier_id UUID NOT NULL,
    filename    TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'preview'
                     CHECK (status IN ('validating', 'preview', 'committed', 'failed')),
    total_rows  INTEGER NOT NULL DEFAULT 0,
    valid_rows  INTEGER NOT NULL DEFAULT 0,
    error_rows  INTEGER NOT NULL DEFAULT 0,
    -- The full per-row report: parsed values and errors. Held so the commit
    -- step re-reads exactly what the supplier was shown in the preview,
    -- rather than re-parsing an upload that is no longer in hand.
    report      JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    committed_at TIMESTAMPTZ
);

CREATE INDEX csv_imports_supplier_idx ON csv_imports (supplier_id, created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS csv_imports;
DROP TABLE IF EXISTS product_units;
DROP TABLE IF EXISTS products;
DROP FUNCTION IF EXISTS set_updated_at();

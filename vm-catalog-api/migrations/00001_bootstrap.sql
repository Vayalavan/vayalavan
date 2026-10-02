-- Bootstrap migration for the catalog schema.
--
-- Intentionally creates no tables: this is the foundation commit, and the
-- domain tables from CLAUDE.md §5.2 (products, product_units,
-- daily_availability, csv_imports) land in the migration that implements them.
--
-- It exists so the goose wiring is exercised end to end from the first run:
-- the version table is created in the catalog schema, `-migrate up` and
-- `-migrate down` both round-trip, and a broken migration setup is caught
-- now rather than on the day the first real migration is written.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF current_schema() IS DISTINCT FROM 'catalog' THEN
        RAISE EXCEPTION
            'expected search_path to resolve to the catalog schema, got %',
            current_schema();
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

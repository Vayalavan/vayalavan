-- Per-supplier commission.
--
-- The commission we deduct from a grower's payout was a single platform-wide
-- rate in the environment (SUPPLIER_COMMISSION_BPS). That works until the
-- first supplier negotiates a different one, which is a normal thing for a
-- marketplace to do and should not require a redeploy.
--
-- NULL means "use the platform default", and is deliberately the default for
-- every existing and future row: an admin who does not think about commission
-- gets the configured rate, and only a deliberate entry overrides it. Storing
-- 300 everywhere instead would freeze today's rate into history and make
-- changing the platform default a data migration.
--
-- Basis points, like every other rate in this system: an exact integer, so no
-- float ever touches a number that decides what a farmer is paid
-- (CLAUDE.md rule 1). 300 = 3.00%.

-- +goose Up

ALTER TABLE suppliers
    ADD COLUMN commission_bps INTEGER,
    -- 0 is legitimate (a supplier we take nothing from); above 10000 would be
    -- charging more than the whole sale, which is never intended and would
    -- silently zero their payout.
    ADD CONSTRAINT suppliers_commission_bps_range
        CHECK (commission_bps IS NULL OR (commission_bps >= 0 AND commission_bps <= 10000));

COMMENT ON COLUMN suppliers.commission_bps IS
    'Commission in basis points deducted from this supplier''s payout. '
    'NULL means use the platform default (SUPPLIER_COMMISSION_BPS).';

-- +goose Down

ALTER TABLE suppliers
    DROP CONSTRAINT IF EXISTS suppliers_commission_bps_range,
    DROP COLUMN IF EXISTS commission_bps;

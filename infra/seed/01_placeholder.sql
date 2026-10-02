-- Development seed data.
--
-- Empty by design at the foundation stage: the domain tables from CLAUDE.md
-- §5 do not exist yet, so there is nothing to insert. `make seed` runs every
-- .sql file in this directory in filename order, and this file keeps that
-- path exercised rather than untested until the first real seed arrives.
--
-- What lands here once the schema exists:
--   - the seeded admin user (CLAUDE.md §5.1: admins never self-signup)
--   - one approved supplier with bank details, for the payout screens
--   - a handful of products with units and today's availability, so the
--     storefront has something to render
--
-- NEVER put a real credential in this file. Seeded passwords must be
-- obviously-fake local development values (CLAUDE.md rule 4).

DO $$
BEGIN
    RAISE NOTICE 'seed: no domain tables yet — nothing to insert';
END
$$;

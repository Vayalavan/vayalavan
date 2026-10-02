-- The analyst role (CLAUDE.md §7).
--
-- Someone who reads the business's numbers and nothing else: they sign in to
-- the admin console and see only its Analytics section. Not a kind of admin —
-- an admin can approve suppliers, mark payouts paid and cancel orders, and an
-- analyst must be able to do none of it — so a separate role rather than a
-- flag on admin, and every existing admin-only route stays closed to it
-- without anyone having to remember to close it.
--
-- Created like admins, never by self-signup: seeded, or inserted by an
-- operator (infra/DEPLOY.md).

-- +goose Up
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('customer', 'supplier', 'admin', 'analyst'));

-- +goose Down
-- Refuses while any analyst account exists, rather than deleting accounts or
-- quietly turning them into something else: remove them deliberately first.
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('customer', 'supplier', 'admin'));

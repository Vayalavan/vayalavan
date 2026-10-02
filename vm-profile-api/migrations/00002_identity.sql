-- Identity, addresses and suppliers — CLAUDE.md §5.1.
--
-- Everything lives in the `profile` schema, owned by the vm_profile role.
-- Unqualified names resolve there via search_path; `public` is only on the
-- path for shared extension types (citext, gen_random_uuid).

-- +goose Up

-- +goose StatementBegin
-- Keeps updated_at honest without every query having to remember it.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- users — one identity table for all three personas
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- CITEXT so "Deepak@x.com" and "deepak@x.com" are the same account and
    -- cannot both be registered. Nullable because an admin-created supplier
    -- exists before it has confirmed anything.
    email          CITEXT UNIQUE,
    phone          TEXT UNIQUE,
    -- NULL until the user sets a password. An admin-created supplier is
    -- issued a set-password link rather than a password chosen for them.
    password_hash  TEXT,
    role           TEXT NOT NULL CHECK (role IN ('customer', 'supplier', 'admin')),
    status         TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'pending', 'suspended')),
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- A login needs something to log in with.
    CONSTRAINT users_need_an_identifier CHECK (email IS NOT NULL OR phone IS NOT NULL)
);

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Login looks up by email or phone; both are already unique, so no extra
-- index is needed. This one serves the admin user list.
CREATE INDEX users_role_status_idx ON users (role, status);

-- ---------------------------------------------------------------------------
-- customer_profiles
-- ---------------------------------------------------------------------------

CREATE TABLE customer_profiles (
    user_id    UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER customer_profiles_set_updated_at
    BEFORE UPDATE ON customer_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- addresses — soft delete only; orders snapshot them at placement time
-- ---------------------------------------------------------------------------

CREATE TABLE addresses (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    label          TEXT,
    recipient_name TEXT NOT NULL CHECK (length(btrim(recipient_name)) BETWEEN 1 AND 120),
    phone          TEXT NOT NULL,
    line1          TEXT NOT NULL CHECK (length(btrim(line1)) > 0),
    line2          TEXT,
    landmark       TEXT,
    city           TEXT NOT NULL CHECK (length(btrim(city)) > 0),
    state          TEXT NOT NULL CHECK (length(btrim(state)) > 0),
    -- Indian PIN codes are exactly six digits and never start with zero.
    -- Enforced here as well as in the API so a bad row cannot arrive by any
    -- other path.
    pincode        TEXT NOT NULL CHECK (pincode ~ '^[1-9][0-9]{5}$'),
    is_default     BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER addresses_set_updated_at
    BEFORE UPDATE ON addresses
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX addresses_user_live_idx ON addresses (user_id) WHERE deleted_at IS NULL;

-- At most one default per customer, enforced by the database rather than by
-- application discipline. A partial index so soft-deleted rows and non-default
-- rows are exempt.
CREATE UNIQUE INDEX addresses_one_default_per_user
    ON addresses (user_id)
    WHERE is_default AND deleted_at IS NULL;

-- ---------------------------------------------------------------------------
-- suppliers
-- ---------------------------------------------------------------------------

CREATE TABLE suppliers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    business_name       TEXT NOT NULL CHECK (length(btrim(business_name)) BETWEEN 1 AND 200),
    contact_name        TEXT NOT NULL CHECK (length(btrim(contact_name)) BETWEEN 1 AND 120),
    phone               TEXT NOT NULL,
    email               CITEXT NOT NULL,
    gstin               TEXT,
    pan                 TEXT,

    address_line1       TEXT,
    address_line2       TEXT,
    city                TEXT,
    state               TEXT,
    pincode             TEXT CHECK (pincode IS NULL OR pincode ~ '^[1-9][0-9]{5}$'),

    -- Needed for manual NEFT settlement. Masked in every API response except
    -- the admin payout screen (CLAUDE.md §5.1).
    bank_account_name   TEXT,
    bank_account_number TEXT,
    bank_ifsc           TEXT CHECK (bank_ifsc IS NULL OR bank_ifsc ~ '^[A-Z]{4}0[A-Z0-9]{6}$'),

    status              TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'approved', 'suspended', 'rejected')),
    -- Set together on approval; see the CHECK below.
    approved_by         UUID REFERENCES users (id),
    approved_at         TIMESTAMPTZ,
    rejection_reason    TEXT,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- An approved supplier must record who approved it and when. Without this
    -- the audit trail can silently lose its subject.
    CONSTRAINT suppliers_approval_is_attributed CHECK (
        status <> 'approved'
        OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)
    )
);

CREATE TRIGGER suppliers_set_updated_at
    BEFORE UPDATE ON suppliers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX suppliers_status_idx ON suppliers (status, created_at DESC);

-- ---------------------------------------------------------------------------
-- refresh_tokens — rotating, hashed at rest, reuse-detecting
-- ---------------------------------------------------------------------------

CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- SHA-256 of the token, never the token itself. A database dump must not
    -- hand an attacker a set of usable sessions.
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    -- Set when this token is rotated, pointing at its successor. Makes a
    -- reuse attempt traceable to the exact chain it came from.
    replaced_by UUID REFERENCES refresh_tokens (id),
    user_agent  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);
-- Supports the expired-token sweeper.
CREATE INDEX refresh_tokens_expiry_idx ON refresh_tokens (expires_at)
    WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- admin_audit_log
--
-- DEVIATION from CLAUDE.md §5.3, which lists this table in the `orders`
-- schema. Supplier approval happens in vm-profile-api, and CLAUDE.md §3
-- forbids a service touching another service's schema — an isolation the
-- vm_profile role physically cannot violate. Each service therefore keeps an
-- audit log for its own domain, with the identical column shape. Raised with
-- the team; revisit if a single consolidated log is wanted instead.
-- ---------------------------------------------------------------------------

CREATE TABLE admin_audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_user_id UUID NOT NULL REFERENCES users (id),
    action        TEXT NOT NULL,
    entity_type   TEXT NOT NULL,
    entity_id     UUID,
    before        JSONB,
    after         JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX admin_audit_log_entity_idx ON admin_audit_log (entity_type, entity_id, created_at DESC);
CREATE INDEX admin_audit_log_admin_idx ON admin_audit_log (admin_user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- password_set_tokens — for admin-created suppliers (CLAUDE.md §5.1 flow)
-- ---------------------------------------------------------------------------

CREATE TABLE password_set_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Hashed for the same reason refresh tokens are: this token grants the
    -- ability to set a password.
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX password_set_tokens_user_idx ON password_set_tokens (user_id);

-- +goose Down

DROP TABLE IF EXISTS password_set_tokens;
DROP TABLE IF EXISTS admin_audit_log;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS suppliers;
DROP TABLE IF EXISTS addresses;
DROP TABLE IF EXISTS customer_profiles;
DROP TABLE IF EXISTS users;
DROP FUNCTION IF EXISTS set_updated_at();

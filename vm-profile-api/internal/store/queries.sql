-- Queries for vm-profile-api. Generated into Go by sqlc (CLAUDE.md §3), so
-- every statement is parameterised by construction — rule 7.
--
-- Ownership note: queries that read or mutate a resource belonging to a user
-- take the owner's id as a parameter and filter on it IN SQL. Checking
-- ownership in Go after an unfiltered fetch is one forgotten `if` away from
-- an IDOR; this way the wrong row is never returned in the first place.

-- ===========================================================================
-- users
-- ===========================================================================

-- name: CreateUser :one
INSERT INTO users (email, phone, password_hash, role, status, email_verified)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone = $1;

-- name: SetUserPassword :one
UPDATE users SET password_hash = $2 WHERE id = $1
RETURNING *;

-- name: SetUserStatus :one
UPDATE users SET status = $2 WHERE id = $1
RETURNING *;

-- ===========================================================================
-- customer_profiles
-- ===========================================================================

-- name: CreateCustomerProfile :one
INSERT INTO customer_profiles (user_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetCustomerProfile :one
SELECT * FROM customer_profiles WHERE user_id = $1;

-- Joined view for GET /me, so the handler makes one round trip.
-- name: GetCustomerWithProfile :one
SELECT
    u.id, u.email, u.phone, u.role, u.status, u.email_verified, u.created_at,
    cp.name
FROM users u
JOIN customer_profiles cp ON cp.user_id = u.id
WHERE u.id = $1;

-- ===========================================================================
-- addresses
-- ===========================================================================

-- name: CreateAddress :one
INSERT INTO addresses (
    user_id, label, recipient_name, phone,
    line1, line2, landmark, city, state, pincode, is_default
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListAddresses :many
SELECT * FROM addresses
WHERE user_id = $1 AND deleted_at IS NULL
-- Default first, then newest, so the checkout form's first option is the
-- one the customer expects.
ORDER BY is_default DESC, created_at DESC;

-- Ownership is part of the WHERE clause, not a later check.
-- name: GetAddress :one
SELECT * FROM addresses
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: UpdateAddress :one
UPDATE addresses SET
    label          = $3,
    recipient_name = $4,
    phone          = $5,
    line1          = $6,
    line2          = $7,
    landmark       = $8,
    city           = $9,
    state          = $10,
    pincode        = $11
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteAddress :one
UPDATE addresses
SET deleted_at = now(), is_default = FALSE
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: ClearDefaultAddress :exec
UPDATE addresses SET is_default = FALSE
WHERE user_id = $1 AND is_default AND deleted_at IS NULL;

-- name: SetDefaultAddress :one
UPDATE addresses SET is_default = TRUE
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: CountLiveAddresses :one
SELECT count(*) FROM addresses WHERE user_id = $1 AND deleted_at IS NULL;

-- Promotes the most recent remaining address after the default was deleted,
-- so a customer is never left with addresses but no default.
-- Aliased on both sides: without it, `user_id` is ambiguous between the outer
-- UPDATE target and the subquery's own scan of the same table.
-- name: PromoteNewestAddressToDefault :exec
UPDATE addresses AS a SET is_default = TRUE
WHERE a.id = (
    SELECT inner_a.id FROM addresses AS inner_a
    WHERE inner_a.user_id = $1 AND inner_a.deleted_at IS NULL
    ORDER BY inner_a.created_at DESC
    LIMIT 1
);

-- ===========================================================================
-- suppliers
-- ===========================================================================

-- name: CreateSupplier :one
INSERT INTO suppliers (
    user_id, business_name, contact_name, phone, email, gstin, pan,
    address_line1, address_line2, city, state, pincode,
    bank_account_name, bank_account_number, bank_ifsc,
    status, approved_by, approved_at, commission_bps
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
RETURNING *;

-- name: GetSupplierByID :one
SELECT * FROM suppliers WHERE id = $1;

-- name: GetSupplierByUserID :one
SELECT * FROM suppliers WHERE user_id = $1;

-- name: ListSuppliers :many
SELECT * FROM suppliers
-- sqlc.narg means "optional": a NULL status returns every supplier.
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountSuppliers :one
SELECT count(*) FROM suppliers
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text);

-- name: ApproveSupplier :one
UPDATE suppliers
SET status = 'approved', approved_by = $2, approved_at = now(), rejection_reason = NULL
WHERE id = $1
RETURNING *;

-- name: RejectSupplier :one
UPDATE suppliers
SET status = 'rejected', rejection_reason = $2
WHERE id = $1
RETURNING *;

-- name: SuspendSupplier :one
UPDATE suppliers
SET status = 'suspended'
WHERE id = $1
RETURNING *;

-- ===========================================================================
-- refresh_tokens
-- ===========================================================================

-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, user_agent)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- Looked up by hash only: the plaintext token is never stored, so this is the
-- only way to find the row.
-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens WHERE token_hash = $1;

-- name: RevokeRefreshToken :one
UPDATE refresh_tokens
SET revoked_at = now(), replaced_by = $2
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- The reuse-detection hammer: one replayed token invalidates every session
-- for that user, because we can no longer tell which holder is legitimate.
-- name: RevokeAllUserRefreshTokens :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens WHERE expires_at < now();

-- ===========================================================================
-- password_set_tokens
-- ===========================================================================

-- name: CreatePasswordSetToken :one
INSERT INTO password_set_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetPasswordSetTokenByHash :one
SELECT * FROM password_set_tokens WHERE token_hash = $1;

-- name: UsePasswordSetToken :one
UPDATE password_set_tokens
SET used_at = now()
WHERE id = $1 AND used_at IS NULL
RETURNING *;

-- ===========================================================================
-- admin_audit_log
-- ===========================================================================

-- name: InsertAuditLog :one
INSERT INTO admin_audit_log (admin_user_id, action, entity_type, entity_id, before, after)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListAuditLogForEntity :many
SELECT * FROM admin_audit_log
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC
LIMIT $3;

-- Approved supplier ids, for vm-catalog-api's storefront allow-list.
-- CLAUDE.md §3 forbids cross-schema reads, so catalog asks over HTTP instead.
-- name: ListApprovedSupplierIDs :many
SELECT s.id
FROM suppliers s
JOIN users u ON u.id = s.user_id
WHERE s.status = 'approved' AND u.status = 'active'
ORDER BY s.id;

-- Unmasked bank details for the admin settlement worksheet.
-- The ONLY query that returns an account number in the clear; every caller
-- must audit the access (CLAUDE.md §5.1).
-- name: ListSupplierBankDetails :many
SELECT id, business_name, bank_account_name, bank_account_number, bank_ifsc
FROM suppliers
WHERE id = ANY($1::uuid[])
ORDER BY business_name;

-- Masked-safe directory for admin screens that only need names.
-- name: ListSupplierNames :many
SELECT id, business_name FROM suppliers
WHERE id = ANY($1::uuid[])
ORDER BY business_name;

-- name: CountPendingSupplierApplications :one
SELECT count(*) FROM suppliers WHERE status = 'pending';

-- Admin edit of a supplier's business, contact and bank details.
--
-- Deliberately does NOT touch status: approval, rejection and suspension are
-- their own audited transitions, and folding them into a general-purpose
-- update would let a careless PATCH silently approve a pending applicant.
-- name: AdminUpdateSupplier :one
UPDATE suppliers
SET business_name       = $2,
    contact_name        = $3,
    phone               = $4,
    email               = $5,
    gstin               = $6,
    pan                 = $7,
    address_line1       = $8,
    address_line2       = $9,
    city                = $10,
    state               = $11,
    pincode             = $12,
    bank_account_name   = $13,
    bank_account_number = $14,
    bank_ifsc           = $15,
    -- NULL clears an override back to the platform default, which is why this
    -- is a plain assignment rather than a COALESCE that could never unset it.
    commission_bps      = $16
WHERE id = $1
RETURNING *;

-- The successor of a rotated token, used by the replay grace window.
-- name: GetRefreshTokenByID :one
SELECT * FROM refresh_tokens WHERE id = $1;


-- Commission rates for the payout calculation in vm-orders-api.
--
-- Rates only — no contact, address or bank fields. The response shape is the
-- access control: orders-api needs to know what to deduct and nothing else,
-- so there is nothing here to leak even if the call is mis-scoped.
-- NULL commission_bps means "use the platform default"; the caller decides
-- what that is, because the default lives in orders-api's configuration.
-- name: ListSupplierCommissions :many
SELECT id, commission_bps FROM suppliers
WHERE id = ANY($1::uuid[]);

-- Contact details for a set of customers, for transactional email.
--
-- Service-to-service only. Returns the ADDRESS AND NAME and nothing else: the
-- outbox dispatcher in vm-orders-api needs somewhere to send an order
-- confirmation, and no more than that. Customers with no email — a phone-only
-- signup — simply do not come back, which the caller reads as "cannot email
-- this one" rather than as an error (CLAUDE.md §5.1 allows email to be NULL).
-- name: ListCustomerContacts :many
SELECT u.id, u.email, coalesce(p.name, '')::text AS name
FROM users u
LEFT JOIN customer_profiles p ON p.user_id = u.id
WHERE u.id = ANY($1::uuid[]) AND u.email IS NOT NULL;

-- ===========================================================================
-- analytics supplier events — CLAUDE.md §5.4
-- ===========================================================================

-- Written in the caller's transaction, next to the change it describes.
-- name: InsertSupplierEvent :one
INSERT INTO supplier_events_outbox (
    aggregate_id, event_type, schema_version, occurred_at, actor_role, request_id, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- Retention. CDC has already read these from the WAL.
-- name: PruneSupplierEvents :execrows
DELETE FROM supplier_events_outbox WHERE created_at < $1;

-- The backfill's cursor: every supplier, oldest first, a page at a time.
-- name: ListSupplierIDsAfter :many
SELECT id, created_at FROM suppliers
WHERE (created_at, id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY created_at, id
LIMIT sqlc.arg(page_size);

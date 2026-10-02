-- Development seed: one admin, one analyst, two approved suppliers, one
-- customer with two addresses.
--
-- Idempotent: every insert is ON CONFLICT DO NOTHING, so `make seed` can run
-- repeatedly without duplicating rows or failing.
--
-- Passwords are hashed with pgcrypto's crypt(..., gen_salt('bf', 12)), which
-- produces a $2a$ bcrypt hash byte-compatible with Go's golang.org/x/crypto
-- bcrypt — so these accounts log in through the real code path, not a
-- special case.
--
-- CLAUDE.md rule 4: no secrets in the repo. The admin's credentials come from
-- the environment (SEED_ADMIN_EMAIL / SEED_ADMIN_PASSWORD, passed in by
-- infra/scripts/seed.sh), never from this file.

\set ON_ERROR_STOP on

-- ---------------------------------------------------------------------------
-- Admin
-- ---------------------------------------------------------------------------

INSERT INTO profile.users (email, phone, password_hash, role, status, email_verified)
VALUES (
    :'admin_email',
    :'admin_phone',
    crypt(:'admin_password', gen_salt('bf', 12)),
    'admin',
    'active',
    TRUE
)
ON CONFLICT (email) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Analyst — signs in to the admin console and sees only Analytics
-- ---------------------------------------------------------------------------

INSERT INTO profile.users (email, phone, password_hash, role, status, email_verified)
VALUES (
    :'analyst_email',
    :'analyst_phone',
    crypt(:'analyst_password', gen_salt('bf', 12)),
    'analyst',
    'active',
    TRUE
)
ON CONFLICT (email) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Suppliers — both approved, so the storefront has produce to list
-- ---------------------------------------------------------------------------

WITH new_user AS (
    INSERT INTO profile.users (email, phone, password_hash, role, status, email_verified)
    VALUES (
        'greens@vayal.test',
        '9800000001',
        crypt(:'supplier_password', gen_salt('bf', 12)),
        'supplier',
        'active',
        TRUE
    )
    ON CONFLICT (email) DO NOTHING
    RETURNING id
), admin_user AS (
    SELECT id FROM profile.users WHERE email = :'admin_email'
)
INSERT INTO profile.suppliers (
    user_id, business_name, contact_name, phone, email,
    gstin, pan, address_line1, city, state, pincode,
    bank_account_name, bank_account_number, bank_ifsc,
    status, approved_by, approved_at
)
SELECT
    new_user.id, 'Kaveri Greens', 'Anitha R', '9800000001', 'greens@vayal.test',
    '33AABCK1234M1Z5', 'AABCK1234M', '12 Anna Salai', 'Coimbatore', 'Tamil Nadu', '641001',
    'Kaveri Greens', '918273645500', 'HDFC0001234',
    'approved', admin_user.id, now()
FROM new_user, admin_user
ON CONFLICT (user_id) DO NOTHING;

WITH new_user AS (
    INSERT INTO profile.users (email, phone, password_hash, role, status, email_verified)
    VALUES (
        'farm@vayal.test',
        '9800000002',
        crypt(:'supplier_password', gen_salt('bf', 12)),
        'supplier',
        'active',
        TRUE
    )
    ON CONFLICT (email) DO NOTHING
    RETURNING id
), admin_user AS (
    SELECT id FROM profile.users WHERE email = :'admin_email'
)
INSERT INTO profile.suppliers (
    user_id, business_name, contact_name, phone, email,
    gstin, pan, address_line1, city, state, pincode,
    bank_account_name, bank_account_number, bank_ifsc,
    status, approved_by, approved_at
)
SELECT
    new_user.id, 'Nilgiri Microfarms', 'Suresh K', '9800000002', 'farm@vayal.test',
    '33AABCN5678P1Z2', 'AABCN5678P', '4 Market Road', 'Ooty', 'Tamil Nadu', '643001',
    'Nilgiri Microfarms', '918273645511', 'ICIC0005678',
    'approved', admin_user.id, now()
FROM new_user, admin_user
ON CONFLICT (user_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Customer with two addresses (exactly one default)
-- ---------------------------------------------------------------------------

INSERT INTO profile.users (email, phone, password_hash, role, status, email_verified)
VALUES (
    'customer@vayal.test',
    '9900000001',
    crypt(:'customer_password', gen_salt('bf', 12)),
    'customer',
    'active',
    TRUE
)
ON CONFLICT (email) DO NOTHING;

INSERT INTO profile.customer_profiles (user_id, name)
SELECT id, 'Priya Raman' FROM profile.users WHERE email = 'customer@vayal.test'
ON CONFLICT (user_id) DO NOTHING;

INSERT INTO profile.addresses (
    user_id, label, recipient_name, phone,
    line1, line2, landmark, city, state, pincode, is_default
)
SELECT
    u.id, 'Home', 'Priya Raman', '9900000001',
    '7 Gandhi Street', 'Apartment 3B', 'Near Ganesh Temple',
    'Chennai', 'Tamil Nadu', '600001', TRUE
FROM profile.users u
WHERE u.email = 'customer@vayal.test'
  AND NOT EXISTS (
      SELECT 1 FROM profile.addresses a
      WHERE a.user_id = u.id AND a.label = 'Home'
  );

INSERT INTO profile.addresses (
    user_id, label, recipient_name, phone,
    line1, landmark, city, state, pincode, is_default
)
SELECT
    u.id, 'Office', 'Priya Raman', '9900000001',
    '221 Mount Road', 'Opposite Metro Station',
    'Chennai', 'Tamil Nadu', '600002', FALSE
FROM profile.users u
WHERE u.email = 'customer@vayal.test'
  AND NOT EXISTS (
      SELECT 1 FROM profile.addresses a
      WHERE a.user_id = u.id AND a.label = 'Office'
  );

-- Report what exists, so `make seed` output is verifiable at a glance.
\echo ''
SELECT role, status, count(*) AS users FROM profile.users GROUP BY role, status ORDER BY role;
SELECT business_name, status FROM profile.suppliers ORDER BY business_name;
SELECT label, city, is_default FROM profile.addresses ORDER BY is_default DESC;

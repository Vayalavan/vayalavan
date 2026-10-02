-- Admin audit log for the orders domain — CLAUDE.md §5.3.
--
-- A second copy of the table introduced in the profile schema, for the same
-- reason: §3 forbids a service touching another's schema, and the vm_orders
-- role physically cannot write to profile.admin_audit_log. Each service keeps
-- an audit log for its own domain, with the identical column shape.
--
-- Mandatory here for: marking a payout paid, order cancellation, refunds, and
-- every unmasked view of supplier bank details.

-- +goose Up

CREATE TABLE admin_audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- profile.users(id). No FK: different schema, different owner.
    admin_user_id UUID NOT NULL,
    action        TEXT NOT NULL,
    entity_type   TEXT NOT NULL,
    entity_id     UUID,
    before        JSONB,
    after         JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX admin_audit_log_entity_idx
    ON admin_audit_log (entity_type, entity_id, created_at DESC);
CREATE INDEX admin_audit_log_admin_idx
    ON admin_audit_log (admin_user_id, created_at DESC);
-- Answers "who looked at bank details, and when" without scanning the table.
CREATE INDEX admin_audit_log_action_idx ON admin_audit_log (action, created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS admin_audit_log;

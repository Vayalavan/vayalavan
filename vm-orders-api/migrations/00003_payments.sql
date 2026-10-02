-- Payments, webhook events and supplier payouts — CLAUDE.md §5.3, §6.4.

-- +goose Up

CREATE TABLE payments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    provider            TEXT NOT NULL DEFAULT 'razorpay',
    provider_order_id   TEXT,
    provider_payment_id TEXT,
    amount_paise        BIGINT NOT NULL CHECK (amount_paise >= 0),
    status              TEXT NOT NULL DEFAULT 'created'
                             CHECK (status IN ('created', 'captured', 'failed', 'refunded')),
    method              TEXT,
    -- The provider's own payload, kept verbatim for dispute resolution. What
    -- Razorpay says happened is the record that matters in a chargeback.
    raw_payload         JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER payments_set_updated_at
    BEFORE UPDATE ON payments FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX payments_order_idx ON payments (order_id);
-- One captured payment per provider payment id. A replayed webhook that got
-- past the event dedupe still cannot create a second payment row.
CREATE UNIQUE INDEX payments_provider_payment_key
    ON payments (provider_payment_id) WHERE provider_payment_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- webhook_events
--
-- The idempotency guard for CLAUDE.md §6.4 step 5: the unique constraint on
-- provider_event_id IS the dedupe. A replayed delivery collides on insert and
-- the handler returns 200 without reprocessing.
-- ---------------------------------------------------------------------------

CREATE TABLE webhook_events (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider          TEXT NOT NULL DEFAULT 'razorpay',
    provider_event_id TEXT NOT NULL UNIQUE,
    event_type        TEXT NOT NULL,
    payload           JSONB NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at      TIMESTAMPTZ,
    error             TEXT
);

CREATE INDEX webhook_events_unprocessed_idx ON webhook_events (received_at)
    WHERE processed_at IS NULL;

-- ---------------------------------------------------------------------------
-- supplier_payouts
-- ---------------------------------------------------------------------------

CREATE TABLE supplier_payouts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    supplier_id    UUID NOT NULL,
    order_id       UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    -- What the supplier is owed: the sum of THEIR line totals, with no
    -- deductions. Our platform and delivery fees sit on top of the subtotal
    -- and are never taken out of this (CLAUDE.md §6.2).
    amount_paise   BIGINT NOT NULL CHECK (amount_paise >= 0),
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid')),
    marked_paid_by UUID,
    marked_paid_at TIMESTAMPTZ,
    -- The NEFT UTR, recorded when an admin settles by hand.
    reference_no   TEXT,
    notes          TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One row per (order, supplier) — CLAUDE.md §5.3. Also makes payout
    -- creation idempotent: a replayed capture cannot double-pay a supplier.
    CONSTRAINT supplier_payouts_one_per_order_supplier UNIQUE (order_id, supplier_id),

    -- A paid payout must record who paid it and when, or the audit trail
    -- loses its subject.
    CONSTRAINT supplier_payouts_payment_is_attributed CHECK (
        status <> 'paid' OR (marked_paid_by IS NOT NULL AND marked_paid_at IS NOT NULL)
    )
);

CREATE INDEX supplier_payouts_supplier_idx ON supplier_payouts (supplier_id, status, created_at DESC);
CREATE INDEX supplier_payouts_pending_idx ON supplier_payouts (created_at) WHERE status = 'pending';

-- +goose Down

DROP TABLE IF EXISTS supplier_payouts;
DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS payments;

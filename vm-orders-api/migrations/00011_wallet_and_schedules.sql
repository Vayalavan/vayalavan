-- The prepaid wallet and scheduled / repeat orders — CLAUDE.md §6.7.
--
-- A customer tops the wallet up through Razorpay, and a schedule names the
-- produce and the delivery dates. At each date's charging run (shortly before
-- the cutoff of its processing day) the run prices the lines at THAT day's
-- prices, reserves THAT day's stock, debits the wallet and writes an ordinary
-- paid order. From there the order is indistinguishable from a checkout one:
-- same timeline, same payouts, same confirmation email.
--
-- Money stays int64 paise (rule 1). The wallet is closed-loop: it is only ever
-- spent here, and leaves only as an admin-issued refund to the card or account
-- it came from.

-- +goose Up

-- ---------------------------------------------------------------------------
-- wallets
--
-- One row per customer, holding the running balance. The balance is a cache
-- of the ledger below and is only ever changed in the same transaction as a
-- wallet_transactions row that states the new figure, under a row lock. The
-- CHECK is the last line of defence against overdraft: a debit that would take
-- it negative fails in the database even if the application check were wrong.
-- ---------------------------------------------------------------------------

CREATE TABLE wallets (
    -- profile.users(id). No FK: different schema, different owner.
    customer_id   UUID PRIMARY KEY,
    balance_paise BIGINT NOT NULL DEFAULT 0 CHECK (balance_paise >= 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER wallets_set_updated_at
    BEFORE UPDATE ON wallets FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- wallet_topups
--
-- One Razorpay order per top-up. The webhook, not the browser, credits it —
-- exactly as for an order (CLAUDE.md §6.4).
-- ---------------------------------------------------------------------------

CREATE TABLE wallet_topups (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id         UUID NOT NULL REFERENCES wallets (customer_id),
    amount_paise        BIGINT NOT NULL CHECK (amount_paise > 0),
    status              TEXT NOT NULL DEFAULT 'created'
                             CHECK (status IN ('created', 'captured')),
    razorpay_order_id   TEXT UNIQUE,
    razorpay_payment_id TEXT UNIQUE,
    -- How much of this top-up has been sent back by an admin refund. A refund
    -- is issued against the payment it came from, so it can never exceed it.
    refunded_paise      BIGINT NOT NULL DEFAULT 0,
    idempotency_key     TEXT NOT NULL,
    captured_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wallet_topups_refund_within_amount
        CHECK (refunded_paise >= 0 AND refunded_paise <= amount_paise),
    -- A retried "Add money" returns the first top-up rather than a second.
    CONSTRAINT wallet_topups_idempotent UNIQUE (customer_id, idempotency_key)
);

CREATE TRIGGER wallet_topups_set_updated_at
    BEFORE UPDATE ON wallet_topups FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX wallet_topups_customer_idx ON wallet_topups (customer_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- wallet_refunds
--
-- An admin sending balance back to the payment it came from. One row per
-- Razorpay refund call, so a refund spread over two top-ups is two rows.
-- ---------------------------------------------------------------------------

CREATE TABLE wallet_refunds (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id        UUID NOT NULL REFERENCES wallets (customer_id),
    topup_id           UUID NOT NULL REFERENCES wallet_topups (id),
    amount_paise       BIGINT NOT NULL CHECK (amount_paise > 0),
    status             TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'issued', 'failed')),
    razorpay_refund_id TEXT UNIQUE,
    -- profile.users(id) of the admin who issued it. No FK: different schema.
    admin_user_id      UUID NOT NULL,
    notes              TEXT,
    error              TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER wallet_refunds_set_updated_at
    BEFORE UPDATE ON wallet_refunds FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- schedules
--
-- What to deliver, where, and on which EXPECTED DELIVERY dates. The customer
-- chooses a date, never a time: the time follows from the §6.1 cutoff.
-- ---------------------------------------------------------------------------

CREATE TABLE schedules (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id        UUID NOT NULL,
    -- The address, snapshotted when the schedule was made or last edited.
    -- Orders snapshot it again at placement, as every order does.
    address_id         UUID NOT NULL,
    address_snapshot   JSONB NOT NULL,

    frequency          TEXT NOT NULL CHECK (frequency IN ('once', 'daily', 'weekly', 'monthly')),
    -- Weekly: which weekdays, 0 = Sunday .. 6 = Saturday.
    weekdays           SMALLINT[] NOT NULL DEFAULT '{}',
    -- Monthly: the day of the month. 29-31 fall on the last day of a shorter
    -- month rather than skipping it.
    day_of_month       SMALLINT CHECK (day_of_month BETWEEN 1 AND 31),

    start_date         DATE NOT NULL,
    -- Inclusive. NULL repeats until cancelled.
    end_date           DATE,
    -- The next expected delivery date the charging run will attempt. NULL once
    -- nothing is left to deliver (a finished one-off, or past end_date).
    next_delivery_date DATE,

    status             TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN ('active', 'paused', 'cancelled', 'completed')),
    -- Deliveries skipped in a row for want of balance. Three pause the
    -- schedule, so a forgotten wallet does not skip forever in silence.
    low_balance_skips  INT NOT NULL DEFAULT 0,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT schedules_weekly_has_days
        CHECK (frequency <> 'weekly' OR cardinality(weekdays) > 0),
    CONSTRAINT schedules_monthly_has_day
        CHECK (frequency <> 'monthly' OR day_of_month IS NOT NULL),
    CONSTRAINT schedules_end_after_start
        CHECK (end_date IS NULL OR end_date >= start_date)
);

CREATE TRIGGER schedules_set_updated_at
    BEFORE UPDATE ON schedules FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE INDEX schedules_customer_idx ON schedules (customer_id, created_at DESC);
-- What the charging run scans.
CREATE INDEX schedules_due_idx ON schedules (next_delivery_date) WHERE status = 'active';

-- The produce on a schedule. Priced live at each run, never here — the same
-- rule as the cart (CLAUDE.md §5.3). The snapshots are for display only.
CREATE TABLE schedule_items (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id           UUID NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
    product_id            UUID NOT NULL,
    -- catalog.product_pack_options(id). No FK: different schema.
    product_unit_id       UUID NOT NULL,
    qty                   INT NOT NULL CHECK (qty > 0 AND qty <= 99),
    product_name_snapshot TEXT NOT NULL,
    unit_label_snapshot   TEXT NOT NULL,
    size_code_snapshot    TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT schedule_items_one_per_pack UNIQUE (schedule_id, product_unit_id)
);

-- ---------------------------------------------------------------------------
-- schedule_occurrences
--
-- What happened to one date of one schedule. The UNIQUE key is the charging
-- run's idempotency guard: a date is attempted at most once, so a run that
-- crashes and restarts cannot charge the same delivery twice. A customer's
-- skip is written ahead of time as a row the run then finds and passes over.
-- ---------------------------------------------------------------------------

CREATE TABLE schedule_occurrences (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id   UUID NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
    delivery_date DATE NOT NULL,
    status        TEXT NOT NULL CHECK (status IN (
        'placed',               -- an order was written and paid from the wallet
        'skipped_by_customer',  -- the customer skipped this date
        'skipped_no_stock',     -- nothing on the schedule was in stock
        'skipped_low_balance',  -- the wallet could not cover it
        'missed'                -- the run did not reach it (service down)
    )),
    order_id      UUID REFERENCES orders (id),
    -- What the customer is told: which lines were cut short, and why.
    note          TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT schedule_occurrences_one_per_date UNIQUE (schedule_id, delivery_date),
    CONSTRAINT schedule_occurrences_placed_has_order
        CHECK (status <> 'placed' OR order_id IS NOT NULL)
);

-- ---------------------------------------------------------------------------
-- wallet_transactions
--
-- The ledger. Append-only: every movement of balance is one row stating the
-- signed amount and the balance it left, so the history reconciles line by
-- line and the wallets row can always be rebuilt from it.
-- ---------------------------------------------------------------------------

CREATE TABLE wallet_transactions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id         UUID NOT NULL REFERENCES wallets (customer_id),
    kind                TEXT NOT NULL CHECK (kind IN (
        'topup',            -- money in, from a captured Razorpay payment
        'order_debit',      -- money out, for a scheduled delivery
        'refund',           -- money out, sent back to the card by an admin
        'refund_reversal'   -- money back in, when that refund call failed
    )),
    -- Signed: positive credits the wallet, negative debits it.
    amount_paise        BIGINT NOT NULL CHECK (amount_paise <> 0),
    balance_after_paise BIGINT NOT NULL CHECK (balance_after_paise >= 0),
    topup_id            UUID REFERENCES wallet_topups (id),
    order_id            UUID REFERENCES orders (id),
    refund_id           UUID REFERENCES wallet_refunds (id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wallet_transactions_sign_matches_kind CHECK (
        (kind IN ('topup', 'refund_reversal') AND amount_paise > 0)
        OR (kind IN ('order_debit', 'refund') AND amount_paise < 0)
    ),
    CONSTRAINT wallet_transactions_reference_matches_kind CHECK (
        (kind = 'topup' AND topup_id IS NOT NULL)
        OR (kind = 'order_debit' AND order_id IS NOT NULL)
        OR (kind IN ('refund', 'refund_reversal') AND refund_id IS NOT NULL)
    )
);

CREATE INDEX wallet_transactions_customer_idx
    ON wallet_transactions (customer_id, created_at DESC);
-- A top-up is credited once and an order debited once, whatever replays.
CREATE UNIQUE INDEX wallet_transactions_one_credit_per_topup
    ON wallet_transactions (topup_id) WHERE kind = 'topup';
CREATE UNIQUE INDEX wallet_transactions_one_debit_per_order
    ON wallet_transactions (order_id) WHERE kind = 'order_debit';
CREATE UNIQUE INDEX wallet_transactions_one_entry_per_refund_kind
    ON wallet_transactions (refund_id, kind) WHERE refund_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- orders.schedule_id — which schedule placed an order, if any. Lets the
-- clients label a delivery as part of a repeat.
-- ---------------------------------------------------------------------------

ALTER TABLE orders
    ADD COLUMN schedule_id UUID REFERENCES schedules (id) ON DELETE SET NULL;

-- +goose Down

ALTER TABLE orders DROP COLUMN IF EXISTS schedule_id;
DROP TABLE IF EXISTS wallet_transactions;
DROP TABLE IF EXISTS schedule_occurrences;
DROP TABLE IF EXISTS schedule_items;
DROP TABLE IF EXISTS schedules;
DROP TABLE IF EXISTS wallet_refunds;
DROP TABLE IF EXISTS wallet_topups;
DROP TABLE IF EXISTS wallets;

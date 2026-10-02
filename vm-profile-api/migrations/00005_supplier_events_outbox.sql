-- Supplier events for analytics — the profile part of CLAUDE.md §5.4.
--
-- Same shape and rules as catalog.product_events_outbox and
-- orders.order_events_outbox: one row inserted in the same transaction as the
-- supplier change it describes, streamed out by logical replication, never
-- updated, pruned after OUTBOX_RETENTION_DAYS.
--
-- It exists so analytics can NAME the suppliers its order lines and products
-- already carry ids for, and know their status and commission. `payload` is
-- built from a typed struct (internal/analyticsevents) that has no field for
-- the contact person, phone, email, PAN, GSTIN, street address, bank details
-- or a rejection reason: a supplier's business name and town are what a
-- report needs, and they are what the storefront already shows publicly.

-- +goose Up
CREATE TABLE supplier_events_outbox (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL CHECK (event_type IN (
                       'supplier.applied', 'supplier.created', 'supplier.updated',
                       'supplier.approved', 'supplier.rejected', 'supplier.suspended',
                       'supplier.snapshot')),
    schema_version SMALLINT NOT NULL DEFAULT 1,
    occurred_at    TIMESTAMPTZ NOT NULL,
    actor_role     TEXT NOT NULL CHECK (actor_role IN ('supplier', 'admin', 'system')),
    request_id     TEXT,
    payload        JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The retention sweep's index.
CREATE INDEX supplier_events_outbox_created_idx ON supplier_events_outbox (created_at);

-- +goose Down
DROP TABLE IF EXISTS supplier_events_outbox;

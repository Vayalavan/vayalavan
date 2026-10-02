-- Order events for analytics — the orders half of CLAUDE.md §5.4.
--
-- A transactional outbox read by CHANGE DATA CAPTURE, not by a poller: each
-- row is inserted in the same transaction as the order transition it
-- describes, Postgres logical replication streams the INSERT to
-- vm-events-consumer-api, and the consumer projects it into the analytics
-- schema. Nothing ever updates a row, so there is no published_at — the WAL
-- position the consumer has confirmed is the only "sent" marker there is.
--
-- Separate from `outbox` on purpose. That table is the email/alert work
-- queue: its rows are claimed, stamped, retried and suppressed, and streaming
-- it would turn every one of those UPDATEs into noise for analytics.
--
-- `payload` is the whole order as analytics may see it, AFTER the change —
-- event-carried state, so a consumer that misses or repeats an event still
-- converges. It is written from a typed struct (internal/analyticsevents) that
-- has no field for a name, phone, email, street address, payment id or free
-- text, because the analytics role must never read them.
--
-- Rows are pruned after OUTBOX_RETENTION_DAYS by the sweeper. The publication
-- carries inserts only, so that DELETE never reaches the consumer.

-- +goose Up
CREATE TABLE order_events_outbox (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL CHECK (event_type IN (
                       'order.placed', 'order.paid', 'order.expired',
                       'order.cancelled', 'order.processed', 'order.dispatched',
                       'order.refunded', 'order.snapshot')),
    schema_version SMALLINT NOT NULL DEFAULT 1,
    occurred_at    TIMESTAMPTZ NOT NULL,
    actor_role     TEXT NOT NULL CHECK (actor_role IN ('customer', 'admin', 'system')),
    request_id     TEXT,
    payload        JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The retention sweep's index.
CREATE INDEX order_events_outbox_created_idx ON order_events_outbox (created_at);

-- +goose Down
DROP TABLE IF EXISTS order_events_outbox;

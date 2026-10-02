-- Product events for analytics — the catalog half of CLAUDE.md §5.4.
--
-- A transactional outbox read by change data capture: each row is inserted in
-- the same transaction as the product change it describes, Postgres logical
-- replication streams the INSERT to vm-events-consumer-api, and the consumer
-- projects it into the analytics schema. Rows are never updated, so there is
-- no published_at; the consumer's confirmed WAL position is the only marker.
--
-- `payload` is the whole product as analytics may see it AFTER the change —
-- its grades, packs, prices and markup — so a consumer that misses or repeats
-- an event still converges. It is built from a typed struct
-- (internal/analyticsevents) with no field for the description, media object
-- keys or anything about the grower beyond their id.
--
-- Worth knowing for anyone reading these events: a product save DELETES and
-- re-creates the whole grade tree (products.go), so pack ids change on every
-- edit. That is why the analytics side keeps pack versions rather than
-- overwriting one row per pack id.
--
-- Rows are pruned after OUTBOX_RETENTION_DAYS. The publication carries inserts
-- only, so that DELETE never reaches the consumer.

-- +goose Up
CREATE TABLE product_events_outbox (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL CHECK (event_type IN (
                       'product.created', 'product.updated',
                       'product.markup_changed', 'product.archived',
                       'product.snapshot')),
    schema_version SMALLINT NOT NULL DEFAULT 1,
    occurred_at    TIMESTAMPTZ NOT NULL,
    actor_role     TEXT NOT NULL CHECK (actor_role IN ('supplier', 'admin', 'system')),
    request_id     TEXT,
    payload        JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The retention sweep's index.
CREATE INDEX product_events_outbox_created_idx ON product_events_outbox (created_at);

-- +goose Down
DROP TABLE IF EXISTS product_events_outbox;

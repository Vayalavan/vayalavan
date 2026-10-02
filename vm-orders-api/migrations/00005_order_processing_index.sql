-- An index for the one query that runs every minute forever.
--
-- The order processor claims work with:
--
--     SELECT id FROM orders
--     WHERE status = 'paid' AND processing_at <= now()
--     ORDER BY processing_at LIMIT $1 FOR UPDATE SKIP LOCKED
--
-- The existing orders_status_idx is (status, placed_at DESC), which narrows on
-- status but cannot serve the ordering, so the plan was a scan plus a sort.
-- Free at four rows; not something to leave on the hot path of a job that runs
-- unattended for the life of the service.
--
-- PARTIAL, not a (status, processing_at) composite. The processor only ever
-- asks about status = 'paid', and an order leaves that state permanently
-- within a day, so this index holds only the orders actually awaiting
-- processing — a handful at any moment — rather than every order ever placed.
-- That means it stays small however large `orders` grows, it self-prunes as
-- rows are processed, and it adds almost nothing to the write cost of placing
-- an order, because non-paid rows are never indexed at all.
--
-- Postgres can use it here because `status = 'paid'` appears as a literal
-- predicate it can match against the index definition.

-- +goose Up

CREATE INDEX orders_due_for_processing_idx
    ON orders (processing_at)
    WHERE status = 'paid';

-- +goose Down

DROP INDEX IF EXISTS orders_due_for_processing_idx;

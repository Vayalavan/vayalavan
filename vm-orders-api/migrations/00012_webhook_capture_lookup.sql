-- Lets the reservation sweeper ask "has Razorpay told us this order was paid?"
--
-- A webhook event is recorded before it is processed, so a capture whose
-- processing then fails still leaves its row here. The sweeper refuses to
-- release the stock of an order with such a row: the customer has paid, and
-- their produce must not go back on sale while a human settles the order.
-- The lookup is by the Razorpay order id inside the payload.

-- +goose Up
CREATE INDEX webhook_events_captured_order_idx
    ON webhook_events ((payload -> 'payload' -> 'payment' -> 'entity' ->> 'order_id'))
    WHERE event_type = 'payment.captured';

-- +goose Down
DROP INDEX IF EXISTS webhook_events_captured_order_idx;

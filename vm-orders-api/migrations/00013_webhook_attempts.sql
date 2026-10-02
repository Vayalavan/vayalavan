-- Lets a webhook that failed for a TEMPORARY reason be processed again.
--
-- The dedupe used to treat an event as done the moment it was received, so a
-- capture that hit a database blip was never retried: Razorpay's redeliveries
-- were all answered "already processed". Now an event is done only once
-- processed_at is set — on success, or on a PERMANENT failure (an amount
-- mismatch, which no retry can fix). A temporary failure leaves processed_at
-- NULL, and the next redelivery runs it again.
--
-- attempts counts deliveries that got as far as processing, so an error
-- mistaken for temporary cannot be retried forever: past
-- WEBHOOK_MAX_ATTEMPTS it is closed as failed and left to a human.

-- +goose Up
ALTER TABLE webhook_events ADD COLUMN attempts INT NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE webhook_events DROP COLUMN IF EXISTS attempts;

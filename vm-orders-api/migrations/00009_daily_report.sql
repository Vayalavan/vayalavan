-- The daily courier report's send log.
--
-- The row IS the guard, exactly like webhook_events.provider_event_id: the job
-- polls every minute, so without something durable it would send the same
-- report sixty times an hour, and a primary key on the date is the cheapest
-- correct way to make "send once per day" true across restarts and replicas.
--
-- The date is the IST business day the report covers, decided in Go where
-- isttime owns the definition (CLAUDE.md rule 2).

-- +goose Up

CREATE TABLE daily_report_sends (
    report_date DATE PRIMARY KEY,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- What was in the report, so a question about a mail nobody kept can be
    -- answered from the database.
    order_count INTEGER NOT NULL,
    recipients  TEXT NOT NULL
);

COMMENT ON TABLE daily_report_sends IS
    'One row per IST day whose courier report has been emailed. The primary key is the send-once guard.';

-- +goose Down

DROP TABLE IF EXISTS daily_report_sends;

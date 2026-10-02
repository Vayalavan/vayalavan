# Transactional email — PARKED for next release

`order_confirmation.go` renders the confirmation email required by
CLAUDE.md §6.1: order summary, price breakdown, the three-milestone timeline
with dates, the expected delivery date, the no-live-tracking note and the
support address. It is complete and unit-testable.

**What is missing:** the outbox dispatcher that drains `orders.outbox` and
sends these. `order.confirmed` rows ARE being written (see
`internal/api/webhook.go`, in the same transaction as the capture), so nothing
is lost — the queue simply accumulates until a dispatcher is added.

To finish it you need:
1. A worker on a ticker, guarded by a Postgres advisory lock (mirror
   `internal/sweeper`), claiming rows with `ClaimOutboxBatch` (already written,
   uses `FOR UPDATE SKIP LOCKED`).
2. A customer-contact lookup against vm-profile-api — email and name live in
   that schema and cannot be read directly (CLAUDE.md §3).
3. `MarkOutboxPublished` / `MarkOutboxFailed` on the way out, with a retry
   ceiling so an undeliverable address does not retry forever.

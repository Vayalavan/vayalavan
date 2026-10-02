/**
 * Idempotency keys for order placement (CLAUDE.md rule 6).
 *
 * `crypto.randomUUID()` does not exist in Hermes, and the web storefront calls
 * it directly — so this is the one thing the mobile checkout cannot copy. A
 * polyfill would be the obvious answer, but it is a dependency for one string
 * and the property actually required here is narrower than a UUID's.
 *
 * vm-orders-api stores the key against the CUSTOMER (customer_id, key), so the
 * only collision that could do harm is one customer generating the same key
 * for two different checkouts — which would silently hand them their previous
 * order instead of placing the new one. Milliseconds plus ~41 bits of
 * randomness makes that impossible in practice for a single person's ordering
 * history, and two customers colliding is not a shared namespace at all.
 *
 * The key is generated ONCE per checkout attempt and reused on every retry.
 * Generating it inside the request would defeat the entire point: a double-tap
 * or a retry after a timeout would each get a fresh key and place a second
 * real order.
 */

/** A new key. Prefixed so an order placed from a phone is obvious in the logs. */
export function newIdempotencyKey(): string {
  const time = Date.now().toString(36);
  const random = `${Math.random().toString(36).slice(2, 10)}${Math.random()
    .toString(36)
    .slice(2, 10)}`;
  return `mob-order-${time}-${random}`;
}

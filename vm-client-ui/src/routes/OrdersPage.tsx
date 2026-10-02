/**
 * A customer's own orders.
 *
 * The timeline here is the same component the admin sees, fed by the same
 * server-computed milestones — a customer and an ops person looking at one
 * order must never be shown different dates (CLAUDE.md §6.1).
 */
import { type ReactElement } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Card, PageHeading, OrderTimeline, Skeleton, type ApiError, type TimelineMilestone,
} from "@vayal/ui-kit";
import { OrderThumbnails } from "../components/OrderThumbnails.js";
import { api } from "../lib/api.js";
import { useAuth } from "../lib/auth.js";
import { config } from "../lib/config.js";
import { paymentCopy, usePayment } from "../lib/payment.js";

/**
 * The values ARE snapshots — orders-api stores them in `*_snapshot` columns so
 * an order still renders after the product is edited or archived (CLAUDE.md
 * §5.3) — but the JSON does not carry that suffix. Naming these
 * `product_name_snapshot` here made every field `undefined` at runtime, which
 * is how "undefined × 2" reached this page. Match the wire, not the schema:
 * `vm-orders-api/internal/api/api.go`, `orderItemResponse`.
 */
interface OrderItem {
  product_id: string;
  product_name: string;
  unit_label: string;
  /**
   * Presigned at read time and absent when the product has no photograph, was
   * deleted, or the catalogue could not be reached — the one field on an order
   * line that is NOT a snapshot (CLAUDE.md §5.2).
   */
  image_url?: string;
  /** `omitempty` server-side: absent, not null, when the product has no grade. */
  grade?: string;
  /**
   * The SIZE CODE this line was bought at, snapshotted when the order was
   * placed. Absent on ungraded listings and on every order placed before size
   * codes existed — render a missing one as no grade at all.
   */
  size_code?: string;
  size_meta?: string;
  qty: number;
  unit_price_display: string;
  line_total_display: string;
}

interface Order {
  id: string;
  order_number: string;
  status: string;
  /** Set when a scheduled or repeat order placed this one (CLAUDE.md §6.7). */
  schedule_id?: string;
  /**
   * Sent by vm-orders-api only while the order is still `pending_payment`, so
   * the customer can resume a payment they abandoned. Absent on every other
   * status — a paid order has nothing to pay.
   */
  razorpay_order_id?: string;
  razorpay_key_id?: string;
  placed_at: string;
  delivery_day: string;
  expected_delivery_date: string;
  subtotal_display: string;
  platform_fee_display: string;
  delivery_fee_display: string;
  total_display: string;
  items?: OrderItem[];
  milestones?: TimelineMilestone[];
  expected_delivery_text?: string;
  courier_notice?: string;
  support_notice?: string;
  /** The snapshot taken at placement — the API calls it `address`. */
  address?: Record<string, string>;
}

/**
 * How each status reads to a customer.
 *
 * The database's own words are not the customer's: "processed" describes our
 * warehouse step, and a customer wants to know whether their food is coming.
 */
const STATUS_COPY: Record<string, { label: string; tone: "good" | "wait" | "bad" }> = {
  pending_payment: { label: "Awaiting payment", tone: "wait" },
  paid: { label: "Confirmed", tone: "good" },
  processed: { label: "Being prepared", tone: "good" },
  // "Dispatched", not "On its way": it is the word the rest of the platform
  // uses for this transition — the admin queue, the emails, the order timeline
  // — and a customer comparing a mail to this screen must not have to work out
  // that two phrasings mean one state.
  dispatched: { label: "Dispatched", tone: "good" },
  payment_failed: { label: "Payment failed", tone: "bad" },
  expired: { label: "Expired", tone: "bad" },
  cancelled: { label: "Cancelled", tone: "bad" },
  refunded: { label: "Refunded", tone: "bad" },
};

/**
 * Why an order stopped, in the customer's terms.
 *
 * Only the statuses that end an order appear here; anything else is still
 * moving and has a timeline to show instead. The wording avoids blame — an
 * expired order is usually a checkout someone simply walked away from.
 */
const TERMINAL_COPY: Record<string, string> = {
  expired:
    "Payment wasn't completed in time, so we released the items back to the " +
    "shop. Nothing was charged. You're welcome to order them again if they're " +
    "still available today.",
  payment_failed:
    "The payment didn't go through, so this order wasn't placed. Nothing was " +
    "charged. Please try again, or use a different payment method.",
  cancelled: "This order was cancelled. Anything charged is refunded to the original payment method.",
  refunded: "This order was refunded to the original payment method.",
};

function StatusBadge({ status }: { status: string }): ReactElement {
  const copy = STATUS_COPY[status] ?? { label: status.replace(/_/g, " "), tone: "wait" as const };
  const tone =
    copy.tone === "good"
      ? "bg-primary-100 text-primary-800"
      : copy.tone === "bad"
        ? "bg-accent-100 text-accent-900"
        : "bg-surface-sunken text-primary-900/70";
  return (
    <span className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${tone}`}>
      {copy.label}
    </span>
  );
}

function formatDate(iso: string): string {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return iso;
  return new Intl.DateTimeFormat("en-IN", {
    day: "numeric", month: "short", year: "numeric",
    hour: "numeric", minute: "2-digit", hour12: true,
    // Business days are IST, never the device's zone (CLAUDE.md rule 2).
    timeZone: "Asia/Kolkata",
  }).format(parsed);
}

export function OrdersPage(): ReactElement {
  // The access token lives in memory only, so a hard reload has none until the
  // stored refresh token has been traded in. Firing before that finishes races
  // the bootstrap and 401s — which surfaced as "Authentication is required" on
  // refresh even though the session was perfectly valid.
  const { initialising } = useAuth();

  const orders = useQuery<{ orders: Order[]; total: number }, ApiError>({
    queryKey: ["my-orders"],
    queryFn: () => api.get<{ orders: Order[]; total: number }>("/orders"),
    enabled: !initialising,
  });

  if (orders.isPending) {
    return (
      <div className="space-y-3">
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (orders.isError) {
    return (
      <Card>
        <p role="alert" className="text-sm text-accent-800">{orders.error.message}</p>
      </Card>
    );
  }

  return (
    <>
      <PageHeading title="Your orders" description="Everything you have ordered from us." />

      {orders.data.orders.length === 0 ? (
        <Card>
          <h2 className="font-semibold text-primary-900">No orders yet</h2>
          <p className="mt-1 text-sm text-primary-900/70">
            When you order today&rsquo;s produce it will appear here.
          </p>
          <Link
            to="/"
            className="mt-4 inline-block rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white"
          >
            Browse today&rsquo;s produce
          </Link>
        </Card>
      ) : (
        <div className="grid gap-3">
          {orders.data.orders.map((order) => (
            <Card key={order.id}>
              <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <Link
                      to={`/orders/${order.id}`}
                      className="font-semibold text-primary-900 underline underline-offset-2"
                    >
                      {order.order_number}
                    </Link>
                    <StatusBadge status={order.status} />
                    {order.schedule_id && (
                      <Link
                        to={`/schedules/${order.schedule_id}`}
                        className="rounded-full bg-gold-50 px-2 py-0.5 text-[11px] font-semibold text-gold-700 ring-1 ring-gold-200 hover:bg-gold-100"
                      >
                        Scheduled
                      </Link>
                    )}
                  </div>
                  <p className="mt-0.5 text-xs text-primary-900/60">
                    Placed {formatDate(order.placed_at)} · expected{" "}
                    {order.expected_delivery_date}
                  </p>
                  {/* What was in it, before any of the text is read. */}
                  <div className="mt-2">
                    <OrderThumbnails items={order.items} />
                  </div>
                </div>
                <div className="flex items-center gap-3">
                  <span className="text-lg font-semibold tabular-nums text-primary-900">
                    {order.total_display}
                  </span>
                  <Link
                    to={`/orders/${order.id}`}
                    className="rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800"
                  >
                    View
                  </Link>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </>
  );
}

export function OrderDetailPage(): ReactElement {
  const { id = "" } = useParams();
  const { user, initialising } = useAuth();
  const payment = usePayment();

  // Gated on the auth bootstrap for the same reason as the list above.
  const order = useQuery<Order, ApiError>({
    queryKey: ["my-order", id],
    queryFn: () => api.get<Order>(`/orders/${id}`),
    enabled: !initialising,
  });

  // `isPending` stays true while the query is disabled, so the skeleton covers
  // the bootstrap too rather than flashing an error first.
  if (initialising || order.isPending) return <Skeleton className="h-64 w-full" />;

  if (order.isError) {
    return (
      <Card>
        <h1 className="font-semibold text-primary-900">
          {order.error.status === 404 ? "Order not found" : "Something went wrong"}
        </h1>
        <p className="mt-1 text-sm text-primary-900/70">
          {order.error.status === 404
            ? "We could not find that order on your account."
            : order.error.message}
        </p>
        <Link to="/orders" className="mt-4 inline-block text-sm text-primary-700 underline">
          Back to your orders
        </Link>
      </Card>
    );
  }

  const data = order.data;
  const address = data.address;

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-4 text-sm">
        <Link to="/orders" className="text-primary-700 underline underline-offset-2">
          Your orders
        </Link>
        <span className="mx-2 text-primary-900/40">/</span>
        <span className="text-primary-900/70">{data.order_number}</span>
      </nav>

      <PageHeading
        title={data.order_number}
        description={`Placed ${formatDate(data.placed_at)}`}
        actions={<StatusBadge status={data.status} />}
      />

      <div className="grid gap-6 lg:grid-cols-[1fr_360px]">
        <div className="space-y-6">
          {/* An unpaid order is the one state on this page with something to
              DO. It sits above the timeline because a customer who opened this
              page from an abandoned checkout came here to finish paying, and
              the stock is only held for RESERVATION_TTL_MINUTES. */}
          {data.status === "pending_payment" && (
            <Card>
              <h2 className="font-semibold text-primary-900">
                {payment.phase === "paid"
                  ? "Payment received"
                  : "Finish paying for this order"}
              </h2>
              <p className="mt-1 text-sm text-primary-900/70" aria-live="polite">
                {paymentCopy(payment.phase) ??
                  payment.message ??
                  `Your produce is reserved for a short while. Pay ${data.total_display} to confirm this order — if we don't hear from you, the reservation is released and the stock returns to today's catalog.`}
              </p>
              {payment.phase !== "paid" && payment.phase !== "slow" && (
                <button
                  type="button"
                  disabled={payment.busy || !data.razorpay_order_id}
                  onClick={() =>
                    payment.pay(data, {
                      name: user?.name ?? undefined,
                      email: user?.email ?? undefined,
                      contact: data.address?.phone ?? user?.phone ?? undefined,
                    })
                  }
                  className="mt-3 rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40"
                >
                  {payment.busy ? "Please wait…" : `Pay ${data.total_display}`}
                </button>
              )}
              {/* No provider order means Razorpay was unreachable at
                  placement. Saying so beats a button that cannot work. */}
              {!data.razorpay_order_id && (
                <p className="mt-2 text-xs text-primary-900/60">
                  We could not reach the payment provider for this order. Write
                  to {config.supportEmail} and we&rsquo;ll sort it out.
                </p>
              )}
            </Card>
          )}

          {/* An order that stopped has no timeline — the server sends none for
              a terminal status — so the card explains what happened instead of
              leaving the page silent about it. */}
          {TERMINAL_COPY[data.status] && (
            <Card>
              <h2 className="mb-2 font-semibold text-primary-900">
                {STATUS_COPY[data.status]?.label ?? "This order stopped"}
              </h2>
              <p className="text-sm text-primary-900/70">{TERMINAL_COPY[data.status]}</p>
            </Card>
          )}

          {data.milestones && data.milestones.length > 0 && (
            <Card>
              <h2 className="mb-4 font-semibold text-primary-900">Timeline</h2>
              <OrderTimeline
                milestones={data.milestones}
                expectedDeliveryText={
                  data.expected_delivery_text ??
                  `Expected delivery: ${data.expected_delivery_date}`
                }
                courierNotice={
                  data.courier_notice ??
                  "We hand your order to professional courier partners after processing, so live tracking isn't available. Dates shown are estimates."
                }
                supportNotice={
                  data.support_notice ??
                  `Any issues? Write to us at ${config.supportEmail}`
                }
              />
            </Card>
          )}

          <Card>
            <h2 className="font-semibold text-primary-900">Items</h2>
            <ul className="mt-3 divide-y divide-surface-border">
              {(data.items ?? []).map((item, index) => (
                <li key={index} className="flex justify-between gap-3 py-2 text-sm">
                  <span className="min-w-0">
                    {/* Snapshots, so this renders correctly even after the
                        product is edited or removed (CLAUDE.md §5.3). */}
                    <span className="block font-medium text-primary-900">
                      {item.product_name}
                      {item.grade && (
                        <span className="ml-2 text-xs text-primary-900/60">
                          {item.grade}
                        </span>
                      )}
                    </span>
                    <span className="text-primary-900/60">
                      {item.size_code ? `${item.size_code} · ` : ""}
                      {item.unit_label} × {item.qty} ·{" "}
                      {item.unit_price_display} each
                    </span>
                  </span>
                  <span className="shrink-0 font-medium tabular-nums text-primary-900">
                    {item.line_total_display}
                  </span>
                </li>
              ))}
            </ul>
          </Card>
        </div>

        <div className="space-y-4">
          <Card>
            <h2 className="font-semibold text-primary-900">Payment</h2>
            <dl className="mt-3 space-y-1 text-sm">
              <div className="flex justify-between">
                <dt className="text-primary-900/70">Subtotal</dt>
                <dd className="tabular-nums text-primary-900">{data.subtotal_display}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-primary-900/70">Platform fee</dt>
                <dd className="tabular-nums text-primary-900">{data.platform_fee_display}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-primary-900/70">Delivery charge</dt>
                <dd className="tabular-nums text-primary-900">{data.delivery_fee_display}</dd>
              </div>
              <div className="mt-2 flex justify-between border-t border-surface-border pt-2 text-base font-semibold">
                <dt className="text-primary-900">Total</dt>
                <dd className="tabular-nums text-primary-900">{data.total_display}</dd>
              </div>
            </dl>
          </Card>

          {address && (
            <Card>
              <h2 className="font-semibold text-primary-900">Delivering to</h2>
              {/* The snapshot taken at placement, not the current address
                  book: editing an address later must not silently rewrite
                  where a past order went. */}
              <address className="mt-2 text-sm not-italic text-primary-900/80">
                {address.recipient_name && (
                  <span className="block font-medium text-primary-900">
                    {address.recipient_name}
                  </span>
                )}
                {[address.line1, address.line2, address.landmark]
                  .filter(Boolean)
                  .map((part) => (
                    <span key={part} className="block">{part}</span>
                  ))}
                <span className="block">
                  {[address.city, address.state, address.pincode].filter(Boolean).join(", ")}
                </span>
                {address.phone && <span className="block">{address.phone}</span>}
              </address>
            </Card>
          )}
        </div>
      </div>
    </>
  );
}

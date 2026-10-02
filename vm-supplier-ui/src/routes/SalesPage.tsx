/**
 * The grower's own record of what they have sold and what they are owed.
 *
 * Settlement is a manual NEFT transfer decided on the admin's screen, so this
 * is the supplier's only way to check that figure independently. Both screens
 * read the same supplier_payouts rows, which is what makes them agree: if this
 * page says ₹4,460 outstanding, the admin's settlement screen says the same,
 * because it is the same row and not a second calculation.
 */
import { useState, type ReactElement } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Card, PageHeading, RangePicker, SALES_RANGES, type ApiError,
  gradeLabel,
} from "@vayal/ui-kit";
import { api } from "../lib/api.js";

interface SaleLine {
  product_name: string;
  unit_label: string;
  grade: string | null;
  /**
   * The GRADE this pack was sold at — "XL2". Null on an ungraded listing and
   * on orders placed before size codes existed. Distinct from `grade`, which
   * is the product-level quality label the grower types.
   */
  size_code: string | null;
  size_meta: string | null;
  weight_grams: number;
  qty: number;
  unit_price_display: string;
  line_total_display: string;
}

interface SupplierOrder {
  order_id: string;
  order_number: string;
  placed_at: string;
  delivery_day: string;
  status: string;
  units: number;
  amount_display: string;
  payout_status: string;
  payout_reference: string;
  payout_paid_at: string | null;
  items: SaleLine[];
}

interface ProductRow {
  product_name: string;
  grade: string | null;
  units: number;
  grams: number;
  amount_display: string;
}

interface Charges {
  commission_rate: string;
  gross_display: string;
  commission_display: string;
  net_display: string;
}

interface Sales {
  charges: Charges;
  summary: {
    order_count: number;
    gross_display: string;
    pending_display: string;
    settled_display: string;
  };
  products: ProductRow[];
  orders: SupplierOrder[];
  orders_total: number;
  limit: number;
  offset: number;
  /** The window the figures above were computed over, resolved server-side. */
  range: {
    key: string;
    label: string;
    all_time: boolean;
    from?: string;
    to?: string;
  };
}

/** Rows per page in the orders table. */
const PAGE_SIZE = 20;

/** Grams as the supplier thinks of them — kilos above 1 kg. */
function formatGrams(grams: number): string {
  if (grams >= 1000) {
    const kg = grams / 1000;
    return `${Number.isInteger(kg) ? kg : kg.toFixed(2)} kg`;
  }
  return `${grams} g`;
}

function formatDateTime(iso: string): string {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return iso;
  return new Intl.DateTimeFormat("en-IN", {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
    hour12: true,
    // Business days are IST (CLAUDE.md rule 2), never the device's zone.
    timeZone: "Asia/Kolkata",
  }).format(parsed);
}

export function SalesPage(): ReactElement {
  const [page, setPage] = useState(0);
  // All time, which is what this screen showed before the filter existed and
  // is the figure a grower reconciles a bank statement against. The server
  // resolves every other period in IST — the browser's clock never decides
  // which orders fall in "today" (CLAUDE.md rule 2).
  const [range, setRange] = useState("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  // A custom range is incomplete until both ends are picked. Sending half of
  // one would be rejected, so the screen keeps showing the last good result
  // until the second date arrives.
  const custom = range === "custom";
  const rangeQuery = custom
    ? from !== "" && to !== ""
      ? { range, from, to }
      : null
    : { range };

  function changeRange(next: string): void {
    setRange(next);
    // A new period is a different set of orders; staying on page 4 of the old
    // one would land on an empty table.
    setPage(0);
  }

  const sales = useQuery<Sales, ApiError>({
    queryKey: ["supplier-sales", page, rangeQuery],
    enabled: rangeQuery !== null,
    queryFn: () =>
      api.get<Sales>("/supplier/sales", {
        query: { limit: PAGE_SIZE, offset: page * PAGE_SIZE, ...rangeQuery },
      }),
    staleTime: 30_000,
    // Keeps the previous page on screen while the next loads, so the table
    // does not collapse to "Loading…" and jump the page on every click.
    placeholderData: (previous) => previous,
  });

  const picker = (
    <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
      <RangePicker
        range={range}
        from={from}
        to={to}
        onRange={changeRange}
        onFrom={(value) => {
          setFrom(value);
          setPage(0);
        }}
        onTo={(value) => {
          setTo(value);
          setPage(0);
        }}
        options={SALES_RANGES}
        idPrefix="sales-range"
        label="Sales period"
      />
      {custom && (from === "" || to === "") && (
        <p className="text-sm text-primary-900/60">
          Pick both dates to apply the range.
        </p>
      )}
    </div>
  );

  // A disabled query is "pending" forever, so an unfinished custom range must
  // say what it is waiting for rather than spin.
  if (rangeQuery === null) {
    return (
      <>
        {picker}
        <Card>Choose a start and end date to see that period.</Card>
      </>
    );
  }

  if (sales.isPending) {
    return (
      <>
        {picker}
        <Card>Loading your sales…</Card>
      </>
    );
  }

  if (sales.isError) {
    return (
      <>
        {picker}
        <Card>
          <p role="alert" className="text-sm text-accent-800">
            {sales.error.message}
          </p>
        </Card>
      </>
    );
  }

  const { summary, products, orders } = sales.data;
  const total = sales.data.orders_total;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <>
      <PageHeading
        title="Sales and settlement"
        description="Everything you have sold, and what we still owe you. These are the same figures our settlement screen pays against."
      />

      {picker}

      {/* Which period the numbers below belong to. Without this, a filter left
          on last week looks exactly like a bad week. */}
      <p className="mb-4 text-sm text-primary-900/70">
        Showing <span className="font-medium text-primary-900">{sales.data.range.label}</span>
        {sales.data.range.all_time
          ? " — every order you have ever had."
          : ` (${sales.data.range.from} to ${sales.data.range.to}).`}
      </p>

      <div className="mb-6 grid gap-3 sm:grid-cols-3">
        <SummaryTile
          label={sales.data.range.all_time ? "Sold to date" : "Sold in this period"}
          value={summary.gross_display}
          note={`${summary.order_count} order${summary.order_count === 1 ? "" : "s"}`}
        />
        <SummaryTile
          label="Awaiting payment to you"
          value={summary.pending_display}
          note="Paid by NEFT after settlement"
          emphasis
        />
        <SummaryTile
          label="Already settled"
          value={summary.settled_display}
          note="Transferred to your bank"
        />
      </div>

      {/* The deduction, stated before a grower has to work it out from a bank
          statement. Showing gross, commission and net together is the whole
          point: "3% is charged" alone still leaves them doing arithmetic. */}
      <Card className="mb-6">
        <h2 className="font-semibold text-primary-900">What you are paid</h2>
        <dl className="mt-3 space-y-1.5 text-sm">
          <div className="flex justify-between gap-3">
            <dt className="text-primary-900/70">Your produce sold</dt>
            <dd className="tabular-nums text-primary-900">
              {sales.data.charges.gross_display}
            </dd>
          </div>
          <div className="flex justify-between gap-3">
            <dt className="text-primary-900/70">
              Vayalavan commission ({sales.data.charges.commission_rate})
            </dt>
            <dd className="tabular-nums text-accent-800">
              &minus; {sales.data.charges.commission_display}
            </dd>
          </div>
          <div className="flex justify-between gap-3 border-t border-surface-border pt-2 text-base font-semibold">
            <dt className="text-primary-900">You receive</dt>
            <dd className="tabular-nums text-primary-900">
              {sales.data.charges.net_display}
            </dd>
          </div>
        </dl>
        <p className="mt-3 text-xs text-primary-900/60">
          The commission of {sales.data.charges.commission_rate} covers listing
          and selling your produce. The delivery charge and platform fee the
          customer pays are separate and are not taken from you — every figure
          on this page is already net of commission.
        </p>
      </Card>

      <h2 className="mb-3 text-lg font-semibold text-primary-900">By produce</h2>
      {products.length === 0 ? (
        <Card className="mb-6">
          <p className="text-primary-900/70">
            Nothing sold yet. Once customers buy today&rsquo;s produce it will
            appear here.
          </p>
        </Card>
      ) : (
        <Card className="mb-6 overflow-x-auto p-0">
          <table className="w-full min-w-[32rem] text-left text-sm">
            <thead className="border-b border-surface-border text-xs uppercase tracking-wide text-primary-900/60">
              <tr>
                <th className="px-4 py-3">Produce</th>
                <th className="px-4 py-3 text-right">Packs</th>
                <th className="px-4 py-3 text-right">Weight</th>
                <th className="px-4 py-3 text-right">Value</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-surface-border">
              {products.map((row) => (
                <tr key={`${row.product_name}-${row.grade ?? ""}`}>
                  <td className="px-4 py-3 font-medium text-primary-900">
                    {row.product_name}
                    {row.grade && (
                      <span className="ml-2 rounded-full bg-surface-sunken px-2 py-0.5 text-xs text-primary-900/70">
                        {row.grade}
                      </span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums">{row.units}</td>
                  <td className="px-4 py-3 text-right tabular-nums">
                    {formatGrams(row.grams)}
                  </td>
                  <td className="px-4 py-3 text-right font-medium tabular-nums text-primary-900">
                    {row.amount_display}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}

      <h2 className="mb-3 text-lg font-semibold text-primary-900">Orders</h2>
      {orders.length === 0 ? (
        <Card>
          <p className="text-primary-900/70">No orders yet.</p>
        </Card>
      ) : (
        <>
          <Card className="overflow-x-auto p-0">
            <table className="w-full min-w-[44rem] text-left text-sm">
              <thead className="border-b border-surface-border text-xs uppercase tracking-wide text-primary-900/60">
                <tr>
                  <th scope="col" className="px-4 py-3">Order</th>
                  <th scope="col" className="px-4 py-3">Placed</th>
                  <th scope="col" className="px-4 py-3">Delivery</th>
                  <th scope="col" className="px-4 py-3 text-right">Packs</th>
                  <th scope="col" className="px-4 py-3">Payment to you</th>
                  <th scope="col" className="px-4 py-3 text-right">Your share</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-surface-border">
                {orders.map((order) => (
                  <OrderRow key={order.order_id} order={order} />
                ))}
              </tbody>
            </table>
          </Card>

          {pages > 1 && (
            <div className="mt-4 flex items-center justify-between gap-3">
              <button
                onClick={() => setPage((p) => Math.max(0, p - 1))}
                disabled={page === 0}
                className="rounded-card border border-surface-border px-4 py-2 text-sm font-medium text-primary-800 disabled:opacity-40"
              >
                Previous
              </button>
              <span className="text-sm text-primary-900/60">
                Page {page + 1} of {pages} &middot; {total} order
                {total === 1 ? "" : "s"}
              </span>
              <button
                onClick={() => setPage((p) => Math.min(pages - 1, p + 1))}
                disabled={page >= pages - 1}
                className="rounded-card border border-surface-border px-4 py-2 text-sm font-medium text-primary-800 disabled:opacity-40"
              >
                Next
              </button>
            </div>
          )}
        </>
      )}

    </>
  );
}

function SummaryTile({
  label,
  value,
  note,
  emphasis = false,
}: {
  label: string;
  value: string;
  note: string;
  emphasis?: boolean;
}): ReactElement {
  return (
    <Card className={emphasis ? "border-primary-300 bg-primary-50" : ""}>
      <p className="text-xs uppercase tracking-wide text-primary-900/60">{label}</p>
      <p className="mt-1 text-2xl font-semibold tabular-nums text-primary-900">{value}</p>
      <p className="mt-0.5 text-xs text-primary-900/60">{note}</p>
    </Card>
  );
}

/**
 * One order as a table row, with its line items behind a disclosure.
 *
 * The detail stays available rather than being dropped for the table: a grower
 * reconciling a bank transfer needs to see WHICH produce made up an amount,
 * and sending them to another screen to find out defeats the point of the page.
 */
/**
 * One order as a table row, with its line items behind a disclosure.
 *
 * The detail stays available rather than being dropped for the table: a grower
 * reconciling a bank transfer needs to see WHICH produce made up an amount,
 * and sending them to another screen to find out defeats the point of the page.
 */
function OrderRow({ order }: { order: SupplierOrder }): ReactElement {
  const [open, setOpen] = useState(false);
  const settled = order.payout_status === "paid";

  return (
    <>
      <tr className={open ? "bg-primary-50/50" : ""}>
        <td className="px-4 py-3">
          <button
            type="button"
            onClick={() => setOpen((current) => !current)}
            aria-expanded={open}
            className="flex items-center gap-1.5 font-medium text-primary-900 underline underline-offset-2"
          >
            {order.order_number}
            <span aria-hidden="true" className="text-xs text-primary-900/40">
              {open ? "\u25b2" : "\u25bc"}
            </span>
          </button>
        </td>
        <td className="px-4 py-3 text-primary-900/70">
          {formatDateTime(order.placed_at)}
        </td>
        <td className="px-4 py-3 text-primary-900/70">{order.delivery_day}</td>
        <td className="px-4 py-3 text-right tabular-nums text-primary-900/70">
          {order.units}
        </td>
        <td className="px-4 py-3">
          <span
            className={`rounded-full px-2 py-0.5 text-xs font-medium ${
              settled
                ? "bg-primary-100 text-primary-800"
                : "bg-accent-100 text-accent-900"
            }`}
          >
            {settled ? "Settled" : "Awaiting"}
          </span>
          {settled && order.payout_reference && (
            <span className="ml-2 text-xs text-primary-900/55">
              {order.payout_reference}
            </span>
          )}
        </td>
        <td className="px-4 py-3 text-right font-semibold tabular-nums text-primary-900">
          {order.amount_display}
        </td>
      </tr>

      {open && (
        <tr className="bg-surface-sunken/40">
          {/* Spans every column so the detail reads as part of the row above
              rather than as a malformed one of its own. */}
          <td colSpan={6} className="px-4 py-3">
            <ul className="divide-y divide-surface-border">
              {order.items.map((item, index) => (
                <li
                  key={`${item.product_name}-${item.unit_label}-${index}`}
                  className="flex justify-between gap-3 py-2 text-sm"
                >
                  <span className="min-w-0">
                    <span className="block font-medium text-primary-900">
                      {item.product_name}
                      {/* The SIZE CODE, prominent: this is the crate the
                          grower has to pick from, and two lines of one produce
                          are otherwise identical. */}
                      {gradeLabel(item.size_code) && (
                        <span className="ml-2 rounded-full bg-primary-100 px-2 py-0.5 text-xs font-medium text-primary-800">
                          {gradeLabel(item.size_code)}
                        </span>
                      )}
                      {item.grade && (
                        <span className="ml-2 text-xs text-primary-900/60">
                          {item.grade}
                        </span>
                      )}
                    </span>
                    <span className="text-primary-900/60">
                      {item.size_meta ? `${item.size_meta} · ` : ""}
                      {item.unit_label} &times; {item.qty} &middot;{" "}
                      {item.unit_price_display} each &middot;{" "}
                      {formatGrams(item.weight_grams * item.qty)}
                    </span>
                  </span>
                  <span className="shrink-0 font-medium tabular-nums text-primary-900">
                    {item.line_total_display}
                  </span>
                </li>
              ))}
            </ul>
            <p className="mt-2 text-xs text-primary-900/55">
              Your share of this order, already net of commission. The customer
              also paid a platform fee and delivery charge, which are not taken
              from you.
              {settled && order.payout_paid_at && (
                <> Paid {formatDateTime(order.payout_paid_at)}.</>
              )}
            </p>
          </td>
        </tr>
      )}
    </>
  );
}

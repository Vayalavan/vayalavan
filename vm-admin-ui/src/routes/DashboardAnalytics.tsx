/**
 * The dashboard's breakdowns: where the money came from, what was sold, and
 * how the period moved day to day.
 *
 * Everything here obeys the SAME period the tiles above it do — the range is
 * passed in, never chosen locally, so the screen cannot end up showing a
 * month's revenue split under a day's headline. The server resolves it in IST
 * (CLAUDE.md rule 2); this file never computes a date.
 *
 * The marks come from vm-ui-kit's chart primitives, which the supplier's own
 * analytics screen also uses: two copies of a bar row drift within a release.
 * This file decides WHAT to plot and how to label it; the kit decides how a
 * bar looks.
 */
import { type ReactElement } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Card, ApiError, ChartHeading, LineChart, MetricBar, SplitBar,
  chartSharePct, gradeLabel, type SeriesPoint,
} from "@vayal/ui-kit";
import { api } from "../lib/api.js";

interface SupplierRow {
  supplier_id: string;
  business_name: string;
  sales_paise: number;
  sales_display: string;
  payable_paise: number;
  payable_display: string;
  margin_paise: number;
  margin_display: string;
  units: number;
  grams: number;
  orders: number;
}

interface ProduceRow {
  product_name: string;
  grade: string | null;
  /**
   * The GRADE this row sold out of. "" on ungraded listings and on orders
   * placed before size codes existed. The rows are grouped by it, so one
   * produce can appear once per grade — which is the point: they are separate
   * crates at separate prices.
   */
  size_code: string;
  sales_paise: number;
  sales_display: string;
  markup_paise: number;
  markup_display: string;
  units: number;
  grams: number;
  orders: number;
}

/** One GRADE of one produce, across every supplier. */
interface SizeCodeRow {
  product_name: string;
  size_code: string;
  size_meta: string;
  /** How many suppliers sold this grade. One is a habit; five is a market. */
  suppliers: number;
  sales_paise: number;
  sales_display: string;
  markup_paise: number;
  markup_display: string;
  units: number;
  grams: number;
  orders: number;
}

interface DayRow {
  date: string;
  orders: number;
  paid_orders: number;
  gmv_paise: number;
  gmv_display: string;
}

interface OutcomeRow {
  status: string;
  orders: number;
  value_paise: number;
  value_display: string;
}

interface DestinationRow {
  city: string;
  pincode: string;
  orders: number;
  gmv_paise: number;
  gmv_display: string;
}

interface Analytics {
  range: { key: string; label: string; from: string; to: string; days: number };
  by_supplier: SupplierRow[];
  by_supplier_truncated: boolean;
  by_supplier_total: number;
  by_produce: ProduceRow[];
  by_produce_truncated: boolean;
  by_produce_total: number;
  /** Graded lines only. Empty when nothing sold was graded. */
  by_size_code: SizeCodeRow[];
  by_size_code_truncated: boolean;
  by_size_code_total: number;
  /** Fixed seven-day window. Independent of the period selected above. */
  last_7_days: { from: string; to: string; days: DayRow[] };
  outcomes: OutcomeRow[];
  orders_placed: number;
  basket: {
    paid_orders: number;
    gmv_paise: number;
    gmv: string;
    average_order_paise: number;
    average_order: string;
    units: number;
    units_per_order_centi: number;
  };
  customers: { total: number; new: number; returning: number };
  cutoff: { hour_ist: number; before: number; after: number };
  destinations: DestinationRow[];
}

export interface DashboardAnalyticsProps {
  /** The period key the tiles above are showing — "today", "7d", "custom"… */
  range: string;
  /** Only meaningful when range === "custom". */
  from: string;
  to: string;
}

export function DashboardAnalytics({
  range,
  from,
  to,
}: DashboardAnalyticsProps): ReactElement | null {
  const analytics = useQuery<Analytics, ApiError>({
    queryKey: ["dashboard-analytics", range, from, to],
    queryFn: () =>
      api.get<Analytics>("/admin/analytics", {
        query: range === "custom" ? { range, from, to } : { range },
      }),
    // Slower than the tiles' minute: these are aggregates over every line item
    // in the window, and nobody watches a supplier split tick over.
    refetchInterval: 300_000,
  });

  if (analytics.isPending) {
    return (
      <Card className="mt-6">
        <p className="text-sm text-primary-900/60">Loading breakdowns…</p>
      </Card>
    );
  }
  if (analytics.isError) {
    // Deliberately not fatal to the screen: the tiles above are already
    // rendered, and they are the figures someone opened this page for.
    return (
      <Card className="mt-6">
        <p role="alert" className="text-sm text-accent-800">
          Breakdowns unavailable: {analytics.error.message}
        </p>
      </Card>
    );
  }

  const a = analytics.data;
  const nothingSold = a.basket.paid_orders === 0;

  return (
    <div className="mt-8 space-y-6">
      <h2 className="text-lg font-semibold text-primary-900">Breakdowns</h2>

      {nothingSold ? (
        <Card>
          <p className="text-sm text-primary-900/70">
            No paid orders in this period, so there is nothing to break down.
            {a.orders_placed > 0 &&
              ` ${a.orders_placed} order${a.orders_placed === 1 ? "" : "s"} ` +
                "were placed — see how they ended below."}
          </p>
          {a.orders_placed > 0 && <Outcomes rows={a.outcomes} total={a.orders_placed} />}
        </Card>
      ) : (
        <>
          <div className="grid gap-6 lg:grid-cols-2">
            <RevenueBySupplier
              rows={a.by_supplier}
              truncated={a.by_supplier_truncated}
              total={a.by_supplier_total}
            />
            <RevenueByProduce
              rows={a.by_produce}
              truncated={a.by_produce_truncated}
              total={a.by_produce_total}
            />
          </div>

          {/* Only when something graded actually sold. A shop selling nothing
              graded should not be shown an empty card asking why. */}
          {a.by_size_code.length > 0 && (
            <RevenueBySizeCode
              rows={a.by_size_code}
              truncated={a.by_size_code_truncated}
              total={a.by_size_code_total}
            />
          )}

          {/* The one constant on the page: always this week, whatever the
              filter says. Sits between the period breakdowns and the
              operational detail so it reads as its own thing. */}
          <LastSevenDays week={a.last_7_days} />

          <div className="grid gap-6 lg:grid-cols-3">
            <BasketAndCustomers basket={a.basket} customers={a.customers} />
            <CutoffSplit cutoff={a.cutoff} />
            <Destinations rows={a.destinations} />
          </div>
        </>
      )}
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Charts                                                                     */
/* -------------------------------------------------------------------------- */

/**
 * Sales across the last seven days — and the only chart on this screen.
 *
 * It does NOT follow the period picker, deliberately: it answers "how is the
 * week going" while everything else on the page answers "what did the selected
 * period do". A column chart of the selected period used to sit above it and
 * was removed as a near-duplicate, so with a long range selected the
 * breakdowns move and this line does not. That is the trade — the tiles and
 * the rankings carry the period, the line carries the week.
 */
function LastSevenDays({
  week,
}: {
  week: { from: string; to: string; days: DayRow[] };
}): ReactElement | null {
  const first = week.days[0];
  if (!first) return null;

  const total = week.days.reduce((sum, d) => sum + d.gmv_paise, 0);
  const peak = week.days.reduce((a, b) => (b.gmv_paise > a.gmv_paise ? b : a), first);

  return (
    <Card>
      <ChartHeading
        title="Sales, last 7 days"
        hint={`${shortDate(week.from)} – ${shortDate(week.to)} · always this week, whatever period is selected above`}
      />
      <LineChart
        points={week.days.map(toPoint)}
        ariaLabel={`Daily sales for the seven days to ${shortDate(week.to)}. The table below carries the figures.`}
        tableCaption="Sales, last 7 days"
        valueHeading="Sales"
      />
      <p className="mt-3 text-xs text-primary-900/50">
        {formatRupeesFromPaise(total)} across the week · peak {peak.gmv_display}
      </p>
    </Card>
  );
}

/** One day as the charts want it: a magnitude, a label, and a detail line. */
function toPoint(day: DayRow): SeriesPoint {
  return {
    key: day.date,
    label: shortDate(day.date),
    value: day.gmv_paise,
    display: day.gmv_display,
    note: `${day.paid_orders} paid of ${day.orders} placed`,
  };
}

/**
 * Revenue by supplier — who we are actually buying from, and what we keep.
 *
 * Sales is what customers paid for that grower's produce; margin is sales
 * minus what their payout pays them. Both are shown because a grower can be
 * the largest by sales and the smallest by margin.
 */
function RevenueBySupplier({
  rows, truncated, total,
}: { rows: SupplierRow[]; truncated: boolean; total: number }): ReactElement {
  const peak = Math.max(...rows.map((r) => r.sales_paise), 1);
  const grand = rows.reduce((sum, r) => sum + r.sales_paise, 0);

  return (
    <Card>
      <ChartHeading
        title="Revenue by supplier"
        hint={`${total} supplier${total === 1 ? "" : "s"} sold in this period`}
      />
      <ul className="mt-4 space-y-3">
        {rows.map((row) => (
          <li key={row.supplier_id}>
            <MetricBar
              label={row.business_name}
              value={row.sales_display}
              share={row.sales_paise / peak}
              note={`${chartSharePct(row.sales_paise, grand)} of revenue · ${row.orders} order${
                row.orders === 1 ? "" : "s"
              } · we kept ${row.margin_display}`}
            />
          </li>
        ))}
      </ul>
      {truncated && (
        <p className="mt-3 text-xs text-primary-900/50">
          Showing the top {rows.length} of {total}.
        </p>
      )}
    </Card>
  );
}

/** Revenue by produce, from the line snapshots — an edited product still
 *  reports under the name it was sold as. */
function RevenueByProduce({
  rows, truncated, total,
}: { rows: ProduceRow[]; truncated: boolean; total: number }): ReactElement {
  const peak = Math.max(...rows.map((r) => r.sales_paise), 1);
  const grand = rows.reduce((sum, r) => sum + r.sales_paise, 0);

  return (
    <Card>
      <ChartHeading
        title="Revenue by produce"
        hint={`${total} product${total === 1 ? "" : "s"} sold in this period`}
      />
      <ul className="mt-4 space-y-3">
        {rows.map((row) => (
          <li key={`${row.product_name}-${row.grade ?? ""}-${row.size_code}`}>
            <MetricBar
              // Produce, its GRADE, then the quality label. The grade is what
              // separates two rows of one fruit, so it comes first.
              label={[
                row.product_name,
                gradeLabel(row.size_code),
                row.grade ?? "",
              ]
                .filter(Boolean)
                .join(" · ")}
              value={row.sales_display}
              share={row.sales_paise / peak}
              note={`${chartSharePct(row.sales_paise, grand)} of revenue · ${row.units} pack${
                row.units === 1 ? "" : "s"
              } · ${formatKg(row.grams)}`}
            />
          </li>
        ))}
      </ul>
      {truncated && (
        <p className="mt-3 text-xs text-primary-900/50">
          Showing the top {rows.length} of {total}.
        </p>
      )}
    </Card>
  );
}

/**
 * Revenue by GRADE, across every supplier.
 *
 * The produce breakdown above answers "what sells"; this answers "which
 * crate", and they are different questions once a grower picks eleven grades
 * of one fruit. The supplier count is the part an admin cannot get anywhere
 * else: one supplier selling XL2 is a habit, five is a market.
 */
function RevenueBySizeCode({
  rows, truncated, total,
}: { rows: SizeCodeRow[]; truncated: boolean; total: number }): ReactElement {
  const peak = Math.max(...rows.map((r) => r.sales_paise), 1);
  const grand = rows.reduce((sum, r) => sum + r.sales_paise, 0);

  return (
    <Card>
      <ChartHeading
        title="Revenue by size"
        hint={`${total} size${total === 1 ? "" : "s"} sold in this period`}
      />
      <ul className="mt-4 space-y-3">
        {rows.map((row) => (
          <li key={`${row.product_name}-${row.size_code}`}>
            <MetricBar
              label={`${row.product_name} · ${row.size_code}`}
              value={row.sales_display}
              share={row.sales_paise / peak}
              note={[
                row.size_meta,
                `${chartSharePct(row.sales_paise, grand)} of revenue`,
                `${row.units} pack${row.units === 1 ? "" : "s"}`,
                formatKg(row.grams),
                `${row.suppliers} supplier${row.suppliers === 1 ? "" : "s"}`,
              ]
                .filter(Boolean)
                .join(" · ")}
            />
          </li>
        ))}
      </ul>
      <p className="mt-3 text-xs text-primary-900/50">
        {truncated
          ? `Showing the top ${rows.length} of ${total}. Ungraded produce is not counted here.`
          : "Ungraded produce is not counted here."}
      </p>
    </Card>
  );
}

/** Basket shape and who was buying. */
function BasketAndCustomers({
  basket, customers,
}: { basket: Analytics["basket"]; customers: Analytics["customers"] }): ReactElement {
  return (
    <Card>
      <ChartHeading title="Basket and buyers" />
      <dl className="mt-4 space-y-3 text-sm">
        <Figure label="Average order" value={basket.average_order} />
        <Figure
          label="Packs per order"
          value={(basket.units_per_order_centi / 100).toFixed(2)}
        />
        <Figure label="Customers who bought" value={String(customers.total)} />
      </dl>

      {customers.total > 0 && (
        <div className="mt-4">
          <SplitBar
            segments={[
              { label: "New", value: customers.new, tone: "primary" },
              { label: "Returning", value: customers.returning, tone: "accent" },
            ]}
          />
          <p className="mt-2 text-xs text-primary-900/50">
            New means first paid order with us, not first in this period.
          </p>
        </div>
      )}
    </Card>
  );
}

/**
 * Which side of the cutoff the orders landed on.
 *
 * Everything after it waits for tomorrow's processing run (CLAUDE.md §6.1),
 * so a period that is mostly "after" is a period of later deliveries — and a
 * reason to push the cutoff in the customer's face earlier in the day.
 */
function CutoffSplit({ cutoff }: { cutoff: Analytics["cutoff"] }): ReactElement {
  const total = cutoff.before + cutoff.after;
  const hour = `${String(cutoff.hour_ist).padStart(2, "0")}:00`;

  return (
    <Card>
      <ChartHeading title={`Orders either side of ${hour}`} />
      {total === 0 ? (
        <p className="mt-4 text-sm text-primary-900/60">No paid orders to split.</p>
      ) : (
        <div className="mt-4">
          <SplitBar
            segments={[
              { label: `Before ${hour}`, value: cutoff.before, tone: "primary" },
              { label: `After ${hour}`, value: cutoff.after, tone: "accent" },
            ]}
          />
          <p className="mt-2 text-xs text-primary-900/50">
            Orders after {hour} IST are processed the next day, so they deliver
            a day later.
          </p>
        </div>
      )}
    </Card>
  );
}

/** Where the parcels went, from the address snapshot on each order. */
function Destinations({ rows }: { rows: DestinationRow[] }): ReactElement {
  const peak = Math.max(...rows.map((r) => r.orders), 1);

  return (
    <Card>
      <ChartHeading title="Where it went" />
      {rows.length === 0 ? (
        <p className="mt-4 text-sm text-primary-900/60">No deliveries in this period.</p>
      ) : (
        <ul className="mt-4 space-y-3">
          {rows.map((row) => (
            <li key={`${row.city}-${row.pincode}`}>
              <MetricBar
                label={`${row.city} ${row.pincode}`}
                value={`${row.orders} order${row.orders === 1 ? "" : "s"}`}
                share={row.orders / peak}
                note={row.gmv_display}
              />
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

/**
 * What became of the orders placed in the window.
 *
 * Only rendered when the period sold nothing — the question "where did they
 * go" only earns space on the screen when the answer is not "into revenue".
 * Amber carries its own meaning here: these are the orders that did not turn
 * into money, and they should not look like the ones that did.
 */
function Outcomes({ rows, total }: { rows: OutcomeRow[]; total: number }): ReactElement {
  if (rows.length === 0) {
    return <p className="mt-4 text-sm text-primary-900/60">No orders placed in this period.</p>;
  }

  return (
    <ul className="mt-4 space-y-3">
      {rows.map((row) => (
        <li key={row.status}>
          <MetricBar
            label={statusLabel(row.status)}
            value={`${row.orders} order${row.orders === 1 ? "" : "s"}`}
            share={total === 0 ? 0 : row.orders / total}
            note={`${chartSharePct(row.orders, total)} of orders placed · ${row.value_display}`}
            tone={CONVERTED.has(row.status) ? "primary" : "accent"}
          />
        </li>
      ))}
    </ul>
  );
}

/** The statuses that became money. Everything else is a leak. */
const CONVERTED = new Set(["paid", "processed", "dispatched"]);

function Figure({ label, value }: { label: string; value: string }): ReactElement {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <dt className="text-primary-900/70">{label}</dt>
      <dd className="font-medium tabular-nums text-primary-900">{value}</dd>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Formatting                                                                 */
/* -------------------------------------------------------------------------- */

/** "2026-08-18" -> "18 Aug". The server sends ISO dates already in IST, so
 *  this only ever reformats — it never converts a timezone. */
function shortDate(iso: string): string {
  const [, month, day] = iso.split("-");
  const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun",
                  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  return `${Number(day)} ${months[Number(month) - 1] ?? ""}`.trim();
}

/**
 * Rupees from paise, for the ONE figure the server does not send formatted:
 * the week's total, which is a sum of days the screen made itself.
 *
 * Integer paise in, string out — the arithmetic never leaves integers, exactly
 * as on the server (CLAUDE.md rule 1). Everything else on this screen prints a
 * display string the API already produced.
 */
function formatRupeesFromPaise(paise: number): string {
  const rupees = Math.trunc(paise / 100);
  const remainder = Math.abs(paise % 100);
  return `₹${rupees.toLocaleString("en-IN")}.${String(remainder).padStart(2, "0")}`;
}

function formatKg(grams: number): string {
  if (grams < 1000) return `${grams} g`;
  return `${(grams / 1000).toFixed(grams % 1000 === 0 ? 0 : 1)} kg`;
}

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    pending_payment: "Awaiting payment",
    paid: "Paid",
    processed: "Processed",
    dispatched: "Dispatched",
    payment_failed: "Payment failed",
    expired: "Expired before payment",
    cancelled: "Cancelled",
    refunded: "Refunded",
  };
  return labels[status] ?? status;
}

import { useState, type ReactElement } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Card, PageHeading, ApiError, RangePicker,
} from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { DashboardAnalytics } from "./DashboardAnalytics.js";

interface DashboardRange {
  key: string;
  label: string;
  from: string;
  to: string;
  days: number;
}

interface Dashboard {
  date: string;
  range: DashboardRange;
  orders_today: number;
  gmv_today: string;
  platform_fees_today: string;
  earnings_today: string;
  earnings_breakdown: {
    platform_fee: string;
    platform_fee_rate: string;
    supplier_commission: string;
    supplier_commission_default_rate: string;
    supplier_gross_paise: number;
    supplier_net_paise: number;
    delivery_margin: string;
    delivery_margin_per_order: string;
    paid_orders: number;
    /** What we added to growers' prices on the orders in this period. */
    markup: string;
    markup_paise: number;
  };
  outstanding_to_suppliers: string;
  awaiting_processing: number;
  pending_supplier_applications: number;
}


/**
 * Operations overview.
 *
 * Every period is resolved server-side in IST — the browser sends a period
 * NAME, never its own idea of what today is (CLAUDE.md rule 2). A laptop with
 * a wrong clock changes nothing about the figures it sees.
 */
export function DashboardPage(): ReactElement {
  const [range, setRange] = useState("today");
  // Only sent when range === "custom"; kept in state so switching away and
  // back does not lose what was typed.
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  const customIncomplete = range === "custom" && (!from || !to);

  const dashboard = useQuery<Dashboard, ApiError>({
    queryKey: ["dashboard", range, from, to],
    queryFn: () =>
      api.get<Dashboard>("/admin/dashboard", {
        query:
          range === "custom" ? { range, from, to } : { range },
      }),
    // Not fetched until both custom dates are filled in — asking with half a
    // range only produces an error banner the operator already knows about.
    enabled: !customIncomplete,
    // Trading figures move through the day; a stale number here misleads.
    refetchInterval: 60_000,
  });

  const picker = (
    <RangePicker
      range={range}
      from={from}
      to={to}
      onRange={setRange}
      onFrom={setFrom}
      onTo={setTo}
    />
  );

  if (customIncomplete) {
    return (
      <>
        <PageHeading
          title="Operations overview"
          description="Choose the start and end of the period."
          actions={picker}
        />
        <Card>
          <p className="text-primary-900/70">
            Pick both dates to see figures for a custom period.
          </p>
        </Card>
      </>
    );
  }

  if (dashboard.isPending) return <Card>Loading figures…</Card>;
  if (dashboard.isError) {
    return (
      <Card>
        <p role="alert" className="text-sm text-accent-800">{dashboard.error.message}</p>
      </Card>
    );
  }

  const d = dashboard.data;

  return (
    <>
      <PageHeading
        title="Operations overview"
        description={
          d.range.days === 1
            ? `Trading figures for ${d.range.to} (IST).`
            : `Trading figures for ${d.range.from} to ${d.range.to} (IST) — ${d.range.days} days.`
        }
        actions={picker}
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Stat label={`Orders ${periodWord(d.range)}`} value={String(d.orders_today)} />
        {/* GMV counts paid orders only — a pending_payment order is not revenue. */}
        <Stat label={`GMV ${periodWord(d.range)}`} value={d.gmv_today} hint="Paid orders only" />
        <EarningsStat
          value={d.earnings_today}
          breakdown={d.earnings_breakdown}
          label={periodWord(d.range)}
        />
        <Stat
          label="Owed to suppliers"
          value={d.outstanding_to_suppliers}
          hint="Across all unpaid payouts"
          to="/payouts"
          cta="Settle"
        />
        <Stat
          label="Awaiting processing"
          value={String(d.awaiting_processing)}
          hint="Paid, not yet dispatched"
          to="/orders?status=paid"
          cta="View orders"
        />
        <Stat
          label="Supplier applications"
          value={String(d.pending_supplier_applications)}
          hint="Waiting for review"
          to="/suppliers"
          cta="Review"
          highlight={d.pending_supplier_applications > 0}
        />
      </div>

      {/* Fetched separately and given the SAME period the tiles were resolved
          for. Passing the selection rather than the resolved range keeps the
          server the only thing that decides what "past 7 days" means. */}
      <DashboardAnalytics range={range} from={from} to={to} />
    </>
  );
}

function Stat({
  label, value, hint, to, cta, highlight,
}: {
  label: string; value: string; hint?: string;
  to?: string; cta?: string; highlight?: boolean;
}): ReactElement {
  return (
    <Card className={highlight ? "border-accent-300 bg-accent-50" : ""}>
      <p className="text-sm text-primary-900/60">{label}</p>
      <p className="mt-1 text-2xl font-semibold tabular-nums text-primary-900">{value}</p>
      {hint && <p className="mt-0.5 text-xs text-primary-900/50">{hint}</p>}
      {to && cta && (
        <Link to={to} className="mt-3 inline-block text-sm font-medium text-primary-700 underline">
          {cta} →
        </Link>
      )}
    </Card>
  );
}


/**
 * Total earnings, with the breakdown behind a disclosure.
 *
 * The headline is one number because that is what gets checked daily. The
 * split is one click away because the three sources behave differently — two
 * scale with basket size, one is flat per order — and "earnings are up" means
 * something different depending on which moved.
 */
function EarningsStat({
  value,
  breakdown,
  label,
}: {
  value: string;
  breakdown: Dashboard["earnings_breakdown"];
  label: string;
}): ReactElement {
  const [open, setOpen] = useState(false);

  return (
    <Card>
      <p className="text-xs uppercase tracking-wide text-primary-900/60">
        Total earnings {label}
      </p>
      <p className="mt-1 text-2xl font-semibold tabular-nums text-primary-900">{value}</p>

      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-expanded={open}
        className="mt-1 text-xs font-medium text-primary-700 underline underline-offset-2"
      >
        {open ? "Hide breakdown" : "View breakdown"}
      </button>

      {open && (
        <dl className="mt-3 space-y-1.5 border-t border-surface-border pt-3 text-sm">
          <Row
            label={`Platform fee (${breakdown.platform_fee_rate})`}
            note="charged to the customer, on top of the subtotal"
            value={breakdown.platform_fee}
          />
          {/* No single percentage in the label. Suppliers can be on
              negotiated rates, so quoting one rate beside a total collected
              across several would be stating something untrue — the default
              is named as the default, and nothing more is claimed. */}
          <Row
            label="Supplier commission"
            note={`kept from grower sales · default ${breakdown.supplier_commission_default_rate}`}
            value={breakdown.supplier_commission}
          />
          <Row
            label="Delivery margin"
            note={`${breakdown.delivery_margin_per_order} per paid order × ${breakdown.paid_orders}`}
            value={breakdown.delivery_margin}
          />
          {/* No rate in the label, and deliberately: markup is set per
              product, so there is no single figure that describes a total
              collected across many of them. */}
          <Row
            label="Markup revenue"
            note="added to growers' prices, per pack sold"
            value={breakdown.markup}
          />
        </dl>
      )}
    </Card>
  );
}

function Row({
  label,
  note,
  value,
}: {
  label: string;
  note: string;
  value: string;
}): ReactElement {
  return (
    <div className="flex justify-between gap-3">
      <dt className="min-w-0">
        <span className="block text-primary-900">{label}</span>
        <span className="block text-xs text-primary-900/55">{note}</span>
      </dt>
      <dd className="shrink-0 font-medium tabular-nums text-primary-900">{value}</dd>
    </div>
  );
}


/**
 * How a period reads inside a tile label: "Orders today", "Orders (7 days)".
 *
 * Tile labels must follow the selection or the screen lies — "Orders today"
 * above a month's worth of orders is worse than no label at all.
 */
function periodWord(range: DashboardRange): string {
  if (range.days === 1) return "today";
  if (range.key === "custom") return `(${range.days} days)`;
  return `(${range.label.replace(/^Past /, "past ")})`;
}

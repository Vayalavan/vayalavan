/**
 * The grower's own trend screen: what is selling, in which packs, and whether
 * it is going up.
 *
 * A separate tab from Sales rather than more of it, because the two answer
 * different questions and want different defaults. Sales is a reconciliation
 * document — every order, all time, checked against a bank statement. This is
 * a period view that defaults to the past week, because a trend needs a
 * window, and the two defaults cannot live on one screen.
 *
 * Every figure is the GROWER's own money: their listed price, with our markup
 * subtracted, which is what a payout pays them. The same subtraction the sales
 * screen makes, so the two can never quote different totals for one period.
 *
 * The charts come from vm-ui-kit, shared with the admin console, so a fix
 * lands on both. There is exactly ONE time chart: a fixed-week panel used to
 * sit under it, and with the picker defaulting to the past week it was mostly
 * the same line drawn twice.
 */
import { useState, type ReactElement } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Card, PageHeading, RangePicker, ChartHeading, LineChart,
  MetricBar, SplitBar, chartSharePct, type ApiError, type SeriesPoint,
} from "@vayal/ui-kit";
import { api } from "../lib/api.js";

interface DayRow {
  date: string;
  orders: number;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
}

interface ProduceRow {
  product_name: string;
  grade: string | null;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
}

/**
 * One GRADE of one produce: "Pomegranate XL2".
 *
 * Named by both, because a grower's M2 pomegranate and M2 tomato are different
 * crates and a row reading "M2" alone would be summing two of them.
 */
interface SizeCodeRow {
  product_name: string;
  size_code: string;
  /** The grower's own words for the grade — "250 g - 300 g", or "". */
  size_meta: string;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
  orders: number;
}

interface PackRow {
  unit_label: string;
  weight_grams: number;
  units: number;
  grams: number;
  amount_paise: number;
  amount_display: string;
  orders: number;
}

interface Analytics {
  range: { key: string; label: string; all_time: boolean; from?: string; to?: string };
  summary: {
    orders: number;
    gross_paise: number;
    gross: string;
    pending_paise: number;
    pending: string;
    settled_paise: number;
    settled: string;
    average_order_paise: number;
    average_order: string;
    units: number;
    grams: number;
  };
  daily: DayRow[];
  by_produce: ProduceRow[];
  by_produce_total: number;
  by_produce_truncated: boolean;
  by_pack: PackRow[];
  by_pack_total: number;
  by_pack_truncated: boolean;
  /** Graded lines only. Empty for a grower who does not grade. */
  by_size_code: SizeCodeRow[];
  by_size_code_total: number;
  by_size_code_truncated: boolean;
}

export function AnalyticsPage(): ReactElement {
  // The past week, which is the shape this screen exists to show. The server
  // resolves it — and every other period — in IST from its own clock, so a
  // laptop with a wrong date sees the same week as everyone else
  // (CLAUDE.md rule 2).
  const [range, setRange] = useState("7d");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  const custom = range === "custom";
  const incomplete = custom && (from === "" || to === "");

  const analytics = useQuery<Analytics, ApiError>({
    queryKey: ["supplier-analytics", range, from, to],
    enabled: !incomplete,
    queryFn: () =>
      api.get<Analytics>("/supplier/analytics", {
        query: custom ? { range, from, to } : { range },
      }),
    staleTime: 30_000,
    placeholderData: (previous) => previous,
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

  if (incomplete) {
    return (
      <>
        <PageHeading
          title="Insights"
          description="Choose the start and end of the period."
          actions={picker}
        />
        <Card>
          <p className="text-sm text-primary-900/70">
            Pick both dates to see figures for a custom period.
          </p>
        </Card>
      </>
    );
  }

  if (analytics.isPending) {
    return (
      <>
        <PageHeading title="Insights" actions={picker} />
        <Card>
          <p className="text-sm text-primary-900/60">Loading your figures…</p>
        </Card>
      </>
    );
  }

  if (analytics.isError) {
    return (
      <>
        <PageHeading title="Insights" actions={picker} />
        <Card>
          <p role="alert" className="text-sm text-accent-800">
            {analytics.error.message}
          </p>
        </Card>
      </>
    );
  }

  const a = analytics.data;
  const sold = a.summary.orders > 0;

  return (
    <>
      <PageHeading
        title="Insights"
        description={`What sold ${a.range.label.toLowerCase()}, and how it is moving. Amounts are yours — our markup is already taken out.`}
        actions={picker}
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Your earnings" value={a.summary.gross} hint={`${a.summary.orders} orders`} />
        <Stat label="Average order" value={a.summary.average_order} hint="Your share per order" />
        <Stat label="Packs sold" value={String(a.summary.units)} hint={formatKg(a.summary.grams)} />
        <Stat
          label="Awaiting settlement"
          value={a.summary.pending}
          hint={`${a.summary.settled} already paid`}
        />
      </div>

      {!sold ? (
        <Card className="mt-6">
          <p className="text-sm text-primary-900/70">
            Nothing sold in this period. Declare today's availability and your
            produce goes back on the storefront.
          </p>
        </Card>
      ) : (
        <div className="mt-6 space-y-6">
          {/* First, above the charts: what a grower opens this app to check is
              whether they have been paid. The trends are what they stay for. */}
          <Card>
            <ChartHeading
              title="Settlement"
              hint="Paid by NEFT, marked off by the Vayalavan team"
            />
            <div className="mt-4">
              {/* Sized by paise, labelled in rupees: the bar has to be drawn
                  from the integer amount, and nobody should ever read one. */}
              <SplitBar
                segments={[
                  {
                    label: "Awaiting payment",
                    value: a.summary.pending_paise,
                    display: a.summary.pending,
                    tone: "accent",
                  },
                  {
                    label: "Already settled",
                    value: a.summary.settled_paise,
                    display: a.summary.settled,
                    tone: "primary",
                  },
                ]}
              />
              <p className="mt-2 text-xs text-primary-900/50">
                Settlement is a manual NEFT transfer. This is the same figure
                the Vayalavan team settles against.
              </p>
            </div>
          </Card>

          {a.daily.length > 1 && (
            <Card>
              <ChartHeading
                title="Your sales by day"
                hint={peakHint(a.daily)}
              />
              <LineChart
                points={a.daily.map(toPoint)}
                ariaLabel="Your earnings for each day in the selected period. The table below carries the figures."
                tableCaption="Your sales by day"
                valueHeading="Your earnings"
              />
            </Card>
          )}

          <div className="grid gap-6 lg:grid-cols-2">
            <ByProduce
              rows={a.by_produce}
              total={a.by_produce_total}
              truncated={a.by_produce_truncated}
            />
            <ByPack
              rows={a.by_pack}
              total={a.by_pack_total}
              truncated={a.by_pack_truncated}
            />
          </div>

          {/* Only for growers who actually grade. A single-grade listing has
              nothing to compare, and an empty card would be a question mark
              on the screen of someone this does not apply to. */}
          {a.by_size_code.length > 0 && (
            <BySizeCode
              rows={a.by_size_code}
              total={a.by_size_code_total}
              truncated={a.by_size_code_truncated}
            />
          )}
        </div>
      )}
    </>
  );
}

/** One day as the charts want it. */
function toPoint(day: DayRow): SeriesPoint {
  return {
    key: day.date,
    label: shortDate(day.date),
    value: day.amount_paise,
    display: day.amount_display,
    note: `${day.units} pack${day.units === 1 ? "" : "s"} · ${day.orders} order${
      day.orders === 1 ? "" : "s"
    }`,
  };
}

function peakHint(days: DayRow[]): string {
  const first = days[0];
  if (!first) return "";
  const best = days.reduce((a, b) => (b.amount_paise > a.amount_paise ? b : a), first);
  return `Best day ${best.amount_display} on ${shortDate(best.date)}`;
}

/** What sold, by produce. */
function ByProduce({
  rows, total, truncated,
}: { rows: ProduceRow[]; total: number; truncated: boolean }): ReactElement {
  const peak = Math.max(...rows.map((r) => r.amount_paise), 1);
  const grand = rows.reduce((sum, r) => sum + r.amount_paise, 0);

  return (
    <Card>
      <ChartHeading
        title="What sold"
        hint={`${total} product${total === 1 ? "" : "s"}`}
      />
      {rows.length === 0 ? (
        <p className="mt-4 text-sm text-primary-900/60">Nothing sold in this period.</p>
      ) : (
        <ul className="mt-4 space-y-3">
          {rows.map((row) => (
            <li key={`${row.product_name}-${row.grade ?? ""}`}>
              <MetricBar
                label={row.grade ? `${row.product_name} · ${row.grade}` : row.product_name}
                value={row.amount_display}
                share={row.amount_paise / peak}
                note={`${chartSharePct(row.amount_paise, grand)} of your earnings · ${
                  row.units
                } pack${row.units === 1 ? "" : "s"} · ${formatKg(row.grams)}`}
              />
            </li>
          ))}
        </ul>
      )}
      {truncated && (
        <p className="mt-3 text-xs text-primary-900/50">
          Showing your top {rows.length} of {total}.
        </p>
      )}
    </Card>
  );
}

/**
 * Which pack SIZES sell.
 *
 * The breakdown a grower cannot get anywhere else: units are theirs to choose
 * (CLAUDE.md §5.2), and nothing else on the platform says whether the 10 kg is
 * worth listing.
 */
function ByPack({
  rows, total, truncated,
}: { rows: PackRow[]; total: number; truncated: boolean }): ReactElement {
  const peak = Math.max(...rows.map((r) => r.amount_paise), 1);
  const grand = rows.reduce((sum, r) => sum + r.amount_paise, 0);

  return (
    <Card>
      <ChartHeading
        title="Which packs sell"
        hint={`${total} pack size${total === 1 ? "" : "s"}`}
      />
      {rows.length === 0 ? (
        <p className="mt-4 text-sm text-primary-900/60">No packs sold in this period.</p>
      ) : (
        <ul className="mt-4 space-y-3">
          {rows.map((row) => (
            <li key={`${row.unit_label}-${row.weight_grams}`}>
              <MetricBar
                label={row.unit_label}
                value={row.amount_display}
                share={row.amount_paise / peak}
                note={`${chartSharePct(row.amount_paise, grand)} of your earnings · ${
                  row.units
                } sold · ${formatKg(row.grams)}`}
              />
            </li>
          ))}
        </ul>
      )}
      {truncated && (
        <p className="mt-3 text-xs text-primary-900/50">
          Showing your top {rows.length} of {total}.
        </p>
      )}
    </Card>
  );
}

/**
 * Which GRADES sell.
 *
 * The decision this answers is next season's: a grower picking eleven grades
 * of pomegranate has to know which crates paid for the picking. "What sold"
 * cannot say — it sums every grade of a produce into one row — and "which
 * packs sell" is about box sizes, which are the same across grades.
 */
function BySizeCode({
  rows, total, truncated,
}: { rows: SizeCodeRow[]; total: number; truncated: boolean }): ReactElement {
  const peak = Math.max(...rows.map((r) => r.amount_paise), 1);
  const grand = rows.reduce((sum, r) => sum + r.amount_paise, 0);

  return (
    <Card>
      <ChartHeading
        title="Which sizes sell"
        hint={`${total} size${total === 1 ? "" : "s"} sold`}
      />
      <ul className="mt-4 space-y-3">
        {rows.map((row) => (
          <li key={`${row.product_name}-${row.size_code}`}>
            <MetricBar
              label={`${row.product_name} · ${row.size_code}`}
              value={row.amount_display}
              share={row.amount_paise / peak}
              note={`${row.size_meta ? `${row.size_meta} · ` : ""}${chartSharePct(
                row.amount_paise,
                grand,
              )} of your earnings · ${row.units} pack${
                row.units === 1 ? "" : "s"
              } · ${formatKg(row.grams)}`}
            />
          </li>
        ))}
      </ul>
      <p className="mt-3 text-xs text-primary-900/50">
        {truncated
          ? `Showing your top ${rows.length} of ${total}. Ungraded produce is not counted here.`
          : "Ungraded produce is not counted here."}
      </p>
    </Card>
  );
}

function Stat({
  label, value, hint,
}: { label: string; value: string; hint?: string }): ReactElement {
  return (
    <Card>
      <p className="text-sm text-primary-900/60">{label}</p>
      <p className="mt-1 text-2xl font-semibold tabular-nums text-primary-900">{value}</p>
      {hint && <p className="mt-0.5 text-xs text-primary-900/50">{hint}</p>}
    </Card>
  );
}

/** "2026-08-18" -> "18 Aug". Server dates are already IST; this reformats. */
function shortDate(iso: string): string {
  const [, month, day] = iso.split("-");
  const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun",
                  "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  return `${Number(day)} ${months[Number(month) - 1] ?? ""}`.trim();
}

/** Grams as a grower thinks of them — kilos above 1 kg. */
function formatKg(grams: number): string {
  if (grams < 1000) return `${grams} g`;
  const kg = grams / 1000;
  return `${Number.isInteger(kg) ? kg : kg.toFixed(1)} kg`;
}

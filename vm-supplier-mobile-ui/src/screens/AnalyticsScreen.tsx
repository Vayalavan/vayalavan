/**
 * The grower's own trend screen: what is selling, in which packs, and whether
 * it is going up.
 *
 * A separate tab from Sales rather than more of it, exactly as on the web. The
 * two answer different questions and want different defaults — Sales is a
 * reconciliation document covering all time, this is a period view that opens
 * on the past week, and one screen cannot default to both.
 *
 * At parity with vm-supplier-ui's Insights page, down to the chart forms. Only
 * the geometry around them differs — cards stacked in one column instead of
 * side by side, because a phone has one column.
 */
import { useState, type ReactElement } from "react";
import { View } from "react-native";
import { useQuery } from "@tanstack/react-query";

import { Card } from "../components/Card";
import { LineChart, MetricBar, SplitBar, type SeriesPoint } from "../components/Charts";
import { EmptyState, ErrorState, Loading } from "../components/Feedback";
import { RangeFilter } from "../components/RangeFilter";
import { PageHeading, Screen } from "../components/Screen";
import { Text } from "../components/Text";
import { formatKg, peakOf, sharePct, shortDate } from "../lib/analytics";
import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import { rangeQueryFor, TREND_RANGES } from "../lib/salesRange";
import type { SupplierAnalytics, SupplierAnalyticsDay } from "../lib/types";
import { spacing } from "../theme/tokens";

export function AnalyticsScreen(): ReactElement {
  // The past week, which is the shape this screen exists to show. The server
  // resolves it — and every other period — in IST from its own clock, so a
  // handset with a wrong date sees the same week as everyone else
  // (CLAUDE.md rule 2).
  const [range, setRange] = useState("7d");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  const rangeQuery = rangeQueryFor(range, from, to);

  const analytics = useQuery<SupplierAnalytics, ApiError>({
    queryKey: ["supplier-analytics", rangeQuery],
    enabled: rangeQuery !== null,
    queryFn: () => api.get<SupplierAnalytics>("/supplier/analytics", { query: rangeQuery ?? {} }),
    staleTime: 30_000,
    placeholderData: (previous) => previous,
  });

  const filter = (
    <RangeFilter
      range={range}
      from={from}
      to={to}
      onRange={setRange}
      onFrom={setFrom}
      onTo={setTo}
      // Without all-time: this endpoint refuses it, and offering a chip that
      // returns a 400 is how the screen came to greet a grower with
      // "range must be one of…".
      options={TREND_RANGES}
    />
  );

  // A disabled query stays "pending" forever, so a half-typed custom range has
  // to say what it is waiting for rather than spin.
  if (rangeQuery === null) {
    return (
      <Screen>
        {filter}
        <Card>
          <Text tone="muted">Enter a start and end date to see that period.</Text>
        </Card>
      </Screen>
    );
  }

  if (analytics.isPending) {
    return (
      <Screen>
        {filter}
        <Loading label="Loading your figures…" />
      </Screen>
    );
  }

  if (analytics.isError) {
    return (
      <Screen>
        {filter}
        <ErrorState error={analytics.error} onRetry={() => void analytics.refetch()} />
      </Screen>
    );
  }

  const a = analytics.data;

  return (
    <Screen>
      <PageHeading
        title="Insights"
        description="Amounts are yours — our markup is already taken out."
      />
      {filter}

      <View style={{ gap: spacing.md }}>
        <Card>
          <View style={{ gap: spacing.md }}>
            <Figure label="Your earnings" value={a.summary.gross} hint={`${a.summary.orders} order${a.summary.orders === 1 ? "" : "s"}`} />
            <Figure label="Average order" value={a.summary.average_order} hint="Your share per order" />
            <Figure
              label="Packs sold"
              value={String(a.summary.units)}
              hint={formatKg(a.summary.grams)}
            />
          </View>
        </Card>

        {a.summary.orders === 0 ? (
          <EmptyState
            title="Nothing sold in this period"
            body="Declare today's availability and your produce goes back on the storefront."
          />
        ) : (
          <>
            {/* First, above the charts: what a grower opens this app to check
                is whether they have been paid. The trends are what they stay
                for. Same order as the web's Insights page. */}
            <Card>
              <View style={{ gap: spacing.md }}>
                <SectionTitle title="Settlement" hint="Paid by NEFT by the Vayalavan team" />
                <SplitBar
                  // Sized by paise, labelled in rupees: the bar has to be
                  // drawn from the integer amount, and nobody should read one.
                  segments={[
                    {
                      label: "Awaiting",
                      value: a.summary.pending_paise,
                      display: a.summary.pending,
                      tone: "accent",
                    },
                    {
                      label: "Settled",
                      value: a.summary.settled_paise,
                      display: a.summary.settled,
                      tone: "primary",
                    },
                  ]}
                />
              </View>
            </Card>

            {a.daily.length > 1 && (
              <Card>
                <View style={{ gap: spacing.md }}>
                  <SectionTitle title="Your sales by day" hint={peakHint(a.daily)} />
                  <LineChart
                    points={a.daily.map(toPoint)}
                    accessibilityLabel={`Your earnings for each day from ${shortDate(
                      a.daily[0]!.date,
                    )} to ${shortDate(a.daily[a.daily.length - 1]!.date)}.`}
                  />
                </View>
              </Card>
            )}

            <Card>
              <View style={{ gap: spacing.md }}>
                <SectionTitle
                  title="What sold"
                  hint={`${a.by_produce_total} product${
                    a.by_produce_total === 1 ? "" : "s"
                  }`}
                />
                {a.by_produce.map((row) => (
                  <MetricBar
                    key={`${row.product_name}-${row.grade ?? ""}`}
                    label={row.grade ? `${row.product_name} · ${row.grade}` : row.product_name}
                    value={row.amount_display}
                    share={row.amount_paise / peakOf(a.by_produce.map((p) => p.amount_paise))}
                    note={`${sharePct(
                      row.amount_paise,
                      a.by_produce.reduce((sum, p) => sum + p.amount_paise, 0),
                    )} of your earnings · ${row.units} pack${
                      row.units === 1 ? "" : "s"
                    } · ${formatKg(row.grams)}`}
                  />
                ))}
                {a.by_produce_truncated && (
                  <Text variant="caption" tone="faint">
                    Showing your top {a.by_produce.length} of {a.by_produce_total}.
                  </Text>
                )}
              </View>
            </Card>

            <Card>
              <View style={{ gap: spacing.md }}>
                {/* The breakdown a grower cannot get anywhere else: pack sizes
                    are theirs to choose (CLAUDE.md §5.2), and nothing else on
                    the platform says whether the 10 kg is worth listing. */}
                <SectionTitle
                  title="Which packs sell"
                  hint={`${a.by_pack_total} pack size${a.by_pack_total === 1 ? "" : "s"}`}
                />
                {a.by_pack.map((row) => (
                  <MetricBar
                    key={`${row.unit_label}-${row.weight_grams}`}
                    label={row.unit_label}
                    value={row.amount_display}
                    share={row.amount_paise / peakOf(a.by_pack.map((p) => p.amount_paise))}
                    note={`${sharePct(
                      row.amount_paise,
                      a.by_pack.reduce((sum, p) => sum + p.amount_paise, 0),
                    )} of your earnings · ${row.units} sold · ${formatKg(row.grams)}`}
                  />
                ))}
                {a.by_pack_truncated && (
                  <Text variant="caption" tone="faint">
                    Showing your top {a.by_pack.length} of {a.by_pack_total}.
                  </Text>
                )}
              </View>
            </Card>

            {/* Only for growers who actually grade. A single-grade listing has
                nothing to compare, and an empty card would be a question mark
                on the screen of someone this does not apply to. */}
            {a.by_size_code.length > 0 && (
              <Card>
                <View style={{ gap: spacing.md }}>
                  {/* The decision this answers is next season's: a grower
                      picking eleven grades of pomegranate has to know which
                      crates paid for the picking. "What sold" sums every grade
                      of a produce into one row, and pack sizes are the same
                      across grades. */}
                  <SectionTitle
                    title="Which sizes sell"
                    hint={`${a.by_size_code_total} size${
                      a.by_size_code_total === 1 ? "" : "s"
                    } sold`}
                  />
                  {a.by_size_code.map((row) => (
                    <MetricBar
                      key={`${row.product_name}-${row.size_code}`}
                      label={`${row.product_name} · ${row.size_code}`}
                      value={row.amount_display}
                      share={
                        row.amount_paise / peakOf(a.by_size_code.map((p) => p.amount_paise))
                      }
                      note={`${row.size_meta !== "" ? `${row.size_meta} · ` : ""}${sharePct(
                        row.amount_paise,
                        a.by_size_code.reduce((sum, p) => sum + p.amount_paise, 0),
                      )} of your earnings · ${row.units} pack${
                        row.units === 1 ? "" : "s"
                      } · ${formatKg(row.grams)}`}
                    />
                  ))}
                  <Text variant="caption" tone="faint">
                    {a.by_size_code_truncated
                      ? `Showing your top ${a.by_size_code.length} of ${a.by_size_code_total}. Ungraded produce is not counted here.`
                      : "Ungraded produce is not counted here."}
                  </Text>
                </View>
              </Card>
            )}
          </>
        )}
      </View>
    </Screen>
  );
}

/** One day as the charts want it, including what a tap on it should say. */
function toPoint(day: SupplierAnalyticsDay): SeriesPoint {
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

function peakHint(days: SupplierAnalyticsDay[]): string {
  const first = days[0];
  if (first === undefined) return "";
  const best = days.reduce((a, b) => (b.amount_paise > a.amount_paise ? b : a), first);
  return `Best day ${best.amount_display} on ${shortDate(best.date)}`;
}

function SectionTitle({ title, hint }: { title: string; hint?: string }): ReactElement {
  return (
    <View style={{ gap: spacing.xs }}>
      <Text variant="bodyStrong" tone="strong">
        {title}
      </Text>
      {hint !== undefined && hint !== "" && (
        <Text variant="caption" tone="muted">
          {hint}
        </Text>
      )}
    </View>
  );
}

function Figure({
  label, value, hint,
}: { label: string; value: string; hint?: string }): ReactElement {
  return (
    <View style={{ flexDirection: "row", alignItems: "baseline", gap: spacing.sm }}>
      <View style={{ flex: 1 }}>
        <Text tone="muted">{label}</Text>
        {hint !== undefined && (
          <Text variant="caption" tone="faint">
            {hint}
          </Text>
        )}
      </View>
      <Text variant="title" tone="strong" tabular>
        {value}
      </Text>
    </View>
  );
}

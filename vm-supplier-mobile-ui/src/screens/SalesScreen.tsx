/**
 * The grower's own record of what they have sold and what they are owed.
 *
 * Settlement is a manual NEFT transfer decided on the admin's screen, so this
 * is the supplier's only way to check that figure independently. Both screens
 * read the same supplier_payouts rows, which is what makes them agree: if this
 * says ₹4,460 outstanding, the admin's settlement screen says the same, because
 * it is the same row and not a second calculation.
 *
 * The web version is two wide tables. Tables do not survive a 360px screen —
 * a horizontal scroll inside a vertical scroll is miserable to use one-handed —
 * so the same data is laid out as cards and disclosure rows here. The figures
 * and the wording are unchanged; only the geometry is.
 */
import { useState, type ReactElement } from "react";
import { Pressable, View } from "react-native";
import { useQuery } from "@tanstack/react-query";

import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Badge, EmptyState, ErrorState, Loading } from "../components/Feedback";
import { RangeFilter } from "../components/RangeFilter";
import { PageHeading, Screen } from "../components/Screen";
import { Text } from "../components/Text";
import type { ApiError } from "../lib/api";
import { api } from "../lib/client";
import { formatISTDate, formatISTDateTime } from "../lib/datetime";
import { formatGrams } from "../lib/money";
import { rangeQueryFor } from "../lib/salesRange";
import type { Sales, SupplierOrder } from "../lib/types";
import { gradeLabel } from "../lib/grade";
import { colors, spacing } from "../theme/tokens";

/** Rows per page in the orders list. */
const PAGE_SIZE = 20;

export function SalesScreen(): ReactElement {
  const [page, setPage] = useState(0);
  // All time, which is what this screen showed before the filter existed and
  // is the figure a grower reconciles a bank statement against.
  const [range, setRange] = useState("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  const rangeQuery = rangeQueryFor(range, from, to);

  function changeRange(next: string): void {
    setRange(next);
    // A new period is a different set of orders; staying on page 4 of the old
    // one would land on an empty list.
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
    // Keeps the previous page on screen while the next loads, so the list does
    // not collapse to a spinner and jump the scroll position on every tap.
    placeholderData: (previous) => previous,
  });

  const filter = (
    <RangeFilter
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

  if (sales.isPending) return <Loading label="Loading your sales…" />;
  if (sales.isError) {
    return (
      <Screen>
        <ErrorState error={sales.error} onRetry={() => void sales.refetch()} />
      </Screen>
    );
  }

  const { summary, products, orders, charges } = sales.data;
  const total = sales.data.orders_total;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <Screen onRefresh={() => void sales.refetch()} refreshing={sales.isRefetching}>
      <PageHeading
        title="Sales and settlement"
        description="Everything you have sold, and what we still owe you. These are the same figures our settlement screen pays against."
      />

      {filter}

      {/* Which period the numbers below belong to. Without this, a filter left
          on last week looks exactly like a bad week. */}
      <Text variant="caption" tone="muted">
        Showing {sales.data.range.label}
        {sales.data.range.all_time
          ? " — every order you have ever had."
          : ` (${sales.data.range.from} to ${sales.data.range.to}).`}
      </Text>

      <SummaryTile
        label="Awaiting payment to you"
        value={summary.pending_display}
        note="Paid by NEFT after settlement"
        emphasis
      />
      <View style={{ flexDirection: "row", gap: spacing.md }}>
        <View style={{ flex: 1 }}>
          <SummaryTile
            label={sales.data.range.all_time ? "Sold to date" : "Sold in period"}
            value={summary.gross_display}
            note={`${summary.order_count} order${summary.order_count === 1 ? "" : "s"}`}
          />
        </View>
        <View style={{ flex: 1 }}>
          <SummaryTile
            label="Already settled"
            value={summary.settled_display}
            note="Transferred to your bank"
          />
        </View>
      </View>

      {/* The deduction, stated before a grower has to work it out from a bank
          statement. Showing gross, commission and net together is the whole
          point: "3% is charged" alone still leaves them doing arithmetic. */}
      <Card>
        <View style={{ gap: spacing.md }}>
          <Text variant="heading" tone="strong">
            What you are paid
          </Text>

          <View style={{ gap: spacing.sm }}>
            <AmountRow label="Your produce sold" value={charges.gross_display} />
            <AmountRow
              label={`Vayalavan commission (${charges.commission_rate})`}
              value={`− ${charges.commission_display}`}
              tone="danger"
            />
            <View
              style={{
                borderTopWidth: 1,
                borderTopColor: colors.surface.border,
                paddingTop: spacing.sm,
              }}
            >
              <AmountRow label="You receive" value={charges.net_display} strong />
            </View>
          </View>

          <Text variant="caption" tone="muted">
            The commission of {charges.commission_rate} covers listing and selling your
            produce. The delivery charge and platform fee the customer pays are separate
            and are not taken from you — every figure on this page is already net of
            commission.
          </Text>
        </View>
      </Card>

      <Text variant="title" tone="strong" accessibilityRole="header">
        By produce
      </Text>

      {products.length === 0 ? (
        <EmptyState
          title="Nothing sold yet"
          body="Once customers buy today's produce it will appear here."
        />
      ) : (
        <Card>
          <View style={{ gap: spacing.md }}>
            {products.map((row, index) => (
              <View
                key={`${row.product_name}-${row.grade ?? ""}`}
                style={{
                  gap: spacing.xs,
                  ...(index > 0 && {
                    borderTopWidth: 1,
                    borderTopColor: colors.surface.border,
                    paddingTop: spacing.md,
                  }),
                }}
              >
                <View
                  style={{
                    flexDirection: "row",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: spacing.md,
                  }}
                >
                  <View
                    style={{
                      flexDirection: "row",
                      alignItems: "center",
                      gap: spacing.sm,
                      flex: 1,
                    }}
                  >
                    <Text variant="bodyStrong" tone="strong">
                      {row.product_name}
                    </Text>
                    {row.grade !== null && <Badge label={row.grade} />}
                  </View>
                  <Text variant="bodyStrong" tone="strong" tabular>
                    {row.amount_display}
                  </Text>
                </View>
                <Text variant="caption" tone="muted" tabular>
                  {row.units} pack{row.units === 1 ? "" : "s"} · {formatGrams(row.grams)}
                </Text>
              </View>
            ))}
          </View>
        </Card>
      )}

      <Text variant="title" tone="strong" accessibilityRole="header">
        Orders
      </Text>

      {orders.length === 0 ? (
        <EmptyState title="No orders yet" />
      ) : (
        <>
          {orders.map((order) => (
            <OrderCard key={order.order_id} order={order} />
          ))}

          {pages > 1 && (
            <View
              style={{
                flexDirection: "row",
                alignItems: "center",
                justifyContent: "space-between",
                gap: spacing.md,
              }}
            >
              <Button
                label="Previous"
                onPress={() => setPage((p) => Math.max(0, p - 1))}
                disabled={page === 0}
                variant="secondary"
                size="sm"
              />
              <Text variant="caption" tone="muted" tabular>
                {page + 1} / {pages}
              </Text>
              <Button
                label="Next"
                onPress={() => setPage((p) => Math.min(pages - 1, p + 1))}
                disabled={page >= pages - 1}
                variant="secondary"
                size="sm"
              />
            </View>
          )}
        </>
      )}
    </Screen>
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
    <Card tone={emphasis ? "primary" : "default"}>
      <View style={{ gap: spacing.xs }}>
        <Text variant="caption" tone="muted">
          {label}
        </Text>
        <Text variant={emphasis ? "display" : "title"} tone="strong" tabular>
          {value}
        </Text>
        <Text variant="caption" tone="muted">
          {note}
        </Text>
      </View>
    </Card>
  );
}

function AmountRow({
  label,
  value,
  tone = "body",
  strong = false,
}: {
  label: string;
  value: string;
  tone?: "body" | "danger";
  strong?: boolean;
}): ReactElement {
  return (
    <View
      style={{
        flexDirection: "row",
        justifyContent: "space-between",
        gap: spacing.md,
      }}
    >
      <Text
        variant={strong ? "bodyStrong" : "body"}
        tone={strong ? "strong" : "muted"}
        style={{ flex: 1 }}
      >
        {label}
      </Text>
      <Text
        variant={strong ? "bodyStrong" : "body"}
        tone={strong ? "strong" : tone}
        tabular
      >
        {value}
      </Text>
    </View>
  );
}

/**
 * One order, with its line items behind a disclosure.
 *
 * The detail stays available rather than being dropped for space: a grower
 * reconciling a bank transfer needs to see WHICH produce made up an amount, and
 * sending them to another screen to find out defeats the point of the page.
 */
function OrderCard({ order }: { order: SupplierOrder }): ReactElement {
  const [open, setOpen] = useState(false);
  const settled = order.payout_status === "paid";

  return (
    <Card>
      <Pressable
        onPress={() => setOpen((current) => !current)}
        accessibilityRole="button"
        accessibilityState={{ expanded: open }}
        accessibilityLabel={`Order ${order.order_number}, ${order.amount_display}`}
        accessibilityHint={open ? "Hides the produce in this order" : "Shows the produce in this order"}
        style={{ gap: spacing.sm }}
      >
        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            justifyContent: "space-between",
            gap: spacing.md,
          }}
        >
          <Text variant="bodyStrong" tone="strong">
            {order.order_number}
          </Text>
          <Text variant="bodyStrong" tone="strong" tabular>
            {order.amount_display}
          </Text>
        </View>

        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            flexWrap: "wrap",
            gap: spacing.sm,
          }}
        >
          <Badge
            label={settled ? "Settled" : "Awaiting"}
            tone={settled ? "primary" : "accent"}
          />
          {settled && order.payout_reference !== "" && (
            <Text variant="caption" tone="faint">
              {order.payout_reference}
            </Text>
          )}
          <Text variant="caption" tone="muted" tabular>
            {order.units} pack{order.units === 1 ? "" : "s"}
          </Text>
        </View>

        <Text variant="caption" tone="muted">
          Placed {formatISTDateTime(order.placed_at)} · Delivery{" "}
          {formatISTDate(order.delivery_day)}
        </Text>

        <Text variant="caption" tone="faint">
          {open ? "Hide produce ▲" : "Show produce ▼"}
        </Text>
      </Pressable>

      {open && (
        <View
          style={{
            marginTop: spacing.md,
            paddingTop: spacing.md,
            borderTopWidth: 1,
            borderTopColor: colors.surface.border,
            gap: spacing.md,
          }}
        >
          {order.items.map((item, index) => (
            <View
              key={`${item.product_name}-${item.unit_label}-${index}`}
              style={{
                flexDirection: "row",
                justifyContent: "space-between",
                gap: spacing.md,
              }}
            >
              <View style={{ flex: 1, gap: 2 }}>
                <View
                  style={{ flexDirection: "row", alignItems: "center", gap: spacing.sm }}
                >
                  <Text variant="bodyStrong" tone="strong">
                    {item.product_name}
                  </Text>
                  {/* The SIZE CODE, prominent: this is the crate the grower
                      has to pick from, and two lines of one produce are
                      otherwise identical. */}
                  {gradeLabel(item.size_code) !== "" && (
                    <Badge label={gradeLabel(item.size_code)} tone="primary" />
                  )}
                  {item.grade !== null && (
                    <Text variant="caption" tone="muted">
                      {item.grade}
                    </Text>
                  )}
                </View>
                <Text variant="caption" tone="muted" tabular>
                  {item.size_meta !== null && item.size_meta !== ""
                    ? `${item.size_meta} · `
                    : ""}
                  {item.unit_label} × {item.qty} · {item.unit_price_display} each ·{" "}
                  {formatGrams(item.weight_grams * item.qty)}
                </Text>
              </View>
              <Text variant="bodyStrong" tone="strong" tabular>
                {item.line_total_display}
              </Text>
            </View>
          ))}

          <Text variant="caption" tone="faint">
            Your share of this order, already net of commission. The customer also paid a
            platform fee and delivery charge, which are not taken from you.
            {settled && order.payout_paid_at !== null
              ? ` Paid ${formatISTDateTime(order.payout_paid_at)}.`
              : ""}
          </Text>
        </View>
      )}
    </Card>
  );
}

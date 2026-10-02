/**
 * The fulfilment queue.
 *
 * Two tabs over the same table. "All orders" is where paid orders get
 * processed; "Processed" is where processed orders get dispatched and the
 * courier sheet is exported. The fulfilment flow is:
 *
 *   pending_payment → paid → processed → dispatched
 *
 * Processing usually happens by itself at the 4pm IST cutoff. These controls
 * are for doing it early, or for the orders that arrive after it.
 */
import { useEffect, useMemo, useState, type ReactElement } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, useToast, ApiError, RangePicker, SALES_RANGES,
} from "@vayal/ui-kit";
import { api, getAccessToken } from "../lib/api.js";
import { config } from "../lib/config.js";
import { formatPaise } from "../lib/money.js";

interface OrderRow {
  id: string;
  order_number: string;
  status: string;
  placed_at: string;
  delivery_day: string;
  total_paise: number;
  total_display: string;
  item_count?: number;
}

interface OrderPage {
  orders: OrderRow[];
  total: number;
  limit: number;
  offset: number;
}

type Tab = "all" | "processed";

const PAGE_SIZE = 25;

const STATUS_TONE: Record<string, string> = {
  pending_payment: "bg-surface-sunken text-primary-900/70",
  paid: "bg-accent-100 text-accent-900",
  processed: "bg-primary-100 text-primary-800",
  dispatched: "bg-primary-600 text-white",
  cancelled: "bg-surface-sunken text-primary-900/50",
  refunded: "bg-surface-sunken text-primary-900/50",
  expired: "bg-surface-sunken text-primary-900/50",
  payment_failed: "bg-accent-100 text-accent-900",
};

function StatusBadge({ status }: { status: string }): ReactElement {
  return (
    <span
      className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${
        STATUS_TONE[status] ?? "bg-surface-sunken text-primary-900/70"
      }`}
    >
      {status.replace(/_/g, " ")}
    </span>
  );
}

function formatDateTime(iso: string): string {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return iso;
  return new Intl.DateTimeFormat("en-IN", {
    day: "2-digit", month: "short", hour: "numeric", minute: "2-digit",
    hour12: true, timeZone: "Asia/Kolkata",
  }).format(parsed);
}

export function OrdersTable(): ReactElement {
  const queryClient = useQueryClient();
  const { showToast } = useToast();

  const [tab, setTab] = useState<Tab>("all");
  // All time, not today: this is a queue an operator works through, and an
  // order placed at 11pm yesterday is still waiting this morning. The figures
  // on the dashboard are the ones that want a period.
  const [range, setRange] = useState("all");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const customIncomplete = range === "custom" && (!from || !to);

  // The period and tab both change what is on screen, so a selection made
  // against the previous view must not survive into the next one — acting on
  // rows the operator can no longer see is how bulk actions go wrong.
  useEffect(() => {
    setSelected(new Set());
    setPage(0);
  }, [tab, range, from, to]);

  const periodQuery = useMemo(
    () => (range === "custom" ? { range, from, to } : { range }),
    [range, from, to],
  );

  const orders = useQuery<OrderPage, ApiError>({
    queryKey: ["admin-orders", tab, range, from, to, page],
    queryFn: () =>
      api.get<OrderPage>("/admin/orders", {
        query: {
          ...periodQuery,
          // The "Processed" tab is a status filter, nothing more.
          ...(tab === "processed" ? { status: "processed" } : {}),
          limit: PAGE_SIZE,
          offset: page * PAGE_SIZE,
        },
      }),
    enabled: !customIncomplete,
  });

  const rows = orders.data?.orders ?? [];
  const total = orders.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  // Only orders in the right state for this tab's action can be selected.
  // A processed order has nothing left to process, so its checkbox is not
  // merely ignored — it is absent, which is the honest way to say so.
  const actionable = (row: OrderRow) =>
    tab === "all" ? row.status === "paid" : row.status === "processed";

  const selectableIDs = rows.filter(actionable).map((r) => r.id);
  const allSelected =
    selectableIDs.length > 0 && selectableIDs.every((id) => selected.has(id));

  function toggle(id: string): void {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function toggleAll(): void {
    setSelected(allSelected ? new Set() : new Set(selectableIDs));
  }

  const endpoint = tab === "all" ? "bulk-process" : "bulk-dispatch";
  const verb = tab === "all" ? "processed" : "dispatched";

  const bulk = useMutation<
    { requested: number; skipped: number } & Record<string, number>,
    ApiError,
    { all: boolean }
  >({
    mutationFn: ({ all }) =>
      api.post(
        `/admin/orders/${endpoint}`,
        all ? { all: true } : { order_ids: [...selected] },
        { query: periodQuery },
      ),
    onSuccess: (result) => {
      const moved = result[verb] ?? 0;
      // The skipped count is surfaced rather than swallowed: a selection can
      // go stale between rendering and clicking, and "12 of 15" tells the
      // operator something real happened to the other three.
      showToast(
        result.skipped > 0
          ? `${moved} ${verb}. ${result.skipped} were already handled by someone else.`
          : `${moved} order${moved === 1 ? "" : "s"} ${verb}.`,
      );
      setSelected(new Set());
      void queryClient.invalidateQueries({ queryKey: ["admin-orders"] });
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
    onError: (err) => showToast(err.message, "error"),
  });

  /**
   * The courier sheet.
   *
   * Fetched directly rather than through the typed API client, because the
   * response is a file: the client parses JSON, and a CSV body would fail
   * before it reached the browser's downloader. The bearer token cannot ride
   * on a plain <a href>, which is why this is a fetch and not a link.
   */
  const exporting = useMutation<void, Error>({
    mutationFn: async () => {
      const token = getAccessToken();
      const params = new URLSearchParams({ ...periodQuery, status: "processed" });
      const response = await fetch(
        `${config.apiBaseUrl}/admin/orders/export.csv?${params}`,
        {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          credentials: "include",
        },
      );
      // Failures are raised, not swallowed. A download that quietly does
      // nothing is indistinguishable from a browser blocking it, and an
      // operator waiting on a courier sheet needs to know which.
      if (!response.ok) {
        throw new Error(
          response.status === 401
            ? "Your session expired. Sign in again to export."
            : `The export failed (${response.status}).`,
        );
      }

      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `vayal-courier-${new Date().toISOString().slice(0, 10)}.csv`;
      link.click();
      // Revoked on a delay: Safari has not finished reading the blob when the
      // click() call returns, and revoking immediately produces an empty file.
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    },
    onSuccess: () =>
      showToast("Courier sheet downloaded. It contains customer addresses."),
    onError: (err) => showToast(err.message, "error"),
  });

  const picker = (
    <RangePicker
      range={range} from={from} to={to}
      onRange={setRange} onFrom={setFrom} onTo={setTo}
      // All time first in the list, because it is this screen's default.
      options={SALES_RANGES}
    />
  );

  return (
    <>
      <PageHeading
        title="Orders"
        description={
          tab === "all"
            ? "Process paid orders. Anything not processed by hand is picked up automatically at the 4pm cutoff."
            : "Dispatch processed orders and export the courier sheet."
        }
        actions={picker}
      />

      <div role="tablist" aria-label="Order queue" className="mb-4 flex flex-wrap gap-2">
        {([["all", "All orders"], ["processed", "Processed"]] as const).map(
          ([value, label]) => (
            <button
              key={value}
              role="tab"
              aria-selected={tab === value}
              onClick={() => setTab(value)}
              className={`rounded-full px-4 py-2 text-sm font-medium ${
                tab === value
                  ? "bg-primary-600 text-white"
                  : "border border-surface-border bg-surface-raised text-primary-800 hover:border-primary-400"
              }`}
            >
              {label}
            </button>
          ),
        )}
      </div>

      {/* Action bar. The selected-count button is disabled with nothing
          selected; the "all" button is always available because it does not
          depend on the selection. */}
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <button
          onClick={() => bulk.mutate({ all: false })}
          disabled={selected.size === 0 || bulk.isPending}
          className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40"
        >
          {tab === "all" ? "Process" : "Dispatch"} selected
          {selected.size > 0 ? ` (${selected.size})` : ""}
        </button>

        <button
          onClick={() => bulk.mutate({ all: true })}
          disabled={bulk.isPending}
          className="rounded-card border border-primary-300 px-4 py-2 text-sm font-medium text-primary-800 hover:bg-primary-50 disabled:opacity-50"
        >
          {tab === "all" ? "Process all paid" : "Dispatch all processed"}
        </button>

        {tab === "processed" && (
          <button
            onClick={() => exporting.mutate()}
            disabled={exporting.isPending}
            className="rounded-card border border-surface-border px-4 py-2 text-sm font-medium text-primary-800 disabled:opacity-50"
          >
            {exporting.isPending ? "Preparing…" : "Export courier sheet (CSV)"}
          </button>
        )}

        <span className="ml-auto text-sm text-primary-900/60">
          {total} order{total === 1 ? "" : "s"}
        </span>
      </div>

      {/* "All" acts on the period on screen, never the whole table. Said out
          loud because the button cannot say it without becoming a paragraph. */}
      {/* The button acts on the PERIOD, so the period is named rather than
          left to be inferred from a dropdown at the other end of the row. */}
      <p className="mb-3 text-xs text-primary-900/55">
        &ldquo;{tab === "all" ? "Process all paid" : "Dispatch all processed"}&rdquo;
        applies to {range === "all" ? "every order, not just this page" : "the selected period only"}.
      </p>

      {customIncomplete && (
        <Card>
          <p className="text-primary-900/70">Pick both dates to see orders.</p>
        </Card>
      )}

      {!customIncomplete && orders.isPending && <Card>Loading orders…</Card>}

      {orders.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">{orders.error.message}</p>
        </Card>
      )}

      {!customIncomplete && orders.data && rows.length === 0 && (
        <Card>
          <p className="text-primary-900/70">
            No orders in this period.
          </p>
        </Card>
      )}

      {rows.length > 0 && (
        <Card className="overflow-x-auto p-0">
          <table className="w-full min-w-[52rem] text-left text-sm">
            <thead className="border-b border-surface-border text-xs uppercase tracking-wide text-primary-900/60">
              <tr>
                <th scope="col" className="w-10 px-4 py-3">
                  <input
                    type="checkbox"
                    checked={allSelected}
                    disabled={selectableIDs.length === 0}
                    onChange={toggleAll}
                    aria-label={`Select all ${tab === "all" ? "paid" : "processed"} orders on this page`}
                  />
                </th>
                <th scope="col" className="px-4 py-3">Order</th>
                <th scope="col" className="px-4 py-3">Placed</th>
                <th scope="col" className="px-4 py-3">Delivery</th>
                <th scope="col" className="px-4 py-3">Status</th>
                <th scope="col" className="px-4 py-3 text-right">Total</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-surface-border">
              {rows.map((row) => {
                const canAct = actionable(row);
                return (
                  <tr
                    key={row.id}
                    className={selected.has(row.id) ? "bg-primary-50" : ""}
                  >
                    <td className="px-4 py-3">
                      {canAct ? (
                        <input
                          type="checkbox"
                          checked={selected.has(row.id)}
                          onChange={() => toggle(row.id)}
                          aria-label={`Select ${row.order_number}`}
                        />
                      ) : (
                        // No disabled checkbox: an unusable control invites a
                        // click and explains nothing.
                        <span aria-hidden="true" className="text-primary-900/20">
                          —
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <Link
                        to={`/orders/${row.id}`}
                        className="font-medium text-primary-900 underline underline-offset-2"
                      >
                        {row.order_number}
                      </Link>
                    </td>
                    <td className="px-4 py-3 text-primary-900/70">
                      {formatDateTime(row.placed_at)}
                    </td>
                    <td className="px-4 py-3 text-primary-900/70">{row.delivery_day}</td>
                    <td className="px-4 py-3"><StatusBadge status={row.status} /></td>
                    <td className="px-4 py-3 text-right font-medium tabular-nums text-primary-900">
                      {row.total_display ?? formatPaise(row.total_paise)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </Card>
      )}

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
            Page {page + 1} of {pages}
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

      <p className="mt-6 text-xs text-primary-900/50">
        Questions about an order? {config.supportEmail}
      </p>
    </>
  );
}

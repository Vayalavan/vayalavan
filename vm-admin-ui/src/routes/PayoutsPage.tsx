import { useMemo, useState, type FormEvent, type ReactElement } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, ApiError } from "@vayal/ui-kit";
import { api, getAccessToken } from "../lib/api.js";
import { config } from "../lib/config.js";
import { formatPaise, formatDate } from "../lib/money.js";

interface SummaryRow {
  supplier_id: string;
  business_name: string;
  pending_paise: number;
  pending_display: string;
  pending_orders: number;
  oldest_pending_at: string;
  age_days: number;
}

interface Summary {
  suppliers: SummaryRow[];
  total_pending_paise: number;
  total_pending: string;
}

interface PayoutLine {
  id: string;
  supplier_id: string;
  business_name: string;
  order_id: string;
  order_number: string;
  placed_at: string;
  amount_paise: number;
  amount_display: string;
  status: "pending" | "paid";
  reference_no: string | null;
  marked_paid_at: string | null;
}

interface PayoutList {
  payouts: PayoutLine[];
  total_paise: number;
}

/**
 * Settlement.
 *
 * Suppliers are paid manually by NEFT, so this screen exists to answer one
 * question at a time: who is owed money, which orders make up that total, and
 * — once the transfer is made — recording the UTR against those exact rows.
 *
 * Summary first, then drill into one supplier. Consolidating by supplier is
 * the whole point: an admin makes ONE bank transfer per grower, not one per
 * order.
 */
export function PayoutsPage(): ReactElement {
  const [selectedSupplier, setSelectedSupplier] = useState<SummaryRow | null>(null);

  const summary = useQuery<Summary, ApiError>({
    queryKey: ["payout-summary"],
    queryFn: () => api.get<Summary>("/admin/payouts/summary"),
  });

  if (selectedSupplier) {
    return (
      <SupplierPayouts
        supplier={selectedSupplier}
        onBack={() => setSelectedSupplier(null)}
      />
    );
  }

  return (
    <>
      <PageHeading
        title="Settlement"
        description="What each supplier is owed. One NEFT transfer per supplier, then record the UTR."
        actions={
          <button
            onClick={() => void downloadExport()}
            className="rounded-card border border-surface-border bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800 hover:bg-primary-50"
          >
            Export all pending (CSV)
          </button>
        }
      />

      {summary.isPending && <Card>Loading settlement figures…</Card>}
      {summary.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">
            {summary.error.message}
          </p>
        </Card>
      )}

      {summary.data && summary.data.suppliers.length === 0 && (
        <Card>
          <p className="text-primary-900/70">
            Nothing outstanding. Every supplier has been paid.
          </p>
        </Card>
      )}

      {summary.data && summary.data.suppliers.length > 0 && (
        <>
          <Card className="mb-4">
            <p className="text-sm text-primary-900/60">Total outstanding</p>
            <p className="text-2xl font-semibold tabular-nums text-primary-900">
              {summary.data.total_pending}
            </p>
            <p className="mt-1 text-sm text-primary-900/60">
              across {summary.data.suppliers.length} supplier
              {summary.data.suppliers.length === 1 ? "" : "s"}
            </p>
          </Card>

          <div className="grid gap-3">
            {summary.data.suppliers.map((row) => (
              <Card key={row.supplier_id}>
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <h2 className="font-semibold text-primary-900">
                      {row.business_name || row.supplier_id}
                    </h2>
                    <p className="mt-0.5 text-sm text-primary-900/70">
                      {row.pending_orders} order{row.pending_orders === 1 ? "" : "s"} ·
                      oldest {formatDate(row.oldest_pending_at)}
                      {/* Ageing is the signal an ops person acts on: a
                          fortnight-old balance is a relationship problem. */}
                      {row.age_days >= 7 && (
                        <span className="ml-2 rounded-full bg-accent-100 px-2 py-0.5 text-xs font-medium text-accent-900">
                          {row.age_days} days old
                        </span>
                      )}
                    </p>
                  </div>

                  <div className="flex items-center gap-4">
                    <p className="text-lg font-semibold tabular-nums text-primary-900">
                      {row.pending_display}
                    </p>
                    <button
                      onClick={() => setSelectedSupplier(row)}
                      className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700"
                    >
                      Settle
                    </button>
                  </div>
                </div>
              </Card>
            ))}
          </div>
        </>
      )}
    </>
  );
}

function SupplierPayouts({
  supplier,
  onBack,
}: {
  supplier: SummaryRow;
  onBack: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [showModal, setShowModal] = useState(false);

  const payouts = useQuery<PayoutList, ApiError>({
    queryKey: ["payouts", supplier.supplier_id],
    queryFn: () =>
      api.get<PayoutList>("/admin/payouts", {
        query: { supplier_id: supplier.supplier_id, status: "pending", limit: 500 },
      }),
  });

  const lines = payouts.data?.payouts ?? [];
  const allSelected = lines.length > 0 && selected.size === lines.length;

  const selectedTotal = useMemo(
    () =>
      lines
        .filter((line) => selected.has(line.id))
        .reduce((sum, line) => sum + line.amount_paise, 0),
    [lines, selected],
  );

  function toggle(id: string) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <>
      <PageHeading
        title={supplier.business_name || "Supplier"}
        description="Select the orders covered by your NEFT transfer, then record the UTR."
        actions={
          <div className="flex gap-2">
            <button
              onClick={() => void downloadExport(supplier.supplier_id)}
              className="rounded-card border border-surface-border bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800"
            >
              Export CSV
            </button>
            <button
              onClick={onBack}
              className="rounded-card px-4 py-2 text-sm font-medium text-primary-800/70"
            >
              Back
            </button>
          </div>
        }
      />

      {/* The CSV is the only place account numbers appear unmasked, and every
          export is written to the audit log. */}
      <Card className="mb-4 border-accent-200 bg-accent-50">
        <p className="text-sm text-accent-900">
          The CSV export contains full bank account numbers so you can make the
          transfer. Every export is recorded in the audit log against your
          account.
        </p>
      </Card>

      {payouts.isPending && <Card>Loading…</Card>}

      {payouts.data && lines.length === 0 && (
        <Card>
          <p className="text-primary-900/70">Nothing outstanding for this supplier.</p>
        </Card>
      )}

      {lines.length > 0 && (
        <Card>
          <div className="mb-3 flex items-center justify-between">
            <label className="flex items-center gap-2 text-sm font-medium text-primary-900">
              <input
                type="checkbox"
                checked={allSelected}
                onChange={() =>
                  setSelected(allSelected ? new Set() : new Set(lines.map((l) => l.id)))
                }
                className="h-4 w-4"
              />
              Select all {lines.length}
            </label>
            <p className="text-sm text-primary-900/70">
              Selected:{" "}
              <strong className="tabular-nums text-primary-900">
                {formatPaise(selectedTotal)}
              </strong>
            </p>
          </div>

          <div className="-mx-2 overflow-x-auto">
            <table className="w-full min-w-[32rem] text-left text-sm">
              <thead>
                <tr className="border-b border-surface-border text-xs uppercase tracking-wide text-primary-900/60">
                  <th className="px-2 py-2 w-8" />
                  <th className="px-2 py-2">Order</th>
                  <th className="px-2 py-2">Placed</th>
                  <th className="px-2 py-2 text-right">Amount</th>
                </tr>
              </thead>
              <tbody>
                {lines.map((line) => (
                  <tr key={line.id} className="border-b border-surface-border/60">
                    <td className="px-2 py-2">
                      <input
                        type="checkbox"
                        checked={selected.has(line.id)}
                        onChange={() => toggle(line.id)}
                        aria-label={`Include order ${line.order_number}`}
                        className="h-4 w-4"
                      />
                    </td>
                    <td className="px-2 py-2 font-medium text-primary-900">
                      {line.order_number}
                    </td>
                    <td className="px-2 py-2 text-primary-900/70">
                      {formatDate(line.placed_at)}
                    </td>
                    <td className="px-2 py-2 text-right tabular-nums">
                      {line.amount_display}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-4 flex justify-end">
            <button
              onClick={() => setShowModal(true)}
              disabled={selected.size === 0}
              className="rounded-card bg-primary-600 px-5 py-2.5 font-medium text-white hover:bg-primary-700 disabled:opacity-50"
            >
              Mark {selected.size} as paid
            </button>
          </div>
        </Card>
      )}

      {showModal && (
        <MarkPaidModal
          payoutIds={[...selected]}
          totalPaise={selectedTotal}
          onClose={() => setShowModal(false)}
          onDone={() => {
            setShowModal(false);
            setSelected(new Set());
            void queryClient.invalidateQueries({ queryKey: ["payouts"] });
            void queryClient.invalidateQueries({ queryKey: ["payout-summary"] });
          }}
        />
      )}
    </>
  );
}

/**
 * The confirmation step.
 *
 * A reference number is mandatory — the server rejects the request without
 * one. A payout marked paid with no UTR cannot be reconciled against a bank
 * statement later, which is exactly when someone needs it.
 */
function MarkPaidModal({
  payoutIds,
  totalPaise,
  onClose,
  onDone,
}: {
  payoutIds: string[];
  totalPaise: number;
  onClose: () => void;
  onDone: () => void;
}): ReactElement {
  const [reference, setReference] = useState("");
  const [notes, setNotes] = useState("");
  const [error, setError] = useState<string | null>(null);

  const markPaid = useMutation<unknown, ApiError>({
    mutationFn: () =>
      api.post("/admin/payouts/mark-paid", {
        payout_ids: payoutIds,
        reference_no: reference.trim(),
        notes: notes.trim(),
      }),
    onSuccess: onDone,
    onError: (err) => setError(err.message),
  });

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    markPaid.mutate();
  }

  return (
    // A real modal: labelled, and the backdrop is not the only way out.
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="markpaid-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-primary-950/40 p-4"
    >
      <div className="w-full max-w-md rounded-card bg-surface-raised p-5 shadow-card">
        <h2 id="markpaid-title" className="text-lg font-semibold text-primary-900">
          Record payment
        </h2>
        <p className="mt-1 text-sm text-primary-900/70">
          Marking <strong>{payoutIds.length}</strong> payout
          {payoutIds.length === 1 ? "" : "s"} totalling{" "}
          <strong className="tabular-nums">{formatPaise(totalPaise)}</strong> as paid.
          This cannot be undone.
        </p>

        <form onSubmit={onSubmit} className="mt-4 space-y-3" noValidate>
          <div>
            <label htmlFor="reference" className="block text-sm font-medium text-primary-900">
              NEFT reference (UTR)
            </label>
            <input
              id="reference"
              value={reference}
              onChange={(e) => setReference(e.target.value)}
              required
              autoFocus
              placeholder="e.g. SBIN123456789"
              className="mt-1 w-full rounded-card border border-surface-border px-3 py-2"
            />
          </div>

          <div>
            <label htmlFor="notes" className="block text-sm font-medium text-primary-900">
              Notes <span className="text-primary-900/50">(optional)</span>
            </label>
            <textarea
              id="notes"
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              rows={2}
              className="mt-1 w-full rounded-card border border-surface-border px-3 py-2"
            />
          </div>

          {error && (
            <p role="alert" className="rounded-card bg-accent-50 px-3 py-2 text-sm text-accent-800">
              {error}
            </p>
          )}

          <div className="flex gap-2 pt-1">
            <button
              type="submit"
              disabled={markPaid.isPending || !reference.trim()}
              className="flex-1 rounded-card bg-primary-600 px-4 py-2.5 font-medium text-white disabled:opacity-50"
            >
              {markPaid.isPending ? "Recording…" : "Confirm payment"}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="rounded-card border border-surface-border px-4 py-2.5 font-medium text-primary-800"
            >
              Cancel
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

/** Downloads the NEFT worksheet, preserving the auth header. */
async function downloadExport(supplierId?: string): Promise<void> {
  const token = getAccessToken();
  const query = supplierId ? `?supplier_id=${encodeURIComponent(supplierId)}` : "";
  const response = await fetch(`${config.apiBaseUrl}/admin/payouts/export.csv${query}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    credentials: "include",
  });
  if (!response.ok) return;

  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = "vayal-pending-payouts.csv";
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

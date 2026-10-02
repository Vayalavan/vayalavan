/**
 * Customer wallets — CLAUDE.md §6.7.
 *
 * What customers hold in prepaid balance, and the one way it leaves other
 * than as a delivery: a refund to the payment it came from. A refund is
 * split across the customer's top-ups newest first, each slice a Razorpay
 * refund, and every one is written to the audit log with the reason given
 * here.
 */
import { useState, type FormEvent, type ReactElement } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { formatDate } from "../lib/money.js";

interface WalletRow {
  customer_id: string;
  customer_name: string;
  customer_email: string;
  balance_paise: number;
  balance_display: string;
  active_schedules: number;
  updated_at: string;
}

interface WalletList {
  wallets: WalletRow[];
  funded_wallets: number;
  total_balance_display: string;
}

interface LedgerEntry {
  id: string;
  kind: string;
  label: string;
  amount_paise: number;
  amount_display: string;
  balance_after_display: string;
  order_number?: string;
  created_at: string;
}

interface RefundRow {
  id: string;
  amount_display: string;
  status: "pending" | "issued" | "failed";
  razorpay_refund_id: string | null;
  notes: string | null;
  error: string | null;
  created_at: string;
}

interface WalletDetail {
  customer_id: string;
  customer_name: string;
  customer_email: string;
  balance_paise: number;
  balance_display: string;
  transactions: LedgerEntry[];
  refunds: RefundRow[];
}

interface RefundResult {
  refunds: Array<{ id: string; status: string; amount_display: string; error?: string }>;
  failed_paise: number;
  balance_after_display: string;
}

export function WalletsPage(): ReactElement {
  const [selected, setSelected] = useState<string | null>(null);
  const list = useQuery<WalletList, ApiError>({
    queryKey: ["admin-wallets"],
    queryFn: () => api.get<WalletList>("/admin/wallets"),
  });

  if (selected) {
    return <WalletDetailView customerId={selected} onBack={() => setSelected(null)} />;
  }

  return (
    <>
      <PageHeading
        title="Wallets"
        description="Prepaid balances customers hold for scheduled deliveries. Refunds go back to the original payment."
      />

      {list.isPending && <Card>Loading wallets…</Card>}
      {list.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">{list.error.message}</p>
        </Card>
      )}

      {list.data && (
        <>
          <Card className="mb-4">
            <p className="text-sm text-primary-900/60">Held across all wallets</p>
            <p className="text-2xl font-semibold tabular-nums text-primary-900">
              {list.data.total_balance_display}
            </p>
            <p className="mt-1 text-sm text-primary-900/60">
              in {list.data.funded_wallets} wallet{list.data.funded_wallets === 1 ? "" : "s"}. This
              is customer money, not revenue, until it is spent on a delivery.
            </p>
          </Card>

          {list.data.wallets.length === 0 ? (
            <Card>
              <p className="text-primary-900/70">No customer holds a balance.</p>
            </Card>
          ) : (
            <div className="grid gap-3">
              {list.data.wallets.map((row) => (
                <Card key={row.customer_id}>
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div className="min-w-0">
                      <h2 className="truncate font-semibold text-primary-900">
                        {row.customer_name || row.customer_email || row.customer_id}
                      </h2>
                      <p className="mt-0.5 text-sm text-primary-900/70">
                        {row.customer_email}
                        {row.active_schedules > 0 &&
                          ` · ${row.active_schedules} active schedule${row.active_schedules === 1 ? "" : "s"}`}
                      </p>
                    </div>
                    <div className="flex items-center gap-4">
                      <p className="text-lg font-semibold tabular-nums text-primary-900">
                        {row.balance_display}
                      </p>
                      <button
                        onClick={() => setSelected(row.customer_id)}
                        className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700"
                      >
                        Open
                      </button>
                    </div>
                  </div>
                </Card>
              ))}
            </div>
          )}
        </>
      )}
    </>
  );
}

function WalletDetailView({
  customerId,
  onBack,
}: {
  customerId: string;
  onBack: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const [amount, setAmount] = useState("");
  const [notes, setNotes] = useState("");
  const [result, setResult] = useState<RefundResult | null>(null);

  const detail = useQuery<WalletDetail, ApiError>({
    queryKey: ["admin-wallet", customerId],
    queryFn: () => api.get<WalletDetail>(`/admin/wallets/${customerId}`),
  });

  const refund = useMutation<RefundResult, ApiError, { amount_paise: number; notes: string }>({
    mutationFn: (body) => api.post<RefundResult>(`/admin/wallets/${customerId}/refund`, body),
    onSuccess: (outcome) => {
      setResult(outcome);
      setAmount("");
      setNotes("");
      void queryClient.invalidateQueries({ queryKey: ["admin-wallet", customerId] });
      void queryClient.invalidateQueries({ queryKey: ["admin-wallets"] });
    },
  });

  const paise = toPaise(amount);
  const balance = detail.data?.balance_paise ?? 0;
  const amountError =
    amount.trim() === ""
      ? null
      : paise === null
        ? "Enter an amount like 250 or 250.50."
        : paise <= 0 || paise > balance
          ? "Must be more than zero and no more than the balance."
          : null;

  function submit(event: FormEvent): void {
    event.preventDefault();
    if (paise === null || amountError || notes.trim() === "") return;
    setResult(null);
    refund.mutate({ amount_paise: paise, notes: notes.trim() });
  }

  return (
    <>
      <button onClick={onBack} className="mb-4 text-sm text-primary-700 underline underline-offset-2">
        ← All wallets
      </button>

      {detail.isPending && <Card>Loading…</Card>}
      {detail.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">{detail.error.message}</p>
        </Card>
      )}

      {detail.data && (
        <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_360px]">
          <div className="space-y-4">
            <Card>
              <p className="text-sm text-primary-900/60">
                {detail.data.customer_name || "Customer"} · {detail.data.customer_email}
              </p>
              <p className="mt-1 text-3xl font-semibold tabular-nums text-primary-900">
                {detail.data.balance_display}
              </p>
            </Card>

            <Card>
              <h2 className="font-semibold text-primary-900">Ledger</h2>
              <table className="mt-3 w-full text-sm">
                <thead>
                  <tr className="text-left text-xs uppercase tracking-wide text-primary-900/50">
                    <th className="py-1.5 font-medium">When</th>
                    <th className="py-1.5 font-medium">What</th>
                    <th className="py-1.5 text-right font-medium">Amount</th>
                    <th className="py-1.5 text-right font-medium">Balance</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-surface-border">
                  {detail.data.transactions.map((entry) => (
                    <tr key={entry.id}>
                      <td className="py-2 text-primary-900/70">{formatDate(entry.created_at)}</td>
                      <td className="py-2 text-primary-900">{entry.label}</td>
                      <td
                        className={`py-2 text-right tabular-nums ${entry.amount_paise > 0 ? "text-primary-700" : "text-primary-900"}`}
                      >
                        {entry.amount_display}
                      </td>
                      <td className="py-2 text-right tabular-nums text-primary-900/70">
                        {entry.balance_after_display}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>
          </div>

          <div className="space-y-4">
            <Card>
              <h2 className="font-semibold text-primary-900">Refund balance</h2>
              <p className="mt-1 text-sm text-primary-900/60">
                Sent back to the customer&rsquo;s original payments through Razorpay. It leaves the
                wallet immediately; a refund Razorpay refuses is returned to the wallet.
              </p>
              <form onSubmit={submit} className="mt-3 space-y-3">
                <label className="block text-sm">
                  <span className="font-medium text-primary-900">Amount (₹)</span>
                  <input
                    inputMode="decimal"
                    value={amount}
                    onChange={(e) => setAmount(e.target.value)}
                    placeholder={detail.data.balance_display.replace("₹", "")}
                    className="mt-1 w-full rounded-card border border-surface-border px-3 py-2 tabular-nums"
                  />
                  {amountError && <span className="mt-1 block text-xs text-accent-800">{amountError}</span>}
                </label>
                <label className="block text-sm">
                  <span className="font-medium text-primary-900">Reason (for the audit log)</span>
                  <textarea
                    value={notes}
                    onChange={(e) => setNotes(e.target.value)}
                    rows={2}
                    maxLength={500}
                    className="mt-1 w-full rounded-card border border-surface-border px-3 py-2"
                  />
                </label>
                <button
                  type="submit"
                  disabled={!amount || !!amountError || notes.trim() === "" || refund.isPending}
                  className="w-full rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40"
                >
                  {refund.isPending ? "Refunding…" : "Refund"}
                </button>
              </form>
              {refund.isError && (
                <p role="alert" className="mt-3 text-sm text-accent-800">{refund.error.message}</p>
              )}
              {result && (
                <div className="mt-3 rounded-card bg-primary-50 p-3 text-sm">
                  {result.refunds.map((slice) => (
                    <p key={slice.id} className={slice.status === "failed" ? "text-accent-800" : "text-primary-800"}>
                      {slice.amount_display} — {slice.status}
                      {slice.error ? `: ${slice.error}` : ""}
                    </p>
                  ))}
                  <p className="mt-1 text-primary-900/70">Balance now {result.balance_after_display}</p>
                </div>
              )}
            </Card>

            {detail.data.refunds.length > 0 && (
              <Card>
                <h2 className="font-semibold text-primary-900">Refunds</h2>
                <ul className="mt-2 divide-y divide-surface-border text-sm">
                  {detail.data.refunds.map((row) => (
                    <li key={row.id} className="py-2">
                      <div className="flex justify-between">
                        <span className="tabular-nums text-primary-900">{row.amount_display}</span>
                        <span className="capitalize text-primary-900/70">{row.status}</span>
                      </div>
                      <p className="text-xs text-primary-900/55">
                        {formatDate(row.created_at)}
                        {row.razorpay_refund_id ? ` · ${row.razorpay_refund_id}` : ""}
                        {row.notes ? ` · ${row.notes}` : ""}
                      </p>
                      {row.error && <p className="text-xs text-accent-800">{row.error}</p>}
                    </li>
                  ))}
                </ul>
              </Card>
            )}
          </div>
        </div>
      )}
    </>
  );
}

/** Rupees typed by an admin, to paise, without a float multiply (rule 1). */
function toPaise(input: string): number | null {
  const trimmed = input.trim().replace(/,/g, "").replace(/^₹/, "");
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return null;
  const [whole = "0", fraction = ""] = trimmed.split(".");
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}

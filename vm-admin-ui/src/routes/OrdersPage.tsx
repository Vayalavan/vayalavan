import { useState, type ReactElement } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Card, PageHeading, OrderTimeline, useToast, ApiError, gradedName,
} from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { formatDate } from "../lib/money.js";

interface OrderItem {
  id: string; product_name: string; unit_label: string;
  /** The GRADE this pack was bought at. Absent on an ungraded listing. */
  size_code?: string;
  qty: number; line_total_display: string;
}
interface Order {
  id: string; order_number: string; status: string;
  subtotal_display: string; platform_fee_display: string;
  delivery_fee_display: string; total_display: string;
  placed_at: string; items: OrderItem[];
  milestones: { name: string; at: string; completed: boolean }[];
  expected_delivery_text: string; courier_notice: string; support_notice: string;
  address: Record<string, string>;
}
/** One supplier's share of an order — the same figure their payout uses. */
interface SupplierSubtotal {
  supplier_id: string;
  business_name: string;
  amount_paise: number;
  amount_display: string;
  item_count: number;
}

interface OrderDetail {
  order: Order;
  supplier_subtotals: SupplierSubtotal[];
  payment: Record<string, unknown> | null;
  payouts: { id: string; business_name: string; amount_display: string; status: string }[];
}


/**
 * One order, in full.
 *
 * The list lives in OrdersTable; this is only ever reached at /orders/:id, so
 * it reads the id from the URL rather than from internal state — which is what
 * makes an order's page linkable and survive a refresh.
 */
export function OrdersPage(): ReactElement {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  return <OrderDetailView id={id} onBack={() => navigate("/orders")} />;
}

function OrderDetailView({ id, onBack }: { id: string; onBack: () => void }): ReactElement {
  const queryClient = useQueryClient();
  const { showToast } = useToast();
  const [reason, setReason] = useState("");
  const [cancelling, setCancelling] = useState(false);
  const [recording, setRecording] = useState(false);
  const [paymentRef, setPaymentRef] = useState("");
  const [paymentMethod, setPaymentMethod] = useState("upi");
  const [error, setError] = useState<string | null>(null);

  const detail = useQuery<OrderDetail, ApiError>({
    queryKey: ["admin-order", id],
    queryFn: () => api.get<OrderDetail>(`/admin/orders/${id}`),
  });

  const act = useMutation<unknown, ApiError, { action: string; reason?: string }>({
    mutationFn: ({ action, reason: why }) =>
      api.post(`/admin/orders/${id}/${action}`, why ? { reason: why } : {}),
    onSuccess: () => {
      setCancelling(false); setError(null);
      void queryClient.invalidateQueries({ queryKey: ["admin-order", id] });
      void queryClient.invalidateQueries({ queryKey: ["admin-orders"] });
    },
    onError: (err) => setError(err.message),
  });

  /**
   * Records a payment taken outside Razorpay.
   *
   * This is the only payment-status change offered, deliberately. Marking an
   * order paid is what creates the supplier payout rows, so a free-form status
   * dropdown would let a mistyped click decide who gets paid. The server runs
   * the same transaction the Razorpay webhook runs.
   */
  const recordPayment = useMutation<unknown, ApiError>({
    mutationFn: () =>
      api.post(`/admin/orders/${id}/record-payment`, {
        reference: paymentRef, method: paymentMethod,
      }),
    onSuccess: () => {
      showToast("Payment recorded. Supplier payouts created.");
      setRecording(false); setPaymentRef(""); setError(null);
      void queryClient.invalidateQueries({ queryKey: ["admin-order", id] });
      void queryClient.invalidateQueries({ queryKey: ["admin-orders"] });
      // Settlement and the dashboard both change the moment an order is paid.
      void queryClient.invalidateQueries({ queryKey: ["admin-payouts"] });
      void queryClient.invalidateQueries({ queryKey: ["admin-dashboard"] });
    },
    onError: (err) => setError(err.message),
  });

  if (detail.isPending) return <Card>Loading…</Card>;
  if (detail.isError) return <Card><p role="alert">{detail.error.message}</p></Card>;

  const { order, supplier_subtotals, payouts } = detail.data;

  return (
    <>
      <PageHeading title={order.order_number}
        description={`${order.status.replace(/_/g, " ")} · placed ${formatDate(order.placed_at)}`}
        actions={
          <div className="flex flex-wrap gap-2">
            {/* Process, not dispatch. Dispatching from here would let an
                order skip the processed stage the courier sheet is built
                from — the queue is where dispatch belongs, after the parcel
                has actually been handed over. */}
            {order.status === "paid" && (
              <button onClick={() => act.mutate({ action: "process" })} disabled={act.isPending}
                className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-50">
                Process order
              </button>
            )}
            {order.status === "pending_payment" && (
              <button onClick={() => setRecording((o) => !o)}
                className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white">
                Record payment
              </button>
            )}
            {order.status === "processed" && (
              <button onClick={() => act.mutate({ action: "dispatch" })} disabled={act.isPending}
                className="rounded-card border border-primary-300 px-4 py-2 text-sm font-medium text-primary-800 disabled:opacity-50">
                Mark dispatched
              </button>
            )}
            <button onClick={() => setCancelling((o) => !o)}
              className="rounded-card border border-surface-border px-4 py-2 text-sm text-primary-900/70">
              Cancel
            </button>
            <button onClick={onBack} className="rounded-card px-4 py-2 text-sm text-primary-800/70">Back</button>
          </div>
        } />

      {recording && (
        <Card className="mb-4 border-primary-300 bg-primary-50">
          <h2 className="font-semibold text-primary-900">Record a payment</h2>
          <p className="mt-1 text-sm text-primary-900/70">
            For money taken outside Razorpay. This marks the order paid, commits
            the reserved stock and creates the supplier payouts — the same steps
            an online payment triggers. It cannot be undone from this screen.
          </p>
          <form className="mt-3 flex flex-col gap-2 sm:flex-row"
            onSubmit={(e) => { e.preventDefault(); recordPayment.mutate(); }}>
            <div className="flex-1">
              <label htmlFor="payment-ref" className="sr-only">Payment reference</label>
              <input id="payment-ref" value={paymentRef} required
                onChange={(e) => setPaymentRef(e.target.value)}
                placeholder="UTR, UPI reference or receipt number"
                className="w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm" />
            </div>
            <div>
              <label htmlFor="payment-method" className="sr-only">Payment method</label>
              <select id="payment-method" value={paymentMethod}
                onChange={(e) => setPaymentMethod(e.target.value)}
                className="rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm">
                <option value="upi">UPI</option>
                <option value="neft">NEFT / bank transfer</option>
                <option value="cash">Cash</option>
                <option value="offline">Other</option>
              </select>
            </div>
            <button type="submit" disabled={recordPayment.isPending}
              className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-50">
              {recordPayment.isPending ? "Recording…" : "Confirm payment"}
            </button>
          </form>
          {/* A reference is required so this can be matched to a bank
              statement later; the server enforces it too. */}
          <p className="mt-2 text-xs text-primary-900/60">
            The reference is recorded in the audit log against your account.
          </p>
        </Card>
      )}

      {cancelling && (
        <Card className="mb-4">
          <form className="flex flex-col gap-2 sm:flex-row"
            onSubmit={(e) => { e.preventDefault(); act.mutate({ action: "cancel", reason }); }}>
            <label htmlFor="cancel-reason" className="sr-only">Reason for cancelling</label>
            <input id="cancel-reason" value={reason} onChange={(e) => setReason(e.target.value)} required
              placeholder="Why? (recorded in the audit log, and releases held stock)"
              className="flex-1 rounded-card border border-surface-border px-3 py-2 text-sm" />
            <button type="submit" disabled={!reason.trim() || act.isPending}
              className="rounded-card bg-accent-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-50">
              Confirm cancellation
            </button>
          </form>
        </Card>
      )}

      {error && <Card className="mb-4"><p role="alert" className="text-sm text-accent-800">{error}</p></Card>}

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Card>
            <h2 className="mb-3 font-semibold text-primary-900">Items</h2>
            <ul className="space-y-2 text-sm">
              {order.items.map((item) => (
                <li key={item.id} className="flex justify-between gap-4">
                  {/* The grade is part of the identity of the line: an admin
                      chasing a query about "the pomegranate box" needs to know
                      which crate it came out of. */}
                  <span>
                    {gradedName(item.product_name, item.size_code)} × {item.qty} —{" "}
                    {item.unit_label}
                  </span>
                  <span className="tabular-nums">{item.line_total_display}</span>
                </li>
              ))}
            </ul>
            <dl className="mt-4 space-y-1 border-t border-surface-border pt-3 text-sm">
              <Row label="Subtotal" value={order.subtotal_display} />
              <Row label="Platform fee (3%)" value={order.platform_fee_display} />
              <Row label="Delivery charge" value={order.delivery_fee_display} />
              <Row label="Total" value={order.total_display} bold />
            </dl>
          </Card>

          {/* Per-supplier split — computed from the line items, the same source
              the payout rows come from, so the two always agree. */}
          <Card>
            <h2 className="mb-3 font-semibold text-primary-900">Per supplier</h2>
            <ul className="space-y-2 text-sm">
              {supplier_subtotals.map((s) => (
                <li key={s.supplier_id} className="flex justify-between gap-4">
                  <span>{s.business_name || s.supplier_id} ({s.item_count} item{s.item_count === 1 ? "" : "s"})</span>
                  <span className="tabular-nums">{s.amount_display}</span>
                </li>
              ))}
            </ul>
            {payouts.length > 0 && (
              <p className="mt-3 text-xs text-primary-900/60">
                Payout rows: {payouts.map((p) => `${p.business_name || "supplier"} ${p.amount_display} (${p.status})`).join(" · ")}
              </p>
            )}
          </Card>
        </div>

        <div className="space-y-4">
          {/* Terminal orders carry no milestones (CLAUDE.md §6.1) — the whole
              card goes, not just its contents, so there is no empty heading. */}
          {order.milestones && order.milestones.length > 0 && (
            <Card>
              <h2 className="mb-3 font-semibold text-primary-900">Timeline</h2>
              <OrderTimeline
                milestones={order.milestones}
                expectedDeliveryText={order.expected_delivery_text}
                courierNotice={order.courier_notice}
                supportNotice={order.support_notice}
              />
            </Card>
          )}

          <Card>
            <h2 className="mb-2 font-semibold text-primary-900">Delivering to</h2>
            <address className="text-sm not-italic text-primary-900/80">
              {[order.address["recipient_name"], order.address["line1"], order.address["line2"],
                order.address["landmark"],
                `${order.address["city"] ?? ""} ${order.address["pincode"] ?? ""}`.trim(),
                order.address["state"], order.address["phone"]]
                .filter((l) => l && String(l).trim())
                .map((line, i) => <div key={i}>{line}</div>)}
            </address>
          </Card>
        </div>
      </div>
    </>
  );
}

function Row({ label, value, bold }: { label: string; value: string; bold?: boolean }): ReactElement {
  return (
    <div className={`flex justify-between ${bold ? "font-semibold text-primary-900" : ""}`}>
      <dt>{label}</dt>
      <dd className="tabular-nums">{value}</dd>
    </div>
  );
}

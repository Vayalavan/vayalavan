import { useState, type FormEvent, type ReactElement } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, useToast, ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { formatDate } from "../lib/money.js";

interface Supplier {
  id: string;
  business_name: string;
  contact_name: string;
  phone: string;
  email: string;
  city: string | null;
  state: string | null;
  pincode: string | null;
  bank_account_name: string | null;
  status: "pending" | "approved" | "suspended" | "rejected";
  rejection_reason: string | null;
  approved_at: string | null;
  created_at: string;
  /** Masked everywhere except the payouts screen (CLAUDE.md §5.1). */
  bank_account_number: string | null;
  bank_ifsc: string | null;
  /** null means this supplier is on the platform default rate. */
  commission_bps: number | null;
}

interface SupplierPage {
  suppliers: Supplier[];
  total: number;
}

type Tab = "pending" | "approved" | "suspended" | "all";

const TABS: ReadonlyArray<readonly [Tab, string]> = [
  ["pending", "Awaiting approval"],
  ["approved", "Approved"],
  ["suspended", "Suspended"],
  ["all", "All"],
];

/**
 * Suppliers: the approval queue, the directory, and direct onboarding.
 *
 * Two ways a supplier joins (CLAUDE.md §5.1): they apply through the public
 * form and land here as `pending`, or an admin creates them directly —
 * already approved, with a set-password link emailed to them. The second path
 * is how an ops person onboards someone they met at a market, and it is why
 * this screen leads with the queue but always offers "Add supplier".
 */
export function SuppliersPage(): ReactElement {
  // Opens on the full list rather than the approval queue. Applications are
  // occasional, so leading with them showed an empty screen most days; the
  // badge on the "Awaiting approval" tab still surfaces them when they exist.
  const [tab, setTab] = useState<Tab>("all");
  const [showOnboard, setShowOnboard] = useState(false);
  const [editing, setEditing] = useState<Supplier | null>(null);

  const suppliers = useQuery<SupplierPage, ApiError>({
    queryKey: ["admin-suppliers", tab],
    queryFn: () =>
      api.get<SupplierPage>("/admin/suppliers", {
        query: { status: tab === "all" ? undefined : tab, limit: 100 },
      }),
  });

  const pendingCount = useQuery<SupplierPage, ApiError>({
    queryKey: ["admin-suppliers", "pending-count"],
    queryFn: () =>
      api.get<SupplierPage>("/admin/suppliers", { query: { status: "pending", limit: 1 } }),
  });

  return (
    <>
      <PageHeading
        title="Suppliers"
        description="Approve applications, onboard growers directly, and manage access."
        actions={
          <button
            onClick={() => {
              setEditing(null);
              setShowOnboard((open) => !open);
            }}
            className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700"
          >
            {showOnboard ? "Close" : "Add supplier"}
          </button>
        }
      />

      {(showOnboard || editing) && (
        <SupplierForm
          // Keyed so switching which supplier is being edited remounts the
          // form with the new values rather than keeping the previous
          // grower's details in the boxes.
          key={editing?.id ?? "new"}
          editing={editing ?? undefined}
          onDone={() => {
            setShowOnboard(false);
            setEditing(null);
          }}
        />
      )}

      <div role="tablist" aria-label="Filter suppliers" className="mb-4 flex flex-wrap gap-2">
        {TABS.map(([value, label]) => {
          const active = tab === value;
          const badge =
            value === "pending" ? (pendingCount.data?.total ?? 0) : null;
          return (
            <button
              key={value}
              role="tab"
              aria-selected={active}
              onClick={() => setTab(value)}
              className={`rounded-full px-4 py-2 text-sm font-medium ${
                active
                  ? "bg-primary-600 text-white"
                  : "border border-surface-border bg-surface-raised text-primary-800 hover:border-primary-400"
              }`}
            >
              {label}
              {badge ? (
                <span
                  className={`ml-2 rounded-full px-1.5 py-0.5 text-xs ${
                    active ? "bg-white/20" : "bg-accent-100 text-accent-900"
                  }`}
                >
                  {badge}
                </span>
              ) : null}
            </button>
          );
        })}
      </div>

      {suppliers.isPending && <Card>Loading suppliers…</Card>}
      {suppliers.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">
            {suppliers.error.message}
          </p>
        </Card>
      )}

      {suppliers.data && suppliers.data.suppliers.length === 0 && (
        <Card>
          <p className="text-primary-900/70">
            {tab === "pending"
              ? "No applications waiting. Nothing to review."
              : "No suppliers in this category."}
          </p>
        </Card>
      )}

      <div className="grid gap-3">
        {suppliers.data?.suppliers.map((supplier) => (
          <SupplierRow key={supplier.id} supplier={supplier} onEdit={setEditing} />
        ))}
      </div>
    </>
  );
}

const STATUS_STYLES: Record<string, string> = {
  approved: "bg-primary-100 text-primary-800",
  pending: "bg-accent-100 text-accent-900",
  suspended: "bg-secondary-100 text-secondary-800",
  rejected: "bg-surface-sunken text-primary-900/60",
};

function SupplierRow({
  supplier,
  onEdit,
}: {
  supplier: Supplier;
  onEdit: (supplier: Supplier) => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);

  const decide = useMutation<unknown, ApiError, { action: string; reason?: string }>({
    mutationFn: ({ action, reason: why }) =>
      api.post(`/admin/suppliers/${supplier.id}/${action}`, why ? { reason: why } : {}),
    onSuccess: () => {
      setRejecting(false);
      setReason("");
      setError(null);
      void queryClient.invalidateQueries({ queryKey: ["admin-suppliers"] });
    },
    onError: (err) => setError(err.message),
  });

  return (
    <Card>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-semibold text-primary-900">{supplier.business_name}</h2>
            <span
              className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                STATUS_STYLES[supplier.status] ?? ""
              }`}
            >
              {supplier.status}
            </span>
          </div>
          <p className="mt-1 text-sm text-primary-900/70">
            {supplier.contact_name} · {supplier.email} · {supplier.phone}
          </p>
          {(supplier.city || supplier.state) && (
            <p className="text-sm text-primary-900/60">
              {[supplier.city, supplier.state].filter(Boolean).join(", ")}
            </p>
          )}
          <p className="mt-1 text-xs text-primary-900/50">
            Applied {formatDate(supplier.created_at)}
            {supplier.approved_at && ` · approved ${formatDate(supplier.approved_at)}`}
          </p>
          {supplier.rejection_reason && (
            <p className="mt-1 text-xs text-accent-800">
              Rejected: {supplier.rejection_reason}
            </p>
          )}
          {/* Masked — the digits only ever appear on the payouts export. */}
          {supplier.bank_account_number && (
            <p className="mt-1 text-xs text-primary-900/50">
              Bank {supplier.bank_account_number} · {supplier.bank_ifsc}
              {/* Shown only when it differs from the platform rate: printing
                  "default" on every row would be noise, while a negotiated
                  rate is exactly the thing worth noticing at a glance. */}
              {supplier.commission_bps !== null && (
                <>
                  {" · "}
                  <span className="font-medium text-primary-800">
                    {supplier.commission_bps / 100}% commission
                  </span>
                </>
              )}
            </p>
          )}
        </div>

        <div className="flex shrink-0 flex-wrap gap-2">
          {/* Available in every state: a phone number or IFSC can be wrong
              whether or not the grower is approved yet. */}
          <button
            onClick={() => onEdit(supplier)}
            className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70 hover:bg-surface-sunken"
          >
            Edit
          </button>

          {supplier.status === "pending" && (
            <>
              <button
                onClick={() => decide.mutate({ action: "approve" })}
                disabled={decide.isPending}
                className="rounded-card bg-primary-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-700 disabled:opacity-50"
              >
                Approve
              </button>
              <button
                onClick={() => setRejecting((open) => !open)}
                className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70"
              >
                Reject
              </button>
            </>
          )}
          {supplier.status === "approved" && (
            <button
              onClick={() => decide.mutate({ action: "suspend" })}
              disabled={decide.isPending}
              className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70 disabled:opacity-50"
            >
              Suspend
            </button>
          )}
          {(supplier.status === "suspended" || supplier.status === "rejected") && (
            <button
              onClick={() => decide.mutate({ action: "approve" })}
              disabled={decide.isPending}
              className="rounded-card bg-primary-600 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
            >
              Reinstate
            </button>
          )}
        </div>
      </div>

      {rejecting && (
        <form
          className="mt-3 flex flex-col gap-2 sm:flex-row"
          onSubmit={(e) => {
            e.preventDefault();
            decide.mutate({ action: "reject", reason });
          }}
        >
          <label htmlFor={`reason-${supplier.id}`} className="sr-only">
            Reason for rejecting {supplier.business_name}
          </label>
          <input
            id={`reason-${supplier.id}`}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            required
            placeholder="Why is this being rejected? (recorded in the audit log)"
            className="flex-1 rounded-card border border-surface-border px-3 py-2 text-sm"
          />
          <button
            type="submit"
            disabled={decide.isPending || !reason.trim()}
            className="rounded-card bg-accent-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          >
            Confirm rejection
          </button>
        </form>
      )}

      {error && (
        <p role="alert" className="mt-2 text-sm text-accent-800">
          {error}
        </p>
      )}
    </Card>
  );
}

/**
 * Direct onboarding.
 *
 * No password field, deliberately: an admin who chooses a supplier's password
 * knows it. The server issues a one-time set-password link to the supplier's
 * email instead (CLAUDE.md §5.1), so the grower is the only party who ever
 * sets it.
 */
/**
 * Create a supplier, or edit an existing one.
 *
 * One form for both so the two can never validate differently — an edit must
 * not be able to save a value creation would have rejected. The only
 * difference is the verb and where it posts.
 *
 * Editing deliberately cannot change status: approve, reject and suspend are
 * separate audited decisions, and burying them in a details form would let a
 * careless save approve a pending applicant.
 */
function SupplierForm({
  editing,
  onDone,
}: {
  // Explicitly `| undefined` rather than only optional: the project runs with
  // exactOptionalPropertyTypes, so "absent" and "present but undefined" are
  // different types and the caller passes the latter.
  editing?: Supplier | undefined;
  onDone: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const { showToast } = useToast();
  const [fields, setFields] = useState({
    business_name: editing?.business_name ?? "",
    contact_name: editing?.contact_name ?? "",
    phone: editing?.phone ?? "",
    email: editing?.email ?? "",
    city: editing?.city ?? "",
    state: editing?.state ?? "",
    pincode: editing?.pincode ?? "",
    bank_account_name: editing?.bank_account_name ?? "",
    // Never prefilled from the masked value: writing "XXXX1234" back would
    // destroy the real account number. Blank means "leave it as it is" to the
    // admin, and they retype it only when it actually changes.
    bank_account_number: "",
    bank_ifsc: editing?.bank_ifsc ?? "",
    // Held as a percentage string because that is how an admin thinks about
    // it; converted to basis points on submit. Empty means "use the default",
    // which is a different thing from "0".
    commission_percent:
      editing?.commission_bps === null || editing?.commission_bps === undefined
        ? ""
        : String(editing.commission_bps / 100),
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState<string | null>(null);

  /**
   * The payload, with commission converted from percent to basis points.
   *
   * Blank sends null, which the server reads as "use the platform default" —
   * distinct from 0, a real rate meaning we take nothing from this grower.
   * Rounded because 2.5% is 250 bps exactly, but 1/3 of a percent is not an
   * integer and a fractional basis point is not a thing.
   */
  function payload(): Record<string, unknown> {
    const { commission_percent, ...rest } = fields;
    const trimmed = commission_percent.trim();
    return {
      ...rest,
      commission_bps: trimmed === "" ? null : Math.round(Number(trimmed) * 100),
    };
  }

  const create = useMutation<{ business_name: string }, ApiError>({
    mutationFn: () =>
      editing
        ? api.patch(`/admin/suppliers/${editing.id}`, payload())
        : api.post("/admin/suppliers", payload()),
    onSuccess: (saved) => {
      showToast(
        editing
          ? `${saved.business_name} updated.`
          : `${saved.business_name} created and approved. A set-password link has been emailed to them.`,
      );
      setErrors({});
      void queryClient.invalidateQueries({ queryKey: ["admin-suppliers"] });
      // Back to the listing, where the change is visible. Staying on a blank
      // form after a save leaves the admin unsure whether it took.
      onDone();
    },
    onError: (err) => {
      const details = err.details as Record<string, string> | undefined;
      setErrors(details ?? {});
      if (!details) setBanner(err.message);
    },
  });

  function set(key: keyof typeof fields, value: string) {
    setFields((f) => ({ ...f, [key]: value }));
  }

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    setBanner(null);
    create.mutate();
  }

  const input =
    "mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-sm";

  return (
    <Card className="mb-4">
      <h2 className="font-semibold text-primary-900">Onboard a supplier</h2>
      <p className="mt-1 text-sm text-primary-900/70">
        Creates an approved account and emails a one-time link for them to set
        their own password. Bank details are needed before their first payout.
      </p>

      {banner && (
        <p
          role="status"
          className="mt-3 rounded-card bg-primary-50 px-3 py-2 text-sm text-primary-900"
        >
          {banner}
        </p>
      )}

      <form onSubmit={onSubmit} className="mt-4 grid gap-3 sm:grid-cols-2" noValidate>
        {(
          [
            ["business_name", "Business name", true],
            ["contact_name", "Contact person", true],
            ["phone", "Phone", true],
            ["email", "Email", true],
            ["city", "City", false],
            ["state", "State", false],
            ["pincode", "PIN code", false],
            ["bank_account_name", "Bank account name", false],
            ["bank_account_number", "Bank account number", false],
            ["bank_ifsc", "IFSC", false],
          ] as const
        ).map(([key, label, required]) => (
          <div key={key}>
            <label htmlFor={key} className="block text-sm font-medium text-primary-900">
              {label}
              {!required && <span className="text-primary-900/50"> (optional)</span>}
            </label>
            <input
              id={key}
              value={fields[key]}
              onChange={(e) => set(key, e.target.value)}
              aria-invalid={Boolean(errors[key])}
              className={`${input} ${errors[key] ? "border-accent-500" : ""}`}
            />
            {errors[key] && (
              <p role="alert" className="mt-1 text-xs text-accent-800">
                {label} {errors[key]}
              </p>
            )}
          </div>
        ))}

        {/* Not in the loop above: it needs its own units, hint and default
            behaviour, and forcing it into a generic text field would hide all
            three from the person typing a number that decides what a farmer
            is paid. */}
        <div className="sm:col-span-2">
          <label htmlFor="commission_percent" className="block text-sm font-medium text-primary-900">
            Commission <span className="text-primary-900/50">(optional)</span>
          </label>
          <div className="mt-1 flex items-center gap-2">
            <input
              id="commission_percent"
              type="text"
              inputMode="decimal"
              value={fields.commission_percent}
              onChange={(e) => set("commission_percent", e.target.value)}
              placeholder="Platform default"
              aria-describedby="commission-hint"
              aria-invalid={Boolean(errors.commission_bps)}
              className={`w-40 rounded-card border bg-surface-raised px-3 py-2 text-sm ${
                errors.commission_bps ? "border-accent-500" : "border-surface-border"
              }`}
            />
            <span className="text-sm text-primary-900/60">%</span>
          </div>
          <p id="commission-hint" className="mt-1 text-xs text-primary-900/60">
            Deducted from this supplier&rsquo;s payout. Leave blank to use the
            platform rate. Enter 0 to pay them their listed prices in full.
          </p>
          {errors.commission_bps && (
            <p role="alert" className="mt-1 text-xs text-accent-800">
              Commission {errors.commission_bps}
            </p>
          )}
        </div>

        <div className="flex gap-2 sm:col-span-2">
          <button
            type="submit"
            disabled={create.isPending}
            className="rounded-card bg-primary-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-primary-700 disabled:opacity-60"
          >
            {create.isPending
              ? editing
                ? "Saving…"
                : "Creating…"
              : editing
                ? "Save changes"
                : "Create supplier"}
          </button>
          <button
            type="button"
            onClick={onDone}
            className="rounded-card border border-surface-border px-5 py-2.5 text-sm font-medium text-primary-800"
          >
            Cancel
          </button>
        </div>
      </form>
    </Card>
  );
}

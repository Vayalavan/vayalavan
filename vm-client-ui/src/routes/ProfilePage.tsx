/**
 * Profile and address book.
 *
 * Addresses are soft-deleted server-side and orders snapshot them at placement
 * (CLAUDE.md §5.1), so removing one here never rewrites where a past order
 * went. The copy says so, because "delete" otherwise reads as destructive.
 */
import { useState, type ReactElement } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, Skeleton, useToast, type ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { AddressForm, type Address } from "../components/AddressForm.js";

interface Me {
  id: string;
  email: string | null;
  phone: string | null;
  role: string;
  name?: string | null;
}

export function ProfilePage(): ReactElement {
  // The endpoint wraps the identity in { user: … }; unwrapped here so the
  // rest of the component reads the fields directly.
  const me = useQuery<Me, ApiError>({
    queryKey: ["me"],
    queryFn: async () => (await api.get<{ user: Me }>("/me")).user,
  });

  const addresses = useQuery<{ addresses: Address[] }, ApiError>({
    queryKey: ["addresses"],
    queryFn: () => api.get<{ addresses: Address[] }>("/addresses"),
  });

  const [editing, setEditing] = useState<Address | null>(null);
  const [adding, setAdding] = useState(false);

  return (
    <>
      <PageHeading
        title="Your profile"
        description="Your account details and where we deliver."
      />

      <Card className="mb-6">
        <h2 className="font-semibold text-primary-900">Account</h2>
        {me.isPending ? (
          <Skeleton className="mt-3 h-12 w-full" />
        ) : me.isError ? (
          <p role="alert" className="mt-2 text-sm text-accent-800">{me.error.message}</p>
        ) : (
          <dl className="mt-3 grid gap-3 sm:grid-cols-3">
            <div>
              <dt className="text-xs uppercase tracking-wide text-primary-900/60">Name</dt>
              <dd className="text-primary-900">{me.data.name ?? "—"}</dd>
            </div>
            <div>
              <dt className="text-xs uppercase tracking-wide text-primary-900/60">Email</dt>
              <dd className="break-all text-primary-900">{me.data.email ?? "—"}</dd>
            </div>
            <div>
              <dt className="text-xs uppercase tracking-wide text-primary-900/60">Phone</dt>
              <dd className="text-primary-900">{me.data.phone ?? "—"}</dd>
            </div>
          </dl>
        )}
        {/* Honest about scope rather than offering a control that does
            nothing: changing an email or phone is an identity change that
            needs verification, and that flow does not exist yet. */}
        <p className="mt-3 text-xs text-primary-900/60">
          To change your email or phone number, write to us — these identify
          your account, so we verify the change rather than accept it silently.
        </p>
      </Card>

      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-primary-900">Delivery addresses</h2>
        <button
          onClick={() => { setEditing(null); setAdding((open) => !open); }}
          className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700"
        >
          {adding ? "Close" : "Add address"}
        </button>
      </div>

      {(adding || editing) && (
        <AddressForm
          key={editing?.id ?? "new"}
          editing={editing ?? undefined}
          onDone={() => { setAdding(false); setEditing(null); }}
        />
      )}

      {addresses.isPending && <Skeleton className="h-24 w-full" />}

      {addresses.isError && (
        <Card>
          <p role="alert" className="text-sm text-accent-800">{addresses.error.message}</p>
        </Card>
      )}

      {addresses.data && addresses.data.addresses.length === 0 && !adding && (
        <Card>
          <p className="text-primary-900/70">
            No saved addresses yet. Add one so checkout is a single tap.
          </p>
        </Card>
      )}

      <div className="grid gap-3">
        {addresses.data?.addresses.map((address) => (
          <AddressCard key={address.id} address={address} onEdit={setEditing} />
        ))}
      </div>
    </>
  );
}

function AddressCard({
  address,
  onEdit,
}: {
  address: Address;
  onEdit: (a: Address) => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const { showToast } = useToast();
  const [confirming, setConfirming] = useState(false);

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: ["addresses"] });

  const remove = useMutation<unknown, ApiError>({
    mutationFn: () => api.delete(`/addresses/${address.id}`),
    onSuccess: () => { showToast("Address removed."); invalidate(); },
  });

  const makeDefault = useMutation<unknown, ApiError>({
    mutationFn: () => api.post(`/addresses/${address.id}/default`, {}),
    onSuccess: () => { showToast("Default address updated."); invalidate(); },
  });

  return (
    <Card className={address.is_default ? "border-primary-300" : ""}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-primary-900">
              {address.label ?? "Address"}
            </span>
            {address.is_default && (
              <span className="rounded-full bg-primary-100 px-2 py-0.5 text-xs font-medium text-primary-800">
                Default
              </span>
            )}
          </div>
          <address className="mt-1 text-sm not-italic text-primary-900/70">
            <span className="block text-primary-900">{address.recipient_name}</span>
            {[address.line1, address.line2, address.landmark]
              .filter(Boolean)
              .map((part) => <span key={part} className="block">{part}</span>)}
            <span className="block">
              {address.city}, {address.state} {address.pincode}
            </span>
            <span className="block">{address.phone}</span>
          </address>
        </div>

        <div className="flex shrink-0 flex-wrap gap-2">
          {!address.is_default && (
            <button
              onClick={() => makeDefault.mutate()}
              disabled={makeDefault.isPending}
              className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70 disabled:opacity-50"
            >
              Make default
            </button>
          )}
          <button
            onClick={() => onEdit(address)}
            className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70"
          >
            Edit
          </button>
          <button
            onClick={() => setConfirming((open) => !open)}
            className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-accent-800"
          >
            Remove
          </button>
        </div>
      </div>

      {confirming && (
        <div className="mt-3 rounded-card bg-surface-sunken p-3">
          <p className="text-sm text-primary-900/80">
            Remove this address? Orders already placed to it are unaffected —
            they keep their own copy of the address.
          </p>
          <div className="mt-2 flex gap-2">
            <button
              onClick={() => remove.mutate()}
              disabled={remove.isPending}
              className="rounded-card bg-accent-600 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
            >
              {remove.isPending ? "Removing…" : "Yes, remove"}
            </button>
            <button
              onClick={() => setConfirming(false)}
              className="rounded-card border border-surface-border px-3 py-1.5 text-sm text-primary-900/70"
            >
              Keep it
            </button>
          </div>
          {remove.isError && (
            <p role="alert" className="mt-2 text-sm text-accent-800">
              {remove.error.message}
            </p>
          )}
        </div>
      )}
    </Card>
  );
}

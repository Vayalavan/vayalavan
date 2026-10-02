/**
 * The one delivery-address form.
 *
 * Shared by the account page and checkout rather than written twice: the two
 * differ only in what happens after a successful save, and a second copy would
 * be a second place for the delivery-area rule, the field list and the error
 * wiring to drift.
 */
import { useState, type FormEvent, type ReactElement } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Card,
  useToast,
  isServiceablePincode,
  DELIVERY_AREA_LABEL,
  PINCODE_AREA_ERROR,
  type ApiError,
} from "@vayal/ui-kit";
import { api } from "../lib/api.js";

export interface Address {
  id: string;
  label: string | null;
  recipient_name: string;
  phone: string;
  line1: string;
  line2: string | null;
  landmark: string | null;
  city: string;
  state: string;
  pincode: string;
  is_default: boolean;
}

const EMPTY = {
  label: "Home",
  recipient_name: "",
  phone: "",
  line1: "",
  line2: "",
  landmark: "",
  city: "",
  state: "",
  pincode: "",
};

export function AddressForm({
  editing,
  onDone,
  onSaved,
  heading,
  submitLabel,
  framed = true,
}: {
  editing?: Address | undefined;
  /** Called when the customer cancels, and after a successful save. */
  onDone: () => void;
  /**
   * Called with the saved address before onDone. Checkout uses it to select
   * the address that was just added — the customer added it to deliver to it,
   * so making them then pick it off the list would be asking twice.
   */
  onSaved?: (address: Address) => void;
  heading?: string;
  submitLabel?: string;
  /**
   * Whether to draw its own Card. Checkout renders the form INSIDE the
   * delivery-address card, where a second border would read as a second
   * section.
   */
  framed?: boolean;
}): ReactElement {
  const queryClient = useQueryClient();
  const { showToast } = useToast();
  const [fields, setFields] = useState({
    label: editing?.label ?? EMPTY.label,
    recipient_name: editing?.recipient_name ?? "",
    phone: editing?.phone ?? "",
    line1: editing?.line1 ?? "",
    line2: editing?.line2 ?? "",
    landmark: editing?.landmark ?? "",
    city: editing?.city ?? "",
    state: editing?.state ?? "",
    pincode: editing?.pincode ?? "",
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [banner, setBanner] = useState<string | null>(null);

  const save = useMutation<Address, ApiError>({
    mutationFn: () =>
      editing
        ? api.put<Address>(`/addresses/${editing.id}`, fields)
        : api.post<Address>("/addresses", fields),
    onSuccess: (address) => {
      showToast(editing ? "Address updated." : "Address added.");
      void queryClient.invalidateQueries({ queryKey: ["addresses"] });
      onSaved?.(address);
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

  /**
   * The delivery area, checked at the field so the customer is not told after
   * a round trip. vm-profile-api is what actually enforces it.
   *
   * Held back until six digits are typed: complaining about the area while
   * someone is still typing the PIN code is complaining about a prefix.
   */
  const pincodeAreaError =
    fields.pincode.trim().length === 6 && !isServiceablePincode(fields.pincode)
      ? PINCODE_AREA_ERROR
      : undefined;

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    setBanner(null);
    // Already shown against the field; saving would only fetch the same
    // answer from the server.
    if (pincodeAreaError) return;
    save.mutate();
  }

  const body = (
    <>
      <h3 className="font-semibold text-primary-900">
        {heading ?? (editing ? "Edit address" : "New address")}
      </h3>
      <p className="mt-0.5 text-sm text-primary-900/60">
        We deliver in {DELIVERY_AREA_LABEL}.
      </p>

      {banner && (
        <p role="alert" className="mt-2 rounded-card bg-accent-50 px-3 py-2 text-sm text-accent-800">
          {banner}
        </p>
      )}

      <form onSubmit={onSubmit} className="mt-3 grid gap-3 sm:grid-cols-2">
        <Field id="label" label="Label" value={fields.label} error={errors.label}
          onChange={(v) => set("label", v)} placeholder="Home, Office…" />
        <Field id="recipient_name" label="Recipient name" required
          value={fields.recipient_name} error={errors.recipient_name}
          onChange={(v) => set("recipient_name", v)} />
        <Field id="phone" label="Phone" required inputMode="numeric"
          value={fields.phone} error={errors.phone} onChange={(v) => set("phone", v)} />
        <Field id="pincode" label="PIN code" required inputMode="numeric"
          value={fields.pincode} error={pincodeAreaError ?? errors.pincode}
          onChange={(v) => set("pincode", v)} />
        <Field id="line1" label="Address line 1" required className="sm:col-span-2"
          value={fields.line1} error={errors.line1} onChange={(v) => set("line1", v)} />
        <Field id="line2" label="Address line 2" className="sm:col-span-2"
          value={fields.line2} error={errors.line2} onChange={(v) => set("line2", v)} />
        <Field id="landmark" label="Landmark" className="sm:col-span-2"
          value={fields.landmark} error={errors.landmark} onChange={(v) => set("landmark", v)} />
        <Field id="city" label="City" required value={fields.city} error={errors.city}
          onChange={(v) => set("city", v)} />
        <Field id="state" label="State" required value={fields.state} error={errors.state}
          onChange={(v) => set("state", v)} />

        <div className="flex gap-2 sm:col-span-2">
          <button type="submit" disabled={save.isPending}
            className="rounded-card bg-primary-600 px-4 py-2 text-sm font-medium text-white disabled:opacity-50">
            {save.isPending
              ? "Saving…"
              : (submitLabel ?? (editing ? "Save changes" : "Add address"))}
          </button>
          <button type="button" onClick={onDone}
            className="rounded-card border border-surface-border px-4 py-2 text-sm text-primary-900/70">
            Cancel
          </button>
        </div>
      </form>
    </>
  );

  return framed ? <Card className="mb-4">{body}</Card> : <div>{body}</div>;
}

function Field({
  id, label, value, onChange, error, required = false,
  className = "", placeholder = "", inputMode,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  error?: string | undefined;
  required?: boolean;
  className?: string;
  placeholder?: string;
  inputMode?: "numeric" | "text";
}): ReactElement {
  return (
    <div className={className}>
      <label htmlFor={id} className="block text-sm font-medium text-primary-900">
        {label}
        {required && <span aria-hidden="true" className="text-accent-700"> *</span>}
      </label>
      <input
        id={id}
        value={value}
        required={required}
        placeholder={placeholder}
        {...(inputMode ? { inputMode } : {})}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={Boolean(error)}
        aria-describedby={error ? `${id}-error` : undefined}
        className={`mt-1 w-full rounded-card border bg-surface-raised px-3 py-2 text-sm ${
          error ? "border-accent-500 bg-accent-50" : "border-surface-border"
        }`}
      />
      {/* Field-level, from the server's own details map, so a rejection points
          at the box that caused it rather than a banner at the top. */}
      {error && (
        <p id={`${id}-error`} role="alert" className="mt-1 text-xs text-accent-800">
          {label} {error}
        </p>
      )}
    </div>
  );
}

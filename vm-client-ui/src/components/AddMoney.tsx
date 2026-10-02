import { useState, type ReactElement } from "react";
import { useAuth } from "../lib/auth.js";
import { TOPUP_COPY, useTopup, type WalletLimits } from "../lib/wallet.js";
import { formatRupees, rupeesToPaise } from "../lib/walletFormat.js";

/** Amounts offered as one tap. Whole rupees, in paise. */
const PRESETS = [50_000, 100_000, 200_000, 500_000];

/**
 * Adding money to the wallet: a few preset amounts, or any amount typed.
 *
 * `suggestPaise` pre-selects an amount — at checkout, the shortfall between
 * the wallet and the first delivery, rounded up to a whole hundred so the
 * customer is not topping up ₹37.
 */
export function AddMoney({
  limits,
  suggestPaise,
  compact = false,
  onDone,
}: {
  limits: WalletLimits;
  suggestPaise?: number;
  compact?: boolean;
  onDone?: () => void;
}): ReactElement {
  const { user } = useAuth();
  const topup = useTopup(onDone);

  const suggested =
    suggestPaise && suggestPaise > 0
      ? Math.min(
          Math.max(Math.ceil(suggestPaise / 10_000) * 10_000, limits.min_topup_paise),
          limits.max_topup_paise,
        )
      : null;
  const presets = Array.from(new Set([...(suggested ? [suggested] : []), ...PRESETS]))
    .filter((p) => p >= limits.min_topup_paise && p <= limits.max_topup_paise)
    .slice(0, 4);

  const [chosen, setChosen] = useState<number | null>(suggested ?? presets[1] ?? presets[0] ?? null);
  const [custom, setCustom] = useState("");

  const customPaise = custom.trim() === "" ? null : rupeesToPaise(custom);
  const amount = custom.trim() !== "" ? customPaise : chosen;
  const invalid =
    amount === null || amount < limits.min_topup_paise || amount > limits.max_topup_paise;

  const status = TOPUP_COPY[topup.phase];

  return (
    <div>
      <div className={`grid gap-2 ${compact ? "grid-cols-2" : "grid-cols-2 sm:grid-cols-4"}`}>
        {presets.map((preset) => {
          const selected = custom.trim() === "" && chosen === preset;
          return (
            <button
              key={preset}
              type="button"
              onClick={() => {
                setChosen(preset);
                setCustom("");
                topup.reset();
              }}
              aria-pressed={selected}
              className={`flex min-h-[3.25rem] flex-col items-center justify-center rounded-xl border-[1.5px] px-3 py-2 text-sm font-semibold tabular-nums transition ${
                selected
                  ? "border-primary-600 bg-primary-50 text-primary-900 ring-2 ring-primary-600/15"
                  : "border-cream-300 bg-surface-raised text-primary-800 hover:border-primary-300"
              }`}
            >
              <span>{formatRupees(preset)}</span>
              {/* Under the amount, not over the border: a badge on the edge
                  sat on top of the figure it was recommending. */}
              {preset === suggested && (
                <span className="mt-0.5 text-[9px] font-bold uppercase leading-none tracking-wider text-gold-700">
                  Suggested
                </span>
              )}
            </button>
          );
        })}
      </div>

      <label className="mt-3 block">
        <span className="sr-only">Or enter an amount in rupees</span>
        <span className="flex items-center rounded-xl border-[1.5px] border-cream-300 bg-surface-raised px-3 focus-within:border-primary-500">
          <span className="text-sm font-semibold text-primary-900/50">₹</span>
          <input
            inputMode="decimal"
            placeholder={`Other amount (${limits.min_topup_display.replace(".00", "")} – ${limits.max_topup_display.replace(".00", "")})`}
            value={custom}
            onChange={(event) => {
              setCustom(event.target.value);
              topup.reset();
            }}
            className="w-full bg-transparent px-2 py-2.5 text-sm tabular-nums text-primary-900 outline-none placeholder:text-primary-900/40"
          />
        </span>
      </label>
      {custom.trim() !== "" && invalid && (
        <p className="mt-1 text-xs text-accent-800">
          Enter an amount between {limits.min_topup_display} and {limits.max_topup_display}.
        </p>
      )}

      <button
        type="button"
        disabled={invalid || topup.busy}
        onClick={() =>
          amount !== null &&
          topup.addMoney(amount, {
            name: user?.name ?? undefined,
            email: user?.email ?? undefined,
            contact: user?.phone ?? undefined,
          })
        }
        className="mt-3 flex h-12 w-full items-center justify-center rounded-full bg-primary-600 px-5 text-[0.9375rem] font-semibold text-white shadow-[0_8px_24px_-8px] shadow-primary-600/60 transition hover:bg-primary-700 disabled:cursor-not-allowed disabled:bg-surface-sunken disabled:text-primary-900/40 disabled:shadow-none"
      >
        {topup.busy ? "Please wait…" : amount !== null && !invalid ? `Add ${formatRupees(amount)}` : "Add money"}
      </button>

      {status && (
        <p
          role="status"
          className={`mt-2 text-center text-sm ${topup.phase === "done" ? "font-medium text-primary-700" : "text-primary-900/65"}`}
        >
          {status}
        </p>
      )}
      {topup.phase === "error" && topup.message && (
        <p role="alert" className="mt-2 rounded-xl bg-accent-50 px-3 py-2 text-sm text-accent-800">
          {topup.message}
        </p>
      )}
      <p className="mt-2 text-center text-xs text-primary-900/50">
        Paid securely via Razorpay. Your wallet can hold up to {limits.max_balance_display}.
      </p>
    </div>
  );
}

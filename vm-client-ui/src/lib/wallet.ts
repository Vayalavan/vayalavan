/**
 * The prepaid wallet and scheduled orders, client side — CLAUDE.md §6.7.
 *
 * Nothing here moves money. Adding money opens a Razorpay payment; the
 * server's capture webhook is what credits the balance, so after the sheet
 * succeeds this polls the top-up until the server says it is captured —
 * exactly how an order's payment is confirmed (lib/payment.ts).
 */
import { useCallback, useRef, useState } from "react";
import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import type { ApiError } from "@vayal/ui-kit";
import { api } from "./api.js";
import { useAuth } from "./auth.js";
import { config } from "./config.js";
import { openCheckout, type CheckoutSuccess } from "./razorpay.js";
import type { PaymentPrefill } from "./payment.js";

export const WALLET_KEY = ["wallet"] as const;
export const SCHEDULES_KEY = ["schedules"] as const;

export interface WalletTransaction {
  id: string;
  kind: "topup" | "order_debit" | "refund" | "refund_reversal";
  label: string;
  amount_paise: number;
  amount_display: string;
  balance_after_display: string;
  order_id?: string;
  order_number?: string;
  created_at: string;
}

export interface WalletLimits {
  min_topup_paise: number;
  max_topup_paise: number;
  max_balance_paise: number;
  min_topup_display: string;
  max_topup_display: string;
  max_balance_display: string;
}

export interface Wallet {
  balance_paise: number;
  balance_display: string;
  transactions: WalletTransaction[];
  total: number;
  limits: WalletLimits;
}

/**
 * Waits for the session to restore before asking: on a hard load the access
 * token is minted from the refresh cookie a moment after first render, and a
 * request sent before it fails with "authentication required".
 */
export function useWallet(): UseQueryResult<Wallet, ApiError> {
  const { user, initialising } = useAuth();
  return useQuery<Wallet, ApiError>({
    queryKey: WALLET_KEY,
    queryFn: () => api.get<Wallet>("/wallet?limit=50"),
    enabled: !initialising && user !== null,
  });
}

// ---------------------------------------------------------------------------
// Schedules
// ---------------------------------------------------------------------------

export type Frequency = "once" | "daily" | "weekly" | "monthly";

export interface ScheduleItem {
  product_id: string;
  product_unit_id: string;
  product_name: string;
  unit_label: string;
  size_code?: string;
  qty: number;
  available_today: boolean;
  unit_price_display?: string;
  line_total_display?: string;
}

export interface UpcomingDate {
  date: string;
  display: string;
  skipped: boolean;
  locked: boolean;
  charge_at: string;
}

export interface Occurrence {
  date: string;
  display: string;
  status:
    | "placed"
    | "skipped_by_customer"
    | "skipped_no_stock"
    | "skipped_low_balance"
    | "missed";
  order_id?: string;
  order_number?: string;
  total_display?: string;
  note?: string;
}

export interface ScheduleAddress {
  label?: string;
  recipient_name: string;
  line1: string;
  line2?: string;
  city: string;
  state: string;
  pincode: string;
}

export interface Schedule {
  id: string;
  status: "active" | "paused" | "cancelled" | "completed";
  frequency: Frequency;
  weekdays: number[];
  day_of_month?: number;
  summary: string;
  start_date: string;
  end_date?: string;
  next_delivery_date?: string;
  next_delivery_display?: string;
  next_charge_at?: string;
  estimate_paise: number;
  estimate_display: string;
  low_balance_skips: number;
  address: ScheduleAddress;
  items: ScheduleItem[];
  upcoming?: UpcomingDate[];
  history?: Occurrence[];
  created_at: string;
}

export interface ScheduleList {
  schedules: Schedule[];
  wallet_balance_paise: number;
  wallet_balance_display: string;
}

export interface ScheduleOptions {
  earliest_start: string;
  earliest_start_display: string;
  latest_start: string;
  max_end: string;
  charge_lead_minutes: number;
  cutoff_hour_ist: number;
  wallet_balance_paise: number;
  wallet_balance_display: string;
}

export const OCCURRENCE_COPY: Record<Occurrence["status"], string> = {
  placed: "Delivered from your wallet",
  skipped_by_customer: "Skipped by you",
  skipped_no_stock: "Skipped — nothing was in stock",
  skipped_low_balance: "Skipped — wallet balance too low",
  missed: "Not charged",
};

export const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"] as const;

/**
 * "3:30 pm, Tue 29 Sep" — when a date is charged, in IST whatever the
 * browser's zone, because the cutoff is an IST fact.
 */
export function formatChargeAt(iso: string): string {
  return new Intl.DateTimeFormat("en-IN", {
    timeZone: "Asia/Kolkata",
    hour: "numeric",
    minute: "2-digit",
    weekday: "short",
    day: "numeric",
    month: "short",
  }).format(new Date(iso));
}

export { chargeRuleText, rupeesToPaise } from "./walletFormat.js";

// ---------------------------------------------------------------------------
// Adding money
// ---------------------------------------------------------------------------

interface Topup {
  id: string;
  amount_paise: number;
  amount_display: string;
  status: "created" | "captured";
  razorpay_order_id?: string;
  razorpay_key_id?: string;
}

export type TopupPhase = "idle" | "creating" | "opening" | "confirming" | "done" | "slow" | "error";

const POLL_MS = 2000;
const POLL_TIMEOUT_MS = 40000;
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

export interface TopupState {
  phase: TopupPhase;
  message: string | null;
  busy: boolean;
  addMoney(amountPaise: number, prefill?: PaymentPrefill): void;
  reset(): void;
}

/**
 * Adds money: create the top-up, open the sheet, wait for the webhook.
 *
 * One Idempotency-Key per AMOUNT the customer is trying to add, reused on
 * retry, so a double tap opens one payment rather than two.
 */
export function useTopup(onDone?: () => void): TopupState {
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<TopupPhase>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const inFlight = useRef(false);
  const keys = useRef(new Map<number, string>());

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: WALLET_KEY });
    void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
    void queryClient.invalidateQueries({ queryKey: ["schedule-options"] });
  }, [queryClient]);

  const awaitCredit = useCallback(
    async (topupId: string) => {
      const deadline = Date.now() + POLL_TIMEOUT_MS;
      while (Date.now() < deadline) {
        await sleep(POLL_MS);
        try {
          const topup = await api.get<Topup>(`/wallet/topups/${topupId}`);
          if (topup.status === "captured") {
            setPhase("done");
            refresh();
            onDone?.();
            return;
          }
        } catch {
          // Keep polling: one failed read is not a failed payment.
        }
      }
      setPhase("slow");
      refresh();
    },
    [onDone, refresh],
  );

  const addMoney = useCallback(
    (amountPaise: number, prefill: PaymentPrefill = {}) => {
      if (inFlight.current) return;
      inFlight.current = true;
      setPhase("creating");
      setMessage(null);

      let key = keys.current.get(amountPaise);
      if (!key) {
        key = crypto.randomUUID();
        keys.current.set(amountPaise, key);
      }

      void api
        .post<Topup>("/wallet/topups", { amount_paise: amountPaise }, { idempotencyKey: key })
        .then((topup) => {
          if (topup.status === "captured") {
            // A retried key whose payment already landed.
            inFlight.current = false;
            setPhase("done");
            refresh();
            return;
          }
          if (!topup.razorpay_order_id) {
            throw new Error("We could not start the payment. Please try again.");
          }
          setPhase("opening");
          return openCheckout({
            keyId: topup.razorpay_key_id ?? config.razorpayKeyId,
            razorpayOrderId: topup.razorpay_order_id,
            orderNumber: `Wallet top-up ${topup.amount_display}`,
            merchantName: config.appName,
            prefill,
            onSuccess: (response: CheckoutSuccess) => {
              setPhase("confirming");
              // The key is spent once a payment succeeds against it.
              keys.current.delete(amountPaise);
              void api
                .post(`/wallet/topups/${topup.id}/verify`, response)
                .catch(() => undefined)
                .then(() => awaitCredit(topup.id))
                .finally(() => {
                  inFlight.current = false;
                });
            },
            onDismiss: () => {
              inFlight.current = false;
              setPhase("idle");
            },
            onError: (text: string) => {
              inFlight.current = false;
              setPhase("error");
              setMessage(text);
            },
          });
        })
        .catch((error: unknown) => {
          inFlight.current = false;
          setPhase("error");
          setMessage(error instanceof Error ? error.message : "Something went wrong.");
        });
    },
    [awaitCredit, refresh],
  );

  const reset = useCallback(() => {
    setPhase("idle");
    setMessage(null);
  }, []);

  return {
    phase,
    message,
    busy: phase === "creating" || phase === "opening" || phase === "confirming",
    addMoney,
    reset,
  };
}

export const TOPUP_COPY: Partial<Record<TopupPhase, string>> = {
  creating: "Starting your payment…",
  opening: "Opening the payment sheet…",
  confirming: "Confirming your payment with the bank…",
  done: "Money added to your wallet.",
  slow: "Your payment went through. The balance will update in a minute or two.",
};

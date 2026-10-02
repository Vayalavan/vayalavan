/**
 * The prepaid wallet and scheduled orders, on the phone — CLAUDE.md §6.7.
 *
 * The same contract as the web's lib/wallet.ts. Nothing here moves money:
 * adding money opens the Razorpay sheet (the same WebView modal checkout
 * uses), and the server's capture webhook credits the balance, which this
 * polls for.
 */
import { useCallback, useRef, useState } from "react";
import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";

import type { ApiError } from "./api";
import { useAuth } from "./auth";
import { api } from "./client";
import { config } from "./config";
import { newIdempotencyKey } from "./idempotency";
import type { PaymentPrefill } from "./payment";
import type { CheckoutMessage, CheckoutPageOptions } from "./razorpayHtml";

export const WALLET_KEY = ["wallet"] as const;
export const SCHEDULES_KEY = ["schedules"] as const;
export const SCHEDULE_OPTIONS_KEY = ["schedule-options"] as const;

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
  status: "placed" | "skipped_by_customer" | "skipped_no_stock" | "skipped_low_balance" | "missed";
  order_id?: string;
  order_number?: string;
  total_display?: string;
  note?: string;
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
  address: { recipient_name: string; line1: string; city: string; pincode: string };
  items: ScheduleItem[];
  upcoming?: UpcomingDate[];
  history?: Occurrence[];
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

/** Only once signed in: every one of these endpoints is the customer's own. */
export function useWallet(): UseQueryResult<Wallet, ApiError> {
  const { user } = useAuth();
  return useQuery<Wallet, ApiError>({
    queryKey: WALLET_KEY,
    queryFn: () => api.get<Wallet>("/wallet?limit=50"),
    enabled: Boolean(user),
  });
}

/**
 * "Sun 27 Sep, 3:30 pm" — a charging time, in IST by arithmetic, because
 * Hermes ships without full Intl timezone support (see lib/datetime.ts).
 */
export function formatChargeAt(iso: string): string {
  const IST_OFFSET_MS = (5 * 60 + 30) * 60 * 1000;
  const at = new Date(new Date(iso).getTime() + IST_OFFSET_MS);
  const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  const hour24 = at.getUTCHours();
  const hour12 = ((hour24 + 11) % 12) + 1;
  const minutes = String(at.getUTCMinutes()).padStart(2, "0");
  return `${WEEKDAYS[at.getUTCDay()]} ${at.getUTCDate()} ${months[at.getUTCMonth()]}, ${hour12}:${minutes} ${hour24 >= 12 ? "pm" : "am"}`;
}

/** The ISO dates from `from` for `count` days, for the date strip. */
export function isoDatesFrom(from: string, count: number): string[] {
  const [y, m, d] = from.split("-").map(Number);
  const start = Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1);
  return Array.from({ length: count }, (_, i) =>
    new Date(start + i * 86_400_000).toISOString().slice(0, 10),
  );
}

// ---------------------------------------------------------------------------
// Adding money
// ---------------------------------------------------------------------------

interface Topup {
  id: string;
  amount_display: string;
  status: "created" | "captured";
  razorpay_order_id?: string;
  razorpay_key_id?: string;
}

export type TopupPhase = "idle" | "creating" | "opening" | "confirming" | "done" | "slow" | "error";

export const TOPUP_COPY: Partial<Record<TopupPhase, string>> = {
  creating: "Starting your payment…",
  opening: "Opening the payment sheet…",
  confirming: "Confirming your payment with the bank…",
  done: "Money added to your wallet.",
  slow: "Your payment went through. The balance will update in a minute or two.",
};

export interface TopupState {
  phase: TopupPhase;
  message: string | null;
  busy: boolean;
  /** Hand to <RazorpayCheckout request=…>. */
  request: Omit<CheckoutPageOptions, "themeColor"> | null;
  handleResult(message: CheckoutMessage): void;
  addMoney(amountPaise: number, prefill?: PaymentPrefill): void;
  reset(): void;
}

const POLL_MS = 2000;
const POLL_TIMEOUT_MS = 40000;
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

export function useTopup(): TopupState {
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<TopupPhase>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const [request, setRequest] = useState<Omit<CheckoutPageOptions, "themeColor"> | null>(null);
  const current = useRef<Topup | null>(null);
  // One key per amount the customer is trying to add, reused on retry, so a
  // double tap opens one payment rather than two (rule 6).
  const keys = useRef(new Map<number, string>());

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: WALLET_KEY });
    void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
    void queryClient.invalidateQueries({ queryKey: SCHEDULE_OPTIONS_KEY });
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
            return;
          }
        } catch {
          // One failed read is not a failed payment.
        }
      }
      setPhase("slow");
      refresh();
    },
    [refresh],
  );

  const addMoney = useCallback(
    (amountPaise: number, prefill: PaymentPrefill = {}) => {
      if (current.current !== null) return;
      setPhase("creating");
      setMessage(null);
      let key = keys.current.get(amountPaise);
      if (key === undefined) {
        key = newIdempotencyKey();
        keys.current.set(amountPaise, key);
      }
      void api
        .post<Topup>("/wallet/topups", { amount_paise: amountPaise }, { idempotencyKey: key })
        .then((topup) => {
          if (topup.status === "captured") {
            setPhase("done");
            refresh();
            return;
          }
          if (topup.razorpay_order_id === undefined || topup.razorpay_key_id === undefined) {
            throw new Error("We could not start the payment. Please try again.");
          }
          current.current = topup;
          keys.current.delete(amountPaise);
          setPhase("opening");
          setRequest({
            keyId: topup.razorpay_key_id,
            razorpayOrderId: topup.razorpay_order_id,
            orderNumber: `Wallet top-up ${topup.amount_display}`,
            merchantName: config.appName,
            prefill,
          });
        })
        .catch((error: unknown) => {
          setPhase("error");
          setMessage(error instanceof Error ? error.message : "Something went wrong.");
        });
    },
    [refresh],
  );

  const handleResult = useCallback(
    (result: CheckoutMessage) => {
      const topup = current.current;
      setRequest(null);
      current.current = null;
      if (topup === null) return;
      switch (result.type) {
        case "success":
          setPhase("confirming");
          void api
            .post(`/wallet/topups/${topup.id}/verify`, {
              razorpay_order_id: result.razorpay_order_id,
              razorpay_payment_id: result.razorpay_payment_id,
              razorpay_signature: result.razorpay_signature,
            })
            .catch(() => undefined)
            .then(() => awaitCredit(topup.id));
          break;
        case "dismiss":
          setPhase("idle");
          break;
        case "failed":
          setPhase("error");
          setMessage(result.message);
          break;
      }
    },
    [awaitCredit],
  );

  const reset = useCallback(() => {
    current.current = null;
    setRequest(null);
    setPhase("idle");
    setMessage(null);
  }, []);

  return {
    phase,
    message,
    busy: phase === "creating" || phase === "opening" || phase === "confirming",
    request,
    handleResult,
    addMoney,
    reset,
  };
}

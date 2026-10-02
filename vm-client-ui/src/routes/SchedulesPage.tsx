/**
 * Scheduled and repeat orders — CLAUDE.md §6.7.
 *
 * The list says, for each schedule, what arrives when and whether the wallet
 * covers the next one. The detail page is where a customer skips a date,
 * changes quantities, or pauses — each allowed until that date's charging
 * time, which the page states rather than leaving them to guess.
 */
import { useState, type ReactElement } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, PageHeading, Skeleton, gradedName, type ApiError } from "@vayal/ui-kit";
import { api } from "../lib/api.js";
import { useAuth } from "../lib/auth.js";
import {
  OCCURRENCE_COPY,
  SCHEDULES_KEY,
  WALLET_KEY,
  formatChargeAt,
  useWallet,
  type Schedule,
  type ScheduleList,
} from "../lib/wallet.js";
import { AddMoney } from "../components/AddMoney.js";
import { displayDate } from "../lib/walletFormat.js";

const STATUS_STYLE: Record<Schedule["status"], string> = {
  active: "bg-primary-50 text-primary-700 ring-primary-200",
  paused: "bg-gold-50 text-gold-700 ring-gold-200",
  cancelled: "bg-surface-sunken text-primary-900/50 ring-surface-border",
  completed: "bg-surface-sunken text-primary-900/60 ring-surface-border",
};

function StatusBadge({ status }: { status: Schedule["status"] }): ReactElement {
  return (
    <span
      className={`rounded-full px-2.5 py-0.5 text-[11px] font-semibold capitalize ring-1 ${STATUS_STYLE[status]}`}
    >
      {status}
    </span>
  );
}

function RepeatIcon({ className = "h-5 w-5" }: { className?: string }): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={className}>
      <path d="M17 2l4 4-4 4" />
      <path d="M3 11v-1a4 4 0 0 1 4-4h14" />
      <path d="M7 22l-4-4 4-4" />
      <path d="M21 13v1a4 4 0 0 1-4 4H3" />
    </svg>
  );
}

// ---------------------------------------------------------------------------
// /schedules
// ---------------------------------------------------------------------------

export function SchedulesPage(): ReactElement {
  const { user, initialising } = useAuth();
  const list = useQuery<ScheduleList, ApiError>({
    queryKey: SCHEDULES_KEY,
    queryFn: () => api.get<ScheduleList>("/schedules"),
    enabled: !initialising && user !== null,
  });

  if (!initialising && user === null) return <SignInFirst what="your scheduled orders" />;

  return (
    <>
      <PageHeading
        title="Scheduled orders"
        description="Deliveries you have planned ahead — once or on repeat — paid from your wallet."
        actions={
          <Link
            to="/wallet"
            className="rounded-full border border-cream-300 bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800 shadow-card transition hover:border-primary-300"
          >
            Wallet{list.data ? ` · ${list.data.wallet_balance_display}` : ""}
          </Link>
        }
      />

      {list.isPending ? (
        <div className="grid gap-4 md:grid-cols-2">
          <Skeleton className="h-44 w-full rounded-3xl" />
          <Skeleton className="h-44 w-full rounded-3xl" />
        </div>
      ) : list.isError ? (
        <Card>
          <p className="text-sm text-accent-800">{list.error.message}</p>
        </Card>
      ) : list.data.schedules.length === 0 ? (
        <EmptySchedules />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {list.data.schedules.map((schedule) => (
            <ScheduleCard
              key={schedule.id}
              schedule={schedule}
              balancePaise={list.data.wallet_balance_paise}
            />
          ))}
        </div>
      )}
    </>
  );
}

function EmptySchedules(): ReactElement {
  return (
    <div className="mx-auto max-w-xl rounded-3xl border border-cream-300/70 bg-surface-raised p-8 text-center shadow-card">
      <span className="mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-primary-50 text-primary-600">
        <RepeatIcon className="h-7 w-7" />
      </span>
      <h2 className="mt-4 text-2xl font-semibold text-primary-900">Never run out of fruit</h2>
      <p className="mt-2 text-sm text-primary-900/65">
        Fill your cart, then choose <strong>Schedule / Repeat</strong> at checkout. Pick a
        date — or every day, week or month — and each delivery is paid from your wallet.
      </p>
      <Link
        to="/"
        className="mt-5 inline-flex rounded-full bg-primary-600 px-5 py-2.5 text-sm font-semibold text-white shadow-card hover:bg-primary-700"
      >
        Browse today&rsquo;s produce
      </Link>
    </div>
  );
}

function ScheduleCard({
  schedule,
  balancePaise,
}: {
  schedule: Schedule;
  balancePaise: number;
}): ReactElement {
  const live = schedule.status === "active";
  const short = live && schedule.estimate_paise > balancePaise;

  return (
    <Link
      to={`/schedules/${schedule.id}`}
      className="group flex flex-col rounded-3xl border border-cream-300/70 bg-surface-raised p-5 shadow-card transition hover:-translate-y-0.5 hover:shadow-lift"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-3">
          <span className="flex h-10 w-10 items-center justify-center rounded-full bg-primary-50 text-primary-600">
            <RepeatIcon />
          </span>
          <div>
            <p className="font-display text-lg font-semibold leading-tight text-primary-900">
              {schedule.summary}
            </p>
            <p className="text-xs text-primary-900/55">
              {schedule.items.length} item{schedule.items.length === 1 ? "" : "s"} · to{" "}
              {schedule.address.recipient_name}, {schedule.address.city}
            </p>
          </div>
        </div>
        <StatusBadge status={schedule.status} />
      </div>

      <p className="mt-4 line-clamp-2 text-sm text-primary-900/70">
        {schedule.items
          .map((item) => `${gradedName(item.product_name, item.size_code)} × ${item.qty}`)
          .join(", ")}
      </p>

      <div className="mt-4 flex items-end justify-between gap-3 border-t border-cream-200 pt-4">
        <div>
          <p className="text-xs uppercase tracking-wide text-primary-900/45">Next delivery</p>
          <p className="text-sm font-semibold text-primary-900">
            {live ? (schedule.next_delivery_display ?? "—") : "—"}
          </p>
        </div>
        <div className="text-right">
          <p className="text-xs uppercase tracking-wide text-primary-900/45">About</p>
          <p className="text-sm font-semibold tabular-nums text-primary-900">
            {schedule.estimate_display}
          </p>
        </div>
      </div>

      {short && (
        <p className="mt-3 rounded-xl bg-gold-50 px-3 py-2 text-xs font-medium text-gold-700 ring-1 ring-gold-200">
          Your wallet won&rsquo;t cover the next delivery. Top up before{" "}
          {schedule.next_charge_at ? formatChargeAt(schedule.next_charge_at) : "it is charged"}.
        </p>
      )}
    </Link>
  );
}

// ---------------------------------------------------------------------------
// /schedules/:id
// ---------------------------------------------------------------------------

export function ScheduleDetailPage(): ReactElement {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const wallet = useWallet();
  const [draft, setDraft] = useState<Record<string, number> | null>(null);
  const [confirmCancel, setConfirmCancel] = useState(false);
  const { user, initialising } = useAuth();

  const key = ["schedule", id];
  const detail = useQuery<Schedule, ApiError>({
    queryKey: key,
    queryFn: () => api.get<Schedule>(`/schedules/${id}`),
    enabled: !initialising && user !== null,
  });

  const refresh = (updated?: Schedule): void => {
    if (updated) queryClient.setQueryData(key, updated);
    void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
  };

  const action = useMutation<Schedule, ApiError, "pause" | "resume">({
    mutationFn: (verb) => api.post<Schedule>(`/schedules/${id}/${verb}`, {}),
    onSuccess: refresh,
  });
  const cancel = useMutation<unknown, ApiError>({
    mutationFn: () => api.post(`/schedules/${id}/cancel`, {}),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: SCHEDULES_KEY });
      navigate("/schedules");
    },
  });
  const skip = useMutation<Schedule, ApiError, { date: string; skipped: boolean }>({
    mutationFn: ({ date, skipped }) =>
      skipped
        ? api.delete<Schedule>(`/schedules/${id}/skip/${date}`)
        : api.post<Schedule>(`/schedules/${id}/skip`, { delivery_date: date }),
    onSuccess: refresh,
  });
  const save = useMutation<Schedule, ApiError, Record<string, number>>({
    mutationFn: (quantities) =>
      api.patch<Schedule>(`/schedules/${id}`, {
        items: Object.entries(quantities).map(([product_unit_id, qty]) => ({ product_unit_id, qty })),
      }),
    onSuccess: (updated) => {
      setDraft(null);
      refresh(updated);
    },
  });

  if (!initialising && user === null) return <SignInFirst what="this schedule" />;
  if (detail.isPending) {
    return <Skeleton className="h-96 w-full rounded-3xl" />;
  }
  if (detail.isError) {
    return (
      <Card>
        <h1 className="font-semibold text-primary-900">
          {detail.error.status === 404 ? "Schedule not found" : "Something went wrong"}
        </h1>
        <Link to="/schedules" className="mt-3 inline-block text-sm text-primary-700 underline">
          Back to scheduled orders
        </Link>
      </Card>
    );
  }

  const schedule = detail.data;
  const open = schedule.status === "active" || schedule.status === "paused";
  const balance = wallet.data?.balance_paise ?? 0;
  const short = schedule.status === "active" && schedule.estimate_paise > balance;
  const quantities = draft ?? Object.fromEntries(schedule.items.map((i) => [i.product_unit_id, i.qty]));
  const dirty = draft !== null;
  const error = action.error ?? skip.error ?? save.error ?? cancel.error;

  return (
    <>
      <nav aria-label="Breadcrumb" className="mb-4 flex items-center gap-1.5 text-sm">
        <Link to="/schedules" className="text-primary-900/60 hover:text-primary-700">
          Scheduled orders
        </Link>
        <span className="text-primary-900/35">›</span>
        <span className="font-medium text-primary-900">{schedule.summary}</span>
      </nav>

      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <div className="space-y-5">
          {/* Summary */}
          <section className="rounded-3xl border border-cream-300/70 bg-surface-raised p-5 shadow-card sm:p-6">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2">
                  <StatusBadge status={schedule.status} />
                  <span className="text-xs text-primary-900/50">
                    From {displayDate(schedule.start_date)}
                    {schedule.end_date ? ` to ${displayDate(schedule.end_date)}` : ""}
                  </span>
                </div>
                <h1 className="mt-2 text-2xl font-semibold text-primary-900 sm:text-3xl">
                  {schedule.summary}
                </h1>
                <p className="mt-1 text-sm text-primary-900/60">
                  To {schedule.address.recipient_name}, {schedule.address.line1},{" "}
                  {schedule.address.city} {schedule.address.pincode}
                </p>
              </div>
              {open && (
                <div className="flex gap-2">
                  <button
                    type="button"
                    disabled={action.isPending}
                    onClick={() => action.mutate(schedule.status === "active" ? "pause" : "resume")}
                    className="rounded-full border border-cream-300 bg-surface-raised px-4 py-2 text-sm font-medium text-primary-800 transition hover:border-primary-300 disabled:opacity-50"
                  >
                    {schedule.status === "active" ? "Pause" : "Resume"}
                  </button>
                  <button
                    type="button"
                    onClick={() => setConfirmCancel(true)}
                    className="rounded-full px-4 py-2 text-sm font-medium text-accent-800 transition hover:bg-accent-50"
                  >
                    Cancel
                  </button>
                </div>
              )}
            </div>

            {confirmCancel && (
              <div className="mt-4 rounded-2xl border border-accent-200 bg-accent-50 p-4">
                <p className="text-sm font-medium text-accent-800">
                  Cancel this schedule? No further deliveries will be charged. Orders
                  already placed are not affected.
                </p>
                <div className="mt-3 flex gap-2">
                  <button
                    type="button"
                    disabled={cancel.isPending}
                    onClick={() => cancel.mutate()}
                    className="rounded-full bg-accent-700 px-4 py-2 text-sm font-semibold text-white disabled:opacity-50"
                  >
                    Yes, cancel it
                  </button>
                  <button
                    type="button"
                    onClick={() => setConfirmCancel(false)}
                    className="rounded-full px-4 py-2 text-sm font-medium text-primary-800"
                  >
                    Keep it
                  </button>
                </div>
              </div>
            )}

            {schedule.status === "paused" && schedule.low_balance_skips > 0 && (
              <p className="mt-4 rounded-xl bg-gold-50 px-3 py-2 text-sm text-gold-700 ring-1 ring-gold-200">
                Paused after deliveries were skipped for a low wallet balance. Top up, then resume.
              </p>
            )}
            {error && (
              <p role="alert" className="mt-4 rounded-xl bg-accent-50 px-3 py-2 text-sm text-accent-800">
                {error.message}
              </p>
            )}
          </section>

          {/* Items */}
          <section className="rounded-3xl border border-cream-300/70 bg-surface-raised p-5 shadow-card sm:p-6">
            <div className="flex items-center justify-between">
              <h2 className="text-lg font-semibold text-primary-900">Each delivery</h2>
              <p className="text-sm text-primary-900/60">
                About <span className="font-semibold text-primary-900">{schedule.estimate_display}</span>
              </p>
            </div>
            <ul className="mt-3 divide-y divide-cream-200">
              {schedule.items.map((item) => {
                const qty = quantities[item.product_unit_id] ?? item.qty;
                return (
                  <li key={item.product_unit_id} className="flex items-center gap-3 py-3">
                    <span className="min-w-0 flex-1">
                      <span className={`block truncate text-sm font-medium ${qty === 0 ? "text-primary-900/40 line-through" : "text-primary-900"}`}>
                        {gradedName(item.product_name, item.size_code)}
                      </span>
                      <span className="block text-xs text-primary-900/55">
                        {item.unit_label}
                        {item.unit_price_display ? ` · ${item.unit_price_display} today` : ""}
                        {!item.available_today && " · not in stock today"}
                      </span>
                    </span>
                    {open ? (
                      <span className="flex items-center gap-1 rounded-full border border-cream-300 p-0.5">
                        <button
                          type="button"
                          aria-label={`Fewer ${item.product_name}`}
                          disabled={qty === 0}
                          onClick={() => setDraft({ ...quantities, [item.product_unit_id]: Math.max(0, qty - 1) })}
                          className="h-8 w-8 rounded-full text-lg text-primary-800 hover:bg-primary-50 disabled:opacity-30"
                        >
                          −
                        </button>
                        <span className="w-6 text-center text-sm font-semibold tabular-nums">{qty}</span>
                        <button
                          type="button"
                          aria-label={`More ${item.product_name}`}
                          disabled={qty >= 99}
                          onClick={() => setDraft({ ...quantities, [item.product_unit_id]: qty + 1 })}
                          className="h-8 w-8 rounded-full text-lg text-primary-800 hover:bg-primary-50 disabled:opacity-30"
                        >
                          +
                        </button>
                      </span>
                    ) : (
                      <span className="text-sm font-semibold">× {qty}</span>
                    )}
                  </li>
                );
              })}
            </ul>
            {dirty && (
              <div className="mt-3 flex items-center justify-end gap-2">
                <button type="button" onClick={() => setDraft(null)} className="rounded-full px-4 py-2 text-sm text-primary-800">
                  Discard
                </button>
                <button
                  type="button"
                  disabled={save.isPending}
                  onClick={() => save.mutate(quantities)}
                  className="rounded-full bg-primary-600 px-5 py-2 text-sm font-semibold text-white hover:bg-primary-700 disabled:opacity-50"
                >
                  {save.isPending ? "Saving…" : "Save changes"}
                </button>
              </div>
            )}
            <p className="mt-3 text-xs text-primary-900/50">
              Each delivery is charged at that day&rsquo;s prices, for what is in stock. Anything
              out of stock is left out and not charged.
            </p>
          </section>

          {/* History */}
          {schedule.history && schedule.history.length > 0 && (
            <section className="rounded-3xl border border-cream-300/70 bg-surface-raised p-5 shadow-card sm:p-6">
              <h2 className="text-lg font-semibold text-primary-900">Past deliveries</h2>
              <ul className="mt-3 divide-y divide-cream-200">
                {schedule.history.map((entry) => (
                  <li key={entry.date} className="py-3 text-sm">
                    <div className="flex items-center justify-between gap-3">
                      <span className="font-medium text-primary-900">{entry.display}</span>
                      {entry.order_id ? (
                        <Link to={`/orders/${entry.order_id}`} className="text-primary-700 hover:underline">
                          {entry.order_number} · {entry.total_display}
                        </Link>
                      ) : (
                        <span className="text-primary-900/55">{OCCURRENCE_COPY[entry.status]}</span>
                      )}
                    </div>
                    {entry.note && <p className="mt-1 text-xs text-gold-700">{entry.note}</p>}
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>

        {/* Upcoming dates + wallet */}
        <div className="space-y-4 lg:sticky lg:top-24">
          {open && schedule.upcoming && schedule.upcoming.length > 0 && (
            <section className="rounded-3xl border border-cream-300/70 bg-surface-raised p-5 shadow-card">
              <h2 className="text-lg font-semibold text-primary-900">Coming up</h2>
              <p className="mt-1 text-xs text-primary-900/55">
                Skip a date any time before it is charged.
              </p>
              <ul className="mt-3 space-y-2">
                {schedule.upcoming.map((date) => (
                  <li
                    key={date.date}
                    className={`flex items-center justify-between gap-3 rounded-2xl border px-3 py-2.5 ${
                      date.skipped ? "border-dashed border-cream-300 bg-cream-50" : "border-cream-200 bg-surface-raised"
                    }`}
                  >
                    <span>
                      <span className={`block text-sm font-semibold ${date.skipped ? "text-primary-900/40 line-through" : "text-primary-900"}`}>
                        {date.display}
                      </span>
                      <span className="block text-[11px] text-primary-900/50">
                        {date.locked
                          ? "Being packed — can't change now"
                          : `Charged ${formatChargeAt(date.charge_at)}`}
                      </span>
                    </span>
                    {!date.locked && (
                      <button
                        type="button"
                        disabled={skip.isPending}
                        onClick={() => skip.mutate({ date: date.date, skipped: date.skipped })}
                        className={`rounded-full px-3 py-1 text-xs font-semibold transition disabled:opacity-50 ${
                          date.skipped
                            ? "bg-primary-600 text-white hover:bg-primary-700"
                            : "border border-cream-300 text-primary-800 hover:border-primary-300"
                        }`}
                      >
                        {date.skipped ? "Undo skip" : "Skip"}
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </section>
          )}

          {wallet.data && open && (
            <section className="rounded-3xl bg-primary-900 p-5 text-white shadow-lift">
              <p className="text-xs font-semibold uppercase tracking-[0.18em] text-gold-200">Wallet</p>
              <p className="mt-1 font-display text-3xl font-semibold tabular-nums">
                {wallet.data.balance_display}
              </p>
              <p className="mt-1 text-xs text-white/60">
                {short
                  ? `Add ${formatShortfall(schedule.estimate_paise - balance)} or more to cover the next delivery.`
                  : "Covers the next delivery."}
              </p>
            </section>
          )}
          {wallet.data && short && (
            <Card className="!rounded-3xl !border-cream-300/70">
              <AddMoney
                limits={wallet.data.limits}
                suggestPaise={schedule.estimate_paise - balance}
                compact
                onDone={() => void queryClient.invalidateQueries({ queryKey: WALLET_KEY })}
              />
            </Card>
          )}
        </div>
      </div>
    </>
  );
}

export function SignInFirst({ what }: { what: string }): ReactElement {
  return (
    <Card className="mx-auto max-w-md text-center">
      <h1 className="text-xl font-semibold text-primary-900">Sign in to see {what}</h1>
      <Link
        to="/account"
        className="mt-4 inline-flex rounded-full bg-primary-600 px-5 py-2.5 text-sm font-semibold text-white hover:bg-primary-700"
      >
        Sign in
      </Link>
    </Card>
  );
}

function formatShortfall(paise: number): string {
  return `₹${new Intl.NumberFormat("en-IN").format(Math.ceil(paise / 100))}`;
}

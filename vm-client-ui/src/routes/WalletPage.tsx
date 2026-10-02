/**
 * The wallet — CLAUDE.md §6.7.
 *
 * The balance, a way to add to it, and every movement of it. Scheduled and
 * repeat deliveries are paid from here at each one's charging time; the page
 * says that plainly because it is the one thing a customer must understand
 * about why they are putting money in.
 */
import type { ReactElement } from "react";
import { Link } from "react-router-dom";
import { Card, Skeleton } from "@vayal/ui-kit";
import { AddMoney } from "../components/AddMoney.js";
import { useWallet, type WalletTransaction } from "../lib/wallet.js";
import { useAuth } from "../lib/auth.js";
import { SignInFirst } from "./SchedulesPage.js";

export function WalletPage(): ReactElement {
  const wallet = useWallet();
  const { user, initialising } = useAuth();

  if (!initialising && user === null) return <SignInFirst what="your wallet" />;
  if (wallet.isPending) {
    return (
      <div className="grid gap-6 lg:grid-cols-[1fr_380px]">
        <Skeleton className="h-48 w-full rounded-3xl" />
        <Skeleton className="h-72 w-full rounded-3xl" />
      </div>
    );
  }
  if (wallet.isError) {
    return (
      <Card>
        <h1 className="font-semibold text-primary-900">We could not load your wallet</h1>
        <p className="mt-1 text-sm text-primary-900/70">{wallet.error.message}</p>
      </Card>
    );
  }

  const data = wallet.data;

  return (
    <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_380px]">
      <div className="space-y-6">
        {/* The balance, as the page's hero. */}
        <section className="relative overflow-hidden rounded-3xl bg-primary-900 p-6 text-white shadow-lift sm:p-8">
          <div
            aria-hidden="true"
            className="pointer-events-none absolute -right-16 -top-20 h-64 w-64 rounded-full bg-gold-500/20 blur-3xl"
          />
          <div
            aria-hidden="true"
            className="pointer-events-none absolute -bottom-24 -left-10 h-64 w-64 rounded-full bg-primary-500/30 blur-3xl"
          />
          <p className="relative text-xs font-semibold uppercase tracking-[0.2em] text-gold-200">
            Vayalavan wallet
          </p>
          <p className="relative mt-3 font-display text-4xl font-semibold tabular-nums tracking-tight sm:text-5xl">
            {data.balance_display}
          </p>
          <p className="relative mt-1 text-sm text-white/65">Available balance</p>
          <div className="relative mt-6 flex flex-wrap gap-2">
            <Link
              to="/schedules"
              className="rounded-full bg-white/10 px-4 py-2 text-sm font-medium text-white ring-1 ring-white/15 transition hover:bg-white/15"
            >
              Your scheduled orders
            </Link>
          </div>
        </section>

        <Card className="!rounded-3xl !border-cream-300/70">
          <h2 className="text-lg font-semibold text-primary-900">Activity</h2>
          {data.transactions.length === 0 ? (
            <p className="mt-2 text-sm text-primary-900/60">
              No activity yet. Money you add, and deliveries paid from the wallet, appear here.
            </p>
          ) : (
            <ul className="mt-3 divide-y divide-cream-200">
              {data.transactions.map((entry) => (
                <TransactionRow key={entry.id} entry={entry} />
              ))}
            </ul>
          )}
        </Card>
      </div>

      <div className="space-y-4 lg:sticky lg:top-24">
        <Card className="!rounded-3xl !border-cream-300/70">
          <h2 className="text-lg font-semibold text-primary-900">Add money</h2>
          <p className="mb-4 mt-1 text-sm text-primary-900/60">
            Top up once, and your scheduled deliveries pay for themselves.
          </p>
          <AddMoney limits={data.limits} />
        </Card>

        <div className="rounded-3xl border border-cream-300/70 bg-cream-50/80 p-5 text-sm text-primary-900/75">
          <h2 className="font-sans text-xs font-semibold uppercase tracking-[0.18em] text-primary-700">
            How the wallet works
          </h2>
          <ul className="mt-3 space-y-2.5">
            <li>
              <strong className="text-primary-900">Charged per delivery.</strong> Each scheduled
              delivery is paid on the afternoon two days before it arrives, at that
              day&rsquo;s prices.
            </li>
            <li>
              <strong className="text-primary-900">Only what we send.</strong> If something is out
              of stock, you are charged only for what is packed.
            </li>
            <li>
              <strong className="text-primary-900">Your money stays yours.</strong> Unused balance
              can be refunded to your original payment method — just write to us.
            </li>
          </ul>
        </div>
      </div>
    </div>
  );
}

function TransactionRow({ entry }: { entry: WalletTransaction }): ReactElement {
  const credit = entry.amount_paise > 0;
  const when = new Intl.DateTimeFormat("en-IN", {
    timeZone: "Asia/Kolkata",
    day: "numeric",
    month: "short",
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(entry.created_at));

  return (
    <li className="flex items-center gap-3 py-3">
      <span
        aria-hidden="true"
        className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-sm font-bold ${
          credit ? "bg-primary-50 text-primary-700" : "bg-cream-200 text-secondary-700"
        }`}
      >
        {credit ? "+" : "−"}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium text-primary-900">
          {entry.order_id ? (
            <Link to={`/orders/${entry.order_id}`} className="hover:underline">
              {entry.label}
            </Link>
          ) : (
            entry.label
          )}
        </span>
        <span className="block text-xs text-primary-900/50">
          {when} · Balance {entry.balance_after_display}
        </span>
      </span>
      <span
        className={`shrink-0 text-sm font-semibold tabular-nums ${
          credit ? "text-primary-700" : "text-primary-900"
        }`}
      >
        {credit ? "+" : ""}
        {entry.amount_display}
      </span>
    </li>
  );
}

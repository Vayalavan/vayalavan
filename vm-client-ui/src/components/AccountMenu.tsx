/**
 * The account control: a user icon that opens a menu.
 *
 * Opens on hover AND on click, deliberately. Hover alone would make it
 * unreachable on every phone and to anyone navigating by keyboard — and this
 * menu is the only route to orders and addresses, so "hover-only" would mean
 * "unreachable on mobile", on a storefront that is mobile-first.
 */
import { useEffect, useRef, useState, type ReactElement } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import { useAuth } from "../lib/auth.js";

/** Delay before a hover-out closes the menu, so a diagonal mouse path to the
 *  first item does not dismiss it mid-travel. */
const CLOSE_DELAY_MS = 180;

const ITEMS: ReadonlyArray<readonly [string, string]> = [
  ["/orders", "Your orders"],
  ["/schedules", "Scheduled orders"],
  ["/wallet", "Wallet"],
  ["/profile", "Profile & addresses"],
];

export function AccountMenu(): ReactElement {
  const { user, signOut, initialising } = useAuth();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const wrapper = useRef<HTMLDivElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  function cancelClose(): void {
    if (closeTimer.current) {
      clearTimeout(closeTimer.current);
      closeTimer.current = null;
    }
  }

  function scheduleClose(): void {
    cancelClose();
    closeTimer.current = setTimeout(() => setOpen(false), CLOSE_DELAY_MS);
  }

  useEffect(() => cancelClose, []);

  // Escape closes, and a click anywhere else dismisses — a menu that can only
  // be closed by finding the button again is a trap on a touch screen.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onPointer = (e: MouseEvent) => {
      if (!wrapper.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onPointer);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onPointer);
    };
  }, [open]);

  if (initialising) {
    return <span className="text-sm text-primary-900/40">…</span>;
  }

  if (!user) {
    return (
      <NavLink
        to="/account"
        className="rounded-full bg-primary-900 px-5 py-2 text-sm font-medium text-cream-50 shadow-card transition hover:bg-primary-800"
      >
        Sign in
      </NavLink>
    );
  }

  const displayName = user.name ?? user.email ?? "Your account";
  const initial = (user.name ?? user.email ?? "?").trim().charAt(0).toUpperCase();

  return (
    <div
      ref={wrapper}
      className="relative"
      onMouseEnter={() => { cancelClose(); setOpen(true); }}
      onMouseLeave={scheduleClose}
      // Keyboard focus moving anywhere outside closes it, so tabbing past the
      // menu does not leave it hanging open over the page.
      onFocus={() => { cancelClose(); setOpen(true); }}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setOpen(false);
      }}
    >
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-expanded={open}
        aria-haspopup="menu"
        aria-label={`Account menu for ${displayName}`}
        className="flex items-center gap-2 rounded-full border border-cream-300 bg-surface-raised py-1 pl-1 pr-3 text-sm font-medium text-primary-800 shadow-card transition hover:border-primary-300"
      >
        {/* The initial rather than a generic silhouette: it confirms WHICH
            account is signed in, which matters on a shared family phone. */}
        <span
          aria-hidden="true"
          className="flex h-7 w-7 items-center justify-center rounded-full bg-primary-600 text-xs font-semibold text-white"
        >
          {initial}
        </span>
        <span className="hidden max-w-[10rem] truncate sm:inline">{displayName}</span>
        <svg viewBox="0 0 12 12" aria-hidden="true" className="h-3 w-3" fill="none">
          <path d="m2.5 4.5 3.5 3.5 3.5-3.5" stroke="currentColor" strokeWidth="1.5"
            strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>

      {open && (
        <div
          role="menu"
          aria-label="Account"
          className="absolute right-0 top-full z-50 mt-2 w-56 overflow-hidden rounded-2xl border border-cream-300 bg-surface-raised py-1 shadow-lift"
        >
          <p className="border-b border-surface-border px-4 py-2 text-xs text-primary-900/60">
            Signed in as{" "}
            <span className="block truncate font-medium text-primary-900">
              {user.email ?? user.name}
            </span>
          </p>

          {ITEMS.map(([to, label]) => (
            <NavLink
              key={to}
              to={to}
              role="menuitem"
              onClick={() => setOpen(false)}
              className="block px-4 py-2 text-sm text-primary-900 hover:bg-primary-50"
            >
              {label}
            </NavLink>
          ))}

          <button
            role="menuitem"
            onClick={() => {
              setOpen(false);
              void signOut();
              // Home rather than the current page: half the menu's
              // destinations require a session, and staying would land on an
              // error the user just caused themselves.
              navigate("/");
            }}
            className="block w-full border-t border-surface-border px-4 py-2 text-left text-sm text-primary-900 hover:bg-primary-50"
          >
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}

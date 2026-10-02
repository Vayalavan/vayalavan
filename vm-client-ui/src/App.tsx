import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Routes, Route, NavLink, Navigate } from "react-router-dom";
import { AppShell, ToastProvider } from "@vayal/ui-kit";
import type { ReactElement } from "react";
import { AuthProvider } from "./lib/auth.js";
import { CatalogPage } from "./routes/CatalogPage.js";
import { AuthPage } from "./routes/AuthPage.js";
import { CutoffBanner } from "./components/CutoffBanner.js";
import { CartProvider } from "./lib/cart.js";
import { CartDrawer } from "./components/CartDrawer.js";
import { CartButton } from "./components/CartButton.js";
import { AccountMenu } from "./components/AccountMenu.js";
import { ProductPage } from "./routes/ProductPage.js";
import { OrdersPage, OrderDetailPage } from "./routes/OrdersPage.js";
import { ProfilePage } from "./routes/ProfilePage.js";
import { WalletPage } from "./routes/WalletPage.js";
import { SchedulesPage, ScheduleDetailPage } from "./routes/SchedulesPage.js";
import { CheckoutPage } from "./routes/CheckoutPage.js";
import {
  AboutPage, ContactPage, TermsPage, PrivacyPage, ShippingPage, RefundPage,
} from "./routes/StaticPages.js";
import { config } from "./lib/config.js";

/**
 * Created once at module scope: a QueryClient rebuilt on every render would
 * discard the cache on each state change.
 */
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Stock moves as other customers buy, so this must not go stale for long.
      staleTime: 20_000,
      refetchOnWindowFocus: true,
      retry: (failureCount, error) => {
        const status = (error as { status?: number }).status ?? 0;
        // A 401/403/404 will not resolve itself by asking again.
        if (status === 401 || status === 403 || status === 404) return false;
        // Nor will a 429 — retrying "too many requests" sends more requests,
        // turning one rate-limited call into three and extending the block.
        if (status === 429) return false;
        return failureCount < 2;
      },
    },
  },
});

/** Reachable from every page — a Razorpay activation requirement. */
const POLICY_LINKS: ReadonlyArray<readonly [string, string]> = [
  ["/about", "About"],
  ["/contact", "Contact"],
  ["/terms", "Terms"],
  ["/privacy", "Privacy"],
  ["/shipping", "Shipping & Delivery"],
  ["/refunds", "Refunds & Cancellation"],
];

function navLinkClass({ isActive }: { isActive: boolean }): string {
  return `rounded-full px-4 py-2 text-sm font-medium transition ${
    isActive
      ? "bg-primary-900 text-cream-50 shadow-card"
      : "text-primary-800 hover:bg-cream-200/70"
  }`;
}

function Storefront(): ReactElement {
  return (
    <AppShell
      appLabel="Shop"
      storefront
      supportEmail={config.supportEmail}
      nav={
        <NavLink to="/" className={navLinkClass} end>
          Today&rsquo;s produce
        </NavLink>
      }
      actions={
        <>
          <CartButton />
          <AccountMenu />
        </>
      }
      banner={<CutoffBanner />}
      footerLinks={
        <>
          {POLICY_LINKS.map(([to, label]) => (
            <NavLink
              key={to}
              to={to}
              className="transition hover:text-white"
            >
              {label}
            </NavLink>
          ))}
        </>
      }
    >
      <Routes>
        <Route path="/" element={<CatalogPage />} />
        <Route path="/about" element={<AboutPage />} />
        <Route path="/contact" element={<ContactPage />} />
        <Route path="/terms" element={<TermsPage />} />
        <Route path="/privacy" element={<PrivacyPage />} />
        <Route path="/shipping" element={<ShippingPage />} />
        <Route path="/refunds" element={<RefundPage />} />
        <Route path="/product/:id" element={<ProductPage />} />
        <Route path="/checkout" element={<CheckoutPage />} />
        <Route path="/orders" element={<OrdersPage />} />
        <Route path="/orders/:id" element={<OrderDetailPage />} />
        <Route path="/profile" element={<ProfilePage />} />
        <Route path="/wallet" element={<WalletPage />} />
        <Route path="/schedules" element={<SchedulesPage />} />
        <Route path="/schedules/:id" element={<ScheduleDetailPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>

      {/* Rendered at the shell level so the panel overlays whichever page is
          open, rather than being clipped inside a route's layout. */}
      <CartDrawer />
    </AppShell>
  );
}

export function App(): ReactElement {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ToastProvider>
        <AuthProvider>
          <CartProvider>
          <Routes>
            {/* Sign in sits outside the shell: it has no navigation to show. */}
            <Route path="/account" element={<AuthPage />} />
            <Route path="/*" element={<Storefront />} />
          </Routes>
          </CartProvider>
        </AuthProvider>
        </ToastProvider>
      </BrowserRouter>
    </QueryClientProvider>
  );
}

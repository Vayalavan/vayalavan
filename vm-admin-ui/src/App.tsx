import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  BrowserRouter, Routes, Route, Navigate, NavLink, useLocation,
} from "react-router-dom";
import { AppShell, ToastProvider } from "@vayal/ui-kit";
import type { ReactElement, ReactNode } from "react";
import { AuthProvider, isAnalyst, useAuth } from "./lib/auth.js";
import { config } from "./lib/config.js";
import { LoginPage } from "./routes/LoginPage.js";
import { DashboardPage } from "./routes/DashboardPage.js";
import { OrdersPage } from "./routes/OrdersPage.js";
import { OrdersTable } from "./routes/OrdersTable.js";
import { SuppliersPage } from "./routes/SuppliersPage.js";
import { PayoutsPage } from "./routes/PayoutsPage.js";
import { WalletsPage } from "./routes/WalletsPage.js";
import { ProductsPage } from "./routes/ProductsPage.js";
import { AnalyticsPage } from "./routes/AnalyticsPage.js";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: true,
      retry: (failureCount, error) => {
        const status = (error as { status?: number }).status ?? 0;
        if (status === 401 || status === 403 || status === 404) return false;
        return failureCount < 2;
      },
    },
  },
});

/**
 * Gate for authenticated routes.
 *
 * Waits for the initial refresh before deciding: redirecting while that is in
 * flight would bounce a signed-in admin to the login screen on every reload.
 */
function RequireAdmin({ children }: { children: ReactNode }): ReactElement {
  const { user, initialising } = useAuth();
  const location = useLocation();

  if (initialising) {
    return <div role="status" className="p-8 text-center text-primary-900/60">Loading…</div>;
  }
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <>{children}</>;
}

/**
 * The sidebar's version: light type on the green panel, full width so the
 * active row reads as a bar rather than a chip.
 *
 * The active state inverts to the off-white surface with dark green type,
 * which is the strongest contrast available from the brand ramp and leaves no
 * doubt about which section is open.
 */
function sideLinkClass({ isActive }: { isActive: boolean }): string {
  return `rounded-card px-3 py-2 text-sm font-medium ${
    isActive
      ? "bg-surface text-primary-900"
      : "text-primary-50/80 hover:bg-primary-700 hover:text-white"
  }`;
}

/**
 * The console's sections, in the order an admin's day runs: what happened,
 * what to fulfil, what we sell, who grows it, who to pay — then the analytics
 * that look back over all of it.
 */
const NAV: ReadonlyArray<readonly [string, string]> = [
  ["/dashboard", "Dashboard"],
  ["/orders", "Orders"],
  ["/products", "Products"],
  ["/suppliers", "Suppliers"],
  ["/payouts", "Settlement"],
  ["/wallets", "Wallets"],
  ["/analytics", "Analytics"],
];

/**
 * An analyst's whole console (CLAUDE.md §7). Hiding the other links is only
 * tidiness: the gateway refuses an analyst every admin route regardless.
 */
const ANALYST_NAV: ReadonlyArray<readonly [string, string]> = [["/analytics", "Analytics"]];

function AdminLayout({ children }: { children: ReactNode }): ReactElement {
  const { user, signOut } = useAuth();
  const nav = isAnalyst(user) ? ANALYST_NAV : NAV;

  return (
    <AppShell
      appLabel="Admin"
      supportEmail={config.supportEmail}
      // One list, one set of links, two shapes: AppShell renders them as the
      // rail on a desk and as the Menu accordion below `lg`. A phone is not
      // this console's home, but an admin checking an order from one should
      // not hit a dead end — which is exactly what the old header row did
      // below `md`, where it was hidden and nothing replaced it.
      sidebar={<>{nav.map(([to, label]) => (
        <NavLink key={to} to={to} className={sideLinkClass}>{label}</NavLink>
      ))}</>}
      actions={
        <div className="flex items-center gap-3">
          <span className="hidden text-sm text-primary-900/70 sm:inline">{user?.email}</span>
          <button
            onClick={() => void signOut()}
            className="rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800"
          >
            Sign out
          </button>
        </div>
      }
    >
      {children}
    </AppShell>
  );
}

export function App(): ReactElement {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ToastProvider>
        <AuthProvider>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route
              path="/*"
              element={
                <RequireAdmin>
                  <AdminLayout>
                    <ConsoleRoutes />
                  </AdminLayout>
                </RequireAdmin>
              }
            />
          </Routes>
        </AuthProvider>
        </ToastProvider>
      </BrowserRouter>
    </QueryClientProvider>
  );
}

/**
 * The routes a signed-in user may open. An analyst gets Analytics and nothing
 * else — any other address lands there — so a bookmarked admin URL is a
 * redirect rather than a screen of failed requests.
 */
function ConsoleRoutes(): ReactElement {
  const { user } = useAuth();

  if (isAnalyst(user)) {
    return (
      <Routes>
        <Route path="/analytics" element={<AnalyticsPage />} />
        <Route path="*" element={<Navigate to="/analytics" replace />} />
      </Routes>
    );
  }

  return (
    <Routes>
      <Route path="/" element={<Navigate to="/dashboard" replace />} />
      <Route path="/dashboard" element={<DashboardPage />} />
      {/* The list is now the fulfilment queue; OrdersPage still owns the
          single-order detail view underneath it. */}
      <Route path="/orders" element={<OrdersTable />} />
      <Route path="/orders/:id" element={<OrdersPage />} />
      <Route path="/products" element={<ProductsPage />} />
      <Route path="/suppliers" element={<SuppliersPage />} />
      <Route path="/payouts" element={<PayoutsPage />} />
      <Route path="/wallets" element={<WalletsPage />} />
      <Route path="/analytics" element={<AnalyticsPage />} />
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}

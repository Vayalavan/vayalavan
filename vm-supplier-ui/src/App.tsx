import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  BrowserRouter,
  Routes,
  Route,
  Navigate,
  NavLink,
  useLocation,
} from "react-router-dom";
import { AppShell } from "@vayal/ui-kit";
import type { ReactElement, ReactNode } from "react";
import { AuthProvider, useAuth } from "./lib/auth.js";
import { config } from "./lib/config.js";
import { LoginPage } from "./routes/LoginPage.js";
import { ProductsPage } from "./routes/ProductsPage.js";
import { ProductFormPage } from "./routes/ProductFormPage.js";
import { SalesPage } from "./routes/SalesPage.js";
import { AnalyticsPage } from "./routes/AnalyticsPage.js";
import { ImportPage } from "./routes/ImportPage.js";
import { AvailabilityPage } from "./routes/AvailabilityPage.js";

/**
 * Created once at module scope, not inside the component: a QueryClient
 * rebuilt on every render would discard the cache on each state change.
 */
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: true,
      // A 401 or 403 will not resolve itself by asking again.
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
 * Waits for the initial refresh attempt before deciding: redirecting while
 * that is still in flight would bounce a signed-in supplier to the login
 * screen on every page load.
 */
function RequireAuth({ children }: { children: ReactNode }): ReactElement {
  const { user, initialising } = useAuth();
  const location = useLocation();

  if (initialising) {
    return (
      <div className="p-8 text-center text-primary-900/60" role="status">
        Loading…
      </div>
    );
  }
  if (!user) {
    // Remember where they were headed so sign-in can return them there.
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  return <>{children}</>;
}

function navLinkClass({ isActive }: { isActive: boolean }): string {
  return `rounded-card px-3 py-2 text-sm font-medium ${
    isActive ? "bg-primary-50 text-primary-800" : "text-primary-800 hover:bg-primary-50"
  }`;
}

/** The signed-in layout: shell, navigation and sign-out. */
function SupplierLayout({ children }: { children: ReactNode }): ReactElement {
  const { user, signOut } = useAuth();
  const { pathname } = useLocation();

  // Insights alone is framed: it is the one screen long enough that the
  // footer — the support address a grower writes to when a figure looks wrong
  // — would otherwise sit a metre below the chart raising the question. The
  // working screens stay a normal scrolling document, because a form that
  // scrolls inside a pane while the page does not is worse, not better.
  const framed = pathname.startsWith("/insights");

  return (
    <AppShell
      appLabel="Supplier"
      supportEmail={config.supportEmail}
      frame={framed}
      nav={
        <>
          <NavLink to="/availability" className={navLinkClass}>
            Today
          </NavLink>
          <NavLink to="/products" className={navLinkClass}>
            Products
          </NavLink>
          <NavLink to="/sales" className={navLinkClass}>
            Sales
          </NavLink>
          {/* After Sales, not before it: settlement is what a grower opens
              this app to check, and insights are what they stay for. */}
          <NavLink to="/insights" className={navLinkClass}>
            Insights
          </NavLink>
          <NavLink to="/imports" className={navLinkClass}>
            Import CSV
          </NavLink>
        </>
      }
      actions={
        <div className="flex items-center gap-3">
          <span className="hidden text-sm text-primary-900/70 sm:inline">
            {user?.name ?? user?.email}
          </span>
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
        <AuthProvider>
          <Routes>
            {/* Login sits outside the shell: it has no navigation to show. */}
            <Route path="/login" element={<LoginPage />} />

            <Route
              path="/*"
              element={
                <RequireAuth>
                  <SupplierLayout>
                    <Routes>
                      {/* Availability is the landing page: it is what a supplier
                          opens the portal to do each morning. */}
                      <Route path="/" element={<Navigate to="/availability" replace />} />
                      <Route path="/availability" element={<AvailabilityPage />} />
                      <Route path="/products" element={<ProductsPage />} />
                      {/* "new" is matched by the same route; the form treats
                          it as create rather than edit. */}
                      <Route path="/products/new" element={<ProductFormPage />} />
                      <Route path="/products/:id" element={<ProductFormPage />} />
                      <Route path="/sales" element={<SalesPage />} />
                      <Route path="/insights" element={<AnalyticsPage />} />
                      <Route path="/imports" element={<ImportPage />} />
                      <Route path="*" element={<Navigate to="/availability" replace />} />
                    </Routes>
                  </SupplierLayout>
                </RequireAuth>
              }
            />
          </Routes>
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  );
}

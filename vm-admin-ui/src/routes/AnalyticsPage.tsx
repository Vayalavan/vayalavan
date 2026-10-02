/**
 * Analytics — rendered by vm-analytics-ui, loaded at runtime as a Module
 * Federation remote.
 *
 * The remote is a separate deploy, so it can be down when the console is
 * not. A failed load is contained to this page: the rest of the console must
 * keep working, and the admin is told what is wrong rather than shown a
 * blank pane.
 */
import { Component, lazy, Suspense, type ReactElement, type ReactNode } from "react";
import { Card, PageHeading } from "@vayal/ui-kit";
import { config } from "../lib/config.js";
import { getAccessToken } from "../lib/api.js";

const Analytics = lazy(() => import("vm_analytics/Analytics"));

class RemoteBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  override state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  override componentDidCatch(error: unknown): void {
    console.error("[analytics] vm-analytics-ui failed to load", error);
  }

  override render(): ReactNode {
    if (!this.state.failed) return this.props.children;
    return (
      <>
        <PageHeading title="Analytics" />
        <Card>
          <p role="alert" className="py-8 text-center text-primary-900/70">
            Analytics could not be loaded. Try again in a minute; if it keeps
            happening, the analytics app may be down.
          </p>
        </Card>
      </>
    );
  }
}

export function AnalyticsPage(): ReactElement {
  return (
    <RemoteBoundary>
      <Suspense fallback={<Card>Loading analytics…</Card>}>
        <Analytics apiBaseUrl={config.apiBaseUrl} getAccessToken={getAccessToken} />
      </Suspense>
    </RemoteBoundary>
  );
}

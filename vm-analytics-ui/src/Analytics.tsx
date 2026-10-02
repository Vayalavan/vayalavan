/**
 * The admin console's Analytics section, exposed as `vm_analytics/Analytics`.
 *
 * Rendered by vm-admin-ui inside its own shell, router and QueryClient.
 * It never signs in on its own: the host hands over the API base URL and a
 * way to read the admin's in-memory access token, so there is still exactly
 * one session and the token is never copied into a second app's storage.
 *
 * Changing these props is a breaking change to the host — keep
 * vm-admin-ui/src/remotes.d.ts in step.
 */
import type { ReactElement } from "react";
import { Card, PageHeading } from "@vayal/ui-kit";
import "./analytics.css";

export interface AnalyticsProps {
  /** The gateway, including its /api mount — the host's VITE_API_BASE_URL. */
  apiBaseUrl: string;
  /** The admin's current access token, or null once signed out. */
  getAccessToken: () => string | null;
}

export default function Analytics(_props: AnalyticsProps): ReactElement {
  return (
    <>
      <PageHeading title="Analytics" />
      <Card>
        <p role="status" className="py-8 text-center text-primary-900/70">
          Analytics is in progress. Check back soon.
        </p>
      </Card>
    </>
  );
}

/**
 * Types for modules loaded from Module Federation remotes at runtime.
 *
 * TypeScript cannot see across to another app's source, so the contract is
 * written down here. It must match vm-analytics-ui/src/Analytics.tsx.
 */
declare module "vm_analytics/Analytics" {
  import type { ReactElement } from "react";

  export interface AnalyticsProps {
    apiBaseUrl: string;
    getAccessToken: () => string | null;
  }

  export default function Analytics(props: AnalyticsProps): ReactElement;
}

/**
 * @vayal/ui-kit — the shared React surface for all three Vayal UIs.
 *
 * Explicit re-exports rather than `export *`, so the package's public API is
 * a list someone can read, and adding a file does not silently widen it.
 */

// Logo (CLAUDE.md §9)
export {
  VayalMark,
  VayalMarkMono,
  VayalLockup,
  type VayalMarkProps,
  type VayalLockupProps,
  type LogoSize,
} from "./logo/VayalLogo.js";

// Layout primitives
export {
  AppShell,
  PageHeading,
  Card,
  type AppShellProps,
  type PageHeadingProps,
  type CardProps,
} from "./components/AppShell.js";

// Order timeline (CLAUDE.md §6.1)
export {
  OrderTimeline,
  type OrderTimelineProps,
  type TimelineMilestone,
} from "./components/OrderTimeline.js";

// Loading placeholders
export {
  Skeleton,
  ProductCardSkeleton,
  ProductGridSkeleton,
} from "./components/Skeleton.js";

// Transient confirmations
export {
  ToastProvider,
  useToast,
  type ToastTone,
} from "./components/Toast.js";

// Stand-in artwork for produce with no photograph
export { ProducePlaceholder } from "./components/ProducePlaceholder.js";

// Reporting-period selector, shared by admin and supplier reporting screens
export {
  RangePicker,
  DEFAULT_RANGES,
  SALES_RANGES,
  type RangePickerProps,
  type RangeOption,
} from "./components/RangePicker.js";

// Reporting charts, shared by the admin and supplier screens
export {
  ChartHeading,
  ChartTable,
  MetricBar,
  SplitBar,
  ColumnChart,
  LineChart,
  chartSharePct,
  type ChartTone,
  type SeriesPoint,
  type ChartHeadingProps,
  type ChartTableProps,
  type MetricBarProps,
  type SplitBarSegment,
  type TimeChartProps,
} from "./components/Charts.js";

// API client
export {
  createApiClient,
  newRequestId,
  ApiError,
  REQUEST_ID_HEADER,
  type ApiClient,
  type ApiClientOptions,
  type ApiErrorEnvelope,
  type RequestOptions,
  type QueryValue,
} from "./api/client.js";

// Naming a line by its grade (CLAUDE.md §5.2)
export { gradeLabel, gradedName } from "./format/grade.js";

// How rare a grade is, from its share of the harvest (CLAUDE.md §5.2)
export {
  harvestTier,
  harvestTierLabel,
  harvestShareSentence,
  type HarvestTier,
} from "./format/harvest.js";

// The delivery area, enforced by vm-go-common's serviceable package
export {
  isServiceablePincode,
  DELIVERY_AREA_LABEL,
  PINCODE_AREA_ERROR,
} from "./format/pincode.js";

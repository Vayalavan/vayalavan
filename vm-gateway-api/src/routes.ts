/**
 * Route table.
 *
 * Every route declares, in one place: its zod body schema, whether it needs
 * authentication, which roles may call it, and where it forwards to. Reading
 * this file is how you learn the API's authorisation model.
 */
import { Router, type Request, type Response, type NextFunction, type RequestHandler } from "express";
import { z } from "zod";
import type { Config } from "./config.js";
import { AppError } from "./errors.js";
import { requireAuth, requireRole, Role } from "./auth.js";
import { forward } from "./upstream.js";
import { credentialRateLimit } from "./middleware.js";

/**
 * Validates the request body against a schema before it can reach an
 * upstream, and replaces req.body with the parsed value.
 *
 * Rejecting malformed input at the edge means the Go services never see it,
 * and the client gets a field-level error in the platform's standard shape.
 */
function validate<T extends z.ZodTypeAny>(schema: T): RequestHandler {
  return (req: Request, _res: Response, next: NextFunction) => {
    const result = schema.safeParse(req.body);
    if (!result.success) {
      const details: Record<string, string> = {};
      for (const issue of result.error.issues) {
        const key = issue.path.join(".") || "body";
        // First error per field: a list of five complaints about one field is
        // noise in a form UI.
        if (!(key in details)) details[key] = issue.message;
      }
      next(
        new AppError(422, "VALIDATION_FAILED", "Some fields need attention.", { details }),
      );
      return;
    }
    req.body = result.data;
    next();
  };
}

/** Wraps an async handler so a rejected promise reaches the error middleware. */
function handler(
  fn: (req: Request, res: Response) => Promise<void>,
): RequestHandler {
  return (req, res, next) => {
    fn(req, res).catch(next);
  };
}

// ---------------------------------------------------------------------------
// Schemas
//
// Shapes only — the authoritative business rules live in vm-profile-api, which
// validates again. These exist to reject obvious junk at the edge cheaply.
// ---------------------------------------------------------------------------

const password = z.string().min(8, "must be at least 8 characters").max(72);
const email = z.string().email("is not a valid email address");
const phone = z
  .string()
  .min(10, "must be a 10-digit Indian mobile number")
  .max(20);

const registerSchema = z.object({
  name: z.string().trim().min(1, "is required").max(120),
  phone,
  email,
  password,
});

const loginSchema = z.object({
  identifier: z.string().trim().min(1, "is required"),
  password: z.string().min(1, "is required"),
});

const refreshSchema = z.object({
  refresh_token: z.string().min(1, "is required"),
});

/**
 * The admin markup, as a RATE in basis points: 3000 is 30%.
 *
 * An integer, and the same wire shape as suppliers.commission_bps — the two
 * rates on this platform are entered and validated identically. Basis points
 * rather than a percent keep two decimal places exact without a float ever
 * touching something that decides money (CLAUDE.md rule 1); the admin UI does
 * the ×100 from the percent someone types.
 *
 * 10000 is 100%: doubling a grower's price. Anything beyond is a typo, most
 * likely a percentage typed as basis points, and the upstream and the database
 * both refuse it too.
 */
const markupSchema = z.object({
  markup_bps: z
    .number()
    .int("must be a whole number of basis points")
    .min(0, "cannot be negative")
    .max(10000, "cannot exceed 100%"),
});

const setPasswordSchema = z.object({
  token: z.string().min(1, "is required"),
  password,
});

const addressSchema = z.object({
  label: z.string().max(40).optional().default(""),
  recipient_name: z.string().trim().min(1, "is required").max(120),
  phone,
  line1: z.string().trim().min(1, "is required").max(200),
  line2: z.string().max(200).optional().default(""),
  landmark: z.string().max(120).optional().default(""),
  city: z.string().trim().min(1, "is required").max(80),
  state: z.string().trim().min(1, "is required").max(80),
  pincode: z.string().regex(/^[1-9][0-9]{5}$/, "must be a 6-digit PIN code"),
  is_default: z.boolean().optional().default(false),
});

const supplierApplySchema = z.object({
  business_name: z.string().trim().min(2, "is required").max(200),
  contact_name: z.string().trim().min(1, "is required").max(120),
  phone,
  email,
  password,
  gstin: z.string().max(20).optional().default(""),
  pan: z.string().max(10).optional().default(""),
  address_line1: z.string().max(200).optional().default(""),
  address_line2: z.string().max(200).optional().default(""),
  city: z.string().max(80).optional().default(""),
  state: z.string().max(80).optional().default(""),
  pincode: z.string().optional().default(""),
});

const adminCreateSupplierSchema = z.object({
  business_name: z.string().trim().min(2, "is required").max(200),
  contact_name: z.string().trim().min(1, "is required").max(120),
  phone,
  email,
  gstin: z.string().max(20).optional().default(""),
  pan: z.string().max(10).optional().default(""),
  address_line1: z.string().max(200).optional().default(""),
  address_line2: z.string().max(200).optional().default(""),
  city: z.string().max(80).optional().default(""),
  state: z.string().max(80).optional().default(""),
  pincode: z.string().optional().default(""),
  bank_account_name: z.string().max(120).optional().default(""),
  bank_account_number: z.string().max(32).optional().default(""),
  bank_ifsc: z.string().max(11).optional().default(""),
  // Per-supplier commission in basis points. Nullable and optional on
  // purpose: omitted or null both mean "use the platform default", while 0 is
  // a real rate (a grower we take nothing from) and must survive the round
  // trip distinctly. No .default() — supplying one would make an omitted
  // field indistinguishable from a deliberate value.
  commission_bps: z.number().int().min(0).max(10000).nullable().optional(),
});

const decisionSchema = z.object({
  reason: z.string().max(500).optional().default(""),
});

// Recording a payment taken outside Razorpay. The reference is mandatory —
// this is the only trace the payment leaves in our system, and it is what an
// audit has to match against a bank statement later.
// Bulk fulfilment. Either a selection or everything eligible in the period —
// the upstream refuses both-or-neither, and caps the selection size.
const bulkOrderSchema = z.object({
  order_ids: z.array(z.string().uuid()).max(500).optional().default([]),
  all: z.boolean().optional().default(false),
});

const recordPaymentSchema = z.object({
  reference: z.string().trim().min(1, "is required").max(100),
  method: z.string().trim().max(40).optional().default("offline"),
  notes: z.string().max(500).optional().default(""),
});

// --- catalog ---------------------------------------------------------------

const productTypes = ["fruit", "vegetable", "microgreen", "other"] as const;

/**
 * Prices cross the wire as STRINGS, never JSON numbers. 45.50 is not exactly
 * representable as a float64, and CLAUDE.md rule 1 forbids a float ever
 * touching money — the Go side parses this to integer paise.
 */
const priceRupees = z
  .string()
  .trim()
  .min(1, "is required")
  .regex(/^₹?\s*\d{1,3}(,\d{2,3})*(\.\d{1,2})?$|^₹?\s*\d+(\.\d{1,2})?$/,
    "must be an amount with at most 2 decimal places");

const unitSchema = z.object({
  label: z.string().trim().min(1, "is required").max(40),
  // Optional detail for the customer — "6-8 fruit", "ventilated carton".
  // Same shape and cap as a size code's, because it is the same field one
  // level down.
  meta: z.string().max(80).optional().default(""),
  weight_grams: z.number().int().positive("must be greater than zero").max(1_000_000),
  price_rupees: priceRupees,
  is_active: z.boolean().optional(),
});

// One gallery item. Position in the array is the display order, so there is
// no sort_order field to keep consistent with it.
const mediaSchema = z.object({
  kind: z.enum(["image", "video"]),
  object_key: z.string().trim().min(1, "is required").max(400),
  content_type: z.string().max(100).optional().default(""),
});

// One GRADE of a product: its code, its optional detail line, its own gallery
// and its own packs. Position in the array is the display order.
//
// A product always has at least one — a grower who does not grade simply has
// one, and the storefront hides the selector when there is nothing to select.
const sizeCodeSchema = z.object({
  code: z.string().trim().min(1, "is required").max(20),
  meta: z.string().max(80).optional().default(""),
  is_active: z.boolean().optional(),
  // Roughly what percent of the product's harvest comes off as this grade.
  // Nullable AND optional, and those are the same answer: the grower has not
  // estimated their split, which is the normal starting state. The shares
  // totalling at most 100% across a product is checked in vm-catalog-api,
  // which is the only layer that sees the whole tree's parsed values.
  harvest_share_pct: z.number().int().min(1).max(100).nullable().optional(),
  // Images and videos, in the grower's order. Empty is valid — produce may be
  // listed before there is a photograph of it. The real cap is
  // MAX_MEDIA_PER_SIZE_CODE in vm-catalog-api, which is the authority; this
  // outer bound only stops an absurd payload reaching it.
  media: z.array(mediaSchema).max(50).optional().default([]),
  packs: z.array(unitSchema).min(1, "at least one pack option is required").max(20),
});

const productSchema = z.object({
  name: z.string().trim().min(1, "is required").max(120),
  type: z.enum(productTypes),
  grade: z.string().max(40).optional().default(""),
  description: z.string().max(2000).optional().default(""),
  status: z.enum(["draft", "active", "archived"]).optional(),
  // The PRODUCT-level gallery: pictures of the farm rather than of a grade,
  // shown to the customer after every grade's own. Optional, because a
  // product needs none — and because a client that predates it must keep
  // working. Same outer bound as a grade's; the real cap is
  // MAX_MEDIA_PER_SIZE_CODE in vm-catalog-api.
  media: z.array(mediaSchema).max(50).optional().default([]),
  // The real cap is MAX_SIZE_CODES_PER_PRODUCT in vm-catalog-api.
  size_codes: z
    .array(sizeCodeSchema)
    .min(1, "at least one size code is required")
    .max(50),
});

const availabilitySchema = z.object({
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, "must be YYYY-MM-DD").optional(),
  entries: z
    .array(
      z.object({
        // The SIZE CODE, not the product: the gram pool belongs to the grade,
        // so a grower declares each grade's crate separately.
        size_code_id: z.string().uuid(),
        // Grams, not kilograms: the UI converts, because grams is what the
        // stock pool is denominated in.
        total_grams: z.number().int().min(0).max(100_000_000),
      }),
    )
    .min(1, "at least one product is required")
    .max(500),
});

const copyYesterdaySchema = z.object({
  date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/, "must be YYYY-MM-DD").optional(),
});

const cartItemSchema = z.object({
  product_unit_id: z.string().uuid(),
  qty: z.number().int().min(1, "must be at least 1").max(99),
});

const cartQtySchema = z.object({
  // Zero removes the line, which is what a stepper pressed down to nothing
  // means to a customer.
  qty: z.number().int().min(0).max(99),
});

const placeOrderSchema = z.object({
  address_id: z.string().uuid(),
});

const verifyPaymentSchema = z.object({
  razorpay_order_id: z.string().min(1),
  razorpay_payment_id: z.string().min(1),
  razorpay_signature: z.string().min(1),
});

// --- wallet and scheduled orders (CLAUDE.md §6.7) ---------------------------

const isoDate = z.string().regex(/^\d{4}-\d{2}-\d{2}$/, "must be YYYY-MM-DD");

const topupSchema = z.object({
  // Paise, like every amount (rule 1). The bounds are env-driven and enforced
  // by vm-orders-api; this only refuses what can never be valid.
  amount_paise: z.number().int().positive(),
});

const createScheduleSchema = z.object({
  address_id: z.string().uuid(),
  frequency: z.enum(["once", "daily", "weekly", "monthly"]),
  weekdays: z.array(z.number().int().min(0).max(6)).max(7).optional().default([]),
  day_of_month: z.number().int().min(1).max(31).optional(),
  start_date: isoDate,
  end_date: isoDate.optional(),
});

const updateScheduleSchema = z.object({
  address_id: z.string().uuid().optional(),
  items: z
    .array(z.object({ product_unit_id: z.string().uuid(), qty: z.number().int().min(0).max(99) }))
    .max(100)
    .optional(),
});

const skipSchema = z.object({ delivery_date: isoDate });

const walletRefundSchema = z.object({
  amount_paise: z.number().int().positive(),
  // Required: it is what the audit log records as the reason.
  notes: z.string().trim().min(1, "is required").max(500),
});

// Must match allowedMediaTypes in vm-catalog-api (CLAUDE.md §6.6). The size
// limit is NOT duplicated here: it differs per kind and comes from env vars
// the catalog service owns, so that check stays in one place.
const presignSchema = z.object({
  content_type: z.enum([
    "image/jpeg",
    "image/png",
    "image/webp",
    "video/mp4",
    "video/quicktime",
    "video/webm",
  ]),
  size_bytes: z.number().int().positive(),
  filename: z.string().max(255).optional().default(""),
});

// ---------------------------------------------------------------------------

export function apiRoutes(config: Config): Router {
  const router = Router();
  const auth = requireAuth(config);

  // Credential endpoints are keyed on IP *and* the identifier being tried, so
  // one attacker cannot lock out an entire NAT, and cannot spray a single
  // account from one address either.
  const credentialLimit = credentialRateLimit(config);

  // --- authentication (public) ---------------------------------------------

  router.post(
    "/auth/register",
    credentialLimit,
    validate(registerSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/auth/register", method: "POST", body: req.body }),
    ),
  );

  router.post(
    "/auth/login",
    credentialLimit,
    validate(loginSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/auth/login", method: "POST", body: req.body }),
    ),
  );

  // Not rate limited by identifier: refresh presents a 256-bit token, not a
  // guessable credential, and the global limiter still applies.
  router.post(
    "/auth/refresh",
    validate(refreshSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/auth/refresh", method: "POST", body: req.body }),
    ),
  );

  router.post(
    "/auth/logout",
    validate(refreshSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/auth/logout", method: "POST", body: req.body }),
    ),
  );

  router.post(
    "/auth/set-password",
    credentialLimit,
    validate(setPasswordSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/auth/set-password", method: "POST", body: req.body }),
    ),
  );

  // --- any signed-in user ---------------------------------------------------

  router.get(
    "/me",
    auth,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/me", method: "GET" }),
    ),
  );

  // --- customer: addresses --------------------------------------------------

  const customerOnly = [auth, requireRole(Role.Customer)];

  router.get(
    "/addresses",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/addresses/", method: "GET" }),
    ),
  );

  router.post(
    "/addresses",
    customerOnly,
    validate(addressSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/addresses/", method: "POST", body: req.body }),
    ),
  );

  router.get(
    "/addresses/:id",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: `/addresses/${req.params.id}`, method: "GET" }),
    ),
  );

  router.put(
    "/addresses/:id",
    customerOnly,
    validate(addressSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: `/addresses/${req.params.id}`, method: "PUT", body: req.body }),
    ),
  );

  router.delete(
    "/addresses/:id",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: `/addresses/${req.params.id}`, method: "DELETE" }),
    ),
  );

  router.post(
    "/addresses/:id/default",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: `/addresses/${req.params.id}/default`, method: "POST" }),
    ),
  );

  // --- suppliers ------------------------------------------------------------

  router.post(
    "/suppliers/apply",
    credentialLimit,
    validate(supplierApplySchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/suppliers/apply", method: "POST", body: req.body }),
    ),
  );

  // A supplier may read only its own record; vm-profile-api enforces that on
  // the forwarded identity.
  router.get(
    "/suppliers/:id",
    auth,
    requireRole(Role.Supplier, Role.Admin),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: `/suppliers/${req.params.id}`, method: "GET" }),
    ),
  );

  // --- analytics: admins and analysts --------------------------------------
  //
  // vm-analytics-api's reports over the analytics schema (CLAUDE.md §5.4).
  // The ONLY routes an analyst can reach besides /me. A named list rather
  // than a wildcard, so a report the Python service adds is not public until
  // someone decides it should be — and each forwards the same period
  // vocabulary as every other reporting route.
  const analyticsReaders = [auth, requireRole(Role.Admin, Role.Analyst)];
  const ANALYTICS_REPORTS = ["summary"] as const;

  for (const report of ANALYTICS_REPORTS) {
    router.get(
      `/analytics/${report}`,
      analyticsReaders,
      handler(async (req, res) =>
        forward(req, res, config, {
          upstream: "analytics",
          path: `/analytics/${report}`,
          method: "GET",
          query: {
            range: typeof req.query.range === "string" ? req.query.range : undefined,
            from: typeof req.query.from === "string" ? req.query.from : undefined,
            to: typeof req.query.to === "string" ? req.query.to : undefined,
            limit: typeof req.query.limit === "string" ? req.query.limit : undefined,
          },
        }),
      ),
    );
  }

  // --- admin ----------------------------------------------------------------

  const adminOnly = [auth, requireRole(Role.Admin)];

  router.get(
    "/admin/suppliers",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "profile",
        path: "/admin/suppliers",
        method: "GET",
        query: {
          status: typeof req.query.status === "string" ? req.query.status : undefined,
          limit: typeof req.query.limit === "string" ? req.query.limit : undefined,
          offset: typeof req.query.offset === "string" ? req.query.offset : undefined,
        },
      }),
    ),
  );

  router.post(
    "/admin/suppliers",
    adminOnly,
    validate(adminCreateSupplierSchema),
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "profile", path: "/admin/suppliers", method: "POST", body: req.body }),
    ),
  );

  // Editing details. Status is NOT editable here — approve/reject/suspend
  // below are separate, audited transitions.
  router.patch(
    "/admin/suppliers/:id",
    adminOnly,
    validate(adminCreateSupplierSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "profile",
        path: `/admin/suppliers/${req.params.id}`,
        method: "PATCH",
        body: req.body,
      }),
    ),
  );

  for (const decision of ["approve", "reject", "suspend"] as const) {
    router.post(
      `/admin/suppliers/:id/${decision}`,
      adminOnly,
      validate(decisionSchema),
      handler(async (req, res) =>
        forward(req, res, config, {
          upstream: "profile",
          path: `/admin/suppliers/${req.params.id}/${decision}`,
          method: "POST",
          body: req.body,
        }),
      ),
    );
  }

  // --- supplier: products ---------------------------------------------------

  const supplierOnly = [auth, requireRole(Role.Supplier)];

  const listQuery = (req: Request) => ({
    type: typeof req.query.type === "string" ? req.query.type : undefined,
    status: typeof req.query.status === "string" ? req.query.status : undefined,
    search: typeof req.query.search === "string" ? req.query.search : undefined,
    limit: typeof req.query.limit === "string" ? req.query.limit : undefined,
    offset: typeof req.query.offset === "string" ? req.query.offset : undefined,
  });

  router.get(
    "/products",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: "/products/", method: "GET", query: listQuery(req),
      }),
    ),
  );

  router.post(
    "/products",
    supplierOnly,
    validate(productSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: "/products/", method: "POST", body: req.body,
      }),
    ),
  );

  router.get(
    "/products/:id",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: `/products/${req.params.id}`, method: "GET",
      }),
    ),
  );

  router.put(
    "/products/:id",
    supplierOnly,
    validate(productSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: `/products/${req.params.id}`, method: "PUT", body: req.body,
      }),
    ),
  );

  router.post(
    "/products/:id/archive",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: `/products/${req.params.id}/archive`, method: "POST",
      }),
    ),
  );

  // --- supplier: image uploads ----------------------------------------------

  router.post(
    "/uploads/presign",
    supplierOnly,
    validate(presignSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: "/uploads/presign", method: "POST", body: req.body,
      }),
    ),
  );

  // --- supplier: daily availability -----------------------------------------

  router.get(
    "/supplier/availability",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: "/supplier/availability/",
        method: "GET",
        query: { date: typeof req.query.date === "string" ? req.query.date : undefined },
      }),
    ),
  );

  router.put(
    "/supplier/availability",
    supplierOnly,
    validate(availabilitySchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: "/supplier/availability/", method: "PUT", body: req.body,
      }),
    ),
  );

  router.post(
    "/supplier/availability/copy-from-yesterday",
    supplierOnly,
    validate(copyYesterdaySchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: "/supplier/availability/copy-from-yesterday",
        method: "POST",
        body: req.body,
      }),
    ),
  );

  router.post(
    "/supplier/availability/:id/close",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: `/supplier/availability/${req.params.id}/close`,
        method: "POST",
      }),
    ),
  );

  // Reopening is deliberately its own call, not a side effect of saving a
  // quantity — see the ReopenAvailability handler in vm-catalog-api.
  router.post(
    "/supplier/availability/:id/reopen",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: `/supplier/availability/${req.params.id}/reopen`,
        method: "POST",
      }),
    ),
  );

  // A supplier's own sales and settlement position. The upstream scopes this
  // to the calling supplier; there is no id in the path to tamper with.
  router.get(
    "/supplier/sales",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/supplier/sales",
        method: "GET",
        query: {
          limit: typeof req.query.limit === "string" ? req.query.limit : undefined,
          offset: typeof req.query.offset === "string" ? req.query.offset : undefined,
          // The date window. Forwarded as written and validated upstream,
          // which is also where a preset like "7d" is resolved — in IST, from
          // the server's clock rather than from a handset's.
          //
          // `range` is the one that selects the period. Dropping it here is
          // silent: the upstream simply falls back to its default and returns
          // a perfectly valid answer to a question nobody asked.
          range: typeof req.query.range === "string" ? req.query.range : undefined,
          from: typeof req.query.from === "string" ? req.query.from : undefined,
          to: typeof req.query.to === "string" ? req.query.to : undefined,
        },
      }),
    ),
  );

  // The same supplier's trend screen: what sold, in which packs, day by day.
  // Scoped upstream to the calling supplier, like /supplier/sales.
  router.get(
    "/supplier/analytics",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/supplier/analytics",
        method: "GET",
        query: {
          range: typeof req.query.range === "string" ? req.query.range : undefined,
          from: typeof req.query.from === "string" ? req.query.from : undefined,
          to: typeof req.query.to === "string" ? req.query.to : undefined,
        },
      }),
    ),
  );

  // The authoritative order cutoff. Public and unauthenticated: a customer
  // deciding whether to shop now needs it before signing in.
  router.get(
    "/cutoff",
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "orders", path: "/cutoff", method: "GET" }),
    ),
  );

  // --- customer: cart -------------------------------------------------------
  //
  // The cart is re-priced from the catalogue on every read (CLAUDE.md §5.3),
  // so every one of these returns the whole cart rather than just the changed
  // line — the client never has to reconcile a partial update.

  router.get(
    "/cart",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "orders", path: "/cart/", method: "GET" }),
    ),
  );

  router.post(
    "/cart/items",
    customerOnly,
    validate(cartItemSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/cart/items", method: "POST", body: req.body,
      }),
    ),
  );

  router.patch(
    "/cart/items/:id",
    customerOnly,
    validate(cartQtySchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/cart/items/${req.params.id}`,
        method: "PATCH", body: req.body,
      }),
    ),
  );

  router.delete(
    "/cart/items/:id",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/cart/items/${req.params.id}`, method: "DELETE",
      }),
    ),
  );

  // --- customer: orders -----------------------------------------------------

  router.get(
    "/orders",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/orders/", method: "GET",
        query: {
          limit: typeof req.query.limit === "string" ? req.query.limit : undefined,
          offset: typeof req.query.offset === "string" ? req.query.offset : undefined,
        },
      }),
    ),
  );

  // Idempotency-Key is forwarded by the upstream helper (CLAUDE.md rule 6),
  // so a retried checkout returns the original order rather than charging twice.
  router.post(
    "/orders",
    customerOnly,
    validate(placeOrderSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/orders/", method: "POST", body: req.body,
      }),
    ),
  );

  router.get(
    "/orders/:id",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/orders/${req.params.id}`, method: "GET",
      }),
    ),
  );

  // A UX hint only — it can never be the sole path to `paid` (§6.4 step 3).
  router.post(
    "/orders/:id/verify-payment",
    customerOnly,
    validate(verifyPaymentSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/orders/${req.params.id}/verify-payment`,
        method: "POST", body: req.body,
      }),
    ),
  );

  // --- customer: wallet (CLAUDE.md §6.7) -------------------------------------
  //
  // Nothing here credits a balance. A top-up opens a Razorpay order; the
  // capture webhook — already routed above — is the only thing that adds money.

  router.get(
    "/wallet",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/wallet/", method: "GET",
        query: passQuery(req, ["limit", "offset"]),
      }),
    ),
  );

  // Idempotency-Key is forwarded, so a double-tapped "Add money" opens one
  // payment, not two.
  router.post(
    "/wallet/topups",
    customerOnly,
    validate(topupSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/wallet/topups", method: "POST", body: req.body,
      }),
    ),
  );

  router.get(
    "/wallet/topups/:id",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/wallet/topups/${encodeURIComponent(req.params.id ?? "")}`, method: "GET",
      }),
    ),
  );

  // A UX hint only, like /orders/:id/verify-payment.
  router.post(
    "/wallet/topups/:id/verify",
    customerOnly,
    validate(verifyPaymentSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/wallet/topups/${encodeURIComponent(req.params.id ?? "")}/verify`,
        method: "POST", body: req.body,
      }),
    ),
  );

  // --- customer: scheduled and repeat orders (CLAUDE.md §6.7) -----------------

  router.get(
    "/schedules",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "orders", path: "/schedules/", method: "GET" }),
    ),
  );

  // Registered before /schedules/:id so "options" is not read as an id.
  router.get(
    "/schedules/options",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "orders", path: "/schedules/options", method: "GET" }),
    ),
  );

  router.post(
    "/schedules",
    customerOnly,
    validate(createScheduleSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/schedules/", method: "POST", body: req.body,
      }),
    ),
  );

  router.get(
    "/schedules/:id",
    customerOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/schedules/${encodeURIComponent(req.params.id ?? "")}`, method: "GET",
      }),
    ),
  );

  router.patch(
    "/schedules/:id",
    customerOnly,
    validate(updateScheduleSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/schedules/${encodeURIComponent(req.params.id ?? "")}`,
        method: "PATCH", body: req.body,
      }),
    ),
  );

  for (const action of ["pause", "resume", "cancel"] as const) {
    router.post(
      `/schedules/:id/${action}`,
      customerOnly,
      handler(async (req, res) =>
        forward(req, res, config, {
          upstream: "orders", path: `/schedules/${encodeURIComponent(req.params.id ?? "")}/${action}`,
          method: "POST", body: {},
        }),
      ),
    );
  }

  router.post(
    "/schedules/:id/skip",
    customerOnly,
    validate(skipSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/schedules/${encodeURIComponent(req.params.id ?? "")}/skip`,
        method: "POST", body: req.body,
      }),
    ),
  );

  router.delete(
    "/schedules/:id/skip/:date",
    customerOnly,
    handler(async (req, res) => {
      if (!/^\d{4}-\d{2}-\d{2}$/.test(req.params.date ?? "")) {
        throw AppError.badRequest("date must be YYYY-MM-DD");
      }
      return forward(req, res, config, {
        upstream: "orders",
        path: `/schedules/${encodeURIComponent(req.params.id ?? "")}/skip/${req.params.date ?? ""}`,
        method: "DELETE",
      });
    }),
  );

  // --- customer: the storefront ---------------------------------------------
  //
  // Deliberately unauthenticated: browsing today's produce needs no account.
  // The business day is computed server-side in IST by vm-catalog-api, so a
  // client cannot ask for another day's stock.

  router.get(
    "/catalog",
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: "/catalog",
        method: "GET",
        query: {
          date: typeof req.query.date === "string" ? req.query.date : undefined,
          type: typeof req.query.type === "string" ? req.query.type : undefined,
          grade: typeof req.query.grade === "string" ? req.query.grade : undefined,
          search: typeof req.query.search === "string" ? req.query.search : undefined,
          limit: typeof req.query.limit === "string" ? req.query.limit : undefined,
          offset: typeof req.query.offset === "string" ? req.query.offset : undefined,
        },
      }),
    ),
  );

  // One product, for the shareable detail page. Public like the catalogue
  // itself — a customer deciding whether to shop should not have to sign in
  // to read a description.
  router.get(
    "/catalog/:id",
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: `/catalog/${req.params.id}`,
        method: "GET",
      }),
    ),
  );

  // --- supplier: CSV imports ------------------------------------------------
  //
  // The upload route is NOT registered here: it is multipart, and this router
  // sits behind express.json(). It is streamed by a dedicated handler mounted
  // in app.ts before the JSON parser.

  router.get(
    "/imports/template",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: "/imports/template",
        method: "GET",
        // The one route that returns a file rather than JSON.
        allowContentTypes: ["text/csv"],
      }),
    ),
  );

  router.get(
    "/imports",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, { upstream: "catalog", path: "/imports/", method: "GET" }),
    ),
  );

  router.get(
    "/imports/:id",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: `/imports/${req.params.id}`, method: "GET",
      }),
    ),
  );

  router.post(
    "/imports/:id/commit",
    supplierOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: `/imports/${req.params.id}/commit`, method: "POST",
      }),
    ),
  );

  // --- admin: products ------------------------------------------------------

  router.get(
    "/admin/products",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: "/admin/products",
        method: "GET",
        query: {
          ...listQuery(req),
          supplier_id:
            typeof req.query.supplier_id === "string" ? req.query.supplier_id : undefined,
        },
      }),
    ),
  );

  router.get(
    "/admin/products/:id",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: `/admin/products/${req.params.id}`, method: "GET",
      }),
    ),
  );

  // What we add to a grower's price. Admin only — there is no supplier-facing
  // route to this anywhere, and the field is absent from every supplier
  // response, so a markup cannot leak to the person whose price it sits on.
  router.put(
    "/admin/products/:id/markup",
    adminOnly,
    validate(markupSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog",
        path: `/admin/products/${req.params.id}/markup`,
        method: "PUT",
        body: req.body,
      }),
    ),
  );

  router.post(
    "/admin/products/:id/archive",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "catalog", path: `/admin/products/${req.params.id}/archive`, method: "POST",
      }),
    ),
  );

  // --- admin: orders, dashboard and settlement ------------------------------

  const passQuery = (req: Request, keys: string[]) =>
    Object.fromEntries(
      keys.map((k) => [k, typeof req.query[k] === "string" ? (req.query[k] as string) : undefined]),
    );

  router.get(
    "/admin/dashboard",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/admin/dashboard",
        method: "GET",
        // The reporting period, forwarded as-is. The upstream resolves and
        // validates it in IST — the gateway must never decide what "today" is.
        query: {
          range: typeof req.query.range === "string" ? req.query.range : undefined,
          from: typeof req.query.from === "string" ? req.query.from : undefined,
          to: typeof req.query.to === "string" ? req.query.to : undefined,
        },
      }),
    ),
  );

  // The dashboard's breakdowns. Same period vocabulary, same upstream
  // resolution in IST — a separate request so the headline tiles are never
  // waiting on seven aggregates.
  router.get(
    "/admin/analytics",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/admin/analytics",
        method: "GET",
        query: {
          range: typeof req.query.range === "string" ? req.query.range : undefined,
          from: typeof req.query.from === "string" ? req.query.from : undefined,
          to: typeof req.query.to === "string" ? req.query.to : undefined,
        },
      }),
    ),
  );

  router.get(
    "/admin/orders",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/admin/orders/",
        method: "GET",
        query: passQuery(req, [
          "status", "customer_id", "supplier_id", "order_number",
          // `range` is the named period (today, 7d, …); from/to carry a
          // custom one. The upstream resolves both in IST.
          "range", "from", "to", "limit", "offset",
        ]),
      }),
    ),
  );

  // The courier handover sheet. CSV, so the JSON-only relay guard has to be
  // opened for this one route explicitly.
  router.get(
    "/admin/orders/export.csv",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/admin/orders/export.csv",
        method: "GET",
        allowContentTypes: ["text/csv"],
        query: {
          range: typeof req.query.range === "string" ? req.query.range : undefined,
          from: typeof req.query.from === "string" ? req.query.from : undefined,
          to: typeof req.query.to === "string" ? req.query.to : undefined,
          status: typeof req.query.status === "string" ? req.query.status : undefined,
        },
      }),
    ),
  );

  router.get(
    "/admin/orders/:id",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/admin/orders/${req.params.id}`, method: "GET",
      }),
    ),
  );

  // Fulfilment: paid -> processed -> dispatched. The period parameters are
  // forwarded so "process all" means "all in the period on screen", never the
  // whole table.
  for (const action of ["process", "dispatch"] as const) {
    router.post(
      `/admin/orders/bulk-${action}`,
      adminOnly,
      validate(bulkOrderSchema),
      handler(async (req, res) =>
        forward(req, res, config, {
          upstream: "orders",
          path: `/admin/orders/bulk-${action}`,
          method: "POST",
          body: req.body,
          query: {
            range: typeof req.query.range === "string" ? req.query.range : undefined,
            from: typeof req.query.from === "string" ? req.query.from : undefined,
            to: typeof req.query.to === "string" ? req.query.to : undefined,
          },
        }),
      ),
    );
  }


  router.post(
    "/admin/orders/:id/process",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: `/admin/orders/${req.params.id}/process`,
        method: "POST",
      }),
    ),
  );

  router.post(
    "/admin/orders/:id/record-payment",
    adminOnly,
    validate(recordPaymentSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: `/admin/orders/${req.params.id}/record-payment`,
        method: "POST",
        body: req.body,
      }),
    ),
  );

  router.post(
    "/admin/orders/:id/dispatch",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/admin/orders/${req.params.id}/dispatch`, method: "POST",
      }),
    ),
  );

  router.post(
    "/admin/orders/:id/cancel",
    adminOnly,
    validate(z.object({ reason: z.string().trim().min(1, "is required").max(500) })),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: `/admin/orders/${req.params.id}/cancel`,
        method: "POST", body: req.body,
      }),
    ),
  );

  // --- admin: wallets (CLAUDE.md §6.7) ----------------------------------------

  router.get(
    "/admin/wallets",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/admin/wallets/", method: "GET",
        query: passQuery(req, ["limit", "offset", "include_empty"]),
      }),
    ),
  );

  router.get(
    "/admin/wallets/:customerId",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: `/admin/wallets/${encodeURIComponent(req.params.customerId ?? "")}`,
        method: "GET",
      }),
    ),
  );

  // Sends balance back to the payments it came from. Audited upstream.
  router.post(
    "/admin/wallets/:customerId/refund",
    adminOnly,
    validate(walletRefundSchema),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: `/admin/wallets/${encodeURIComponent(req.params.customerId ?? "")}/refund`,
        method: "POST", body: req.body,
      }),
    ),
  );

  router.get(
    "/admin/payouts/summary",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/admin/payouts/summary", method: "GET",
      }),
    ),
  );

  // Returns text/csv, so the route opts in explicitly — the default is
  // JSON-only, which stops an upstream error page reaching a browser.
  router.get(
    "/admin/payouts/export.csv",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/admin/payouts/export.csv",
        method: "GET",
        query: passQuery(req, ["supplier_id"]),
        allowContentTypes: ["text/csv"],
      }),
    ),
  );

  router.get(
    "/admin/payouts",
    adminOnly,
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders",
        path: "/admin/payouts/",
        method: "GET",
        query: passQuery(req, ["supplier_id", "status", "limit", "offset"]),
      }),
    ),
  );

  router.post(
    "/admin/payouts/mark-paid",
    adminOnly,
    validate(
      z.object({
        payout_ids: z.array(z.string().uuid()).min(1, "at least one payout is required").max(200),
        // The NEFT UTR. Required so a settlement can be reconciled against a
        // bank statement.
        reference_no: z.string().trim().min(1, "is required").max(64),
        notes: z.string().max(500).optional().default(""),
      }),
    ),
    handler(async (req, res) =>
      forward(req, res, config, {
        upstream: "orders", path: "/admin/payouts/mark-paid",
        method: "POST", body: req.body,
      }),
    ),
  );

  return router;
}

# vm-client-mobile-ui

The **Vayalavan** customer app for Android and iOS. Feature parity with
the `vm-client-ui` web storefront, built with Expo (SDK 57) and React Native.

The second of the two mobile apps in this repository. Its sibling,
[`vm-supplier-mobile-ui`](../vm-supplier-mobile-ui/README.md), is the grower's
app. They are deliberately near-identical in structure, tooling and SDK — a
developer who has run one can run the other, and a phone with one Expo Go
install can run both. **Anything about Expo Go, SDK 57 or EAS applies equally to
both apps and is explained once, in the supplier app's README.** This one covers
what is different here.

---

## Quick start

Two terminals. The backend belongs to `./scripts/start.sh`; this app belongs to
`./scripts/start-mobile.sh customer`.

```bash
./scripts/start.sh                     # terminal 1 — Postgres, the APIs, the three web UIs
./scripts/start-mobile.sh customer     # terminal 2 — this app's Expo dev server
```

Then scan the QR code with Expo Go.

`./scripts/start-mobile.sh` with no argument starts the **supplier** app; this one is
always named. Both can run at once — they have separate Metro ports (8090 and
8091) and share one `MOBILE_API_BASE_URL`, because there is one gateway.

The script re-points `MOBILE_API_BASE_URL` at this machine's current LAN
address, frees this app's Metro port, and checks the gateway answers on the
address the phone will actually use. `--tunnel`, `--ios`, `--android`,
`--clear`, `--keep-url` and `--stop` all work as they do for the supplier app;
`--stop` frees both ports.

| Target | What it does |
| --- | --- |
| `make -C infra mobile-client` | Expo dev server (QR code, simulators) |
| `make -C infra mobile-ip` | Rewrite `MOBILE_API_BASE_URL` — affects both apps |
| `make -C infra mobile-client-tunnel` | Metro over a public tunnel |
| `make -C infra mobile-client-android` | Native debug build onto a device or emulator |
| `make -C infra mobile-client-ios` | Native debug build onto a simulator or device |
| `make -C infra mobile-test` | Unit tests — **both** mobile apps |
| `make -C infra mobile-lint` | `tsc --noEmit` — **both** mobile apps |
| `make -C infra mobile-doctor` | `expo-doctor` — **both** mobile apps |

`make -C infra test` and `make -C infra lint` include this app.

Browsing needs no account. To order, sign in as the seeded customer
(`customer@vayal.test`, password `SEED_CUSTOMER_PASSWORD` from `.env`) or create
an account from the app — customers self-register, unlike suppliers.

---

## What it does

Everything the web storefront does:

| Screen | The web route it replaces |
| --- | --- |
| **Shop** | `/` — today's produce, category and grade filters, the cutoff countdown |
| **Product** | `/product/:id` — full description, the grower, every pack size |
| **Cart** | the slide-over drawer — quantities, the re-pricing notice, the fee breakdown |
| **Checkout** | `/checkout` — address, breakdown, place order |
| **Orders** | `/orders` and `/orders/:id` — the three-milestone timeline |
| **Account** | `/profile` plus the header menu and the footer |

The four tabs are Shop, Cart, Orders and Account, mirroring the web header:
today's produce, the cart button, the orders link and the account menu.

### What is deliberately different

**Signed-out browsing.** The supplier app swaps its whole navigation tree on
sign-in; this one does not. A shop that demands an account before showing a
price is a shop people close, the web storefront lets anyone browse, and App
Store review section 5.1.1 requires an app to work for the parts that do not
need an account. Sign-in is a modal pushed at the moment it is needed — adding
to a cart, or opening Orders — and dismissing it returns you to the catalogue
with your scroll position intact.

**The cart is a tab, not a drawer.** A phone has no room for an overlay that
covers what you were reading. The tab badge is the cart count, visible from
every screen without opening anything.

**Sold-out produce is shown, not hidden** (CLAUDE.md §5.2), the same as the web
storefront: the photograph is dimmed behind a "Sold out for today" label and the
pack sizes are struck through. React Native has no CSS `grayscale` filter, so
the treatment is a scrim rather than the web's desaturation — see the note in
`components/ProduceImage.tsx`.

**The policy pages are links, not screens.** Terms, privacy, shipping and
refunds open the published web pages in the phone's browser via
`MOBILE_STOREFRONT_URL`. Two copies of the same legal text drift, and the copy
someone agrees to when they pay is the published one. With the variable unset,
the links are simply absent.

### Payments

Razorpay Standard Checkout, in a **modal WebView** — not the native SDK. The
native SDK is a native module and is therefore not in Expo Go, so adopting it
would mean a development build for anyone who wants to test a payment on a real
handset, which is the exact cost SDK 57 is pinned to avoid (CLAUDE.md §3).
Checkout is a web sheet either way, and this way the phone and the browser open
the same one.

Three files:

| File | Job |
|------|-----|
| `src/lib/razorpayHtml.ts` | Builds the page the WebView loads, and parses what it posts back. Pure, and tested — a customer's own name goes into a `<script>` block, so the escaping is a correctness property. |
| `src/components/RazorpayCheckout.tsx` | The modal. Shows the sheet, returns exactly one outcome, and is closable even if the page fails to load. |
| `src/lib/payment.ts` | Decides what an outcome means. Mirrors vm-client-ui's module of the same name; `payment.test.ts` asserts the wording and the phase list still agree. |

**The key id comes from the server, not the manifest.** `POST /orders` returns
`razorpay_key_id` alongside `razorpay_order_id`, so there is no
`MOBILE_RAZORPAY_KEY_ID` to bake in — a key rotated at the server reaches an
already-installed binary on its own, and an order can never be opened under a
different key than it was created with. Nothing secret is involved either way:
the key id is public, and the key secret never leaves vm-orders-api.

**A paid sheet does not mean a paid order.** The success callback only starts
the app polling `GET /orders/{id}`; the **webhook** is what moves an order to
`paid` (CLAUDE.md §6.4 step 4). If the webhook is slow the screen says the
payment went through and the confirmation is still coming — it never shows a
tick for an order the server has not moved. If the webhook is not reachable at
all — the usual case on a laptop, since Razorpay cannot call `localhost` — the
order stays in `pending_payment` and this is a **local setup** problem, not an
app bug. See the root README, "Testing payments locally".

**A dismissed sheet is not a failure.** The order keeps its reservation for
`RESERVATION_TTL_MINUTES`, and both the confirmation screen and the order
screen offer Pay now until the sweeper releases it.

---

## Configuration

Reads the **repository root `.env`** — the same single file the gateway, the
three web UIs and the supplier app use (CLAUDE.md §8). `app.config.ts` loads it
in Node at build time and copies what the app needs into the Expo manifest's
`extra` block.

| Variable | Purpose |
| --- | --- |
| `MOBILE_API_BASE_URL` | The gateway, including `/api`, as the phone can reach it. **Shared with the supplier app** |
| `SUPPORT_EMAIL` | Shown on sign-in and Account; shared with the rest of the platform |
| `MOBILE_CLIENT_METRO_PORT` | This app's Metro port, 8091. The supplier app has 8090 |
| `MOBILE_CLIENT_EAS_PROJECT_ID` | Written by `eas init`. Its own project — only cloud builds need it |
| `MOBILE_STOREFRONT_URL` | Optional. Where the policy pages are published |
| `APP_ENV` | `production` turns off the local-network HTTP exemption |

The two required variables are checked in `app.config.ts`, which **fails the
build** naming the missing one. That is the only fail-fast that means anything
here: a native binary cannot be reconfigured after it ships, so a runtime check
would be a white screen on a customer's phone rather than an error on the
machine that built it. `src/lib/config.ts` re-checks at runtime purely so a
corrupted manifest renders an explanation instead of a blank view.

Everything in `extra` ships inside the app and can be read out of it. **Never
put a secret there.** This app needs none — and when Razorpay is enabled, the
key that goes here is the public key id.

CORS does not apply (a native app sends no `Origin` header), and the cleartext
HTTP exemption for `make dev` is granted only when `APP_ENV` is not
`production`. Both work exactly as documented in the supplier app's README.

---

## Building for the stores

Same as the supplier app, with its own EAS project and its own bundle id
(`com.vayalmikrogreenz.shop`, against the supplier's
`com.vayalmikrogreenz.supplier`):

```bash
eas init                                   # writes MOBILE_CLIENT_EAS_PROJECT_ID
eas env:create --name MOBILE_API_BASE_URL --value https://api.example.com/api
eas env:create --name SUPPORT_EMAIL --value vayal.mikrogreenz@gmail.com
eas env:create --name MOBILE_STOREFRONT_URL --value https://example.com

eas build --profile preview --platform android   # internal APK
eas build --profile production --platform all
```

`MOBILE_STOREFRONT_URL` is optional for development and effectively required
before submission: both stores want a reachable privacy policy, and this is how
the app links to it.

---

## Layout

```
app.config.ts        Expo config; reads the root .env and fails the build if incomplete
index.ts             Native entry point
assets/              Icons, the lockup and the produce placeholder,
                     generated by `make -C infra sync-brand`
src/
  App.tsx            Providers, the config gate, the error boundary
  theme/tokens.ts    The Vayal palette, bound to vm-ui-kit by tokens.test.ts
  lib/
    api.ts           The HTTP client. Pure TypeScript, no React Native imports
    client.ts        The app's single client instance and access-token store
    auth.tsx         Sign in, register, single-flight refresh, SecureStore
    cart.tsx         The server-side cart, as a TanStack Query wrapper
    catalogCounts.ts The "N items available today" line
    config.ts        Runtime config, read from the Expo manifest
    countdown.ts     The cutoff countdown's two renderings, visible and spoken
    datetime.ts      IST formatting without Intl
    idempotency.ts   Order-placement keys, because Hermes has no crypto.randomUUID
    money.ts         Paise and grams
    queryClient.ts   TanStack Query, wired to NetInfo and AppState
    types.ts         Wire types for the customer API surface
  components/        Text, Card, Button, Field, Feedback, Screen, Logo,
                     ProduceImage, QuantityStepper, CutoffBanner, OrderTimeline,
                     SignInPrompt
  navigation/        The tab and stack graph
  screens/           Login, Catalog, Product, Cart, Checkout, Orders,
                     OrderDetail, Account, Addresses, AddressForm
```

### Why this is not an npm workspace

The same reason as the supplier app: Expo SDK 57 pins React 19.1, the three web
UIs are on React 18.3, and hoisting both into one root `node_modules` leaves npm
to pick. Its own install costs disk and buys a lockfile that means what it says.
It therefore cannot import `@vayal/ui-kit` (which is React DOM anyway).

Where that forces a duplicate, a test asserts the copies agree rather than
leaving them to drift:

| Duplicated | Bound by |
| --- | --- |
| The colour ramps | `theme/tokens.test.ts`, against vm-ui-kit's Tailwind preset |
| The rupee formatter | `lib/money.test.ts`, the same cases the web and Go suites run |
| The count-line copy | `lib/catalogCounts.test.ts`, the same cases as vm-client-ui |
| The splash/icon background hex | `theme/tokens.test.ts`, against `app.config.ts` |

---

## Decisions worth knowing about

### The cutoff countdown stops when the app is backgrounded

The server owns the decision — before or after the 4pm cutoff, and how many
seconds are left (CLAUDE.md rule 2). The local timer only animates between
server answers.

A phone adds a case the web banner does not have: a backgrounded app stops
running timers. Resuming a frozen countdown from where it stopped would be a lie
about time remaining, so the interval is torn down on blur and the server is
re-asked on focus.

### The quantity stepper debounces, and sends absolute quantities

Holding "+" to reach 15 is one request, not fifteen. Each tap would otherwise
fire a PATCH *and* a cart refetch — thirty requests, enough to trip the
gateway's rate limit, the UI attacking its own API. The value sent is the
absolute quantity rather than a delta, so collapsing ten taps into one request
still lands on the number the customer chose.

### Order placement generates its key once

`crypto.randomUUID()` does not exist in Hermes, so `lib/idempotency.ts` builds
the `Idempotency-Key` itself (CLAUDE.md rule 6). It is generated once per
checkout attempt and held in a ref: minting a fresh one per render — or per
retry — would defeat the entire point and place a second real order on a
double-tap.

### The catalogue is a FlatList, everything else is a ScrollView

`components/Screen.tsx` is the shared scaffold and it wraps a ScrollView, which
renders every child up front. On a morning where forty growers have listed, that
is forty presigned image fetches before the customer has scrolled past the third
card. The catalogue is the one screen that breaks the rule; the hero, banner and
filters ride along as `ListHeaderComponent` so it still scrolls as one page.

### Session storage differs from the web app

The refresh token lives in `expo-secure-store` — the iOS Keychain and Android's
EncryptedSharedPreferences — under a key namespaced to this app
(`vayal.customer.refresh`). Both apps can be installed on one handset, and a
shared key name would have them overwriting each other's session.

Refreshes are single-flight. vm-profile-api rotates refresh tokens and treats a
reused one as theft, revoking every session for the account; two concurrent
refreshes would not race, they would sign the customer out of everything.

---

## Testing

```bash
npm test          # or: make -C infra mobile-test  (runs both mobile apps)
```

`node --test` over the pure-TypeScript modules — no React Native renderer, no
Jest. What is covered is what breaks silently and costs money or trust:

- **money** — Indian digit grouping, and that no paise is lost to a float
- **catalogCounts** — the count line, including "everything sold out"
- **countdown** — the visible clock ticks every second, the spoken one does not
- **datetime** — the IST day boundary, computed by arithmetic rather than `Intl`
- **idempotency** — 10,000 keys in a burst, all distinct, all within the
  server's length limit
- **api** — URL joining, the timeout, abort handling, and the 401-refresh-retry
- **tokens** — every colour ramp against vm-ui-kit's preset

Screens are not unit-tested. Their logic lives in the modules above; what is
left is layout, and a snapshot of a `<View>` tree tests nothing a person would
notice.

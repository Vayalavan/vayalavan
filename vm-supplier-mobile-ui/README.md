# vm-supplier-mobile-ui

The Vayalavan Supplier app for Android and iOS. Feature parity with the
`vm-supplier-ui` web portal, built with Expo (SDK 57) and React Native.

One of two mobile apps here; the other is
[`vm-client-mobile-ui`](../vm-client-mobile-ui/README.md), the customer
storefront. They share this SDK pin, this tooling and one `MOBILE_API_BASE_URL`,
and have separate Metro ports so both can run at once. Everything below about
Expo Go, SDK 57 and EAS applies to both.

Suppliers declare availability from a field at 6am, not from a desk. This app
exists because that is a phone job: it does everything the web portal does, and
adds the two things a browser cannot — a camera straight into a product
listing, and a session that survives being backgrounded all morning.

---

## Quick start

Two terminals. The backend belongs to `./start.sh`; this app belongs to
`./start-mobile.sh`, and neither touches the other's processes.

```bash
./start.sh          # terminal 1 — Postgres, the APIs, the three web UIs
./start-mobile.sh   # terminal 2 — just the Expo dev server
```

With no argument that script starts THIS app. `./start-mobile.sh customer`
starts the storefront app instead, on its own Metro port.

Then scan the QR code with Expo Go.

`start-mobile.sh` does the three things that have actually gone wrong on a real
handset, before printing the QR code: it re-points `MOBILE_API_BASE_URL` at this
machine's current LAN address, frees the Metro port if a previous run is holding
it, and checks the gateway is answering — on the LAN address the phone will use,
not just on localhost. It never starts the backend; it only tells you when it is
missing.

| Flag | |
| --- | --- |
| `--tunnel` | Metro over a public tunnel, when the LAN will not carry it |
| `--ios` / `--android` | also open a simulator or emulator |
| `--clear` | start with an empty Metro cache |
| `--keep-url` | leave `MOBILE_API_BASE_URL` alone (pointing at a deployed API) |
| `--stop` | free both apps' Metro ports and exit |

A public `MOBILE_API_BASE_URL` is never rewritten — only a private or loopback
one — so pointing the app at staging survives a run of this script.

The `make` targets below do the same jobs individually if you prefer them.

Sign in with the seeded supplier account — `make -C infra seed` prints it, and
the password is `SEED_SUPPLIER_PASSWORD` from `.env`.

| Target | What it does |
| --- | --- |
| `make -C infra mobile` | Expo dev server (QR code, simulators) |
| `make -C infra mobile-ip` | Rewrite `MOBILE_API_BASE_URL` to this machine's LAN address |
| `make -C infra mobile-tunnel` | Metro over a public tunnel, when the LAN will not carry it |
| `make -C infra mobile-android` | Native debug build onto a device or emulator |
| `make -C infra mobile-ios` | Native debug build onto a simulator or device |
| `make -C infra mobile-test` | Unit tests — **both** mobile apps |
| `make -C infra mobile-lint` | `tsc --noEmit` — **both** mobile apps |
| `make -C infra mobile-doctor` | `expo-doctor` — **both** mobile apps |

`make -C infra test` and `make -C infra lint` include this app, so it is covered
by the same commands as everything else.

### Expo Go or a development build?

Expo Go is enough for everything except a native `expo-secure-store` keychain
entry surviving a reinstall, and it is the fastest way to see a change. Use
`mobile-android` / `mobile-ios` when you need to test the camera on a real
handset, permission prompts, or anything about how the app behaves cold.

Every dependency here is either an Expo SDK package or one Expo Go bundles
(`@react-native-community/netinfo`, `react-native-screens`,
`react-native-safe-area-context`, `react-native-gesture-handler`), so nothing in
this app requires a custom build to run.

---

## Troubleshooting

### "Project is incompatible with this version of Expo Go"

Expo Go supports exactly **one** SDK per release. This project targets SDK 57
precisely so that the store builds work (see "Why the SDK is pinned" below), so the usual
cause is a *newer* SDK having been introduced into `package.json`.

Check what the project asks for against what the stores actually ship:

```bash
npx expo config --type public | grep sdkVersion            # expect 54.0.0

# App Store Expo Go
curl -s "https://itunes.apple.com/lookup?id=982107779" \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['results'][0]['version'])"

# Which Expo Go build serves a given SDK
curl -s https://api.expo.dev/v2/versions/latest \
  | python3 -c "import json,sys; v=json.load(sys.stdin)['data']['sdkVersions']['54.0.0']; \
      print('Expo Go — ios', v['iosClientVersion'], '| android', v['androidClientVersion'])"
```

If the SDK has drifted upward, bring it back with `npx expo install --fix`
rather than bumping packages one at a time — they only work as a set.

### Expo Go: "There was a problem running the requested app" / timed out

This is Expo Go failing to fetch the **JS bundle from Metro** — it happens before
any of your code runs, so it is never an app bug. Check the Mac first, then the
phone.

**Mac** (all of these should pass; substitute your own LAN address):

```bash
lsof -nP -iTCP:8090 -sTCP:LISTEN            # Metro listening, on *:8090 not 127.0.0.1
curl -s http://<lan-ip>:8090/status         # -> packager-status:running
/usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate
ifconfig | awk '/^[a-z0-9]+:/{i=$1} /inet /{print i, $2}' | grep -v 127.0.0.1
```

If Metro answers on the LAN address, the Mac is fine and the problem is the
phone reaching it. **Open `http://<lan-ip>:8090/status` in the phone's browser** —
that one test splits the two causes apart:

- **It loads.** The phone can reach the Mac, so Expo Go specifically is blocked.
  On iOS: *Settings → Privacy & Security → Local Network → Expo Go*. iOS gates
  LAN access per app; denied once, Expo Go can never reach Metro and times out
  exactly like this. The browser has its own permission, which is why it still
  works.
- **It does not load.** The phone is not routing to the Mac. Check both are on
  the same SSID (not a guest network or a separate 5 GHz twin, and that the
  phone has not fallen back to cellular), that neither is on a VPN, and that the
  router does not have **AP / client isolation** enabled — it blocks
  device-to-device traffic and is common on ISP-supplied and guest networks.

Useful probes from the Mac, given the phone's own IP (Settings → Wi-Fi → ⓘ):

```bash
ping -c2 192.168.1.1                 # the router — should always answer
ping -c2 <phone-ip>                  # no reply + an "(incomplete)" ARP entry
arp -a -n | grep 192.168.1.          # is the isolation signature
```

**If the network cannot be fixed, use the phone's Personal Hotspot and join the
Mac to it.** Both devices end up on `172.20.10.x` with no router in between,
iOS hotspot does not isolate clients, and traffic between the two stays local —
it does not consume cellular data. This is the only workaround that fixes both
Metro and the API:

```bash
make -C infra mobile-ip     # picks up the new 172.20.10.x address
make -C infra mobile
```

`make -C infra mobile-tunnel` is the alternative, but it only solves Metro:
`MOBILE_API_BASE_URL` is still a LAN address, so the app would load and then
fail every request. An end-to-end session over a tunnel means exposing the
gateway through one too.

### The app loads, then times out on every request

`MOBILE_API_BASE_URL` is wrong. `app.config.ts` now refuses to build for the two
common mistakes, so this should announce itself — but if you see a spinner that
ends in "that took too long", check it first.

It must carry **the scheme, the LAN host, the gateway port, and `/api`**:

```
MOBILE_API_BASE_URL=http://192.168.1.20:8080/api
                    ^^^^  ^^^^^^^^^^^^ ^^^^ ^^^^
                    │     │            │    the gateway serves the API here
                    │     │            GATEWAY_PORT — omit it and you get :80,
                    │     │            where nothing is listening, which shows
                    │     │            up as a timeout rather than a refusal
                    │     this Mac, as the phone sees it: ipconfig getifaddr en0
                    plain http is fine on the LAN in development
```

Then restart Expo. The value is **baked in at build time**, so editing `.env`
while the dev server is running changes nothing.

Prove the path from the outside in:

```bash
curl -m 5 http://<lan-ip>:8080/healthz        # from this Mac
```

Then open the same URL in the phone's browser. If curl works and the phone does
not, it is the network — a guest VLAN, AP isolation, or a VPN on either device.

In a non-production build, the app prints the URL it failed to reach underneath
the error, on the sign-in screen and on every error state.

### "Unable to resolve react-native-web/dist/exports/View"

Something asked for a **web** bundle. `app.config.ts` sets
`platforms: ["ios", "android"]` precisely to stop that — Expo's default includes
`"web"`, which makes `expo start` offer a web target and then fail on a missing
`react-native-web`.

The renderer is deliberately not installed. A web supplier portal already exists
as `vm-supplier-ui`, and it is the better thing on a desktop; a second one built
out of React Native primitives would be a worse copy that still has to be kept
in step. If the error reappears, check that `platforms` survived in
`app.config.ts` and that nothing is passing `--web`.

### Port 8081 is in use

That is `vm-profile-api`. This app's Metro server runs on `MOBILE_METRO_PORT`
(8090 by default) for exactly that reason — use `make -C infra mobile` rather
than a bare `npx expo start`, which would fall back to Metro's default.

---

## Configuration

Reads the **repository root `.env`** — the same single file the gateway and the
three web UIs use (CLAUDE.md §8). `app.config.ts` loads it in Node at build
time and copies what the app needs into the Expo manifest's `extra` block.

| Variable | Purpose |
| --- | --- |
| `MOBILE_API_BASE_URL` | The gateway, including `/api`, as the phone can reach it |
| `SUPPORT_EMAIL` | Shown on sign-in and Account; shared with the rest of the platform |
| `MOBILE_METRO_PORT` | This app's Metro port, 8090. The customer app has 8091 |
| `MOBILE_EAS_PROJECT_ID` | Written by `eas init`. Only cloud builds need it |
| `APP_ENV` | `production` turns off the local-network HTTP exemption |

Both required variables are checked in `app.config.ts`, which **fails the build**
with the variable's name if one is missing. That is the only fail-fast that
means anything here: a native binary cannot be reconfigured after it ships, so a
runtime check would be a white screen on a supplier's phone rather than an error
on the machine that built it. `src/lib/config.ts` re-checks at runtime purely so
a corrupted manifest renders an explanation instead of a blank view.

Everything in `extra` ships inside the app and can be read out of it. **Never
put a secret there.** This app needs none: it authenticates with the supplier's
own password and holds only their tokens.

### CORS

None needed. A native app sends no `Origin` header, so the gateway's allow-list
does not apply — adding an entry for the app would do nothing.

### Cleartext HTTP

Android blocks plain HTTP, and iOS blocks it through ATS. Both are correct;
production is HTTPS. `make dev` serves the gateway over plain HTTP on the LAN,
so `app.config.ts` grants a local-networking exemption **only** when
`APP_ENV` is not `production`. A production build cannot carry it.

---

## Building for the stores

Native projects are generated, not committed: `app.config.ts` is the source of
truth, and a checked-in `android/` or `ios/` drifts the first time someone edits
an `Info.plist` by hand instead of the config. `expo prebuild` regenerates them.

EAS uploads only this directory, so it never sees the root `.env`. Set the same
variables as EAS environment variables:

```bash
eas init                                   # writes MOBILE_EAS_PROJECT_ID
eas env:create --name MOBILE_API_BASE_URL --value https://api.example.com/api
eas env:create --name SUPPORT_EMAIL --value vayal.mikrogreenz@gmail.com

eas build --profile preview --platform android   # internal APK
eas build --profile production --platform all
```

A build with a variable missing fails immediately, naming it, rather than
producing an app that cannot reach the server.

---

## Layout

```
app.config.ts        Expo config; reads the root .env and fails the build if incomplete
index.ts             Native entry point
assets/              Icons and the lockup, generated by `make -C infra sync-brand`
src/
  App.tsx            Providers, the config gate, the error boundary
  theme/tokens.ts    The Vayal palette, bound to vm-ui-kit by tokens.test.ts
  lib/
    api.ts           The HTTP client. Pure TypeScript, no React Native imports
    client.ts        The app's single client instance and access-token store
    auth.tsx         Sign in/out, single-flight refresh, SecureStore
    config.ts        Runtime config, read from the Expo manifest
    datetime.ts      IST formatting without Intl (see below)
    money.ts         Paise, rupees and grams
    queryClient.ts   TanStack Query, wired to NetInfo and AppState
    types.ts         Wire types for the supplier API surface
  components/        Text, Card, Button, Field, Feedback, Screen, Logo, image upload
  navigation/        The tab and stack graph
  screens/           Login, Availability, Products, ProductForm, Sales, Import, Account
```

Screens map one to one onto the web app's routes, with `AccountScreen` added:
sign-out lives in the web header and the support address in its footer, and
neither has anywhere to go on a phone.

---

## Decisions worth knowing about

### Why the SDK is pinned

**SDK 57 is the newest SDK that the App Store and Play Store builds of Expo Go
support.** Expo stopped publishing Expo Go to the stores after 54; builds for 55
and later exist only as GitHub release artefacts.

That is survivable on Android, where the artefact is an APK you can sideload. It
is not on iOS: the iOS artefact is an unpacked `.app` for the **Simulator**, and
Apple permits no sideload path onto a physical iPhone. A newer SDK therefore
means no Expo Go on a real iPhone at all — only an Xcode simulator, which cannot
test the camera, or a paid Apple Developer membership for a development build.

Since the camera is half the reason this app exists, SDK 57 it is.

| | Ships | Supports |
| --- | --- | --- |
| App Store Expo Go | 54.0.2 | SDK 57 |
| Play Store Expo Go | 54.0.8 | SDK 57 |

Move off 54 when there is a reason to — an EAS development build removes the
constraint entirely, and you need one before launch anyway to test real
permission prompts. Until then, upgrading the SDK silently costs iPhone testing.
Change versions only as a set, with `npx expo install --fix`.

Two consequences worth knowing, both marked in the code:

- `expo-file-system` v19 has no `UploadTask` or `File.upload()` — those arrived
  in SDK 57. The photo upload and the CSV template download use
  `expo-file-system/legacy`, which on this SDK is the stable API for network
  transfers, not a deprecated one.
- `tsconfig.json` maps `expo-file-system/legacy` to the declarations the package
  ships. Without an `exports` map for that subpath, TypeScript resolves it to
  the library's own source and then typechecks it under our settings, where it
  fails `exactOptionalPropertyTypes`. Metro is unaffected.

### Why this is not an npm workspace

Expo SDK 57 pins React 19.1; the three web UIs are on React 18.3. Hoisted into
one root `node_modules`, npm has to choose, and it resolves that choice
differently depending on install order and machine. This app keeps its own
`node_modules` and its own `package-lock.json`. It costs disk and buys a
lockfile that means what it says.

The knock-on effect is that `@vayal/ui-kit` cannot be imported here — and it
could not be used anyway, since it is React DOM and Tailwind classes. What it
owns that this app also needs is the **palette**, so `theme/tokens.ts`
transcribes the ramps and `theme/tokens.test.ts` imports
`packages/vm-ui-kit/tailwind-preset.js` and asserts every value matches. Change
the brand in the preset and this app's tests fail until it is updated, which is
the guarantee that actually matters. Components still never write a hex value.

### Why the API client is rewritten rather than shared

`@vayal/ui-kit`'s client is built on three browser primitives Hermes does not
have: `AbortSignal.timeout()`, `AbortSignal.any()` and `crypto.randomUUID()`.
Importing it would mean requests with no timeout at all, hanging forever, on a
handset, in a field. `src/lib/api.ts` keeps the same contract — Bearer token,
`X-Request-Id` on every request, the platform's single error envelope decoded
into a typed `ApiError` — and rebuilds those three pieces. It has no React
Native imports, so it is tested under `node --test`.

It adds one thing the web client has no need for: **refresh-and-retry on 401**.
A browser tab gets reloaded, which re-runs the refresh flow; a phone app is
backgrounded for hours and resumed, and with a 15-minute access token every
resume would otherwise be a sign-in screen. A 401 triggers one refresh and one
retry — never more, so a revoked session cannot become a loop.

### Why dates are not formatted with Intl

The web UIs pass `timeZone: "Asia/Kolkata"` to `Intl.DateTimeFormat`. Hermes
ships without a full ICU database on Android, where that option is either
ignored — silently formatting in device local time, the exact bug CLAUDE.md rule
2 exists to prevent — or throws. `src/lib/datetime.ts` shifts by a fixed
UTC+05:30 instead. India has had one offset nationwide and no daylight saving
since 1945, so there is no rule to look up: this is both more portable and more
correct here. `datetime.test.ts` runs the cutoff table from CLAUDE.md §6.1 and
passes under any `TZ`.

### Refresh tokens go in the keychain

The web app keeps its refresh token in `localStorage` and documents that as a
deliberate trade against XSS. There is no such trade on a phone: `expo-secure-store`
is the iOS Keychain and Android's EncryptedSharedPreferences. It is written
`WHEN_UNLOCKED_THIS_DEVICE_ONLY`, so a stolen encrypted backup cannot restore a
supplier's session onto another handset. The access token stays in memory and
dies with the process.

Refreshes are single-flight. `vm-profile-api` rotates refresh tokens and treats
a reused one as theft, revoking every session for that account — so two
concurrent refreshes do not race, they sign the supplier out of everything.
Every path into a refresh shares one promise.

### Photos are downscaled before upload

A photo off a modern phone camera is 8–12 MB, comfortably over the 5 MB server
limit (CLAUDE.md §6.6). Without downscaling, a supplier would tap "Take photo"
and be told their own camera's output is too big, with no way to fix it.
`ProductImageField` resizes to 1600px and re-encodes as JPEG — around 300–600 KB
for a produce photo, which also matters on the mobile data these uploads happen
over. Re-encoding also strips EXIF, which otherwise carries the GPS coordinates
of the supplier's farm into a storage bucket.

### Offline behaviour

TanStack Query gets two signals from a browser that React Native does not
provide, and both matter in the conditions suppliers work in. `queryClient.ts`
wires `onlineManager` to NetInfo, so a request made in a dead spot pauses
instead of failing and retrying against nothing, and `focusManager` to
`AppState`, so returning to the app revalidates the availability sheet. Every
list screen also has pull-to-refresh.

Mutations are never retried automatically. Not every supplier write is
idempotent — "copy yesterday" and the CSV commit are not — and a silent second
attempt after an ambiguous failure is how a catalogue gets imported twice.

### Deliberate differences from the web UI

| Web | Here | Why |
| --- | --- | --- |
| Drag-and-drop CSV | System file picker | There is no drag and drop on a phone |
| "Download template" | Fetch, then the share sheet | A phone has no Downloads folder a supplier can find; the share sheet puts the file into Sheets, Drive or WhatsApp |
| CSV preview: every row in a table | Only the rows that failed | Scrolling 2000 rows to find four broken ones is not review |
| Sales: two wide tables | Cards and disclosure rows | A horizontal scroll inside a vertical scroll is miserable one-handed |
| `<select>` dropdowns | Chip rows | Fewer taps, and every option is visible at once |
| Four nav links incl. Import CSV | Four tabs; Import in the Products stack | Import is a once-a-season job; a fifth tab would shrink the four done daily |
| Sign-out in the header | Account tab | Nowhere else for it to live |

---

## Testing

```bash
npm test          # or make -C infra mobile-test
npm run lint      # tsc --noEmit
```

Covers what CLAUDE.md rule 9 names, for the parts this app owns:

- **money** — paise arithmetic, Indian digit grouping, the exact-decimal parser
  (`parseFloat("45.50") * 100` is `4549.999…`, which is why it is hand-rolled),
  and round trips through both the product form and the availability box.
- **datetime** — the §6.1 cutoff table, UTC-to-IST conversion, and the bare-date
  off-by-one that moves a delivery day.
- **api** — URL joining (the `/api` mount point that `new URL` silently drops),
  timeout versus caller-cancellation, the 401 refresh-and-retry bound, and error
  envelope decoding.
- **tokens** — parity with `vm-ui-kit`'s Tailwind preset.

Screens are not unit-tested. The logic they contain lives in the modules above,
which are; what is left is layout, and a snapshot of that would need updating
every time the design moves without ever catching a bug worth catching.

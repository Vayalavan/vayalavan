#!/usr/bin/env bash
#
# Copies the master logo from packages/vm-ui-kit into each app's public/.
#
# The components reference "/vayal-logo.png" by URL rather than importing it
# (Vite's library mode would inline a ~1 MB base64 data URI into the shared
# bundle), so every app needs its own served copy. This keeps them from
# drifting out of sync with the master.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BRAND="$ROOT/packages/vm-ui-kit/src/logo"
PRODUCE="$ROOT/packages/vm-ui-kit/src/brand"

for ui in vm-client-ui vm-admin-ui vm-supplier-ui; do
    mkdir -p "$ROOT/$ui/public"
    # Served copy is 512px; the 1024px master is kept for print and stores.
    sips -Z 512 "$BRAND/vayal-logo.png" --out "$ROOT/$ui/public/vayal-logo.png" >/dev/null
    cp "$BRAND/vayal-mark-180.png" "$ROOT/$ui/public/apple-touch-icon.png"
    cp "$BRAND/vayal-mark-32.png"  "$ROOT/$ui/public/favicon-32.png"
    cp "$BRAND/vayal-logo-192.png" "$ROOT/$ui/public/icon-192.png"
    cp "$BRAND/vayal-logo-512.png" "$ROOT/$ui/public/icon-512.png"
    # Stand-in artwork for produce with no photograph. Served at 320px: it is
    # only ever drawn inside a card thumbnail, and the 1000px master triples
    # the bytes for detail nobody sees. One URL, so the browser fetches it
    # once however many cards are missing a photo.
    sips -Z 320 "$PRODUCE/produce-placeholder-master.png" \
        --out "$ROOT/$ui/public/produce-placeholder.png" >/dev/null
    echo "  synced $ui/public"
done

# The Expo apps take the same master through a different set of shapes. Icons
# there are consumed by the native build rather than served, and the names are
# fixed by each app.config.ts.
#
# Both apps get the same artwork on purpose. They are two store listings of one
# brand, and a customer who also grows should see the same leaf twice — the app
# NAME is what distinguishes them ("Vayalavan" and "Vayalavan Supplier").
for app in vm-supplier-mobile-ui vm-client-mobile-ui; do
    MOBILE="$ROOT/$app/assets"
    mkdir -p "$MOBILE"

    # Store and launcher icon, and the splash artwork: the full 1024px master,
    # as both stores expect.
    cp "$BRAND/vayal-logo.png" "$MOBILE/icon.png"
    cp "$BRAND/vayal-logo.png" "$MOBILE/splash-icon.png"

    # Shown inside the app, on the sign-in and account screens.
    sips -Z 512 "$BRAND/vayal-logo.png" --out "$MOBILE/logo-lockup.png" >/dev/null

    # Android adaptive icons are cropped to a shape the launcher chooses — a
    # circle, a squircle, a teardrop — and only the middle ~66% is guaranteed to
    # survive. The mark is scaled to fit that safe zone and padded out to 1024 on
    # the surface colour, so no launcher shape clips the leaf off.
    sips -Z 620 "$BRAND/vayal-logo.png" --out "$MOBILE/.adaptive-inner.png" >/dev/null
    sips -p 1024 1024 --padColor FAF9F6 "$MOBILE/.adaptive-inner.png" \
        --out "$MOBILE/adaptive-icon.png" >/dev/null
    rm -f "$MOBILE/.adaptive-inner.png"

    echo "  synced $app/assets"
done

# Only the customer app draws produce cards, so only it needs the stand-in for
# produce with no photograph. Bundled at 320px for the same reason the web copy
# is: it is never drawn larger than a card thumbnail.
sips -Z 320 "$PRODUCE/produce-placeholder-master.png" \
    --out "$ROOT/vm-client-mobile-ui/assets/produce-placeholder.png" >/dev/null
echo "  synced vm-client-mobile-ui/assets/produce-placeholder.png"

# ---------------------------------------------------------------------------
# Storefront photography
# ---------------------------------------------------------------------------
#
# The hero, the category tiles and the "Why choose" panel. Only the two
# CUSTOMER clients show them: an admin console and a grower's portal are tools,
# and dressing a tool in stock photography wastes bytes on people who open it
# forty times a day.
#
# Served small on purpose. The tiles are never drawn wider than ~320px on the
# web or ~180px on a phone, and a 1000px master behind a 160px tile is three
# quarters of a megabyte of detail nobody sees.
PHOTOS="$ROOT/packages/vm-ui-kit/src/brand/photos"

mkdir -p "$ROOT/vm-client-ui/public/photos"
sips -Z 1600 "$PHOTOS/hero-master.jpg" --out "$ROOT/vm-client-ui/public/photos/hero.jpg" >/dev/null
sips -Z 1200 "$PHOTOS/why-master.jpg"  --out "$ROOT/vm-client-ui/public/photos/why.jpg"  >/dev/null
for cat in all fruit vegetable microgreen other; do
    sips -Z 640 "$PHOTOS/cat-$cat-master.jpg" \
        --out "$ROOT/vm-client-ui/public/photos/cat-$cat.jpg" >/dev/null
done
echo "  synced vm-client-ui/public/photos"

# The app bundles what it ships, so the phone copies are smaller again: these
# ride inside the binary an Indian customer downloads over mobile data.
mkdir -p "$ROOT/vm-client-mobile-ui/assets/photos"
sips -Z 1080 "$PHOTOS/hero-master.jpg" \
    --out "$ROOT/vm-client-mobile-ui/assets/photos/hero.jpg" >/dev/null
sips -Z 900 "$PHOTOS/why-master.jpg" \
    --out "$ROOT/vm-client-mobile-ui/assets/photos/why.jpg" >/dev/null
for cat in all fruit vegetable microgreen other; do
    sips -Z 420 "$PHOTOS/cat-$cat-master.jpg" \
        --out "$ROOT/vm-client-mobile-ui/assets/photos/cat-$cat.jpg" >/dev/null
done
echo "  synced vm-client-mobile-ui/assets/photos"

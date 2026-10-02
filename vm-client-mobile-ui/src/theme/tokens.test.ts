/**
 * Binds this app's palette to the platform's single source of truth.
 *
 * CLAUDE.md §9 says the brand is defined once, in vm-ui-kit, and never as hex
 * values in a component. This app cannot import the Tailwind preset at runtime
 * (see the note at the top of tokens.ts), so the guarantee is enforced here
 * instead: the test imports the preset from the repository and asserts every
 * ramp, every step, matches. A brand change made in one place and not the
 * other fails this suite rather than shipping a customer app in last season's
 * green.
 *
 * The preset is plain ESM with no dependencies, so it imports cleanly in Node.
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import path from "node:path";
import { pathToFileURL } from "node:url";

import { colors, radius } from "./tokens";

/**
 * Resolved from this file rather than from process.cwd(), so the suite passes
 * whether it is run from this app, from infra/Makefile, or from the repo root.
 */
const PRESET_PATH = path.resolve(
  __dirname,
  "../../../packages/vm-ui-kit/tailwind-preset.js",
);

interface TailwindPreset {
  theme: {
    extend: {
      colors: Record<string, Record<string, string> | string>;
      borderRadius: Record<string, string>;
    };
  };
}

async function loadPreset(): Promise<TailwindPreset["theme"]["extend"]> {
  const module = (await import(pathToFileURL(PRESET_PATH).href)) as {
    default: TailwindPreset;
  };
  return module.default.theme.extend;
}

const RAMPS = [
  "field",
  "earth",
  "harvest",
  "primary",
  "secondary",
  "accent",
  // The rarity metals, bound the same way: a gold that drifts from the web's
  // would mean the same grade looked rarer on a phone than in a browser.
  "gold",
  "bronze",
  "silver",
  // The storefront canvas: the web's page and this app's must be one colour.
  "cream",
] as const;

for (const ramp of RAMPS) {
  test(`the ${ramp} ramp matches vm-ui-kit's Tailwind preset`, async () => {
    const preset = await loadPreset();
    const expected = preset.colors[ramp];
    assert.ok(
      expected && typeof expected === "object",
      `the preset no longer defines a "${ramp}" colour ramp`,
    );
    // Compared whole rather than step by step: a step ADDED to the preset and
    // missing here is just as much a drift as a step whose value changed.
    assert.deepEqual(
      { ...colors[ramp] },
      expected,
      `colors.${ramp} in tokens.ts has drifted from the preset`,
    );
  });
}

test("the surface colours match vm-ui-kit's Tailwind preset", async () => {
  const preset = await loadPreset();
  assert.deepEqual({ ...colors.surface }, preset.colors["surface"]);
});

test("radius.card matches the preset's rounded-card, converted to points", async () => {
  const preset = await loadPreset();
  // The preset states it in rem. React Native has no rem, so the conversion
  // happens once — here — at the browser default of 16px to the rem.
  const rem = Number(preset.borderRadius["card"]?.replace("rem", ""));
  assert.ok(Number.isFinite(rem), "the preset no longer defines borderRadius.card");
  assert.equal(radius.card, rem * 16);
});

test("radius.panel matches the preset's rounded-panel, converted to points", async () => {
  const preset = await loadPreset();
  const rem = Number(preset.borderRadius["panel"]?.replace("rem", ""));
  assert.ok(Number.isFinite(rem), "the preset no longer defines borderRadius.panel");
  assert.equal(radius.panel, rem * 16);
});

test("the splash and adaptive-icon background in app.config.ts is the surface colour", async () => {
  // app.config.ts is JSON-shaped config evaluated by Expo's own loader, so it
  // cannot import tokens.ts. That leaves exactly one hex value duplicated in
  // this app, and this assertion is what keeps it honest.
  const { readFileSync } = await import("node:fs");
  const source = readFileSync(path.resolve(__dirname, "../../app.config.ts"), "utf8");

  const occurrences = source.match(/backgroundColor: "(#[0-9a-fA-F]{6})"/g) ?? [];
  assert.ok(occurrences.length > 0, "app.config.ts declares no backgroundColor");

  for (const occurrence of occurrences) {
    const hex = occurrence.slice(occurrence.indexOf('"') + 1, -1);
    assert.equal(
      hex.toLowerCase(),
      colors.surface.DEFAULT,
      "app.config.ts paints a background that is not the Vayal surface colour",
    );
  }
});

import vayalPreset from "@vayal/ui-kit/tailwind-preset";

/** @type {import('tailwindcss').Config} */
export default {
  // Brand tokens come from the shared preset (CLAUDE.md §9), exactly as in
  // the other UIs. No local colours.
  presets: [vayalPreset],
  content: [
    "./index.html",
    "./src/**/*.{ts,tsx}",
    // Source, not dist — see vm-admin-ui/tailwind.config.js.
    "../packages/vm-ui-kit/src/**/*.{ts,tsx}",
  ],
};

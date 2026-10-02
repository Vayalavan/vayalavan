import vayalPreset from "@vayal/ui-kit/tailwind-preset";

/** @type {import('tailwindcss').Config} */
export default {
  // All brand tokens come from the shared preset (CLAUDE.md §9). This app
  // must not redefine colours locally.
  presets: [vayalPreset],
  content: [
    "./index.html",
    "./src/**/*.{ts,tsx}",
    // Scan the ui-kit SOURCE, not its build output. Tailwind needs to see
    // the class names the shared components use, or they are purged from
    // this app's stylesheet and the shell renders unstyled. Pointing at
    // source also means it works before ui-kit has been built, and avoids
    // globbing through a workspace symlink.
    "../packages/vm-ui-kit/src/**/*.{ts,tsx}",
  ],
};

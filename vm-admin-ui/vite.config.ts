import { fileURLToPath } from "node:url";
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

/**
 * The repository root, where the single .env lives. Resolved from this
 * file's own location so it works regardless of the directory vite is
 * invoked from.
 */
const rootDir = fileURLToPath(new URL("..", import.meta.url));

/** This app's slot in the platform port map; override with ADMIN_UI_PORT in .env. */
const DEFAULT_PORT = 5174;

export default defineConfig(({ mode }) => {
  // Empty prefix loads every variable, not just VITE_*, so the dev server
  // port can come from the same root .env as everything else. Only VITE_*
  // is ever exposed to client code — see envDir below.
  const env = loadEnv(mode, rootDir, "");

  // Where vm-analytics-ui's remoteEntry.js is served. Baked in at build time
  // like every VITE_ value; required, so a build cannot quietly point the
  // Analytics section at nothing (CLAUDE.md rule 3).
  const analyticsRemote = env.VITE_ANALYTICS_REMOTE_URL;
  if (!analyticsRemote) {
    throw new Error(
      "Missing required environment variable VITE_ANALYTICS_REMOTE_URL. " +
        "Copy .env.example to .env and set it.",
    );
  }

  return {
    plugins: [
      react(),
      // The Module Federation HOST. The shared list must match
      // vm-analytics-ui's: these run as one copy, the host's.
      federation({
        name: "vm_admin",
        remotes: {
          vm_analytics: {
            type: "module",
            name: "vm_analytics",
            entry: analyticsRemote,
          },
        },
        shared: {
          react: { singleton: true },
          "react-dom": { singleton: true },
          "@tanstack/react-query": { singleton: true },
          "@vayal/ui-kit": { singleton: true },
        },
        dts: false,
      }),
    ],
    // Module Federation emits top-level await.
    build: { target: "esnext" },
    // Read .env from the repository root instead of this app's directory.
    // One file for the whole platform (see .env.example).
    envDir: rootDir,
    server: {
      port: Number(env.ADMIN_UI_PORT ?? DEFAULT_PORT),
      // Fail loudly instead of silently sliding to the next free port, which
      // would break the gateway's CORS allow-list and the documented map.
      strictPort: true,
    },
    preview: {
      port: Number(env.ADMIN_UI_PORT ?? DEFAULT_PORT),
      strictPort: true,
    },
  };
});

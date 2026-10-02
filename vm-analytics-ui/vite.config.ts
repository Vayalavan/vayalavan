import { fileURLToPath } from "node:url";
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

/** The repository root, where the single .env lives. */
const rootDir = fileURLToPath(new URL("..", import.meta.url));

/** This app's slot in the platform port map; override with ANALYTICS_UI_PORT in .env. */
const DEFAULT_PORT = 5176;

/**
 * A Module Federation REMOTE: vm-admin-ui loads `./Analytics` from this app's
 * remoteEntry.js at runtime, so analytics ships without rebuilding the console.
 *
 * The shared list must match vm-admin-ui's. React and React Query are
 * singletons — two Reacts on one page break hooks, and two QueryClients
 * would split the cache — so the host's copies are the ones that run.
 */
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, rootDir, "");
  const port = Number(env.ANALYTICS_UI_PORT ?? DEFAULT_PORT);

  return {
    plugins: [
      react(),
      federation({
        name: "vm_analytics",
        filename: "remoteEntry.js",
        exposes: { "./Analytics": "./src/Analytics.tsx" },
        shared: {
          react: { singleton: true },
          "react-dom": { singleton: true },
          "@tanstack/react-query": { singleton: true },
          "@vayal/ui-kit": { singleton: true },
        },
        // The host loads this module's stylesheet along with its code; it has
        // no other way to learn about it.
        bundleAllCSS: true,
        dts: false,
      }),
    ],
    envDir: rootDir,
    // Module Federation emits top-level await.
    build: { target: "esnext" },
    // An absolute origin, not "/": the remote's chunks are fetched by a page
    // on the ADMIN origin, where a root-relative URL would resolve to admin.
    base: env.ANALYTICS_UI_PUBLIC_URL || `http://localhost:${port}/`,
    server: { port, strictPort: true, origin: `http://localhost:${port}` },
    preview: { port, strictPort: true },
  };
});

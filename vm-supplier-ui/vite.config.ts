import { fileURLToPath } from "node:url";
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

/**
 * The repository root, where the single .env lives. Resolved from this
 * file's own location so it works regardless of the directory vite is
 * invoked from.
 */
const rootDir = fileURLToPath(new URL("..", import.meta.url));

/** This app's slot in the platform port map; override with SUPPLIER_UI_PORT in .env. */
const DEFAULT_PORT = 5175;

export default defineConfig(({ mode }) => {
  // Empty prefix loads every variable, not just VITE_*, so the dev server
  // port can come from the same root .env as everything else. Only VITE_*
  // is ever exposed to client code — see envDir below.
  const env = loadEnv(mode, rootDir, "");

  return {
    plugins: [react()],
    // Read .env from the repository root instead of this app's directory.
    // One file for the whole platform (see .env.example).
    envDir: rootDir,
    server: {
      port: Number(env.SUPPLIER_UI_PORT ?? DEFAULT_PORT),
      // Fail loudly instead of silently sliding to the next free port, which
      // would break the gateway's CORS allow-list and the documented map.
      strictPort: true,
    },
    preview: {
      port: Number(env.SUPPLIER_UI_PORT ?? DEFAULT_PORT),
      strictPort: true,
    },
  };
});

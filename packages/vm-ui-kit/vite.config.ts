import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

/**
 * Library build for @vayal/ui-kit.
 *
 * ES-only output: every consumer is a Vite app, so a CommonJS build would be
 * dead weight. Type declarations are emitted separately by tsc
 * (see tsconfig.build.json) rather than by a plugin.
 */
export default defineConfig({
  plugins: [react()],
  build: {
    lib: {
      entry: fileURLToPath(new URL("src/index.ts", import.meta.url)),
      formats: ["es"],
      fileName: () => "index.js",
    },
    rollupOptions: {
      // React must not be bundled: two copies of React in one app breaks
      // hooks at runtime with an error that is miserable to diagnose.
      external: ["react", "react-dom", "react/jsx-runtime"],
    },
    sourcemap: true,
    // The three apps each import this package; leaving previous output in
    // place would let a deleted export keep resolving.
    emptyOutDir: true,
  },
});

/**
 * Standalone harness: renders the exposed component on its own so the remote
 * can be worked on without the console. Not part of what the host loads.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import Analytics from "./Analytics.js";
import "./index.css";

const container = document.getElementById("root");
if (!container) throw new Error("Root element #root was not found in index.html.");

createRoot(container).render(
  <StrictMode>
    <main className="mx-auto max-w-5xl p-6">
      <Analytics
        apiBaseUrl={import.meta.env.VITE_API_BASE_URL ?? ""}
        getAccessToken={() => null}
      />
    </main>
  </StrictMode>,
);

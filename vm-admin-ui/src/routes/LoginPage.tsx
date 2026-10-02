import { useState, type FormEvent, type ReactElement } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import { Card, VayalLockup, ApiError } from "@vayal/ui-kit";
import { useAuth } from "../lib/auth.js";
import { config } from "../lib/config.js";

export function LoginPage(): ReactElement {
  const { signIn } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();

  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Return the supplier to the page they were trying to reach.
  const returnTo =
    (location.state as { from?: string } | null)?.from ?? "/dashboard";

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await signIn(identifier, password);
      navigate(returnTo, { replace: true });
    } catch (err) {
      // The server returns one message for every credential failure, on
      // purpose — do not try to be more specific here.
      setError(
        err instanceof ApiError
          ? err.message
          : "Something went wrong. Please try again.",
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-4 py-10">
      <div className="mb-8 flex justify-center">
        <VayalLockup size="lg" />
      </div>

      <Card>
        <h1 className="text-xl font-semibold text-primary-900">Admin sign in</h1>
        <p className="mt-1 text-sm text-primary-900/70">
          Operations, settlement and supplier management.
        </p>

        <form onSubmit={onSubmit} className="mt-6 space-y-4" noValidate>
          <div>
            <label htmlFor="identifier" className="block text-sm font-medium text-primary-900">
              Email or phone
            </label>
            <input
              id="identifier"
              name="identifier"
              type="text"
              autoComplete="username"
              required
              value={identifier}
              onChange={(e) => setIdentifier(e.target.value)}
              className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-base"
              placeholder="you@example.com"
            />
          </div>

          <div>
            <label htmlFor="password" className="block text-sm font-medium text-primary-900">
              Password
            </label>
            <input
              id="password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2 text-base"
            />
          </div>

          {error && (
            // role=alert so a screen reader announces the failure rather than
            // leaving the user wondering why nothing happened.
            <p role="alert" className="rounded-card bg-accent-50 px-3 py-2 text-sm text-accent-800">
              {error}
            </p>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="w-full rounded-card bg-primary-600 px-4 py-2.5 font-medium text-white hover:bg-primary-700 disabled:opacity-60"
          >
            {submitting ? "Signing in…" : "Sign in"}
          </button>
        </form>
      </Card>

      <p className="mt-6 text-center text-sm text-primary-900/60">
        Need access? Write to{" "}
        <a href={`mailto:${config.supportEmail}`} className="font-medium text-primary-700 underline">
          {config.supportEmail}
        </a>
      </p>
    </div>
  );
}

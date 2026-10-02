import { useState, type FormEvent, type ReactElement } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import { Card, VayalLockup, ApiError } from "@vayal/ui-kit";
import { useAuth } from "../lib/auth.js";
import { api } from "../lib/api.js";
import { config } from "../lib/config.js";

type Mode = "signin" | "register";

/** Field-level errors returned by the gateway's zod layer. */
type FieldErrors = Record<string, string>;

/**
 * Sign in and register, on one screen.
 *
 * A single screen with a toggle rather than two routes: a customer who lands
 * here has one goal — get into the store — and making them find a second page
 * to do the other thing is friction at the exact moment they are least
 * patient.
 */
export function AuthPage(): ReactElement {
  const [mode, setMode] = useState<Mode>("signin");
  const { signIn } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();

  const [identifier, setIdentifier] = useState("");
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const returnTo = (location.state as { from?: string } | null)?.from ?? "/";

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    setFieldErrors({});
    setFormError(null);
    setSubmitting(true);

    try {
      if (mode === "register") {
        await api.post("/auth/register", { name, phone, email, password });
        // Registration signs you in server-side, but going through signIn
        // keeps one code path for establishing the session.
        await signIn(email, password);
      } else {
        await signIn(identifier, password);
      }
      navigate(returnTo, { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        const details = err.details as FieldErrors | undefined;
        if (details && typeof details === "object") {
          setFieldErrors(details);
          // Field messages are shown inline; a duplicate banner is noise.
          if (Object.keys(details).length === 0) setFormError(err.message);
        } else {
          setFormError(err.message);
        }
      } else {
        setFormError("Something went wrong. Please try again.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  const inputClass =
    "mt-1 w-full rounded-card border border-surface-border bg-surface-raised px-3 py-2.5 text-base";

  return (
    <div className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-4 py-10">
      <div className="mb-8 flex justify-center">
        <VayalLockup size="lg" />
      </div>

      <Card>
        {/* Tabs are radio-like: exactly one active, so aria-pressed is right. */}
        <div className="mb-5 grid grid-cols-2 gap-1 rounded-card bg-surface-sunken p-1">
          {(
            [
              ["signin", "Sign in"],
              ["register", "Create account"],
            ] as const
          ).map(([value, label]) => (
            <button
              key={value}
              type="button"
              aria-pressed={mode === value}
              onClick={() => {
                setMode(value);
                setFieldErrors({});
                setFormError(null);
              }}
              className={`rounded-card px-3 py-2 text-sm font-medium transition-colors ${
                mode === value
                  ? "bg-surface-raised text-primary-800 shadow-card"
                  : "text-primary-900/60 hover:text-primary-800"
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          {mode === "register" ? (
            <>
              <Field
                id="name"
                label="Your name"
                value={name}
                onChange={setName}
                autoComplete="name"
                error={fieldErrors["name"]}
                inputClass={inputClass}
              />
              <Field
                id="phone"
                label="Phone"
                type="tel"
                inputMode="numeric"
                value={phone}
                onChange={setPhone}
                autoComplete="tel"
                placeholder="98765 43210"
                error={fieldErrors["phone"]}
                inputClass={inputClass}
              />
              <Field
                id="email"
                label="Email"
                type="email"
                value={email}
                onChange={setEmail}
                autoComplete="email"
                error={fieldErrors["email"]}
                inputClass={inputClass}
              />
            </>
          ) : (
            <Field
              id="identifier"
              label="Email or phone"
              value={identifier}
              onChange={setIdentifier}
              autoComplete="username"
              error={fieldErrors["identifier"]}
              inputClass={inputClass}
            />
          )}

          <Field
            id="password"
            label="Password"
            type="password"
            value={password}
            onChange={setPassword}
            autoComplete={mode === "register" ? "new-password" : "current-password"}
            error={fieldErrors["password"]}
            inputClass={inputClass}
            hint={mode === "register" ? "At least 8 characters." : undefined}
          />

          {formError && (
            <p
              role="alert"
              className="rounded-card bg-accent-50 px-3 py-2 text-sm text-accent-800"
            >
              {formError}
            </p>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="w-full rounded-card bg-primary-600 px-4 py-3 font-medium text-white hover:bg-primary-700 disabled:opacity-60"
          >
            {submitting
              ? "Please wait…"
              : mode === "register"
                ? "Create account"
                : "Sign in"}
          </button>
        </form>
      </Card>

      <p className="mt-6 text-center text-sm text-primary-900/60">
        Need help? Write to{" "}
        <a
          href={`mailto:${config.supportEmail}`}
          className="font-medium text-primary-700 underline"
        >
          {config.supportEmail}
        </a>
      </p>
    </div>
  );
}

interface FieldProps {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  inputMode?: "text" | "numeric" | "tel" | "email";
  autoComplete?: string;
  placeholder?: string;
  error?: string | undefined;
  hint?: string | undefined;
  inputClass: string;
}

/**
 * One labelled input.
 *
 * The label is a real <label for>, and the error is wired with
 * aria-describedby, so a screen reader announces the problem rather than
 * leaving a blind user with a form that silently refuses to submit.
 */
function Field({
  id,
  label,
  value,
  onChange,
  type = "text",
  inputMode,
  autoComplete,
  placeholder,
  error,
  hint,
  inputClass,
}: FieldProps): ReactElement {
  const describedBy = [error ? `${id}-error` : null, hint ? `${id}-hint` : null]
    .filter(Boolean)
    .join(" ");

  return (
    <div>
      <label htmlFor={id} className="block text-sm font-medium text-primary-900">
        {label}
      </label>
      <input
        id={id}
        name={id}
        type={type}
        {...(inputMode ? { inputMode } : {})}
        {...(autoComplete ? { autoComplete } : {})}
        {...(placeholder ? { placeholder } : {})}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={Boolean(error)}
        {...(describedBy ? { "aria-describedby": describedBy } : {})}
        className={`${inputClass} ${error ? "border-accent-500" : ""}`}
      />
      {hint && !error && (
        <p id={`${id}-hint`} className="mt-1 text-xs text-primary-900/50">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-error`} role="alert" className="mt-1 text-xs text-accent-800">
          {label} {error}
        </p>
      )}
    </div>
  );
}

/**
 * Transient confirmations.
 *
 * For "it worked" after an action whose result is a page away — saving a
 * supplier, recording a payment. Not for errors that need a decision: those
 * belong beside the thing that failed, where they stay put and can be read
 * twice.
 */
import {
  createContext, useCallback, useContext, useEffect, useMemo, useRef, useState,
  type ReactElement, type ReactNode,
} from "react";

export type ToastTone = "success" | "error";

interface Toast {
  id: number;
  message: string;
  tone: ToastTone;
}

interface ToastContextValue {
  showToast: (message: string, tone?: ToastTone) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

/** How long a toast stays before dismissing itself. */
const TOAST_MS = 4000;

export function ToastProvider({ children }: { children: ReactNode }): ReactElement {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextID = useRef(0);
  // Kept so a fast unmount cannot leave timers firing into a dead tree.
  const timers = useRef<Array<ReturnType<typeof setTimeout>>>([]);

  useEffect(
    () => () => {
      for (const timer of timers.current) clearTimeout(timer);
    },
    [],
  );

  const showToast = useCallback((message: string, tone: ToastTone = "success") => {
    const id = nextID.current++;
    setToasts((current) => [...current, { id, message, tone }]);
    timers.current.push(
      setTimeout(() => {
        setToasts((current) => current.filter((t) => t.id !== id));
      }, TOAST_MS),
    );
  }, []);

  const value = useMemo(() => ({ showToast }), [showToast]);

  return (
    <ToastContext.Provider value={value}>
      {children}

      {/* Horizontally centred at both sizes: bottom on a phone (near the
          thumb, clear of the header), top on a desktop.

          Top-RIGHT was wrong — it landed squarely on the period picker in the
          admin header, covering a control the operator had just used. The
          centre is the one strip of a page that is reliably empty. */}
      <div
        // A live region so the confirmation is announced, not just seen. The
        // container is always mounted: a region added at the same moment as
        // its content is frequently missed by screen readers.
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed inset-x-0 bottom-4 z-[60] flex flex-col items-center gap-2 px-4 sm:bottom-auto sm:top-20"
      >
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={`pointer-events-auto w-full max-w-sm rounded-card border px-4 py-3 text-sm shadow-card ${
              toast.tone === "error"
                ? "border-accent-500/40 bg-accent-50 text-accent-900"
                : "border-primary-200 bg-primary-50 text-primary-900"
            }`}
          >
            {toast.message}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used inside a ToastProvider");
  return ctx;
}

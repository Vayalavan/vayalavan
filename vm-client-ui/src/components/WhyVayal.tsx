import type { ReactElement } from "react";
import { config } from "../lib/config.js";

/**
 * Why choose Vayalavan.
 *
 * Every claim here is something the platform actually enforces, and each one
 * is checkable on the pages either side of it: the grower declares stock each
 * morning, the farm is named on every listing, the grade and its weight range
 * are on the pack you choose, and the cutoff and delivery estimate are shown
 * before you pay. A reason to buy that the product does not back up is just a
 * slogan, and a customer finds out on the first order.
 *
 * Placed BELOW the produce: someone who came to shop reaches the shopping
 * first, and someone still deciding whether to trust us reads on. It stays one
 * band deep either way — this is a reason to buy, not a page about us.
 */

interface Reason {
  title: string;
  body: string;
  /** A line-drawn glyph. Inline SVG so it inherits colour and needs no fetch. */
  icon: ReactElement;
}

const stroke = {
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.5,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
};

const REASONS: Reason[] = [
  {
    title: "Cut this morning, not last week",
    body:
      "Growers declare what they have each day, and the shop only sells what was declared. Produce that did not come off the field today is not on the page.",
    icon: (
      <svg viewBox="0 0 24 24" {...stroke}>
        <path d="M12 21c0-5 3-9 8-11-1 6-4 9-8 11Z" />
        <path d="M12 21c0-4-2.5-7.5-7-9 1 5 3.5 7.5 7 9Z" />
        <path d="M12 21v-4" />
      </svg>
    ),
  },
  {
    title: "You know whose field it came from",
    body:
      "Meet the farm behind every harvest. Each listing tells you who grew it and keeps the grower’s price at the heart of every purchase.",
    icon: (
      <svg viewBox="0 0 24 24" {...stroke}>
        <path d="M4 11 12 4l8 7" />
        <path d="M6 10v9h12v-9" />
        <path d="M10 19v-5h4v5" />
      </svg>
    ),
  },
  {
    title: "The size you picked is the size you get",
    body:
      "Fruit is graded the way the grower actually packs it — M2, L1, XL2 — each with its weight range and its own price. No mixed box, no “large” that means whatever was left.",
    icon: (
      <svg viewBox="0 0 24 24" {...stroke}>
        <path d="M3 7.5 12 3l9 4.5v9L12 21l-9-4.5v-9Z" />
        <path d="m3 7.5 9 4.5 9-4.5" />
        <path d="M12 12v9" />
      </svg>
    ),
  },
  {
    title: "Order by 4 pm, know the day it lands",
    body:
      "Orders placed before the cutoff are processed the same day, and the delivery date is on the order from the moment you place it — not an estimate that moves.",
    icon: (
      <svg viewBox="0 0 24 24" {...stroke}>
        <circle cx="12" cy="12" r="9" />
        <path d="M12 7v5l3.5 2" />
      </svg>
    ),
  },
];

export function WhyVayal(): ReactElement {
  return (
    <section
      aria-labelledby="why-vayal"
      className="mt-14 overflow-hidden rounded-3xl bg-surface-raised shadow-card ring-1 ring-surface-border"
    >
      <div className="grid lg:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)]">
        {/* The photograph, on the left from `lg` and as a band above the text
            below it — one picture either way, never two. */}
        <div className="relative min-h-[14rem] bg-primary-900 lg:min-h-full">
          <div
            aria-hidden="true"
            className="absolute inset-0 bg-[url('/photos/why.jpg')] bg-cover bg-center"
          />
          {/* Two layers, weighted towards the bottom, because that is the only
              part the words touch. A heavy flat wash would read as contrast
              safety and cost the picture the thing it is here for — the green
              of a flooded paddy is the whole photograph. So: a light wash to
              settle the top, and a steep gradient that goes almost solid
              exactly where the caption sits. */}
          <div aria-hidden="true" className="absolute inset-0 bg-primary-950/25" />
          <div
            aria-hidden="true"
            className="absolute inset-0 bg-gradient-to-t from-primary-950 via-primary-950/70 to-transparent"
          />
          <div className="relative flex h-full items-end p-6 sm:p-8">
            <p className="max-w-xs text-lg font-medium leading-snug text-white">
              A <span className="font-semibold">vayalavan</span> is the one who
              works the field — and every box here comes from one.
            </p>
          </div>
        </div>

        <div className="p-6 sm:p-8 lg:p-10">
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-primary-600">
            Why choose us
          </p>
          <h2
            id="why-vayal"
            className="mt-2 text-2xl font-semibold tracking-tight text-primary-900 sm:text-3xl"
          >
            Why choose Vayalavan?
          </h2>
          <p className="mt-2 max-w-lg text-primary-900/70">
            Four things we hold ourselves to. Each one is visible on the
            produce above — not a promise you have to take on trust.
          </p>

          <ul className="mt-8 grid gap-6 sm:grid-cols-2">
            {REASONS.map((reason) => (
              <li key={reason.title} className="flex gap-4">
                <span
                  aria-hidden="true"
                  className="mt-0.5 flex h-11 w-11 shrink-0 items-center justify-center rounded-card bg-primary-50 text-primary-700"
                >
                  <span className="block h-6 w-6">{reason.icon}</span>
                </span>
                <span>
                  <span className="block font-semibold text-primary-900">
                    {reason.title}
                  </span>
                  <span className="mt-1 block text-sm leading-relaxed text-primary-900/70">
                    {reason.body}
                  </span>
                </span>
              </li>
            ))}
          </ul>

          <p className="mt-8 border-t border-surface-border pt-5 text-sm text-primary-900/60">
            Something not right with an order? Write to us at{" "}
            <a
              href={`mailto:${config.supportEmail}`}
              className="font-medium text-primary-700 underline underline-offset-2"
            >
              {config.supportEmail}
            </a>{" "}
            — a person reads it.
          </p>
        </div>
      </div>
    </section>
  );
}

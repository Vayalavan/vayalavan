import type { ReactElement } from "react";

/**
 * The shop front.
 *
 * A photograph rather than a tinted panel: this is produce, and a customer
 * decides whether to shop here in the first second of looking. The picture is
 * a CSS background rather than an <img> so the scrim, the radius and the crop
 * are one object that cannot come apart at a breakpoint — and so a missing
 * file degrades to the gradient underneath instead of a broken-image icon.
 *
 * Deliberately not full-height. The produce grid has to be reachable without
 * scrolling past a billboard, so the hero is tall enough to be a picture and
 * short enough to be a header.
 */
export function HomeHero({ dateLine }: { dateLine: string }): ReactElement {
  return (
    <section className="relative mb-8 overflow-hidden rounded-3xl bg-primary-900 shadow-card">
      {/* The photograph. aria-hidden because it is decoration: everything it
          says is said in the text sitting on top of it. */}
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-[url('/photos/hero.jpg')] bg-cover bg-center"
      />
      {/* Two scrims, not one. The vertical gradient alone left the headline
          sitting on pale barnwood at the top of the frame, where white type on
          a light photograph is a contrast failure rather than a style. The
          horizontal one darkens the side the words are on and leaves the
          produce lit on the other, which is the whole trick. */}
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-t from-primary-950/80 via-primary-950/20 to-transparent"
      />
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-r from-primary-950/95 via-primary-950/70 to-transparent"
      />

      <div className="relative px-6 py-12 sm:px-10 sm:py-16 lg:py-20">
        <p className="text-xs font-semibold uppercase tracking-[0.2em] text-accent-300">
          Vayalavan
        </p>
        <h1 className="mt-3 max-w-2xl text-3xl font-semibold leading-[1.1] tracking-tight text-white sm:text-5xl">
          Picked this morning.
          <br className="hidden sm:block" />{" "}
          <span className="text-accent-300">At your doorstep soon.</span>
        </h1>
        <p className="mt-4 max-w-xl text-base leading-relaxed text-white/80 sm:text-lg">
          Fruit, vegetables and microgreens from growers who list what they cut
          that day — no cold store, no guesswork about how old it is.
          {dateLine}
        </p>

        {/* Three facts, not slogans: each one is something the platform
            actually does, and each is checkable on this page. */}
        <dl className="mt-8 flex flex-wrap gap-x-8 gap-y-4 text-white">
          {[
            ["Same-day", "listed by the grower"],
            ["4 pm", "order cutoff"],
            ["Named", "farm on every item"],
          ].map(([value, label]) => (
            <div key={label}>
              <dt className="text-xl font-semibold tracking-tight sm:text-2xl">{value}</dt>
              <dd className="text-xs uppercase tracking-wider text-white/75">{label}</dd>
            </div>
          ))}
        </dl>
      </div>
    </section>
  );
}

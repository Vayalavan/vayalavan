import { useEffect, useRef, useState, type ReactElement, type TouchEvent } from "react";
import { createPortal } from "react-dom";
import { ProducePlaceholder } from "@vayal/ui-kit";

export interface GalleryMedia {
  id: string;
  kind: "image" | "video";
  url: string | null;
  content_type: string | null;
}

interface ProductGalleryProps {
  media: GalleryMedia[];
  /**
   * The cover, used when the gallery is empty or nothing in it can be
   * rendered — the same URL every catalogue card shows.
   */
  coverUrl: string | null;
  name: string;
  /** Sold out produce is shown greyed rather than hidden (CLAUDE.md §5.2). */
  dimmed: boolean;
  /** The coarse "Only a few left" nudge, pinned to the frame's corner. */
  badge?: string;
}

/**
 * How long a video has to be the slide in view before it plays itself.
 *
 * A second: long enough that passing through a clip on the way to a
 * photograph costs nothing, short enough that stopping to look at one does not
 * feel like waiting for it. The timer is cancelled on every move, so it only ever fires for a
 * slide the customer settled on.
 */
const AUTOPLAY_DELAY_MS = 1000;

/** How far a finger has to travel sideways before it counts as a swipe. */
const SWIPE_THRESHOLD_PX = 40;

/**
 * The product detail viewer: one large image or video at a time, with arrows
 * to move between them and a thumbnail strip underneath.
 *
 * The frame is SQUARE. It used to be a fixed 384px-tall landscape box with the
 * picture contained and padded inside it, which shrank anything square — a
 * grower's comparison chart, a top-down shot of a crate — to a postage stamp
 * with white bars either side. Produce photography and the graphics growers
 * make for it are overwhelmingly square, so the frame now matches them and a
 * square upload fills it edge to edge.
 *
 * Tapping a still opens it full-screen. A chart with six rows of small print is
 * something a customer has to be able to read, not just see.
 *
 * Every item is laid out in one row and the row is translated, so moving is a
 * SLIDE rather than a swap — the customer sees the next photograph arrive from
 * the side it is on, which is what makes a strip of pictures feel like one
 * object. `overflow-hidden` on the frame is what turns the row into a window.
 *
 * Videos are muted, and a clip starts itself only once the customer has STAYED
 * on it for AUTOPLAY_DELAY_MS. Flicking through a gallery to reach the photo
 * on the far side should not start three clips on the way past, and a customer
 * on mobile data has not asked to download fifty megabytes — `preload`
 * fetches a first frame, and the rest moves only when they stop and look.
 * Anything playing is paused when it slides away: without that, a clip left
 * playing keeps running off-screen.
 */
export function ProductGallery({
  media,
  coverUrl,
  name,
  dimmed,
  badge = "",
}: ProductGalleryProps): ReactElement {
  const items = media.filter((item) => item.url);
  const [active, setActive] = useState(0);
  const [zoomed, setZoomed] = useState(false);
  const touchStartX = useRef<number | null>(null);

  // A different product can arrive at the same route. Without this the new
  // gallery opens on the previous one's index.
  useEffect(() => {
    setActive(0);
  }, [media]);

  const index = Math.min(active, Math.max(items.length - 1, 0));
  const current = items[index];
  const dim = dimmed ? "grayscale opacity-70" : "";

  // Every mounted <video>, so the one sliding out of view can be stopped. A
  // map keyed by media id rather than an array: the list can change under us
  // when the customer switches grade, and a stale index would pause the wrong
  // element.
  const videos = useRef(new Map<string, HTMLVideoElement>());
  useEffect(() => {
    for (const [id, element] of videos.current) {
      if (id !== current?.id) element.pause();
    }
  }, [current?.id]);

  // Play the clip the customer settled on, muted, after the delay above.
  //
  // Muted is not a preference here: an unmuted autoplay is blocked by every
  // browser, and a produce page that starts making noise in a shop is the
  // worst version of this feature. A viewer whose system asks for less motion
  // gets the first frame and a play button, which is what that setting is for.
  useEffect(() => {
    if (!current || current.kind !== "video" || zoomed) return;
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;

    const timer = setTimeout(() => {
      // The play() promise rejects if the browser refuses or the element has
      // gone; neither is worth an unhandled rejection in the console.
      void videos.current.get(current.id)?.play().catch(() => {});
    }, AUTOPLAY_DELAY_MS);
    return () => clearTimeout(timer);
  }, [current, zoomed]);

  const atStart = index === 0;
  const atEnd = index === items.length - 1;

  function go(next: number): void {
    setActive(Math.max(0, Math.min(next, items.length - 1)));
  }

  // A phone browser has no arrows worth aiming at on a 360px picture — the
  // thumb swipes, the way it does in every other gallery it has used.
  function onTouchStart(event: TouchEvent): void {
    touchStartX.current = event.touches[0]?.clientX ?? null;
  }
  function onTouchEnd(event: TouchEvent): void {
    const start = touchStartX.current;
    touchStartX.current = null;
    const end = event.changedTouches[0]?.clientX;
    if (start === null || end === undefined) return;
    const delta = end - start;
    if (Math.abs(delta) < SWIPE_THRESHOLD_PX) return;
    go(delta < 0 ? index + 1 : index - 1);
  }

  return (
    <div>
      <div
        className="group relative aspect-square w-full overflow-hidden rounded-3xl border border-cream-300/70 bg-surface-raised shadow-card"
        onTouchStart={items.length > 1 ? onTouchStart : undefined}
        onTouchEnd={items.length > 1 ? onTouchEnd : undefined}
      >
        {!current ? (
          coverUrl ? (
            <img
              src={coverUrl}
              alt={name}
              className={`h-full w-full object-contain ${dim}`}
            />
          ) : (
            <ProducePlaceholder />
          )
        ) : (
          <>
            {/* The track. One slide per item, each exactly the frame's width,
                moved as a whole — so nothing re-mounts mid-transition and the
                outgoing picture stays visible while it leaves.
                motion-reduce drops the animation for a customer who has asked
                their system for less of it; the move still happens. */}
            <div
              className="flex h-full w-full transition-transform duration-500 ease-[cubic-bezier(0.22,1,0.36,1)] motion-reduce:transition-none"
              style={{ transform: `translateX(-${index * 100}%)` }}
            >
              {items.map((item, itemIndex) => (
                <div key={item.id} className="h-full w-full shrink-0">
                  {item.kind === "video" ? (
                    <video
                      ref={(element) => {
                        if (element) videos.current.set(item.id, element);
                        else videos.current.delete(item.id);
                      }}
                      className={`h-full w-full bg-primary-950 object-contain ${dim}`}
                      controls
                      muted
                      playsInline
                      preload="metadata"
                    >
                      <source src={item.url ?? ""} type={item.content_type ?? undefined} />
                      Your browser cannot play this video.
                    </video>
                  ) : (
                    <button
                      type="button"
                      onClick={() => setZoomed(true)}
                      tabIndex={itemIndex === index ? 0 : -1}
                      aria-label={`View ${name} full screen`}
                      className="block h-full w-full cursor-zoom-in"
                    >
                      <img
                        src={item.url ?? ""}
                        alt={name}
                        // Contained, not covered: this is the screen a
                        // customer opens to look closely at the produce, so
                        // cropping the part that identifies it — or the edge
                        // of a grower's chart — is the one thing it must not
                        // do. No padding: the square frame is the margin.
                        className={`h-full w-full object-contain transition-transform duration-500 group-hover:scale-[1.02] motion-reduce:transition-none ${dim}`}
                        draggable={false}
                      />
                    </button>
                  )}
                </div>
              ))}
            </div>

            {items.length > 1 && (
              <>
                {/* Disabled rather than wrapping at the ends — a wrap would
                    have to sweep the whole strip backwards to land on the
                    first slide, which reads as a glitch rather than a move. */}
                <GalleryArrow
                  side="left"
                  label={`Previous ${items[index - 1]?.kind ?? "item"}`}
                  disabled={atStart}
                  onClick={() => go(index - 1)}
                />
                <GalleryArrow
                  side="right"
                  label={`Next ${items[index + 1]?.kind ?? "item"}`}
                  disabled={atEnd}
                  onClick={() => go(index + 1)}
                />

                {/* Where you are in the strip, as dots — the thumbnails say it
                    too, but they sit below the fold on a phone. */}
                <div className="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
                  <div className="flex items-center gap-1.5 rounded-full bg-primary-950/55 px-2.5 py-1.5 backdrop-blur">
                    {items.map((item, dotIndex) => (
                      <span
                        key={item.id}
                        className={`h-1.5 rounded-full transition-all duration-300 ${
                          dotIndex === index ? "w-4 bg-white" : "w-1.5 bg-white/50"
                        }`}
                      />
                    ))}
                    <span className="sr-only">
                      {index + 1} of {items.length}
                    </span>
                  </div>
                </div>
              </>
            )}

            {current.kind === "image" && (
              <button
                type="button"
                onClick={() => setZoomed(true)}
                aria-label="View full screen"
                className="absolute right-3 top-3 flex h-9 w-9 items-center justify-center rounded-full border border-surface-border bg-surface-raised/90 text-primary-900 shadow-card backdrop-blur transition hover:bg-surface-raised"
              >
                <ExpandIcon />
              </button>
            )}
          </>
        )}

        {badge && (
          <span className="absolute left-3 top-3 inline-flex items-center gap-1.5 rounded-full bg-accent-500 px-3 py-1 text-xs font-semibold text-white shadow-card">
            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-white motion-reduce:animate-none" />
            {badge}
          </span>
        )}
      </div>

      {items.length > 1 && (
        <ul className="mt-3 flex gap-2.5 overflow-x-auto p-1">
          {items.map((item, thumbIndex) => (
            <li key={item.id} className="shrink-0">
              <button
                type="button"
                onClick={() => go(thumbIndex)}
                aria-label={`Show ${item.kind} ${thumbIndex + 1} of ${items.length}`}
                aria-current={thumbIndex === index}
                className={`relative block h-14 w-14 overflow-hidden rounded-xl bg-surface-raised transition sm:h-16 sm:w-16 ${
                  thumbIndex === index
                    ? "ring-2 ring-primary-600 ring-offset-2 ring-offset-surface"
                    : "opacity-60 ring-1 ring-surface-border hover:opacity-100"
                }`}
              >
                {item.kind === "video" ? (
                  <>
                    <video
                      src={item.url ?? ""}
                      className="h-full w-full bg-primary-950 object-cover"
                      preload="metadata"
                      muted
                    />
                    {/* A play glyph, because a paused first frame and a still
                        photograph look identical at this size. */}
                    <span
                      aria-hidden="true"
                      className="absolute inset-0 flex items-center justify-center"
                    >
                      <span className="flex h-7 w-7 items-center justify-center rounded-full bg-white/90 pl-0.5 text-[10px] text-primary-900 shadow">
                        ▶
                      </span>
                    </span>
                  </>
                ) : (
                  <img src={item.url ?? ""} alt="" className="h-full w-full object-cover" />
                )}
              </button>
            </li>
          ))}
        </ul>
      )}

      {zoomed && (
        <Lightbox
          items={items}
          index={index}
          name={name}
          onMove={go}
          onClose={() => setZoomed(false)}
        />
      )}
    </div>
  );
}

/**
 * The full-screen viewer. Images only move through it; a video slide shows
 * its own player, which already has a native full-screen button.
 */
function Lightbox({
  items,
  index,
  name,
  onMove,
  onClose,
}: {
  items: GalleryMedia[];
  index: number;
  name: string;
  onMove: (next: number) => void;
  onClose: () => void;
}): ReactElement {
  const closeRef = useRef<HTMLButtonElement>(null);
  const touchStartX = useRef<number | null>(null);
  const current = items[index];

  useEffect(() => {
    closeRef.current?.focus();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = previousOverflow;
    };
  }, []);

  useEffect(() => {
    function onKey(event: KeyboardEvent): void {
      if (event.key === "Escape") onClose();
      if (event.key === "ArrowLeft") onMove(index - 1);
      if (event.key === "ArrowRight") onMove(index + 1);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [index, onClose, onMove]);

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label={`${name}, picture ${index + 1} of ${items.length}`}
      className="fixed inset-0 z-50 flex flex-col bg-primary-950/95 backdrop-blur-sm"
      onClick={onClose}
      onTouchStart={(event) => {
        touchStartX.current = event.touches[0]?.clientX ?? null;
      }}
      onTouchEnd={(event) => {
        const start = touchStartX.current;
        touchStartX.current = null;
        const end = event.changedTouches[0]?.clientX;
        if (start === null || end === undefined) return;
        const delta = end - start;
        if (Math.abs(delta) >= SWIPE_THRESHOLD_PX) onMove(delta < 0 ? index + 1 : index - 1);
      }}
    >
      <div className="flex items-center justify-between px-4 py-3 text-white">
        <span className="text-sm font-medium tabular-nums text-white/80">
          {index + 1} / {items.length}
        </span>
        <button
          ref={closeRef}
          type="button"
          onClick={onClose}
          aria-label="Close"
          className="flex h-10 w-10 items-center justify-center rounded-full bg-white/10 text-white transition hover:bg-white/20"
        >
          <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" className="h-5 w-5">
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
      </div>

      <div className="relative flex min-h-0 flex-1 items-center justify-center px-2 pb-4 sm:px-16">
        {current?.kind === "video" ? (
          <video
            src={current.url ?? ""}
            className="max-h-full max-w-full rounded-2xl"
            controls
            muted
            playsInline
            onClick={(event) => event.stopPropagation()}
          />
        ) : (
          <img
            src={current?.url ?? ""}
            alt={name}
            className="max-h-full max-w-full rounded-2xl bg-white object-contain shadow-2xl"
            onClick={(event) => event.stopPropagation()}
          />
        )}

        {items.length > 1 && (
          <>
            <LightboxArrow side="left" disabled={index === 0} onClick={() => onMove(index - 1)} />
            <LightboxArrow
              side="right"
              disabled={index === items.length - 1}
              onClick={() => onMove(index + 1)}
            />
          </>
        )}
      </div>
    </div>,
    document.body,
  );
}

function LightboxArrow({
  side,
  disabled,
  onClick,
}: {
  side: "left" | "right";
  disabled: boolean;
  onClick: () => void;
}): ReactElement {
  return (
    <button
      type="button"
      disabled={disabled}
      aria-label={side === "left" ? "Previous" : "Next"}
      onClick={(event) => {
        event.stopPropagation();
        onClick();
      }}
      className={`absolute top-1/2 hidden h-12 w-12 -translate-y-1/2 items-center justify-center rounded-full bg-white/10 text-white transition hover:bg-white/20 disabled:opacity-20 sm:flex ${
        side === "left" ? "left-3" : "right-3"
      }`}
    >
      <Chevron side={side} />
    </button>
  );
}

/**
 * One of the two overlay arrows.
 *
 * Revealed on hover with a pointer and always shown on touch, where there is
 * no hover to reveal them — though a phone mostly swipes.
 */
function GalleryArrow({
  side,
  label,
  disabled,
  onClick,
}: {
  side: "left" | "right";
  label: string;
  disabled: boolean;
  onClick: () => void;
}): ReactElement {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      className={`absolute top-1/2 flex h-10 w-10 -translate-y-1/2 items-center justify-center rounded-full border border-surface-border bg-surface-raised/90 text-primary-900 shadow-card backdrop-blur transition hover:scale-105 hover:bg-surface-raised disabled:pointer-events-none disabled:opacity-0 sm:h-12 sm:w-12 [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover:opacity-100 [@media(hover:hover)]:focus-visible:opacity-100 [@media(hover:hover)]:disabled:group-hover:opacity-0 ${
        side === "left" ? "left-3" : "right-3"
      }`}
    >
      <Chevron side={side} />
    </button>
  );
}

/**
 * An inline SVG chevron rather than a text arrow: at this size a glyph renders
 * differently on every platform, and half of them are not centred in their own
 * box.
 */
function Chevron({ side }: { side: "left" | "right" }): ReactElement {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      className="h-5 w-5 sm:h-6 sm:w-6"
    >
      <polyline points={side === "left" ? "15 5 8 12 15 19" : "9 5 16 12 9 19"} />
    </svg>
  );
}

function ExpandIcon(): ReactElement {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className="h-4 w-4">
      <path d="M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7" />
    </svg>
  );
}

/**
 * The page shell shared by the customer, admin and supplier UIs.
 *
 * One shell means the header, focus order and footer are identical across
 * all three apps, and the support email required by CLAUDE.md §9 cannot be
 * forgotten in one of them.
 */
import { useId, useState } from "react";
import type { ReactNode, ReactElement } from "react";
import { VayalLockup } from "../logo/VayalLogo.js";

export interface AppShellProps {
  /**
   * Which app this is — "Customer", "Admin", "Supplier". Rendered as a badge
   * beside the logo so a screenshot is never ambiguous about which UI it is.
   */
  appLabel: string;
  /** Support address for the footer. Comes from VITE_SUPPORT_EMAIL. */
  supportEmail: string;
  /** Primary navigation, rendered in the header on wider viewports. */
  nav?: ReactNode;
  /** Account controls, rendered at the header's trailing edge. */
  actions?: ReactNode;
  /**
   * Policy and information links for the footer.
   *
   * Razorpay requires Terms, Privacy, Shipping and Refund pages to be
   * reachable before it will activate a live account, so these belong on
   * every page rather than buried on one.
   */
  footerLinks?: ReactNode;
  /** Rendered directly under the header, full width. */
  banner?: ReactNode;
  /**
   * Persistent left-hand navigation, for apps with more sections than fit
   * comfortably in a header.
   *
   * The admin console has one; the customer and supplier apps do not, and
   * should not — a shopper needs a shop, not a filing cabinet.
   *
   * Passing it switches the whole shell into console layout: the page stops
   * being a document that scrolls and becomes a fixed frame — header on top,
   * a full-height rail flush against the left edge, a footer pinned to the
   * bottom, and only the working area scrolling between them. That is what a
   * console with tables in it wants, and it means the support address stays
   * on screen on every section rather than at the end of a long table.
   *
   * Below `lg` there is no room for a rail beside a table, so the same links
   * collapse into an accordion under the header: a "Menu" button expands them
   * as a full-width green panel that pushes the page down, and picking one
   * closes it again. Not a drawer — no overlay, no scroll lock, no focus trap
   * to get wrong — and it is the SAME nodes, so the console can never grow a
   * section that exists on a desk but not on a phone.
   *
   * The rail is deep field green (primary-800), so the console's own
   * furniture reads as ours and the working area beside it stays plain white
   * for tables. Links passed in are rendered ON that green and must be light —
   * see vm-admin-ui's sideLinkClass.
   */
  sidebar?: ReactNode;
  /**
   * Anchor the shell to the viewport without a rail.
   *
   * A `sidebar` turns the frame on by itself, because a console needs one. A
   * screen with no rail can still want it: a long reporting page where the
   * support address and the sign-out control should stay put rather than
   * arriving after a metre of scrolling.
   *
   * Only worth setting per SCREEN, not per app — the whole page scrolls in
   * one mode and only the working area scrolls in the other, and flipping
   * that between every route would feel broken rather than considered.
   */
  frame?: boolean;
  /**
   * The customer shop's dress: a warm cream header over a cream page, and a
   * deep field-green footer carrying the brand.
   *
   * The admin console and supplier UI keep the neutral shell. Tables want a
   * plain page; a shop wants to feel like somewhere worth buying fruit from,
   * and the white shell read as a back office.
   */
  storefront?: boolean;
  children: ReactNode;
}

export function AppShell({
  appLabel,
  supportEmail,
  nav,
  actions,
  footerLinks,
  banner,
  sidebar,
  frame = false,
  storefront = false,
  children,
}: AppShellProps): ReactElement {
  // A rail implies the frame; `frame` asks for it without one. Everything
  // downstream cares only about which layout is being drawn, not why.
  const isConsole = Boolean(sidebar) || frame;
  const hasRail = Boolean(sidebar);
  const [sectionsOpen, setSectionsOpen] = useState(false);
  const sectionsId = useId();

  const skipLink = (
    // Keyboard and screen-reader users land here first and can jump past
    // the navigation. Visually hidden until focused.
    <a
      href="#main"
      className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-50 focus:rounded-card focus:bg-surface-raised focus:px-4 focus:py-2 focus:shadow-card"
    >
      Skip to content
    </a>
  );

  const header = (
    <header
      className={`${
        isConsole ? "shrink-0" : "sticky top-0 z-40"
      } border-b ${
        storefront
          ? "border-cream-300/70 bg-cream-50/85 backdrop-blur-md"
          : "border-surface-border bg-surface-raised/95 backdrop-blur"
      }`}
    >
      {/* With a rail the header spans the full width, so the lockup sits
          directly above it. Everywhere else — including a framed screen with
          no rail — it stays centred with the content beneath it. */}
      <div
        className={`flex h-16 w-full items-center gap-3 px-4 sm:px-6 ${
          hasRail ? "" : "mx-auto max-w-7xl"
        }`}
      >
        <a href="/" className="flex shrink-0 items-center gap-2 rounded-card">
          <VayalLockup size="md" />
          <span className="sr-only">Vayalavan home</span>
        </a>

        <span
          className={`hidden rounded-full px-2.5 py-1 text-xs font-medium uppercase tracking-wide sm:inline ${
            storefront
              ? "bg-gold-50 text-gold-700 ring-1 ring-gold-200"
              : "bg-primary-50 text-primary-700"
          }`}
        >
          {appLabel}
        </span>

        {/* Hidden on the narrowest viewports; each app supplies its own
            mobile navigation affordance via `actions`. */}
        {nav && (
          <nav className="ml-4 hidden items-center gap-1 md:flex">{nav}</nav>
        )}

        {/* The rail's stand-in below `lg`, where the rail itself is hidden.
            Sits beside the lockup rather than at the trailing edge so it is
            under a thumb on a phone and never competes with Sign out. */}
        {hasRail && (
          <button
            type="button"
            aria-expanded={sectionsOpen}
            aria-controls={sectionsId}
            onClick={() => setSectionsOpen((open) => !open)}
            className="ml-2 inline-flex items-center gap-2 rounded-card border border-surface-border px-3 py-1.5 text-sm font-medium text-primary-800 lg:hidden"
          >
            <svg viewBox="0 0 20 20" aria-hidden="true" className="h-4 w-4 fill-current">
              <path d="M3 5.5h14v1.6H3zM3 9.2h14v1.6H3zM3 12.9h14v1.6H3z" />
            </svg>
            Menu
          </button>
        )}

        {actions && (
          <div className="ml-auto flex items-center gap-2">{actions}</div>
        )}
      </div>
    </header>
  );

  // One footer, two densities. The document version breathes; the console
  // version is a single pinned strip, because it is on screen permanently and
  // every row it takes is a row of table the admin cannot see. The strip's
  // contents are centred across the full width rather than pushed to the two
  // edges: with the rail taking the left, edge-aligned text read as two
  // stranded fragments instead of one line.
  const footer = storefront ? (
    <footer className="relative mt-12 overflow-hidden bg-primary-950 text-white/70">
      {/* A hairline of gold across the top edge, and a soft glow of field
          green under the brand — the only two flourishes, both decoration. */}
      <div
        aria-hidden="true"
        className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-gold-500/70 to-transparent"
      />
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -left-24 -top-24 h-72 w-72 rounded-full bg-primary-600/25 blur-3xl"
      />
      <div className="relative mx-auto w-full max-w-7xl px-4 py-10 text-sm sm:px-6 sm:py-12">
        <div className="flex flex-col gap-8 sm:flex-row sm:items-start sm:justify-between">
          <div className="max-w-sm">
            <p className="font-display text-2xl font-semibold tracking-tight text-white">
              Vayalavan
            </p>
            <p className="mt-2 leading-relaxed">
              Fruit, vegetables and microgreens, listed each morning by the
              growers who picked them.
            </p>
          </div>
          {footerLinks && (
            <nav
              aria-label="Policies and information"
              className="grid grid-cols-2 gap-x-8 gap-y-2.5 sm:text-right"
            >
              {footerLinks}
            </nav>
          )}
        </div>
        <div className="mt-10 flex flex-col gap-2 border-t border-white/10 pt-6 sm:flex-row sm:items-center sm:justify-between">
          <p>
            &copy; {new Date().getFullYear()} Vayalavan. Fresh from the field.
          </p>
          <p>
            Any issues? Write to us at{" "}
            <a
              href={`mailto:${supportEmail}`}
              className="font-medium text-gold-200 underline underline-offset-2 hover:text-white"
            >
              {supportEmail}
            </a>
          </p>
        </div>
      </div>
    </footer>
  ) : (
    <footer
      className={`${
        // Last row of the frame, and the frame is the viewport — so this is
        // the bottom edge of the screen. `sticky` was tried here and removed:
        // inside an overflow-hidden ancestor it pins to nothing, so it only
        // looked like insurance.
        isConsole ? "shrink-0" : ""
      } border-t border-surface-border bg-surface-sunken`}
    >
      <div
        className={`w-full text-sm text-primary-900/70 ${
          isConsole
            ? "flex flex-wrap items-center justify-center gap-x-5 gap-y-1 px-4 py-2.5 text-center sm:px-6"
            : "mx-auto max-w-7xl px-4 py-6 sm:px-6"
        }`}
      >
        {footerLinks && (
          <nav
            aria-label="Policies and information"
            className={`flex flex-wrap gap-x-5 gap-y-2 ${
              isConsole ? "justify-center" : "mb-4"
            }`}
          >
            {footerLinks}
          </nav>
        )}
        <div
          className={
            isConsole
              ? "flex flex-wrap items-center justify-center gap-x-5 gap-y-1"
              : "flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between"
          }
        >
          <p>
            &copy; {new Date().getFullYear()} Vayalavan. Fresh from the
            field.
          </p>
          <p>
            Any issues? Write to us at{" "}
            <a
              href={`mailto:${supportEmail}`}
              className="font-medium text-primary-700 underline underline-offset-2"
            >
              {supportEmail}
            </a>
          </p>
        </div>
      </div>
    </footer>
  );


  // The rail's below-`lg` form: an accordion, not a drawer. It animates on
  // grid-template-rows so no height has to be measured, and toggles
  // `visibility` rather than opacity so the collapsed links leave the tab
  // order instead of lurking in it invisibly.
  const sectionsAccordion = !hasRail ? null : (
    <div
      id={sectionsId}
      className={`grid shrink-0 overflow-hidden border-b border-primary-900/20 transition-[grid-template-rows,visibility] duration-200 ease-out motion-reduce:transition-none lg:hidden ${
        sectionsOpen ? "visible grid-rows-[1fr]" : "invisible grid-rows-[0fr]"
      }`}
    >
      <div className="min-h-0 overflow-hidden bg-primary-800">
        <nav
          aria-label="Sections"
          className="flex max-h-[60dvh] flex-col gap-1 overflow-y-auto p-2"
          // Picking a section closes the panel: leaving it open would push
          // the page the admin just asked for off the bottom of the screen.
          onClick={(event) => {
            if ((event.target as HTMLElement).closest("a")) setSectionsOpen(false);
          }}
          onKeyDown={(event) => {
            if (event.key === "Escape") setSectionsOpen(false);
          }}
        >
          {sidebar}
        </nav>
      </div>
    </div>
  );

  if (isConsole) {
    return (
      // A frame, not a document: the viewport is the whole app, and only the
      // working area scrolls. `overflow-hidden` stops a wide table dragging
      // the header and rail sideways with it.
      //
      // `fixed inset-0` rather than `h-dvh`, so the frame is measured against
      // the VIEWPORT and nothing above it can change that — not body height,
      // not the mount element, not an ancestor with its own layout ideas. It
      // also cannot be pushed taller by its own contents, which is what turns
      // a pinned footer back into one at the end of a long page.
      <div className="fixed inset-0 flex flex-col overflow-hidden bg-surface">
        {skipLink}
        {header}
        {sectionsAccordion}
        {banner}

        {/* min-h-0 lets the row shrink so its scrolling child actually scrolls
            instead of growing the page past the viewport. */}
        <div className="flex min-h-0 flex-1">
          {/* Flush to the left edge and to the header above it: no margin, no
              radius, no gap. It is furniture, not a card. Absent entirely when
              the frame was asked for without a rail — an empty green column
              beside a report is not navigation, it is decoration. */}
          {hasRail && (
            <aside className="hidden w-56 shrink-0 overflow-y-auto border-r border-primary-900/20 bg-primary-800 p-2 lg:block">
              <nav aria-label="Sections" className="flex flex-col gap-1">
                {sidebar}
              </nav>
            </aside>
          )}

          {/* min-w-0: without it a wide table inside a flex child refuses to
              shrink and pushes the whole row sideways. */}
          <main id="main" className="min-w-0 flex-1 overflow-y-auto">
            <div className="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 sm:py-8">
              {children}
            </div>
          </main>
        </div>

        {footer}
      </div>
    );
  }

  return (
    // Column flex with a growing main, so a short page still pins the footer
    // to the bottom of the viewport instead of floating mid-screen.
    <div className="flex min-h-dvh flex-col">
      {skipLink}
      {header}
      {banner}

      <main id="main" className="mx-auto w-full max-w-7xl flex-1 px-4 py-6 sm:px-6 sm:py-8">
        {children}
      </main>

      {footer}
    </div>
  );
}

export interface PageHeadingProps {
  title: string;
  description?: string;
  actions?: ReactNode;
}

/** Consistent page title block, so the three apps introduce pages the same way. */
export function PageHeading({
  title,
  description,
  actions,
}: PageHeadingProps): ReactElement {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-primary-900">
          {title}
        </h1>
        {description && (
          <p className="mt-1 max-w-2xl text-sm text-primary-900/70">
            {description}
          </p>
        )}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}

export interface CardProps {
  children: ReactNode;
  className?: string;
}

/** Standard raised surface for grouped content. */
export function Card({ children, className = "" }: CardProps): ReactElement {
  return (
    <div
      className={`rounded-card border border-surface-border bg-surface-raised p-5 shadow-card ${className}`.trim()}
    >
      {children}
    </div>
  );
}

import type { ReactElement, ReactNode } from "react";
import { PageHeading } from "@vayal/ui-kit";
import { config } from "../lib/config.js";

/**
 * Static policy pages.
 *
 * ⚠️ PLACEHOLDER CONTENT — REVIEW BEFORE LAUNCH.
 *
 * Razorpay will not activate a live account without Terms, Privacy, Shipping
 * and Refund/Cancellation pages reachable at public URLs, so these exist and
 * are honest about the mechanics the code actually implements — the 4pm IST
 * cutoff, next-day processing, courier handoff with no live tracking, the 3%
 * platform fee and the ₹15 delivery charge.
 *
 * What they are NOT is legal advice. Every page carries a visible review
 * banner, and the business details (registered address, GSTIN, legal entity
 * name) are marked TODO rather than invented — a fabricated registered address
 * on a payments compliance page is worse than an obvious gap.
 */

const LAST_UPDATED = "15 August 2026";

/** Business details still to be supplied before launch. */
const TODO = "[TO BE COMPLETED BEFORE LAUNCH]";

function Page({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}): ReactElement {
  return (
    <>
      <PageHeading title={title} description={description} />

      {/* Deliberately loud. This must not survive to production unnoticed. */}
      <div
        role="note"
        className="mb-6 rounded-card border border-accent-300 bg-accent-50 px-4 py-3 text-sm text-accent-900"
      >
        <strong>Draft for review.</strong> This page describes how the service
        actually works, but has not been reviewed by a lawyer and is missing
        registered business details. Complete it before going live.
      </div>

      {/* prose-ish styling done by hand: the typography plugin is not a
          dependency, and six static pages do not justify adding one. */}
      <article className="max-w-3xl space-y-4 text-primary-900/80 [&_h2]:mt-8 [&_h2]:text-lg [&_h2]:font-semibold [&_h2]:text-primary-900 [&_li]:ml-5 [&_li]:list-disc [&_p]:leading-relaxed">
        {children}
        <p className="pt-6 text-sm text-primary-900/50">
          Last updated: {LAST_UPDATED}
        </p>
      </article>
    </>
  );
}

export function AboutPage(): ReactElement {
  return (
    <Page
      title="About Vayalavan"
      description="Fresh produce, straight from the field."
    >
      <p>
        <em>Vayal</em> (வயல்) is Tamil for a farming field. We connect growers of
        fruit, vegetables and microgreens directly with households, so produce
        reaches you closer to the day it was picked.
      </p>

      <h2>How it works</h2>
      <p>
        Our suppliers declare each morning what they have available that day.
        You order against that day&rsquo;s actual stock rather than a standing
        catalogue, which is why what you see changes daily and why items sell
        out.
      </p>
      <ul>
        <li>Suppliers list availability each morning</li>
        <li>Orders placed before 4:00 PM IST are processed the same day</li>
        <li>Processed orders are handed to courier partners the next day</li>
      </ul>

      <h2>Who we are</h2>
      <p>Legal entity: {TODO}</p>
      <p>Registered address: {TODO}</p>
      <p>GSTIN: {TODO}</p>
      <p>
        Contact: <a className="underline" href={`mailto:${config.supportEmail}`}>{config.supportEmail}</a>
      </p>

    </Page>
  );
}

export function ContactPage(): ReactElement {
  return (
    <Page title="Contact us" description="We read every message.">
      <h2>Support</h2>
      <p>
        Email{" "}
        <a className="underline" href={`mailto:${config.supportEmail}`}>
          {config.supportEmail}
        </a>
        . Please include your order number (it looks like{" "}
        <code className="rounded bg-surface-sunken px-1">VM-260815-0042</code>) so
        we can find your order quickly.
      </p>

      <h2>Response times</h2>
      <p>
        We aim to reply within one working day. Orders placed close to the 4:00
        PM IST cutoff are best queried the same afternoon, since processing
        begins straight afterwards.
      </p>

      <h2>Registered office</h2>
      <p>{TODO}</p>
      <p>Phone: {TODO}</p>
    </Page>
  );
}

export function TermsPage(): ReactElement {
  return (
    <Page title="Terms of Service" description="The agreement between us.">
      <p>
        By placing an order you agree to these terms. They describe how the
        service operates today.
      </p>

      <h2>Orders and availability</h2>
      <p>
        Produce is sold against a supplier&rsquo;s declared availability for a
        specific day. Stock is held for you for a limited period while you
        complete payment; if payment is not completed in that window, the stock
        is released and your order expires.
      </p>
      <p>
        An order is confirmed only once payment is captured. Until then it
        remains pending and may expire.
      </p>

      <h2>Pricing</h2>
      <p>
        Prices are set by suppliers and shown per pack. At checkout we add a
        platform fee of 3% of the order subtotal and a flat delivery charge of
        ₹15. Both are shown separately before you pay. All amounts are in Indian
        Rupees.
      </p>

      <h2>Cutoff and delivery</h2>
      <p>
        Orders placed before 4:00 PM IST are processed the same day. Orders
        placed at or after 4:00 PM are processed the following day. Delivery
        dates shown are estimates.
      </p>

      <h2>Your account</h2>
      <p>
        You are responsible for keeping your password confidential and for
        activity under your account. Tell us immediately if you believe it has
        been compromised.
      </p>

      <h2>Suspension</h2>
      <p>
        We may suspend an account for fraudulent activity, abuse of staff or
        suppliers, or repeated failure to accept delivery.
      </p>

      <h2>Governing law</h2>
      <p>These terms are governed by the laws of India. Jurisdiction: {TODO}</p>
    </Page>
  );
}

export function PrivacyPage(): ReactElement {
  return (
    <Page title="Privacy Policy" description="What we collect, and why.">
      <h2>What we collect</h2>
      <ul>
        <li>Your name, email address and phone number, to identify your account</li>
        <li>Delivery addresses, to fulfil orders</li>
        <li>Order history, to show your past orders and handle support queries</li>
        <li>Technical logs, including a request identifier, to diagnose faults</li>
      </ul>

      <h2>What we do not store</h2>
      <p>
        We do not store card numbers, UPI PINs or bank credentials. Payments are
        processed by Razorpay; card details are entered on their systems and
        never reach ours. We retain only a payment reference and the amount.
      </p>
      <p>
        Passwords are stored as salted bcrypt hashes and cannot be read back by
        us or anyone else.
      </p>

      <h2>Who we share with</h2>
      <ul>
        <li>Suppliers — the items in your order, so they can prepare it</li>
        <li>Courier partners — your delivery address and phone number</li>
        <li>Razorpay — the payment amount and order reference</li>
      </ul>
      <p>We do not sell your data or share it for advertising.</p>

      <h2>Retention</h2>
      <p>
        Order records are retained for as long as required for tax and
        accounting purposes. Deleted addresses are retained against historical
        orders so past invoices remain accurate.
      </p>

      <h2>Your rights</h2>
      <p>
        Write to{" "}
        <a className="underline" href={`mailto:${config.supportEmail}`}>
          {config.supportEmail}
        </a>{" "}
        to access, correct or delete your personal data.
      </p>

      <h2>Data protection contact</h2>
      <p>{TODO}</p>
    </Page>
  );
}

export function ShippingPage(): ReactElement {
  return (
    <Page
      title="Shipping &amp; Delivery Policy"
      description="How and when your produce reaches you."
    >
      <h2>Processing schedule</h2>
      <p>
        Orders placed before <strong>4:00 PM IST</strong> are processed the same
        day. Orders placed at or after 4:00 PM are processed the next day.
      </p>
      <p>
        Your order screen shows three milestones — Order received, Order
        processed, and Delivery day — with the dates that apply to your order.
      </p>

      <h2>Delivery</h2>
      <p>
        We hand your order to professional courier partners after processing, so
        live tracking is not available. Dates shown are estimates and may be
        affected by weather, local conditions or courier delays.
      </p>
      <p>
        Typical timeline: an order processed on day one is handed over on day
        two, with expected delivery on day three.
      </p>

      <h2>Delivery charges</h2>
      <p>A flat delivery charge of ₹15 applies per order, shown at checkout.</p>

      <h2>Serviceable areas</h2>
      <p>{TODO} — list the PIN codes currently served.</p>

      <h2>If something goes wrong</h2>
      <p>
        If your order has not arrived within two days of the expected delivery
        date, email{" "}
        <a className="underline" href={`mailto:${config.supportEmail}`}>
          {config.supportEmail}
        </a>{" "}
        with your order number.
      </p>
    </Page>
  );
}

export function RefundPage(): ReactElement {
  return (
    <Page
      title="Refund &amp; Cancellation Policy"
      description="Fresh produce, and what that means for returns."
    >
      <h2>Cancellation</h2>
      <p>
        You may cancel an order before it is processed — that is, before 4:00 PM
        IST on its processing day. Contact{" "}
        <a className="underline" href={`mailto:${config.supportEmail}`}>
          {config.supportEmail}
        </a>{" "}
        with your order number.
      </p>
      <p>
        Once an order has been processed and handed to a courier it cannot be
        cancelled, because the produce has already been picked and packed for
        you.
      </p>

      <h2>Returns</h2>
      <p>
        We do not accept returns of fresh produce for hygiene and food safety
        reasons.
      </p>

      <h2>When we refund</h2>
      <ul>
        <li>Your order was cancelled before processing</li>
        <li>An item was not delivered</li>
        <li>Produce arrived spoiled, damaged or materially different from what was ordered</li>
      </ul>
      <p>
        Please report quality issues within 24 hours of delivery, with photographs
        where possible.
      </p>

      <h2>How refunds are made</h2>
      <p>
        Approved refunds are returned to the original payment method via
        Razorpay. Banks typically take 5&ndash;7 working days to credit the
        amount.
      </p>
      <p>
        Where only part of an order is affected, we refund the affected items
        and the proportionate platform fee. The delivery charge is refunded only
        where the whole order failed.
      </p>

      <h2>Failed payments</h2>
      <p>
        If a payment fails, no amount is captured and your reserved items are
        released automatically. If your bank shows a debit for a failed order,
        it is an authorisation hold and is released by the bank, usually within
        5&ndash;7 working days.
      </p>
    </Page>
  );
}

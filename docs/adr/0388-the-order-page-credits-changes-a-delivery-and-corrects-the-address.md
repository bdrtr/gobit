# ADR 0388 — The order page credits, changes a delivery and corrects the address

**Summary:** An order's page credits it, puts a pending order's delivery on another quoted option and corrects where it ships,
under `order:write` through `order.admin`; each form carries what the page was drawn with and is refused when that moved.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0196](0196-the-panel-shows-where-an-order-goes.md), whose page now corrects the shipping address, and [0332](0332-the-panel-opens-a-parcel-on-the-delivery-chosen.md), whose deliveries the page now changes

## Context

ADR 0105 gave an order credit lines, ADR 0199 and 0200 a delivery change at the
price the fulfillment module quotes in the same request, and ADR 0195 a
shipping address correction, each over the admin API alone: the panel listed
no credit, showed no delivery's price, could not ask the quote the flow alone
held, and left the address form to a record of its own (ADR 0196). None of the
three writes carries a key, so a button pressed twice credits twice, and a
correction, which writes the whole address, drawn before another writes back
what that one changed.

## Decision

A credit may name the credited total the caller read, a delivery change the
price it was shown from a quote the fulfilling flow now lists, and an address
correction the shipping address row it was drawn from, which the order's read
provider publishes as `shipping_address_id`; each is refused under the order's
lock or by the flow, with `order_credit_moved`, `fulfilling_quote_moved` or
`order_address_revised`, when that moved, unless a correction says what the
current row says and so writes nothing; a correction naming its row keeps the
row's metadata. The order's page lists the credits and, to an operator who
may write orders, the deliveries with their prices, and offers that operator
the credit form on an order that is not canceled and, on a pending order with
no parcel on the page on its way, each delivery's change to a quoted option
and the address prefilled with its country fixed.

## Consequences

- A concession, a pickup point asked for on the phone and a typo in a street
  are answered where the order is read; a cheaper delivery's credit is listed
  with the credits (ADR 0199), and the next parcel carries the corrected
  address (ADR 0195).
- The same form sent twice acts once: a credit or a correction written
  meanwhile, or a quote that moved, refuses the form and the page is drawn
  with what stands now; a refused address form keeps what was typed.
- A change's difference is taken under the lock against the delivery as it
  stands, so two changes at once credit what the sold delivery and the last
  change differ by.
- Leaving out a canceled order is the page's: the module takes the credit as
  the API does, so a form drawn before a cancel and sent after it credits the
  canceled order.
- Each drawing of a writer's pending order with a delivery and no parcel on the
  page on its way asks every calculated option's provider for a price, and so
  does the page drawn after each act on it; parcels the page may not or could
  not read are quoted and left to the flow, and a quote that cannot be read
  draws no form.
- Admin-only options are listed and return options are not, served under the
  order's write as a cart serves its own options (ADR 0292, 0295).
- A row is closed by a correction and rewritten only by an erasure, after which
  the order refuses any correction (ADR 0195), so one id names one address.
- The parcel check stays the flow's: an operator without `fulfillment:read`, or
  on an erased order, is offered the address form and refused.
- A form missing one of the nine address fields is refused, not sent.
- The admin API names no total, price or row, gains no quote, and acts as
  before. A credit is positive; a charge after the sale, the billing address
  (ADR 0195) and a parcel on an option no delivery stands on (ADR 0332) stay
  where they were.

## Rejected

- An idempotency key: it says the form was sent once, and what the form
  carries says the order is as the operator saw it (ADR 0341).
- Writing a delivery at the price quoted when the form is sent: a credit nobody
  read to the customer would be written, and a credit is not taken back (ADR 0291).
- The option's id typed when the quote fails, as a cart's form falls back (ADR 0292): it carries no price drawn.
- The credits as a read-layer entity (ADR 0270): one order's credits are few and read only on its page, as a claim's evidence is (ADR 0325).
- The printed fields as the address's token, as a customer's address is corrected (ADR 0342): that address is updated in place, and an order's correction writes a new row whose id names what was read.
- The moment of the last correction as the token: it is nil on an uncorrected order.
- The metadata in a hidden field: the operator would send back a value they cannot read.
- The credit form on a canceled order: it was canceled with nothing collected (ADR 0339), so a credit has nothing to lower.

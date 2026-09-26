# ADR 0196 — The panel shows where an order goes

**Summary:** The order module's read-layer provider offers an order's current
addresses, the moment its shipping address was last corrected and the order it
adds to, and the panel's order page shows them beside the order's additions. It
costs one batch read of addresses per page that asks for them.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0196](../measurements/0196-a-screen-that-showed-amounts.md)

## Context

ADR 0192, 0193 and 0195 gave the admin API an order's additions, its addresses
and their correction. The panel reads through the read layer and knows no
module (ADR 0011), and the order provider offered none of the three. The
order page showed who ordered, when, how much and where it stood, and an
operator taking a call about a parcel had to leave the panel to see where it was
going.

## Decision

The order provider offers `adds_to_order_id`, which is also a filter, and
`shipping_address`, `billing_address` and `shipping_address_corrected_at`,
which it fills from one read of the page's addresses and only when one of them
is asked for. The panel's order page shows both current addresses, when the
shipping address was last corrected, the order this one adds to and the orders
that add to it.

## Consequences

An address is a map of its non-empty fields by the order API's names, and nil
when the order recorded none. The corrected moment is the latest correction's,
and nil when there was none. The address a correction replaced is on the
timeline and in the person's file, not on this page.

The read layer is in process and no HTTP surface exposes it, so the addresses
reach nothing outside the installation's own code.

A listing that asks for totals reads no address. A listing that asks for an
address pays one read for its whole page, not one per order.

The page lists up to 25 additions. When they cannot be read it still shows the
order and says so, and a parent it cannot read is still linked by its id.

The panel does not correct an address. The correction is the admin API's, and
a form for it on this page is its own record.

## Rejected

- **The panel calling the order's admin API.** The panel reads through the read
  layer so that its screens cannot drift from what the framework serves, and a
  second path for one page is where they would.
- **The address as one formatted string.** The panel prints it as a label
  reads, and the next reader may not.
- **Reading the addresses for every listing.** A page of totals would pay for
  an address nobody shows.

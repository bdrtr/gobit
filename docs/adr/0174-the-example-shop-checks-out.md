# ADR 0174 — The example shop checks out

**Summary:** The storefront example now takes a guest from the cart to an order
through the store surface alone. Every call it makes is written once as a verb
and a bound template, and the arch suite holds each one to the routes the tree
binds.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0174](../measurements/0174-a-shop-that-reaches-an-order.md)

## Context

ADR 0159 put a browser in front of gobit and stopped at the cart: no address,
no shipping, no payment. Those were endpoints nothing in a browser had called,
and the only proof of the path from a cart to an order was Go harnesses driving
HTTP. Driving the checkout in a real browser found that a storefront could not
show the total it would be charged (D129, ADR 0173). Nothing checked what the
script calls: the route audit reads prose, and a script is not prose.

## Decision

The example's fourth page checks the cart out on the store surface alone — an
e-mail and a shipping address, a shipping option, a payment provider, and the
completion with the cart's own total as `expected_total`. The script writes
every store call once, as a verb and the template the route is bound under, and
`TestTheShopCallsOnlyBoundRoutes` refuses one the tree does not bind.

## Consequences

The verb comes from the route table, not from the call site. The gate compares
the verb and the placeholder names as bound. It also requires every mention of
the store prefix in the script to be a table entry, so an address joined from
strings cannot slip past it; the catalog address was joined that way until
this record.

The page approves only the cart's own figure. It never adds a delivery to a
total, and it refuses to offer a total the cart reports as stale.

A shopper who comes back finds the cart still holding the delivery they chose.
Choosing again replaces it: the other methods are removed and the chosen one is
added only if it is missing. A shop that splits a cart across shipping
profiles keeps one method per profile instead; this one has a single choice.

The payment step lists every registered provider, and the store endpoint cannot
say which of them a guest may use. A guest who picks store credit or loyalty
points is refused at the payment step. By then the order has been opened,
`order.placed` published and the order canceled. The page shows the refusal.

The example depends on the first run creating a shipping option, which it does
since ADR 0173. The script grew from 299 lines to 610.

It still signs nobody in (ADR 0008), and the cart id still lives in
`localStorage` (ADR 0159).

## Rejected

- **Computing the total on the page.** It hid D129 rather than finding it; the
  figure a shopper approves is the server's.
- **Skipping the shipping step when no option is listed.** A paid order with
  nowhere to go.
- **Filtering the providers by name in the script.** The script would carry the
  payment module's knowledge of which tenders belong to a person.
- **Auditing the script's paths without verbs.** A POST sent to a GET route is a
  405 the path alone does not catch.

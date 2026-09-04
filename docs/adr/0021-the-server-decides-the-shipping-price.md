# ADR 0021 — The server decides the shipping price, as it already decided the line price

- **Status:** Accepted
- **Date:** 2026-09-05
- **Phase:** after the roadmap

## Context

`POST /store/v1/carts/{id}/shipping-methods` is a storefront endpoint. Its
handler read `amount` out of the request body and passed it to
`cart/service.Service.AddShippingMethod`, which validated only that the number
was within `models.MaxAmount` and wrote it. `calculate_totals` then summed the
stored number, and the checkout plan checked the arithmetic — which was
internally consistent and wrong.

**A shopper could post a real `shipping_option_id` with `amount: 0` and receive
an order that was created AND captured at that price.**

Two things make this worse than an oversight.

**The engine that produces the right number was already built and nothing asked
it.** `fulfillment/service.Interop.ListOptionsJSON` quotes the options eligible
for a cart, including rule-bound ones ("free shipping over 500"), and its godoc
says it is "only resolved by the cart/order flows". It had ZERO consumers. So
the repository carried both halves of the defect at once: a price nobody
verified, and a verifier nobody called.

**The rule that forbids this was already written, about the other price.** The
cart module's own API package doc explains why `AddLineItem` is absent from its
`Carts` interface:

> the endpoint that adds a line item calls the LinePricing flow, because the
> SERVER decides the price … Had the method stayed on the surface, a handler
> bound to it would SILENTLY skip both the pricing and the ceiling.

Every word of that is true of the shipping price. `AddShippingMethod` stayed on
the surface anyway, and a handler bound to it did exactly what the sentence
predicts.

## Decision

**The storefront names WHICH shipping option. The server decides what it
costs.**

- A new flow, `workflows/cart.Workflows.AddQuotedShippingMethod`, quotes the
  option through the fulfillment interop surface and writes the quoted amount.
- `AddShippingMethod` is REMOVED from `cart/api`'s `Carts` interface, so no
  handler can reach the service method again. The service method stays; the
  flow calls it through `cart.interop`.
- `amount` and `name` are removed from the request body. Because this API
  rejects fields it does not recognise, a client still sending either now gets
  an error rather than being quietly overridden.
- Every fact the quote is computed from — region, currency, country, subtotal,
  item count — is read from the cart's own record. The caller supplies the
  option id and a free-form blob that is carried and never read.

**The flow FAILS CLOSED.** With no fulfillment surface wired, the method is not
added.

## Rejected options

**A. Validate the client's amount against the quote and reject a mismatch.**
Equivalent in security, worse in every other way: the client would have to
re-quote before every write, two systems would have to round money identically,
and every rule change would break clients that had cached a price. Sending a
number that must equal a number the server already knows is not an input.

**B. Quote inside the cart module.** The cart module cannot ask fulfillment —
a module does not know another module (Principle 2.1/2.4). Deciding a price from
facts in two modules is what the workflow layer is for (ADR 0006), and the line
price is already decided there.

**C. Let the flow fall back to the caller's amount when fulfillment is absent.**
That is the defect, restored as a degraded path. The comparison with the tax
surface is exact and instructive: a missing tax surface degrades to the region's
rate, which is a real answer computed from a real record. A missing shipping
surface has no such answer — the only other source is the caller.

**D. Fetch the option by id instead of searching the quote.** The listing is not
a catalog; it applies this cart's eligibility rules. Reaching past it to load
the option directly would price something the rules had excluded, and would also
lose the rule-bound prices, which exist only as an outcome of the listing.

**E. Keep `amount` for an admin path.** There is no admin route for cart
shipping methods — the cart's admin surface is read-only by decision. So no
legitimate caller was relying on the field.

## Consequences

**This is a BREAKING API change, deliberately.** A storefront that sent `amount`
now receives a 422. That is the correct outcome: an integrator who was sending
an amount needs to learn it was being obeyed. A silent ignore would leave them
believing they still control the price.

**The subtotal in the quote is taken AFTER discounts,** applied in the same
order `computeTotals` applies them so the two cannot disagree about what the
basket is worth. Threshold rules read this number, and answering them
pre-discount would spend the same discount twice: once off the goods and again
off the delivery.

**A shipping method with no option can no longer be created from the
storefront.** The service still permits one — the schema allows it — but the
flow refuses it, because a method with no option is a method whose price came
from the caller.

**A quote in another currency is refused** rather than added. The number would
be summed into the shipping total as a bare integer and the arithmetic would
look sound while the money was wrong.

**`ListOptionsJSON` now has a consumer,** which is what ADR 0009 asks of any
capability. It had none for two phases.

**The cart carries no weight, so the quote is asked with a weight of zero.** A
weight-banded option therefore prices at its lowest band. That is honest — it is
what this installation knows — and it is the one input in the request that is
not yet a real fact about the cart.

## Reopening

An admin route that sets a manual shipping charge (a negotiated rate, a
goodwill delivery) is a legitimate future need and does NOT reopen this
decision: it would be an admin-scoped endpoint whose caller is authenticated and
audited, which is a different trust boundary from an anonymous storefront body.

The weight input reopens the moment the cart learns what it weighs; nothing else
changes when it does.

## Related

- ADR 0001 — consumer-side interfaces, resolved from the container by name.
- ADR 0006 — cross-module orchestration is the workflow layer's job.
- ADR 0009 — a capability with no consumer. `ListOptionsJSON` was one.
- ADR 0020 — the previous decision that turned on measuring a promise the
  repository had made and not kept.

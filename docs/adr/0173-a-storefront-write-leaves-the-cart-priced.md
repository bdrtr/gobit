# ADR 0173 — A storefront write leaves the cart priced

**Summary:** Every storefront write on a cart now recomputes its totals before
it answers, so the total a shopper reads is the one the completion charges.
Seven writes pay a repricing round they did not pay before.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0173](../measurements/0173-what-a-checkout-could-not-approve.md)

## Context

A storefront never computes a total. It reads the one on the cart and sends it
back as `expected_total`, and the completion refuses a cart whose total has
moved since. Adding or changing a line and applying or removing a coupon go
through a flow that reprices the cart. Seven writes did not: the e-mail and
the customer, both addresses, adding and removing a shipping method, removing
a line, and a merge. Each left `totals_stale` true, and nothing on the store
surface could refresh it — `CalculateTotals` was kept off it on purpose. A
browser that chose a delivery was shown a total without it, and the completion
refused the figure the shopper approved (D129). No test had ever added a
shipping method on the storefront and then completed.

## Decision

Every storefront write on a cart leaves its totals fresh: the shipping flow
reprices after it adds a method, as the line flow does, and the six writes the
cart service makes alone are handed to a cart-flow method that runs them and
then reprices. The completion and the delete are the only cart writes left out,
because they end the cart.

## Consequences

The pairing is structural rather than a habit of the handler. The write runs
inside `RepriceAfter`, so a handler cannot write without the repricing, and a
flow that is missing refuses before anything is written.

A repricing that fails after the write does not undo it. That is the line
flow's existing choice: the answer is an error saying the totals are stale, and
the cart says so too.

The e-mail update and the merge now answer with the cart read after the
repricing, not the copy the service returned before it.

Seven writes cost a repricing round each, which reads pricing, promotion and tax.
An e-mail change pays it without changing a figure. The revision counter does
not tell a write that moves money from one that does not, and teaching it to
would be a second place to decide what affects a total.

An end-to-end gate reads the cart writes off the router. It runs each one on a
cart and requires `totals_stale` to be false afterwards. A new cart write fails
it until it has a scenario. The executed first run now chooses a delivery and
reads the total after it, so the smoke lane is a second witness.

The completion still recomputes on its own and compares. `expected_total`
keeps its meaning: the figure the shopper saw, now one they could have seen.

## Rejected

- **A storefront endpoint that reprices on request.** It ties the amount to
  when a client asks, which is why `CalculateTotals` was kept off the surface.
- **Repricing on read when the totals are stale.** A read that writes, and two
  readers racing to write the same totals.
- **Repricing in the cart service's write frame.** The service cannot reach
  pricing, promotion or tax (ADR 0006).
- **A flow method per write.** Six methods that each do one write and the same
  round; the write is what differs, so it is the argument.

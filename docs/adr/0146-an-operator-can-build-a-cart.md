# ADR 0146 — An operator can build a cart

**Summary:** The cart's admin surface gains two writes — open a cart, add a
priced line — so an order can be taken over the telephone. It costs a channel
claim the server did not prove, and buys an order priced by the same rules a
shopper's goes through.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

A shop that answers the telephone has no way to serve the caller. The admin
surface could read every cart and write none, by decision: a correction made from
the panel changes the amount the customer is looking at, behind their back.

The order module cannot fill the gap either — `CreateOrder` deliberately has no
route, because an order opened over HTTP would carry a total the caller decided.
What makes an amount the server's is the cart: it prices every line from the
catalog, applies the region's tax and the price list, and completion takes the
money against that figure.

So the operator needs a cart, and a cart write needs a sales channel. The
storefront's comes from the publishable key; an administrator's key carries none,
and a principal with no channels is "bound to no channel" rather than "unscoped" —
the catalog would answer with the products assigned to nothing.

Measurement: [measurements/0146](../measurements/0146-what-a-telephone-order-could-not-do.md)

## Decision

The admin surface gains exactly two writes — `POST /admin/v1/carts` and
`POST /admin/v1/carts/{id}/line-items` — and the line write carries a REQUIRED
`sales_channel_id`. The handler ASSERTS that channel into the request's
principal, so the cart's existing scope rule runs unchanged rather than being
skipped, and a request that makes no claim is refused with 422.

## Consequences

The scope on these requests is one the server did not prove, and that is the
price: everything downstream trusts `Principal.SalesChannelIDs` as if the guard
ring had put it there. What keeps it narrow is that exactly one function may do
it. The derivation gate had to be split for that — writing the field and reading
it are opposite acts one test could not tell apart — so the read gate still
refuses a second derivation and a new gate lists who may ASSERT one, and why.

The claim is made per REQUEST and the cart does not remember it, so two lines of
one cart can be written under two channels. It is the operator's own doing: they
hold `cart:write` and the audit ring recorded them. Opening a cart names no
channel at all, because nothing on that path reads one — requiring it would have
been a claim asserted into a context nobody consults, which is the defect this
repository keeps closing rather than a symmetry worth having.

`customer_id` is taken on the operator's word, which on the storefront has to be
proved (ADR 0125) — the endpoint's whole point, because a cart in the customer's
name carries their history and their company's spending limit.

Neither body takes an amount or a title, so the price stays the server's. An
operator can now add a line to a cart a shopper is holding; what stays refused is
everything that CHANGES what they already saw — quantity, removal, address,
shipping method, completion. The money is still taken on the storefront, by the
shopper, against the total in front of them.

Eight mutations bit. Two are one defect from both sides: asserting the claim and
running the flow unscoped anyway, and asserting it into a principal no gate
watched. Both leave every unit test green, because a fake catalog answers the same
for every channel — so the witness is end to end.

## Rejected

- **Let the admin write carry no channel at all.** The catalog would have answered
  404 for every assigned product, with the message a mistyped variant id returns.
- **Store the channel on the cart and claim it once.** No cart carries a channel
  today, and a scope written once is one the caller chose for every later request.
- **Write through the cart module's own service.** The price and the title would
  stop being the catalog's; the flow is what makes them the server's.
- **Complete the order from the admin side too.** The money would be taken without
  the shopper ever seeing the total.
- **Verify the named channel exists.** No module validates another's ids; links
  carry no foreign key, and inventory binds a warehouse to a channel the same way.

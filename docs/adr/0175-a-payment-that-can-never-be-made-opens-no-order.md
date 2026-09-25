# ADR 0175 — A payment that can never be made opens no order

**Summary:** The checkout asks the payment module, before the saga, whether the
chosen payment can be made at all, and refuses the order when the answer is
already no. An unregistered provider and a person's balance for a guest cart
no longer place an order, announce it and cancel it.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0175](../measurements/0175-an-order-announced-and-canceled.md)

## Context

The completion saga opens the order in its second step and asks the payment
provider in its third. Two refusals do not depend on anything the saga
learns: a provider the installation never registered, and store credit or
loyalty points for a cart that names nobody (ADR 0152, 0165). Both were
learned at the third step. By then the order was placed and `order.placed`
published, so a shop with a mail provider told the shopper their order was
placed, then the compensation canceled it (D130). The storefront example lists
both tenders to a guest (ADR 0174), and a typo in a provider id took the same
path.

## Decision

Before the saga, the checkout asks the payment module's `CheckTender` with the
provider and the cart's customer, and a refusal ends the completion with the
payment module's own code. The module refuses only what is known in advance:
an unregistered provider, and a provider that spends a person's balance when
the cart names nobody.

## Consequences

A refused completion opens no order, reserves no stock and publishes nothing.
The status codes do not change — 409 with the tender's code, 404 with
`payment_provider_not_found` — so a storefront reads the refusal as it did.

The rule stays in one place. `balancetender.Machine.CheckOwner` is what both
tenders answer with, and `CreateSession` asks it too. The check and the payment
step cannot disagree about a guest.

A declined card and a balance too small are still refused at the payment step,
after the order opens. Their answer is true only when the provider is asked.

The hook is internal. A provider outside the repository that spends a person's
balance is still refused at the payment step, as before, until it can
implement a published `CheckOwner`. Publishing one is a promise kept forever
(ADR 0026), and nothing outside the tree needs it yet.

The refusal's message now reaches the client, which it did not when the saga
wrapped it. The provider registry's messages were Turkish, so its file was
translated and left the language ledger.

An end-to-end gate completes a guest cart with each tender and with an
unregistered provider, and requires the refusal and no order for the cart.

## Rejected

- **A list of providers per cart.** `GET /store/v1/payment-providers` takes no
  cart, and a list that hid the tenders from a guest would not stop a client
  that sends the id anyway.
- **Opening the order after the payment step.** It reorders the saga that
  every compensation is written against, to spare two refusals a check solves.
- **Checking in the cart module's handler.** The cart module cannot ask the
  payment module (ADR 0006), and a caller of the flow that is not the handler
  would skip the check.

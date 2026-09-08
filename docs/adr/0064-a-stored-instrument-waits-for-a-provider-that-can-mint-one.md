# ADR 0064 — A stored payment instrument waits for a provider that can MINT one

**Summary:** gobit keeps no payment instrument, and what it waits for is a
provider whose upstream mints a reusable credential. Paying with a stored token
already works end to end; minting one and owning one do not.

- **Status:** Accepted
- **Date:** 2026-09-08
- **Phase:** after the roadmap

## Context

Gap B9 asks whether gobit stores a customer's payment instrument, and records
that both halves would widen `core/provider`. Measured: one half already is
published, and widens nothing.

**Paying with a stored token is wired end to end today.** The storefront route
`POST /store/v1/carts/{id}/complete` accepts a `payment_data` object that travels
unread through `CompleteCartInput.PaymentData`, the plan and the payment module's
interop surface into `CreateSessionInput.Data`, whose godoc names "a card token"
as an example of what belongs there. A provider accepting a stored token can be
paid with one now, with no contract change.

**What is absent is MINTING and OWNING.** No provider here returns a reusable
credential: `paymentpaytr` hands back a per-payment iframe key under
`DataKeyIframeToken`, spent when that payment closes, and every money method of
`paymentstripe` returns `stripe_not_implemented`. Nothing owns one either — the
payment module's five tables carry no customer column at all. **So a vault
contract has no implementer and no caller**, and publishing it would fix a
permanent shape on `core/provider` (ADR 0026) guessed from no upstream — what
ADR 0042 rejected in this package for this reason — while breaking the rule that
a capability ships with its first consumer.
[Measurement](../measurements/0064-the-stored-payment-instrument.md).

## Decision

**NOT NOW, and what it waits for is a PROVIDER rather than a feature.**

**The trigger:** an integration under `plugins/` that implements all five money
methods against a real upstream AND whose upstream mints a customer-scoped
credential it will itself accept on a later charge with no shopper present. A
second acquirer, or `paymentstripe` ceasing to be a skeleton, makes it true, and
a person checks it by reading what that provider returns. **Deliberately not
"when subscriptions arrive":** C12 waits on B9, so a subscription-shaped trigger
would be a deadlock, while a provider's capability becomes true alone.

**The shape, fixed here so the next person does not re-derive it.** An OPTIONAL
interface embedding `provider.PaymentProvider`, the shape
`provider.SessionInspector` already argues for in that file: a method on the base
interface changes every provider for one provider's capability, and forces one
that cannot vault to implement a lie. Only minting and listing are new surface.
gobit keeps a REFERENCE and never an instrument — the provider's token plus what
it says for display, no card number ever, the row personal data the module
declares (ADR 0033). It sits in the payment module and reaches the customer
through the link registry — one of the two shapes this repository sanctions,
the other a free `customer_id` column. That choice belongs to its builder.
identity is bound — ADR 0043's row rather than ADR 0057's exception, which was
about not withdrawing a surface in service; one shipping new withdraws nothing,
so the apparent second blocker is answered.

## Consequences

- **B9 stops being a gap and becomes this record**, C12 waits on a named
  observable, and **the row's claim is corrected**: half of B9 needs no widening,
  so a reader who believed it priced the work double.
- **No card is stored.** A shopper retypes at every checkout.
- **The published surface is not spent on a guess**, and nothing enforces that:
  the published-package list catches a new package, not a new type in an old one.

## Rejected

- **Publish the vault contract now, with saved cards as its consumer.** Zero
  providers implement it, so that consumer lists an empty set forever.
- **Put a customer column on `payment_collections` instead.** Not an instrument:
  a collection is per-sale, and it is the column-nothing-writes class of D9/D18.
- **Leave B9 open.** Waiting on a named observable is a decision, not a gap.
- **Give the instrument to the embedder, as ADR 0050 gave the second language.**
  The route runs the whole saga server-side, so an embedder can inject a token
  but cannot LIST one against a customer only it identifies — which is the half
  a saved card is for.

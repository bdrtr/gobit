# ADR 0057 — A storefront request that NAMES a customer proves it, through one comparison

**Summary:** Every storefront surface that names a customer hands the claim to
one published comparison and refuses what the bound identity contradicts. An
installation that bound none is served as before, and is told what that costs.

- **Status:** Accepted
- **Date:** 2026-09-08
- **Extends:** ADR 0008, whose fourth blocker was the guest-to-registered
  handover; ADR 0043, which OWED b2b's copy and DEFERRED the cart — this record
  overturns that deferral

## Context

ADR 0043 required the embedder's identity at the address book, left b2b's copy
of the boundary owed, and did not merely leave the cart open: it said it
"deliberately does not touch" the cart, that the guest-to-registered handover
was "avoided rather than answered", and that taking the contract there would
close the door a shopper without an account walks through. ADR 0008 had listed
that handover as the fourth of its four blockers; ADR 0051 recorded the cart's
`customer_id` as an OPEN DEFECT rather than an exemption. Gap row D3.

That premise no longer holds: the CLAIM can trigger the check instead of the
surface, so a body naming nobody is never asked and the guest door stays open.

The claim was an oracle, and not only for the spending window it was recorded
for: a cart opened for a customer with no e-mail is written with that customer's
REGISTERED address and read back, so one request against an identifier that
travels in every order response answered "does this person exist" and "how do I
reach them". The b2b routes answered the other half. Two copies of the
comparison would be the real hazard, and the b2b package doc had refused the
contract in passing for that reason: a wrong copy keeps answering.

Measurement: [measurements/0057](../measurements/0057-the-storefront-customer-claim.md).

## Decision

**A storefront surface that names a customer hands the claim to one function,
`corehttp.ProvenCustomer`, and refuses what the bound identity CONTRADICTS.** The
b2b storefront and the cart bind it; the address book moves onto it.

**The CLAIM triggers the check, not the surface.** A cart body carrying no
`customer_id` names nobody and is never asked; making the claim is what puts the
burden of proof on it (ADR 0043's "mandatory means backed, not declared").

**A bound identity is required nowhere it was not required before.** With none
bound the b2b storefront and the cart serve the claim as they did, and warn.
ADR 0043 could close the address book outright because no anonymous caller has a
correct use for somebody's street address; these four routes are in service, and
gobit refusing to GUESS is not gobit refusing to serve.

**Each module keeps its own lazy resolve and shares the comparison**, which is
the half that must not diverge.

## Consequences

- **Nothing that works today stops working.** No surface is withdrawn and no
  status changes for a caller telling the truth; the shipped `cmd/server`, which
  binds no identity, keeps its b2b storefront and its smoke coverage.
- **The oracle stays open where no identity is bound**, which is the price of
  the sentence above: a caller who knows an identifier still reads that person's
  employer and allowance and can open a cart in their name. **The way to close it
  is to bind a verifier**; both modules log a WARN naming the empty slot, and
  `docs/known-limits.md` carries the row.
- **`core/http` gains one exported function** and now returns the three identity
  codes it only declared; the package is published already (ADR 0026).
- **A tree-wide gate holds it.** `TestNoStorefrontSurfaceActsOnACustomerItCannotProve`
  takes its population from the routes and the request types, not the property.
- **`plugins/webpush` still takes a `customer_id` on trust** — the gate walks the
  module tree, and that plugin's defect is a standing authority (ADR 0051).

## Rejected

- **Refuse when no identity is bound, as the address book does.** It withdraws a
  working b2b storefront and every customer cart from an installation that did
  nothing wrong.
- **Suppress the e-mail echo and leave the claim unproven.** Cheap, and it
  decides three questions the measurement names without asking them.
- **Copy the comparison into each module.** What ADR 0043 left, and what the b2b
  package doc refused in advance.

# What a telephone order could not do

Evidence for [ADR 0146](../adr/0146-an-operator-can-build-a-cart.md).

Measured 2026-09-12, while working the feature list's A section.

## The surface, before

`cart`'s admin side had two endpoints, both reads, and the absence was a written
decision rather than an omission — the same sentence appears in six places
(`api.go`'s package doc twice, `admin.go`, `store.go`, `module.go`, and a test's
godoc), plus `docs/commerce-flows.md` and ADR 0021's rejected options.

The argument in all of them is one: *a correction made from the admin panel would
change the amount the customer saw behind their back.*

That argument covers changing a cart. It does not cover OPENING one, and the
distinction had never been drawn, because until an operator needed to serve a
caller there was no reason to draw it.

## Why not an order endpoint

`CreateOrder` has no route and its godoc says why: an order opened over HTTP
carries a total the caller decided. Measured on the tree, the amounts an order
carries are written by the completion flow, and the only thing between a client
and a total of zero is the absence of that route.

| Piece | Where the amount comes from |
|---|---|
| line unit price | the catalog, through `AddPricedLineItem` |
| line title | the catalog, same call |
| tax | the region's rate, in the totals pass |
| grand total | the totals pass, re-run on every write |
| captured amount | the completion flow, against `expected_total` |

So the cart is not a detour on the way to an order. It IS the pricing, and an
operator who wants a server-priced order has to go through it.

## What the admin identity holds, and what that does to a catalog read

An admin key is not a publishable key: it carries scopes and no sales channel.
`corehttp.SalesChannelIDs` distinguishes three states, and the middle one is the
one that decides this design:

| Principal | `SalesChannelIDs(ctx)` | What the catalog does |
|---|---|---|
| none in context | `nil` | no filter at all |
| present, no channels | `[]string{}` | only products assigned to NO channel |
| present, with channels | the list | those channels' products |

So serving an admin line write with the caller's own identity is not "unscoped".
On a shop that uses channels it answers 404 for every product — and that 404 is
deliberately indistinguishable from "no such variant"
(`TestAnOutOfScopeVariantDoesNotRevealItsExistence`), so the operator would have
had a working endpoint that refused their whole catalog with a message about the
variant id they typed.

That is why the claim is 422 when it is missing: a refusal that says what is
missing, rather than a success that hides it.

## The half of the design that was measured and removed

The first draft required `sales_channel_id` on BOTH writes, for symmetry. Reading
`CreateCart` end to end killed it:

```
country -> RegionIDForCountry -> RegionCurrency -> CustomerEmail -> OpenCart
```

No catalog, no price list, no channel — the claim would have been asserted into a
context nothing on that path reads. That is this repository's own recurring class
(D62, D66, D71, D78: a mechanism nothing feeds), so the field came off the create
body, and `DisallowUnknownFields` turns a client that still sends it into a 422
rather than silently dropping it.

## Why the gate had to be split

`TestTheChannelDerivationIsNotCopied` refused the handler. Its matcher is
`asksForThePrincipal(fn) && readsTheChannelField(fn)`, and `readsTheChannelField`
matched any `SelectorExpr` named `SalesChannelIDs` — including the one on the
LEFT of an assignment. The handler WRITES the field; the gate read that as a
fourth copy of the derivation.

The exemption list was the wrong instrument: an exemption says "this read is not a
derivation", and this is not a read. So the matcher now ignores assignment
targets (keyed by node, so `p.X = append(p.X, …)` still counts the right-hand
read), and a second gate — `TestOnlyAGrantedSurfaceAssertsAChannel` — lists who
may put a principal into the context with a channel it chose. Exactly one grant
exists.

What the new gate cannot see is written in its godoc: minting a principal from a
key's record (`auth/service/interop.go`) writes the field but never calls
`WithPrincipal`, so the scan does not reach it. That is deliberate — answering
"what does this key hold" is auth's own job.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 33 | `channelScoped` also READS the field | **bit** (derivation gate) |
| 34 | the assertion grant is deleted | **bit** (assertion gate) |
| 35 | `adminui/guard.go` names the channels too | **bit** (assertion gate) |
| 36 | the claim is never written into the principal | **bit** (1 unit, 2 e2e) |
| 37 | a blank channel is accepted | **bit** (1 unit, 1 e2e) |
| 38 | the claim is made and the flow runs under `ctx` | **bit** (1 unit, 2 e2e) |
| 39 | the line answers with the module model, not the DTO | **bit** (1 unit, 1 e2e) |
| 40 | the create body accepts `sales_channel_id` and ignores it | **bit** (1 unit) |

Mutation 38 is the one the design is most exposed to, and it is the inert-claim
class again: the field is read, validated, asserted — and then the flow is handed
the original context. Every unit test that checks the refusal still passes; the
only thing that changes is which catalog the line is priced against. Its witness
is therefore `TestAnOperatorsLineIsPricedInTheChannelItNames`, whose subject is a
variant that exists, is published and is priced, and is refused for one reason.

Mutation 39 is the one that was a real defect rather than a probe: the first draft
of the handler wrote `singleEnvelope{Data: item}` — the module's model, whose Go
field names have no json tags — while `describe.go` published the DTO's schema. No
gate compares the two, because the document is derived from the TYPE named in the
description rather than from what the handler encodes.

## What is NOT closed

The operator cannot finish the order. Addresses, shipping method and payment are
the storefront's surface, so a telephone order needs the shopper to open the cart
link — or an operator holding the shopper's own session. Whether that is a gap or
the decision working as intended depends on the shop, and nothing here decides it.

## What was not measured

Whether an operator adding a line to a cart a shopper is holding causes trouble in
practice. It is now possible, it is refused for every other kind of change, and
the totals the shopper sees are recomputed on the next read — but no installation
has been asked.

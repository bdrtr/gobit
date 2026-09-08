# ADR 0008 — Customer identity: the framework's boundary, the embedder's responsibility

**Summary:** Verifying customer identity is the embedding application's job.
gobit draws the boundary, documents it and pins it with tests, and issues no
customer session, cookie or signing key.

- **Status:** Accepted
- **Date:** 2026-09-01
- **Phase:** after 10 (the v0.4.0 hardening round)

## Context

The repository describes the B2B spending limit as an **enforced rule**:
`docs/commerce-flows.md`'s "Where the check lives and why there" section, the
`order` module's godoc and `internal/e2e/b2b_test.go` all build the same
sentence — a purchase exceeding the limit does not become an order, no money is
captured, stock stays untouched. All of that is **true**. What was missing was
the **condition** under which the rule is enforced.

The rule runs inside `order.CreateOrder`, over `CreateOrderInput.CustomerID`.
That identity enters the head of the chain from the body of the storefront cart:

```
POST /store/v1/carts  {"country_code":"TR","customer_id":"cus_…"}
  -> cart -> complete_cart saga -> order.CreateOrder(CustomerID: "cus_…")
     -> b2b.interop.SpendingLimitJSON("cus_…")
```

The store surface's only identity is the **publishable API key**, and that
represents a sales channel, not a customer: the fields of `corehttp.Principal`
are `ID`, `Kind`, `Scopes` and `SalesChannelIDs` — there is **no** customer
identity. So `customer_id` is not a fact but an **ownership claim** requiring no
evidence at all; `cart/api/store.go` already said so in its own godoc.

Measured on the real binary, with a single publishable key. The same cart, the
same client, the only difference the field in the body (limit `50_000`, cart
total `76_800`):

| Request body | Result |
|---|---|
| `{"country_code":"TR","customer_id":"cus_…"}` | `409 order_spending_limit_exceeded` |
| `{"country_code":"TR"}` | `200`, the order opens (`customer_id: ""`) |

The second half of the same measurement: when a **foreign client** completed a
purchase with somebody else's `customer_id`, the order was written in that
customer's name and the spend came out of their window — at the next step the
**employee's own purchase** took a `409`. So the claim is not just an escape
hatch, it is a way to **burn** the spending allowance of an employee whose name
is known, and because the publishable key is not a secret anyone in the browser
can do it.

There is a third form as well, and it eliminates the options:
`POST /store/v1/customers` **opens a new guest record** with a publishable key.
So even saying "declaring an identity is mandatory" is not enough; the escaping
party sends one more request and produces a fresh identity bound to no company
at all.

The fourth is that attribution **is not tied to cart creation**: a cart opened
as a guest can be handed over to somebody else's `customer_id` with
`POST /store/v1/carts/{id}` and the order is written to that identity (measured:
the handover `200`, the order in the victim's name). This also eliminates every
gate of the form "ask for an identity at creation": attribution rests on the
declaration throughout the cart's LIFETIME, not at a single moment of it.

## Decision

**Verifying customer identity is the embedding application's job, not the
framework's. gobit DOES NOT BUILD identity verification this round; it draws the
boundary, documents it and pins it with tests.**

The exact statement of the boundary is this:

> The spending limit applies to purchases that **declare their customer**. gobit
> does not verify the truth of the declaration. No limit is applied to a
> purchase that does not declare one.

This has three consequences, and all three are on the record:

1. **The documentation was pulled to the truth.** The README's B2B section now
   states the rule's condition in its measured form; the `order` module's godoc,
   the `SpendingPolicy` interface and `CreateOrderInput.CustomerID` repeat the
   same boundary in their own places. The sentence saying the rule "applies to
   every purchase" is left nowhere.
2. **The boundary was pinned with tests.**
   `TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule` and
   `TestTheSpendingRuleIsAppliedToTheDeclaredCustomer` in
   `internal/modules/order/service/spending_test.go` hold today's position of
   the boundary as behaviour. Both of them protect a **decision** rather than a
   capability: when a layer that verifies identity is added they are expected to
   fail, and their failing on that day is the sign that the decision was really
   taken.
3. **The work falling to the embedder was named.** It is the embedder who
   protects the storefront surface with a customer session: `customer_id` must
   come from the session, not from the body, and a mismatch must return
   `errors.Forbidden`. The place that will change in the code is narrow and
   marked — cart creation in `cart/api/store.go`, `b2b/api`'s `storeCustomerID`
   helper and `order`'s `spendingRuleFor` entry point.

## Consequences

**Positive.** The repository's most expensive class of fault was "the code is
right but the documentation says something wrong"; this ADR closes it. The
operator setting up B2B reads what the limit guarantees **before** installing:
the limit enforces accounting discipline on a storefront where identity is
verified; on a storefront where it is not, it only catches the honest client's
mistake.

**Positive.** Because the boundary was **written down** somewhere, it became
measurable: today, two tests in `order` and one table row in the README.
Tomorrow, when identity verification arrives, the list of places that have to
change is a reference, not a guess.

**Negative.** The framework **does not guarantee** the B2B spending limit when
run on its own, and that lowers the feature's marketable strength. Accepted:
giving a wrong guarantee is more expensive than giving none — a limit that is
trusted is more dangerous than a limit that is not.

**Negative.** The boundary is spread across two layers: the place that
**accepts** the identity is `cart`'s storefront endpoint, the place that
**enforces** the rule is `order`. The embedder has to read both. The
countermeasure is that both places refer to each other and to this ADR.

## Rejected alternatives

**A minimal gate: "the cart of a limited customer must not be completed as a
guest."** Not implementable, because `order` **cannot know** that a guest cart
belongs to a limited employee: it holds nothing but an empty `customer_id`. The
only field that could establish the link is the cart's email, and that too is
unverified, freely chosen by the client (and changeable via
`POST /store/v1/carts/{id}`). On top of that, turning `order` into an
email → customer resolver would open a new gate for customer **enumeration**:
the order endpoint's answer would come to answer the question "is this email
registered". In short, the gate would produce a new hole without closing the
escape.

**Making the `customer_id` declaration MANDATORY.** At first glance it closes
the guest escape. It does not: `POST /store/v1/customers` opens a new guest
record with a publishable key, that record is bound to no company and has no
limit either. The price, meanwhile, is real: guest purchasing is the
storefront's **default path** and this change would break it in every
installation — for the sake of a hole it does not close.

**Asking for proof of the declaration (a signed customer token).** This is the
right solution, but not this round's work: **who issues** the token (auth, or
the embedder), how long it lives, how the cart is handed over on the transition
from guest to registered customer, how the authorization model on the admin
surface is affected once `corehttp.Principal` starts carrying a customer
identity — all of these are separate decisions. A half identity layer is more
dangerous than a missing one: a server that thinks it verifies makes worse
decisions than a server that knows it does not.

**A "strict mode" that can be turned off with an environment variable.**
Rejected for the same reason as ADR 0007's argument: a flag accidentally set to
`false` removes the protection without producing a single error. Here it would
also land on the wrong side — an installation saying "strict mode on" would in
reality carry on trusting an unverified declaration.

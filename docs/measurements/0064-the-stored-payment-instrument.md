# The stored payment instrument — what B9 asked, and what half of it already is

Measured 2026-09-08 against the tree at `698b649`, for
[ADR 0064](../adr/0064-a-stored-instrument-waits-for-a-provider-that-can-mint-one.md).

Gap B9 said: *"Stored payment instrument — OPEN, blocked on a published-contract
decision. No table, no column, no symbol; both halves would widen
`core/provider`."*

Three of those four claims hold. The fourth is wrong, and it is the one that
decides the size of the work.

---

## 1. The census: no instrument exists

A tree-wide search for the vocabulary — payment method, payment instrument,
payment token, saved card, stored card, vault, tokenize — over every `.go`,
`.sql` and `.md` file returns nothing but the tokenizer helpers of the arch
tests (SQL parsing, unrelated), one `link` service local variable named `stored`,
and the two paragraphs in
[storefront-speed-and-checkout](storefront-speed-and-checkout.md) that recorded
the absence in the first place.

**No table.** The payment module's migrations create five tables and no more:
`payment_collections`, `payment_sessions`, `payments`, `refunds`, and
`payment_manual_sessions`.

**No column.** Reading every column declaration across the module's three
up-migrations gives 22 distinct names:

```
amount authorized_amount captured_amount captured_at created_at currency_code
data decline_reason deleted_at external_id id idempotency_key metadata
payment_collection_id payment_id payment_session_id provider_id reason
reference refunded_amount status updated_at
```

Not one of them names a customer. (`deleted_at` is in the census because the
init migration declares it; migration 000003 drops it from four tables under
ADR 0054. It changes nothing here.)

**No symbol.** `core/provider` declares `provider.PaymentProvider` with five
money methods — CreateSession, Authorize, Capture, Refund, Cancel — plus the id
method it inherits from the base provider interface. Every one of them is
addressed by a session id. There is no customer parameter anywhere in the file,
no attach, no detach, no mandate, and no payment-method type.

So the absence is real and total. What the row got wrong is the cost of ending
it.

## 2. The correction: paying WITH a stored token already works, end to end

The row says *both halves* would widen `core/provider`. The consumption half
would not: it is published already, and it is wired from the storefront to the
provider.

`provider.CreateSessionInput` carries a free-form `Data` field, and its own
godoc names what belongs in it: *"provider-specific free-form data (a card
token, a return URL and so on)"*. That field is not a plan — it is reached from
the public storefront today.

The path, traced by hand:

| Step | What carries it |
|---|---|
| 1 | `POST /store/v1/carts/{id}/complete` request body, field `payment_data` |
| 2 | The cart module's store handler copies it into the checkout request |
| 3 | `CompleteCartInput.PaymentData`, documented as *"passed to the provider as is"* |
| 4 | The checkout plan (deliberately NOT persisted into the execution record) |
| 5 | The authorize step hands it to the payment module's interop surface |
| 6 | `CreateSessionInput.Data`, given to the provider unread |

Nothing on that path interprets, validates, or reshapes the object. A provider
whose upstream accepts a stored-card token can therefore be paid with one
**today**, with no contract change, no migration and no new route.

That is a correction to the ledger and not a quibble: a reader who took B9 at
its word would budget two published-surface changes and get one.

## 3. What is genuinely missing: minting, and owning

### 3a. No provider in this tree can mint a reusable credential

Two payment providers ship, plus the in-tree manual provider.

| Provider | Real integration | Credential it returns | Reusable |
|---|---|---|---|
| `plugins/paymentpaytr` | yes | an iframe token, under `DataKeyIframeToken` | **no** |
| `plugins/paymentstripe` | no — skeleton | none; every money method errors | n/a |
| the payment module's `manual` provider | n/a — operator-driven | none | n/a |

PayTR's own constant settles its half in its godoc: the iframe token *"is short
lived and is NOT stored anywhere by this plugin."* It is a key to one payment's
iframe, spent when that payment closes. The constant naming the endpoint that
returns it carries the same warning in a different form — it is named for what
the call **does** (open a payment) rather than for what comes back, precisely so
a reader does not mistake the token for a durable thing.

The Stripe plugin is explicit about being a skeleton, and deliberately so: its
package doc argues that a fake success would ship goods for money never taken.
All five money methods return the same `stripe_not_implemented` error.

So the vault interface would have **zero implementations** on the day it was
published.

### 3b. Nothing would own an instrument

An instrument belongs to a person. Section 1 shows the payment module has no
customer column, so the row would have no subject. The customer module has an
address book and a profile; it has nothing payment-shaped.

### 3c. And nothing charges when the shopper is absent

Every path that reaches `provider.CreateSession` starts at an HTTP request:

- two admin routes through the payment module's handlers;
- the checkout saga, through the payment module's interop surface, reached from
  the storefront route in the table above.

The payment module registers no job. The only payment-adjacent scheduled work in
the tree is the PayTR plugin's pending-payment watch, and its own file says what
it is: *"It reads and reports. It does not act."* — nothing there writes,
retries, cancels, refunds or completes anything, on ADR 0017's rule against
running side effects on a schedule nobody watched.

This matters for the trigger: **the case a stored instrument is actually
required for — a charge with nobody at the keyboard — has no path in this tree
at all today.** Saved cards, by contrast, are convenience: a shopper who is
present can retype a card.

## 4. Why the trigger is a PROVIDER and not a feature

The obvious trigger is "when subscriptions arrive". It is not usable, and the
reason is mechanical rather than stylistic:

- gaps.md row C12 (subscriptions) waits on **B9**.
- If B9 waited on subscriptions, each would be the other's precondition.

A trigger has to be a fact that can become true **without** the thing it gates.
A provider's capability qualifies: nobody adds a second acquirer, or finishes
the Stripe plugin, because B9 asked them to. They do it to take money, and the
vault capability arrives with the integration as a side effect.

It is also the fact that removes the guess. ADR 0042 rejected publishing an
installment quote type on this same package for this same reason — the shape of
the thing is exactly what the repository cannot know until a provider says it —
and the vault contract has the identical problem: whether the credential is a
token, a mandate id or a customer id at the provider, whether detaching is the
provider's call or gobit's, and what may be displayed, are answered by the
upstream and not by us.

## 5. The identity question, which looks like a second blocker and is not

A saved-card surface lists a person's cards, so it must know the person. gobit
issues no customer identity (ADR 0008, upheld by ADR 0043).

This does **not** block B9, and the reasoning is already in the tree. ADR 0057
let the cart and the b2b storefront keep serving an unproven claim for one
stated reason: those surfaces ship working, and withdrawing them from an
installation that did nothing wrong costs more than the leak. A surface that has
never shipped has nothing to withdraw, so it takes ADR 0043's row instead and
refuses with `identity_not_bound` when no verifier is bound — the same answer
the address book gives.

The mechanism is published and needs no widening: `corehttp.ProvenCustomer`
compares a claim against the bound identity, and
`TestNoStorefrontSurfaceActsOnACustomerItCannotProve` would pull a new
instrument route into its population automatically, because that gate takes its
population from the routes and the request types rather than from a list.

## 6. What was NOT measured

- **Whether any specific acquirer's API is a good fit.** No upstream
  documentation was read. The trigger is written so that whoever integrates one
  answers this by integrating it.
- **What a subscription module would need beyond an instrument.** A schedule, a
  dunning policy and a retry ladder are all outside B9, and C12 is not costed
  here.
- **PCI scope.** ADR 0064 fixes that gobit stores a reference and never a card
  number, which keeps the question where it is today, but no assessment was
  performed and none is claimed.
- **A residue for whoever builds it:** a merchant-initiated charge runs from a
  job, and Section 3c shows this repository has deliberately never let a job
  move money. ADR 0017 is a second record that will have to be read on that day,
  and it is not reopened here.

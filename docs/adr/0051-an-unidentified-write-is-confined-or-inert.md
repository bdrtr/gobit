# ADR 0051 — The storefront accepts a write from a party it cannot identify only when that write is CONFINED or INERT, and a GATE holds the rule rather than a paragraph

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A15 asks whether the storefront may accept content from a party it CANNOT
IDENTIFY, for this framework to act on later. The question arrived named after
one feature — the back-in-stock waitlist — and was widened on 2026-09-06 when it
turned out to describe a class rather than a table.

**The class is twenty-five endpoints.** Counted from the route registrations
rather than estimated: eleven storefront writes in `internal/modules/cart/api`,
seven in `internal/modules/customer/api`, two in `internal/modules/payment/api`
(opening a payment session on a collection, and cancelling one), one in
`internal/modules/order/api` (the return request), one in
`internal/modules/review/api` (the submission), and three in `plugins/webpush` —
twenty-two under `internal/` and three in a plugin. The storefront's one other
non-GET registration is not in the class: the catalog's GraphQL endpoint is a
POST, and the package doc on `internal/modules/product/graph` says the surface
is read only and has NO mutation, so nothing durable is written through it.
None of them knows who is writing. The storefront's only principal is a
publishable key naming a sales channel, and where a write names a subject it
takes the client's word for it: `storeCustomerID` in the customer module's API
package returns the path parameter and examines nothing.

**The repository has already answered one instance, and the answer is written in
the code.** The godoc above the order module's storefront handlers says the
customer side reads and ASKS: a return request is "a record that moves no stock
and no money until an operator receives it. Everything that acts is admin-only
and scoped." The tree keeps that promise — `order_returns` carries a status, an
amount, a reason, a note and timestamps and stores NO author at all, and the
three endpoints that act on a return (receive, refund, cancel) are registered on
the scoped admin router.

**That sentence has two clauses, and the preliminary answer compressed it to
one:** "does a HUMAN stand between the write and its effect?" The compression
was measured against the tree and it fails three ways.

**One — the rule condemns a shipped feature its own headline example depends
on.** The notification module subscribes the order-placed topic to its
`OrderPlaced` handler and that handler calls `Notify` with the order contact's
e-mail as the recipient. That address enters the tree as a client-supplied field
on an unauthenticated storefront write: the cart's creation body carries `Email`
beside `CustomerID`, and its update body carries `Email` as a POINTER so the
address can be changed after the cart exists. Unverified address, outbound
message, no human, no approval — which is verbatim the reasoning by which the
waitlist was said to fail.

**Two — a third shipped surface has the waitlist's exact shape.** The webpush
plugin mounts three storefront POSTs (subscribe, unsubscribe, unbind), and its
`onOrderPlaced` subscriber fans a push out automatically on a self-declared
customer id. The plugin says so itself: the godoc on `handleList` calls the
customer id a claim, "so a hostile claim binds an attacker's device to somebody
else's orders", and the remediation it names is an operator listing and deleting
rows — a human cleaning up AFTER the effect, not standing between. Checkout is
the third: `sagaSteps` in the checkout workflow returns five steps — reserve
inventory, create order, authorize payment, capture payment, clear cart — behind
`POST /store/v1/carts/{id}/complete`, which the cart module registers on the
bare router with no scope.

**Three — where it bites hardest it prescribes a remedy nobody will build.**
Applied honestly to the seven customer writes, the human rule says: put an
approver between an address edit and its effect. The repository has ALREADY
voted for the correct fix and it is [ADR 0043](0043-gobit-requires-an-identity-it-still-does-not-issue.md),
which binds an identity and makes `storeCustomerID` compare — and that record's
own list of things it does not do states that none of it is code yet. A rule
pointing away from the fix already decided is a rule that will collect
exceptions, and exceptions are how this repository's standing defect — a
document asserting what the code stopped doing — reproduces.

**The diagnosis is that "a human" is not the property.** It is how actuation
happens to be IMPLEMENTED in two modules. The property is inertness plus scoped
actuation, and substituting the agent for the property is what makes the rule
condemn checkout, condemn its own order mail, and misdirect the address-book fix.

**The second measurement is about how a rule is HELD, and it is decisive.** A15
recorded that the discriminator must be held by the TYPE or it is only a
promise, and offered the review module as proof. The write half is real:
`storeSubmitRequest` carries a rating, a title, a body and a byline and nothing
else; `SubmitInput` has no status; `Status: models.StatusSubmitted` is assigned
in Go inside the submit service; two tests hold the described surface —
`TestTheStorefrontIsNotOfferedAStatusParameter` and
`TestTheAdminListingOffersTheStatusFilter` both PASS today, and both read the
OpenAPI document rather than the request types; and the door itself is held over
HTTP by a third test, `TestTheStorefrontCannotPublishItsOwnReview` in
`internal/e2e/review_test.go`, which posts a submission body carrying a status
and requires 422 — REFUSED rather than ignored.

**The read half drifted, and it drifted inside the worked example.** Three
descriptions of one guarantee exist and they do not agree.

- `Filter.Status` in `internal/modules/review/models/models.go` says the
  storefront listing "sets it to 'approved' and never takes it from the
  request".
- `Handler.storeList` in `internal/modules/review/api/store.go` builds the
  filter from a limit, an offset and a cursor. The status is ABSENT — it is
  never set.
- `Repository.ListApproved` in `internal/modules/review/repository` passes the
  product id, the two cursor bounds, the row limit and the row offset into
  `ListApprovedReviewsParams` and DROPS the filter's status entirely.

The guarantee is genuine, and it lives where none of the three sentences says it
does: in the SQL, where `ListApprovedReviews` carries `status = 'approved'` as a
literal the caller cannot supply, and outside the module in two integration
tests that try the door — `TestAnUnapprovedReviewIsInvisibleOnTheStorefront` and
`TestTheStorefrontCannotPublishItsOwnReview`, the second of which asks the
storefront listing for each status by name and requires the unapproved review to
appear in none of them. So the BEHAVIOR held and the DESCRIPTION of it was wrong
at ship time, before any refactor, in the single module offered as evidence that
a type plus a comment is enough. What saved it was a literal and a test, which
is the distinction this record's second half is built on: a comment is not a
holding, and the module that was going to be cited for the comment proves it.

## Decision

**A storefront may accept content from a party it cannot identify only when the
write is CONFINED or INERT.**

- **CONFINED** — the effect completes inside the writer's own reach and touches
  nothing else. Nothing the client sends becomes money, and no subject the
  client names may be a party the writer does not already hold. The money half
  is stated here as a rule and it generalizes three decided instances rather
  than an existing tree-wide invariant: the shipping price is the server's
  ([ADR 0021](0021-the-server-decides-the-shipping-price.md)); the line price is
  the `LinePricing` flow's, and the godoc in `internal/modules/cart/api` says why
  the method is off the handler's surface at all — a handler bound to it would
  SILENTLY skip both the pricing and the line-count ceiling; and the storefront
  payment session covers the collection's remaining total because
  `Handler.createStoreSession` passes the service no amount at all.
- **INERT** — the write moves no stock and no money, and every endpoint that
  ACTS on it is admin-only and scoped.

**The shape neither limb covers is a STANDING AUTHORITY, and it is forbidden:** a
durable row this framework will act on LATER, on a trigger neither the writer
nor an operator chose, toward a destination nobody verified. Naming it is what
makes the waitlist verdict derivable without condemning the order confirmation
mail, which is judged under CONFINED instead.

**The rule is held MECHANICALLY — by a gate that fails when the property stops
being true — at BOTH boundaries.** The type is where the property is expressed;
it is not where the property is CHECKED, and the review module measured the
difference for us.

- At the **WRITE** boundary: by the request and input types, and by an audit of
  the described surface. No field the storefront must not set, and no parameter
  in the OpenAPI document that offers one.
- At the **READ** boundary: by the query parameters struct and the repository
  adapter. No parameter that could widen the predicate, and a test that fails
  when one appears.
- **Prose about either boundary is not the holding, and is not evidence that it
  holds.** A godoc saying a handler sets a status is worth exactly nothing; the
  one in `internal/modules/review/models/models.go` says it and the handler does
  not.

**The discriminator constrains the SCHEMA, not only the flow.** A column that
cannot mean what it will be read to mean is worse stored than absent. The review
module is the worked example and its refusals are the decision, not an omission:
no order id (it would narrow spam and authenticate nobody, and a verified-purchase
badge rendered from it would be a false statement made by the schema), no e-mail
address, no IP address. One identifying column remains — the byline the author
typed in order to have it printed — and the module's `PersonalData` godoc names
decision A15 as the reason all three others were refused.

**The back-in-stock waitlist FAILS this test as specified.** A waitlist row is a
standing authority in the pure form: it is not CONFINED, because its whole point
is an effect that leaves the writer — a message to an address; and it is not
INERT, because nothing admin-only actuates it. The actuation is an event
subscriber, and the human cannot be inserted without deleting the feature, since
the operator would be approving each outbound message one at a time.

**Two shipped surfaces fail this rule TODAY, and they are recorded as OPEN
DEFECTS rather than exemptions.** Writing them as exemptions would make this
record another document asserting what the code does not do, at the exact spot
it was written to prevent that.

1. **The cart's unverified `customer_id`**, accepted in the creation body and
   again on handover, breaks CONFINED for the storefront's most consequential
   write. It is not cosmetic: the order module's `SpendingPolicy` godoc says the
   rule is applied to the customer PLACING the order, inside the transaction
   that writes it, and the identifier reaches there straight from the cart's
   plan — so a declared customer id lands a B2B spending limit on somebody
   else's account. Closing rows: [ADR 0008](0008-musteri-kimligi-guven-siniri.md)
   and ADR 0043.
2. **`plugins/webpush` is a standing authority by construction**, and it
   additionally holds a push endpoint and a `customer_id` column while declaring
   no personal data at all: there is no occurrence of the personal-data
   vocabulary anywhere under `plugins/`, and `registeredModules` in
   `internal/app/personaldata_test.go` builds its registry from an empty
   `config.Config`, so no plugin module ever enters the walk that would notice.
   Closing rows: an A15 entry in `docs/gaps.md` and the third obligation of
   [ADR 0029](0029-the-embedder-is-the-data-controller.md).

**Two is a verdict over the whole class, and the closest call in it is the
payment module's pair.** `POST /store/v1/payment-collections/{id}/payment-sessions`
and `POST /store/v1/payment-sessions/{id}/cancel` are registered on the bare
router with no scope, and the effect is not small: the comment on the cancel
path's constant in `internal/modules/payment/api` says the reservation is held
at the COLLECTION level — an open session covers the collection's remaining
amount, and that is what stops a double charge — so opening one on a stranger's
collection would block their payment and cancelling one would release it. They
pass CONFINED on both clauses anyway. No amount is taken:
`Handler.createStoreSession` refuses the provider behavior keys the admin path
accepts and passes the service no amount at all. And the subject named is an
object rather than a party: what reaches a collection is its identifier, and
`newID` in `internal/modules/payment/models` builds one from a millisecond
timestamp and eighty bits of cryptographic randomness, so naming it is holding
it. That is the footing every storefront path identifier in this tree stands on,
including the cart's — which is why the cart is a defect above for the
`customer_id` in its BODY and not for the `{id}` in its path. The residue is
real and it is the same for all of them: an identifier that leaks is a reach
that leaks with it, and this decision judges the write, not the secrecy of the
identifier it names.

## Rejected alternatives

**Ratify the discriminator as `docs/gaps.md` wrote it — "does a human stand
between the write and its effect?"** It buys the shortest sentence available:
one question, answerable per feature by reading a router, and it reaches the
right verdict on both worked examples and on the waitlist. It is killed by three
shipped counterexamples and one absurd remedy. The order confirmation mail sends
to a client-supplied address with nobody approving it; the webpush plugin fans
out on a self-declared customer id and offers an operator only a cleanup after
the fact; checkout moves stock and money behind an unscoped storefront POST. And
on the surface where the rule would bite hardest — the seven customer writes —
it prescribes an approval queue for address edits, which nobody will build,
while ADR 0043 already names the fix. A verdict you cannot derive without
collateral damage is a verdict that will not survive its first contested
application.

**Ratify the amended discriminator and leave it held by types and prose, as the
review module holds it today.** It buys the whole decision for the price of a
document: no new audit, no new test, and a shipped module to point at. It is
killed by that very module. Its read-side guarantee is described in three places
and lives in two others — the godoc on `Filter.Status` claims the storefront
listing sets the status, `Handler.storeList` never sets it, `Repository.ListApproved`
drops it, and the safety rests on a SQL literal that two integration tests keep
honest by asking the listing for each status by name. That prose was wrong on
the day it shipped and nothing in the type or the comment noticed; what noticed
was a test that tried the door. So the half of this alternative that survives is
the mechanical half, which is the half it proposed to skip — and the
demonstration is not hypothetical, it is the example the rule was going to cite.

**Implement ADR 0043 instead, and treat A15 as a malformed question.** This is
the strongest alternative and it deserves its steelman: A15 asks what may be
accepted from a party the storefront "cannot identify", but that inability is
not a fact about the world — it is a choice this repository has already voted to
reverse and has not executed. Writing a principled rule for the unidentified
case does not merely postpone the identity; it REMOVES the pressure to build it,
by making the unidentified case feel tractable. And on the seven customer writes
it is not an alternative to the rule at all, it is the rule's precondition. It
is killed by span. ADR 0043 decides nothing for the return request, for the
review or for the waitlist, because those have no customer to prove: the review
module deliberately declares a byline and implements no eraser, on the argument
that two shoppers share a display name as easily as two people share a name, so
erasing on that basis would erase strangers ([ADR 0033](0033-erasure-is-a-sweep-that-returns-an-answer.md)).
And ADR 0043's own non-goals leave the cart untouched, so ADR 0008's measurement
— a client purchasing in somebody else's name — reproduces unchanged after it
ships. It closes one surface; the class is twenty-five writes.

**Answer it per feature, which is what the gap row proposed.** It buys the
lightest process: no record, no gate, and each feature gets the reading that
fits it. It is killed by the correction A15 itself carries. The question was
named after the waitlist, and a second feature — the review module — was about
to rediscover the same blocker from scratch; the rule the gaps table drew from
that is that a decision named after the feature that found it will be read as
belonging to that feature, and the next round pays for the question twice.
Answering per feature is not an alternative to this record, it is the state the
record was filed against.

**Keep the human rule and build the waitlist behind an approval queue, so the
feature satisfies it.** It buys consistency at the price of one screen, and it
would let C1 proceed under the sentence already written. It is killed by what
the approval would BE. A review's approval step is the feature — moderation is
the product, and the four transition edges are argued in the module. A
waitlist's approval step is an operator clicking "send" on each back-in-stock
message one at a time, for every shopper and every restock. The human cannot be
inserted there without deleting the point of the feature, which is that nobody
has to remember.

## Consequences

**Positive**

- **The order module's argument is stated in full for the first time, and it now
  governs a class rather than one handler.** Both clauses are kept, so a reader
  cannot apply half of it.
- **Checkout and the order confirmation mail are JUDGED rather than condemned.**
  They fall under CONFINED, and where checkout fails it fails for a nameable
  reason — the client declares whose account the effect lands on — which points
  at ADR 0043 rather than at an approval queue nobody would build.
- **The webpush plugin is named rather than silently exempt.** Its three
  storefront POSTs and its automatic fan-out are the forbidden shape, and they
  are on the record with the rows that close them.
- **The waitlist verdict is unchanged and is now derivable without collateral
  damage.** It fails as a standing authority, and the reasoning does not also
  condemn a mandatory shipped feature.
- **The schema clause has a worked example with numbers on it.** Three columns
  refused, one identifying column kept, and the module's own declaration names
  A15 as the reason — so the refusals read as a decision instead of an
  oversight.

**Negative, and accepted**

- ~~**The gate this record requires does not exist, and this record does not fund
  it.**~~ **Built 2026-09-08; the paragraph is kept struck because the estimate
  in it is the interesting part.** "Is there an admin transition route?" is
  answerable from the router alone; "does this write create a durable row a later
  event will act on?" needs a route-to-table cross-reference that nothing in this
  tree performs. The two halves that DO exist are worth naming, because they are
  what makes the third plausible rather than speculative:
  `internal/arch/consumers_test.go` pairs every published topic with a subscriber
  and every subscription with a publisher, and both of its exemption maps —
  `subscriberlessPublications` and `publisherlessSubscriptions` — are empty as a
  matter of policy, with both tests green; `internal/arch/module_sql_test.go`
  already derives which tables a module's SQL may name; and `adminRoutes` in
  `internal/e2e/authorization_test.go` walks the entire router tree and filters
  on a path prefix, which proves a whole-prefix walker is buildable here. The
  JOIN between them is new work. ~~**Until it exists, the discriminator is held
  by review and argument — the weaker form this record's second half exists to
  forbid.**~~ That is the honest price of preferring an accurate rule to a
  checkable one, and this record pays it deliberately rather than by choosing
  the crude rule. **What the join actually needed was one hop this paragraph did
  not anticipate.** The schema replay and the route walk were both reusable as
  written, but a handler does not reach its own table: the cart opens, prices and
  completes through narrow interfaces its api package declares and the
  composition root binds, so a walk that stopped at the module boundary resolved
  four of the cart's eleven storefront writes to no table at all. Following the
  call graph into `internal/workflows` as well is what closed it, and the guard
  that a storefront write MUST resolve to a table is what says so out loud.
- **Two shipped surfaces are non-compliant on the day this is accepted**, and
  the record fixes neither. The cart's `customer_id` stays an unverified claim
  on creation and on handover; webpush stays a standing authority holding a
  device endpoint and a customer id it declares nowhere.
- **The read boundary of the worked example is held by BEHAVIOR rather than by
  the type, and the PROSE describing it is the part that is loose.** Two tests
  in `internal/e2e/review_test.go` hold the boundary itself:
  `TestAnUnapprovedReviewIsInvisibleOnTheStorefront` submits over HTTP and then
  asserts the real storefront listing and the summary both stay empty while the
  admin surface shows the row, and `TestTheStorefrontCannotPublishItsOwnReview`
  asks that same listing for each of the three statuses in turn and asserts the
  unapproved review appears in none of them. A `Repository.ListApproved` that
  began honoring the filter's status turns one of them red either way it goes —
  an empty status that NARROWS empties the approved page, one that WIDENS puts
  the unapproved review on it. What no test reads is the comment: the godoc on
  `Filter.Status` goes on describing a storefront listing that sets the status
  and `Handler.storeList` still does not set it, and a wrong sentence about a
  guarantee costs the next reader the same hour it cost this one. The two things
  this leaves unheld are named rather than fixed here: that godoc, and the fact
  that both holding tests carry a build tag, so the plain `go test ./...` a
  contributor runs does not execute either of them — the Integration job in
  `.github/workflows/ci.yml` does.
- **CONFINED is the harder limb to judge, and every borderline case lands on
  it.** INERT can be read off a route table — the actuation is admin-only and
  scoped, or it is not. CONFINED asks how far an effect travels, and the answer
  depends on whether a client-named subject is really the writer's. Today, for
  the cart, it is not; so the storefront's most important write is judged
  non-compliant by this record's own rule, and stays that way until ADR 0043 is
  code.
- **A third verdict now exists where there were two.** A feature is compliant,
  or it is a recorded defect with a closing row — and a defect with a closing
  row is a promise this repository has to keep, which is a heavier obligation
  than an exemption list would have been.

## What this deliberately does NOT do

- ~~**It does not build the gate.**~~ **Built 2026-09-08, in
  `internal/arch/storefront_boundary_test.go`.** Both boundaries are now checked
  against the routes the router actually registers: no storefront write decodes a
  request field the operator controls, and no storefront read offers one as a
  query parameter. The population is resolved from PATHS rather than from names,
  and that mattered — a scan for handlers called store-something misses the
  payment module's two, and a scan for types named storeSomethingRequest finds
  exactly the two worked examples this record already cites, so either would have
  passed by looking in the wrong place. Twenty-two storefront write routes and
  the read routes beside them are in scope, and both gates refuse to run on an
  empty scan. Mutation-proved four ways: a status field added to the review
  submission, a status parameter added to the review listing, and each of the two
  blindness guards.

  ~~What it still does NOT do is the route-to-table audit — nothing here reads
  the SCHEMA, so the third part of this decision, that the discriminator
  constrains the columns a table may carry, remains held by review rather than by
  a gate.~~ **The third part was built on 2026-09-08 as well, in
  `internal/arch/storefront_schema_test.go`, and the schema is now read.** Every
  storefront write route **under `internal/modules`** — the twenty-two of the
  twenty-five above; `plugins/webpush` registers its three under a chi route
  PREFIX the scan does not follow, and they stay outside on the open-defect row
  below — is resolved to the tables it can store into: the module it was
  registered in, then the call graph from its handler through
  `internal/workflows` to the SQL, then an intersection with the tables that
  module's own migrations create. Every column of every reached table is then
  read for the four claims this decision refuses in its worked example: a party,
  a contact, a network origin and a prior record of the shop. **What the
  intersection leaves outside is worth naming rather than discovering**, because
  it is this record's own boundary showing up as a blind spot: a write another
  MODULE performs on a route's behalf is not the route's, so the checkout saga's
  own stored claims are unaudited — `orders.customer_id` and `orders.email` were
  measured UNREACHED on 2026-09-08, and they are where the cart's declared
  `customer_id` actually lands. A claim with no verdict fails the gate, a verdict
  for a claim that has gone fails it the same way, and a party column the
  storefront BODY carries may not be recorded under EITHER limb — not CONFINED,
  because that limb says in as many words that no subject the client names may be
  a party the writer does not already hold, and not INERT, because both limbs
  judge the WRITE while the clause at issue judges the COLUMN, which this record
  settles against a client-declared party by refusing an order id on the
  `reviews` table whose write IS inert. Eight claim columns are reachable today
  and each carries a verdict: seven pass a limb, and the cart's `customer_id` is
  the one recorded as an OPEN DEFECT, with the closing rows this record already
  named. The review module's three refusals are held where they live, which is in
  their ABSENCE, so the gate is proved on a planted `reviews` table that has all
  three back. Mutation-proved three times besides: an `ip_address` column added
  to the reviews migration, which the gate reports as an unjudged NETWORK ORIGIN
  naming the route that writes it; the workflows tree taken back out of the walk,
  which the route-reaches-a-table guard reports as the four cart routes it
  blinds; and the cart's row rewritten from OPEN DEFECT to INERT, which stayed
  GREEN on 2026-09-08 until the refusal was widened from CONFINED alone to both
  limbs — the record's own defect class landing inside the gate written to end
  it.
- **It does not forbid the waitlist forever.** What fails here is the waitlist as
  specified — an unverified address turned into a message by a subscriber. A
  design where the destination is verified, or where the actuation is something
  an operator chose, is a DIFFERENT write and gets judged again against the same
  two limbs.
- **It does not implement ADR 0043 and does not touch `storeCustomerID`.** The
  identity stays decided-and-unbuilt, and this record adds a second reason to
  build it rather than a substitute for it.
- **It does not exempt anything.** The two non-compliant surfaces are defects
  with closing rows; nothing in this record grants a permanent pass, and a
  future exemption would be a change to this decision rather than a note under
  it.
- **It does not dispose of the controller obligations.** ADR 0029 leaves the
  lawful basis, the retention window and the consent text with the embedder, and
  a write that passes this test can still hold personal data that has to be
  declared. This decision narrows what those obligations must reach; it does not
  discharge them.
- **It does not change the notification module.** Storing no recipient address
  remains that module's own decision, and the order confirmation mail keeps
  working exactly as it does today.
- **It does not name the twenty-five endpoints one by one.** A hand-kept
  verdict table is the thing this record's second half refuses; the count is a
  measurement of the class, not a list to maintain.

## Related

- [ADR 0008](0008-musteri-kimligi-guven-siniri.md) — the customer-identity trust
  boundary this decision inherits, and the source of the cart's open row.
- [ADR 0043](0043-gobit-requires-an-identity-it-still-does-not-issue.md) — the
  identity that would make CONFINED hold for the cart, decided and not yet
  written.
- [ADR 0029](0029-the-embedder-is-the-data-controller.md) — the declaration
  obligation the webpush plugin does not meet, and the reason the schema clause
  matters beyond correctness.
- [ADR 0021](0021-the-server-decides-the-shipping-price.md) — ONE price: the
  storefront names which shipping option and the server decides what it costs,
  after a client-supplied `amount` shipped and was exploitable. It is the
  precedent the money clause of CONFINED generalizes, not a tree-wide invariant
  it already established.
- [ADR 0018](0018-web-push-is-a-device-registry-not-a-channel.md) — why the push
  destination had to be STORED, which is what makes the plugin a standing
  authority rather than a provider.
- [ADR 0033](0033-erasure-is-a-sweep-that-returns-an-answer.md) — the split
  between declaring and erasing, which is why an unidentifiable author is a
  designed outcome rather than a gap.
- [ADR 0001](0001-modul-arasi-iletisim.md) — cross-module access is a narrow
  interface the CONSUMER defines in its own package; the owner publishing an
  interface is the option that record rejected. It is the boundary the INERT
  limb is read against: the review module publishes no interop surface and no
  read-layer provider, so the only thing that acts on a review is the module's
  own scoped admin route.

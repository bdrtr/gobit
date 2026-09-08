# ADR 0049 — ONE group decides the price, and the MERCHANT ranks the groups

**Summary:** ONE group decides the price, and the MERCHANT ranks the groups.
The cart sends the highest-ranked group the customer belongs to as a single
value, into contexts it already builds.

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A5 asks what the cart should tell the rule engines about a customer's
groups: NOTHING, exactly ONE chosen by a precedence somebody writes, or ALL of
them — and if all, what breaks a tie between two group prices that both match.

The schema allows the ambiguity and the cart does not resolve it.
`customer_group_customer` in
`internal/modules/customer/migrations/000001_customer_init.up.sql` is keyed
`(customer_id, customer_group_id)`, so several groups per customer is what the
storage already permits. What the cart writes into a rule context, measured at
the three sites that build one — `internal/workflows/cart/totals.go`,
`internal/workflows/cart/add_line_item.go` and
`internal/workflows/cart/discount.go` — is ONE attribute, `region_id`, plus a
variant id on each line. No group reaches pricing, promotion or fulfillment from
a storefront cart today.

**The first answer written for this question was candidate (3) — send the whole
set and let pricing's existing ladder decide unchanged, on the grounds that it
adds no tie-break mechanism and leaves the choice with the merchant, who ranks
price lists. An opposition pass defeated it, and the reasoning is this
decision's best argument, so it is recorded rather than summarized away.**

### The merchant's lever is EXHAUSTED at two ranks

`internal/modules/pricing/migrations/000001_pricing_init.up.sql` constrains the
price list with `price_list_type_check CHECK (type IN ('sale', 'override'))`.
There is no rank, position or priority column on that table. The `Priority`
method in `internal/modules/pricing/models/models.go` turns those two values into
the ladder's first rung: override 2, sale 1, and a price with no list 0.

`better` in `internal/modules/pricing/service/calculate.go` compares five things
in order, and the first DIFFERENCE decides: list tier (larger wins), matched rule
count (more wins), quantity span (narrower wins), amount (smaller wins), and
finally the price id.

Now the configuration A5 was written for. A customer belongs to `vip` and to
`wholesale`. Price A sits on an override list with one rule
(`customer_group_id`, `in`, {vip}), quantity one to unbounded, 9000 minor units.
Price B sits on a DIFFERENT override list with one rule (`customer_group_id`,
`in`, {wholesale}), quantity one to unbounded, 8000. Under "send the set" both
survive elimination. Rung one: tier 2 against tier 2. Rung two: one rule against
one rule. Rung three: both unbounded, so `quantitySpan` returns the maximum for
both. Rung four compares AMOUNT, and 8000 wins.

**So "send the set, ladder unchanged" IS cheapest-wins in exactly the
configuration the question is about.** The merchant cannot say "wholesale beats
vip": both contract prices are `override`, there is no third rank, and demoting
one to `sale` changes its meaning in the wrong direction. `PriceListSale` in
`internal/modules/pricing/models/models.go` is documented as the CAMPAIGN
(discount) list, so the demoted contract price drops to tier 1 and loses to
every unrelated OVERRIDE price; and against the campaign prices it now stands
beside, it wins nothing either, because rung one ties and the decision falls to
the rungs below.
The preliminary decision refused candidate (4) as taking the choice away
from the merchant and then delivered candidate (4)'s outcome.

### It inverts the ladder's own stated philosophy

The godoc over the calculation in
`internal/modules/pricing/service/calculate.go` states rung one as a position: a
contract or B2B price overrides a campaign, and a campaign overrides the base
price. A higher-ranked SOURCE wins even when a lower-ranked source is cheaper.
Rung four is the opposite move and the godoc marks it as such — at EQUAL
specificity, decide for the customer. Letting two equal-tier group prices fall
through to rung four does not apply the ladder's philosophy one level down; it
suspends it precisely where rung one runs out of ranks.

### "No new mechanism" is the claim that does not survive

There is nowhere to put a set and no operator that reads one.

- `matchRule` exists in THREE independent copies — in
  `internal/modules/pricing/service/calculate.go`,
  `internal/modules/promotion/service/rule.go` and
  `internal/modules/fulfillment/service/eligibility.go`. Nothing under `core`
  shares them.
- All three take `map[string]string`. Each reads ONE value for the rule's
  attribute, returns no match when the attribute is absent, and for the `in`
  operator asks whether the RULE's value list contains that single value. "Any of
  my groups" is not expressible.
- The pricing HTTP surface refuses to carry a set. `calculateQuery` in
  `internal/modules/pricing/api/api.go` rejects a query parameter given twice,
  and its godoc states the invariant in words: the rule context is a mapping too,
  and a field cannot have two values.
- The published cross-module examples both show a single value.
  `internal/modules/promotion/service/interop.go` documents its request schema
  with `"context": {"region_id": "reg_1", "customer_group_id": "vip"}`, and the
  eligibility input in `internal/modules/fulfillment/service/eligibility.go`
  gives `{"customer_group_id": "vip"}` as its example attribute map.
- The pricing migration's own canonical rule puts the SET on the RULE:
  `("customer_group_id", "in", {"vip","b2b"})`.

**The set was always meant to live on the rule and the single value in the
context.** Sending a set inverts the direction the storage was designed around,
and it does so in three unshared matchers, against a written HTTP invariant and
two published schema examples.

### The repository already wrote down what the blocker is

The cart workflow's package comment in `internal/workflows/cart/doc.go` names it:
pricing's rule context carries a single value per attribute, for a customer in
more than one group it is ambiguous WHICH group would be written, and "silently
picking one would tie the price to map iteration order". The recorded obstacle is
the absence of a DETERMINISTIC choice, not the inability to ship a set.

## Decision

**One group decides, and the merchant ranks the GROUPS. The cart sends the
highest-ranked group the customer belongs to, as a single `customer_group_id`
value, into the contexts it already builds.**

The precedence is O(groups), not O(customers): a shop creates a handful of groups
in its lifetime and ranks them once.

**The shape, in five parts.**

1. **A `rank` integer column on `customer_group`**, in a second migration for the
   customer module — `internal/modules/customer/migrations/` holds ONE migration
   today, `000001_customer_init.up.sql` and its down pair, and the arch test that
   requires that pair makes the second migration two files as well. `NOT NULL
   DEFAULT 0`, and the SMALLER value wins, which is the direction both precedents
   already use.
2. **`rank` becomes a field on the existing write DTOs and the existing
   routes.** `GroupInput` and `UpdateGroupInput` in
   `internal/modules/customer/service/group.go` carry `Name` and `Metadata`
   today; `Handler.Routes` in `internal/modules/customer/api/api.go` already
   registers `POST /admin/v1/customer-groups` and
   `PUT /admin/v1/customer-groups/{id}` on the write-scoped router, their
   handlers `adminCreateGroup` and `adminUpdateGroup` in
   `internal/modules/customer/api/admin.go` already build those two inputs, and
   both routes are already described in
   `internal/modules/customer/api/describe.go`. This is a field on two DTOs and
   two endpoints, **not a new admin surface**.
3. **No new cross-module method.** `CustomerGroupIDs` in
   `internal/modules/customer/service/interop.go` keeps its exact signature. That
   file states why it must: every method on the cross-module surface is a
   contract customer can never change again, because a mismatch is caught at
   container resolution rather than at compile time (ADR 0001). What changes is
   the ORDER, which becomes contractual — rank, then id, so the HEAD of the slice
   is the winner. The order is not currently arbitrary and this record does not
   pretend it is: `ListGroupsOfCustomer` in
   `internal/modules/customer/queries/customer_group.sql` orders by
   `created_at DESC, id DESC`. What is unstated is the SURFACE's promise, and
   restating it is safe because there is no production consumer to break —
   searching `CustomerGroupIDs` across the Go sources returns six lines, all of
   them inside customer's own interop file and its group test. The only other
   mentions anywhere in the tree are in this record.
4. **The cart reads it and writes one value.** The `Customers` surface in
   `internal/workflows/cart/deps.go` publishes a single method today
   (`CustomerEmail`); it gains this one, and the three attribute sites write
   `customer_group_id` from the head of the returned slice.
5. **A guest, or a customer in no group, OMITS the attribute.** `Snapshot` in
   `internal/workflows/cart/snapshot.go` carries an empty customer id for a
   guest, and the elimination rule already handles a missing attribute: a rule
   whose attribute is not in the context does not match, so the price is
   eliminated. The default direction is that a segment price stays CLOSED rather
   than opening to everybody.

**pricing, promotion and fulfillment need no change at all** — no operator, no
matcher, no schema, no HTTP contract, no migration. The ladder's rungs stay
pinned by the tests that already pin them:
`TestSelectPrefersOverrideOverSale`, `TestSelectPrefersListOverBase`,
`TestSelectPrefersMoreSpecificRules`, `TestSelectPrefersNarrowerQuantityRange`,
`TestSelectPrefersLowerAmount` and `TestSelectIsDeterministicOnFullTie`.

**Why this is better rather than merely cheaper.**

- **It DISSOLVES the tie A5 actually asks about, instead of answering it.** A5's
  configuration is one customer in `vip` AND in `wholesale` with a price ruled on
  each. With one group in the context, `matchRule` in
  `internal/modules/pricing/service/calculate.go` eliminates every price whose
  rule names the OTHER group, so two prices ruled on DIFFERENT groups can never
  reach rung four together, and the ladder is never asked to rank two segments it
  has no rank for. This is narrower than "no two group prices can tie": two
  prices carrying the SAME group rule still meet at rung four, and that residue
  is recorded below rather than argued away.
- **It puts the answer in the merchant's hands, which was the stated goal.** The
  price-list tier cannot be that lever, because `type` has exactly two values.
- **It makes `in` do its job.** A merchant who wants ONE price available to two
  segments writes ONE rule — the pricing migration's own canonical example — and
  it matches whichever single group is in the context.
- **It is this repository's established answer to "several candidates, the
  merchant decides".** ADR 0010 settled it for warehouses in its title: coverage
  is a constraint, preference is an ORDER. `internal/modules/product/queries`
  reads FIVE tables rank-first, over the five `rank integer NOT NULL DEFAULT 0`
  columns in `internal/modules/product/migrations/000001_product_init.up.sql` —
  product_category, product_variant, product_option, product_option_value and
  product_image; and
  `internal/modules/fulfillment/migrations/000002_shipping_locations.up.sql`
  carries `priority BIGINT NOT NULL DEFAULT 0` under a heading that says "priority:
  THE SMALLER ONE WINS, negative IS ALLOWED" and justifies it with the operator
  who "wants to put one of three warehouses first" and "must be able to write a
  single row". Reaching rung four instead would be the departure.
- **It makes a published, frozen, unused surface LIVE**, and repairs the text
  around it by making it true rather than by shrinking the claim.
  `internal/modules/customer/service/interop.go` says the customer's segments are
  placed into the price context while a cart total is computed, and that sentence
  is FALSE today. One text names this exact trigger and it is
  `internal/workflows/cart/discount.go`: the group context is added there the day
  the customer surface publishes the group list — a surface that already exists.
  The package comment in `internal/workflows/cart/doc.go` makes no such promise;
  it records the gap and hands the selection rule to pricing, so it is a text
  this decision OBSOLETES rather than fulfills, and it has to be rewritten with
  the change.

## Rejected alternatives

**(1) Send nothing — today's behaviour, made explicit, with group pricing left as
an operator-side feature.** It buys the most: zero migrations, zero risk, and it
is already true. Group prices remain reachable through the admin price
calculation endpoint, which builds an arbitrary attribute map from
`attr_`-prefixed query parameters, so a merchant can still verify one by hand. It
is killed by what it makes permanent. `CustomerGroupIDs` is on a surface that can
never change and has no caller; customer's interop godoc stays false and the
promise in `internal/workflows/cart/discount.go` stays unkept, and a reader of
that godoc will keep believing the cart prices with segments. B2B
group pricing is the reason `customer_group_customer` exists, and answering A5
with "nothing" spends the storage, the surface and the admin endpoints on a
feature no storefront can reach.

**(2) in its weak form — a precedence stored on the CUSTOMER.** This decision
ADOPTS (2) and moves its subject, so the argument is against the placement rather
than the idea. A per-customer precedence buys expressiveness: one shopper can be
priced as wholesale and another in the same two groups as vip. It is killed by
maintenance arithmetic. It is O(customers) — every membership write has to keep
an ordering that no merchant can state once, and there is no screen on which a
merchant would ever say it. On the group it is O(groups), a handful of rows, set
once, by the same person who created the groups.

**(3) Send the SET and let the existing ladder decide unchanged.** It buys the
most honest input — a customer really IS in several groups — with no new merchant
concept and no migration anywhere. It is killed three times over, and each is
enough. It is cheapest-wins under another name, because two override-list group
prices with one rule each and the same quantity band tie at rungs one, two and
three and are decided at rung four by amount. There is no operator that reads a
set: three unshared copies of `matchRule` each compare ONE context value, and
adding an any-of branch means editing all three or letting them diverge. And the
transport refuses it — `calculateQuery` rejects a repeated parameter with the
invariant written into its godoc, and two published cross-module schema examples
show the group as a single value. "No new mechanism" was the argument for (3),
and it is the claim that does not survive.

**(4) The set plus a cheapest-wins rule inside the ladder.** It buys honesty over
(3): it names what (3) does silently, and a merchant reading the documentation
would at least know the outcome. It is killed by being either nothing or a
regression. Rung four ALREADY is amount ascending, so adding the rule where it
sits changes no behaviour; moving amount ABOVE tier or span is the only version
that is a change, and it inverts the ladder's stated position — a contract price
overriding a campaign — and breaks the wholesale band, because rung three is what
makes a 10-to-20 tier beat a one-to-unbounded price. It also still needs every
mechanism (3) needs.

**(5) The set, with the merchant ranking PRICE LISTS rather than groups.**
`docs/gaps.md` records this as "the ladder's existing shape, so it costs the
least new mechanism", and that is the claim the schema refutes. The shape is a
two-value CHECK constraint, not a ranking: `price_list` carries no rank column
and `type` admits only `sale` and `override`. So (5) needs a rank column on
`price_list`, the tier comparison in `better` rewritten to read it, AND every
matcher, HTTP and schema change (3) needs. It is (3) plus a pricing migration —
strictly more expensive than the option it claims to undercut, and it puts the
merchant's lever on the wrong noun besides: a price list is where a price lives,
a group is who the customer is.

## Consequences

**Positive**

- **The tie between two SEGMENTS is dissolved rather than adjudicated.** Prices
  ruled on different groups can no longer both match, so the ladder never has to
  rank `vip` against `wholesale` — a comparison it has no rung for.
- **Three modules are untouched.** pricing, promotion and fulfillment get no
  operator, no matcher change, no migration and no HTTP change, and the ladder's
  FIVE rungs stay pinned by the six tests named above.
- **The merchant's lever is a noun the merchant already manages.** Groups are
  created and named through admin endpoints that exist; the rank rides on the
  same two DTOs and the same two routes.
- **A published surface that could never be changed gets its consumer**, and its
  order stated, before anything outside customer depends on the old one.
- **`in` recovers its designed job.** One price for two segments is one rule with
  two values, which is the example the pricing schema documents itself with.
- **The precedent is consistent.** A merchant-set order over candidates is what
  product does with `rank` and what fulfillment does with `priority`, and ADR
  0010 already argued the general case.

**Negative, and accepted**

- **Two prices ruled on the SAME group still meet at rung four, so cheapest-wins
  is NARROWED and not removed.** `matchRule` in
  `internal/modules/pricing/service/calculate.go` evaluates each price's rules
  against the context independently; one group in the context eliminates a price
  ruled on a different group, and eliminates nothing whose rule names the group
  the cart sent. Two prices on two different override lists, each carrying
  (`customer_group_id`, `in`, {vip}) with the same quantity band, therefore both
  survive, tie at rungs one, two and three, and are decided at rung four by
  AMOUNT — the same outcome this record condemns in candidate (3). The schema
  does not forbid the pair either: there is no UNIQUE constraint anywhere in
  `internal/modules/pricing/migrations/000001_pricing_init.up.sql`, on
  `price_rule` or on any other table. What this decision buys is the dissolution
  of the tie between DIFFERENT groups, which is the tie A5 asks about and the one
  no rung can rank; the same-group duplicate is a merchant who wrote two contract
  prices for one segment, and the ladder answers it deterministically —
  cheapest, then by price id — rather than not at all.
- **The premise that a merchant CAN order their groups was not measurable, and
  the whole decision turns on it.** If a shop's segments are genuinely
  incomparable — a loyalty tier beside an industry vertical — a single global
  order is a lie and the set would be the honest input. Nothing in the tree
  settles it either way: the b2b module's single migration creates only
  `b2b_company` and `b2b_company_employee`, so there is no group linkage to
  appeal to, and there is no consumer count. This one rests on judgement.
- **A second published surface carries the same list and this decision does not
  order it.** The Query layer's customer provider in
  `internal/modules/customer/service/provider.go` publishes a `group_ids` field,
  fed by `ListGroupIDsOfCustomers`, which orders by `(customer_id,
  customer_group_id)` — a DIFFERENT order from the one
  `ListGroupsOfCustomer` uses. The two already disagree; after this decision one
  of them means "the winner is first" and the other still does not, with nothing
  in the tree comparing them.
- **A shop that never touches the rank gets the lexicographic accident back.**
  Every group defaults to 0, so the order falls to the id — deterministic, and
  arbitrary in the merchant's eyes. That is the very outcome ADR 0010 was written
  to remove from warehouse selection, and the answer here is the same as there: a
  default that is stable and a single row the operator can write to change it.
- **The erasure declaration has to grow by one entry, and NOTHING forces it.**
  `notPersonalColumns` in `internal/modules/customer/erasure_test.go` lists
  `customer_group` as `id`, `name` and the three timestamps, and the test around
  it does prove the declaration exhaustive — but only against ONE file. The
  constant `migrationFile` in that test names `000001_customer_init.up.sql` and
  calls it the module's only migration, and `tablesOf` and `columnsOf` parse
  `CREATE TABLE` blocks out of that single string. Part 1 above puts the rank in
  a SECOND migration, where it arrives as an `ALTER TABLE`, and neither the
  parser nor the file list ever sees it: the audit stays green and the column CAN
  appear silently. Nothing else covers it — `internal/arch/arch_test.go` audits
  migration naming and the up/down pair, not personal-column exhaustiveness. An
  integer rank on a merchant-configuration table is plainly not personal data, so
  the entry is one string; it just has to be written by hand. Widening that test
  to every migration the module ships is the real repair and it is not made here.
- **A group price applies in the cart and is never SHOWN before it.**
  `listablePrices` in `internal/modules/pricing/service/provider.go` drops every
  ruled price from both storefront reads, and its godoc gives the reason: a rule
  looks at a context the provider does not carry, and ignoring a condition that
  cannot be evaluated would open the segment price to everyone. So a B2B
  customer sees the public price on the product page and their contract price at
  the cart. This decision does not change that, and ADR 0041 decided the same
  way for the price filter.
- **Fulfillment stays blind.** `quoteRequest` in
  `internal/workflows/cart/shipping.go` carries region, currency, country,
  subtotal, item count and total weight — and no attributes field at all — while
  fulfillment's own eligibility input has an `Attributes` map documented with
  `customer_group_id` as its example. A group-conditioned shipping rule still
  cannot fire from a cart. Only the price and discount contexts gain the group.
- **The ordering contract is held by a godoc sentence and an `ORDER BY`, and
  nothing fails if one of them changes.** A rewritten query would silently
  reorder the slice and silently change which price a B2B customer pays — a
  failure with no error, which is the class this repository keeps getting bitten
  by.

## What this deliberately does NOT do

- ~~**It does not carry the change.**~~ **BUILT 2026-09-08, all five parts.**
  `000002_a_group_carries_a_rank` adds the column; `rank` is a field on the two
  existing DTOs and the two existing routes; `ListGroupsOfCustomer` orders by
  rank then id and `CustomerGroupIDs` now PROMISES that order; and the cart
  resolves the head once, in `Workflows.ruleContext`, which all three attribute
  sites call. Verified against a real database: three groups ranked 1, 1 and 5
  come back as the two rank-1 groups in id order and then the rank-5 one.
  Mutation-proved three ways — taking the last group instead of the head, sending
  an empty attribute for a guest, and failing the cart when the group read fails.
  The container caught the fourth: adding the method to the surface made
  `checkout`'s own stub stop satisfying it, and the resolution error named the
  missing method, which is ADR 0001's mechanism doing exactly what that record
  says it does.

  The pieces below are what the original bullet said would not be carried, and
  they are listed so the record reads as history rather than as a plan:
  no migration was written here, no DTO field
  added, no cart site edited. This record fixes the shape and the reasoning; the
  work is a separate commit that has to add the `notPersonalColumns` entry BY
  HAND — the exhaustiveness test will not ask for it, for the reason recorded
  above — and update the three cart test assertions the new attribute touches.
- **It does not add an any-of operator to the three matchers**, and it does not
  license one later. A rule context stays `map[string]string` in all three
  modules, and the day something genuinely needs a set in a context, that is its
  own decision with its own record.
- **It does not make the group price visible on the product page.** That needs a
  per-viewer price on a read that has no cart and, by the provider's own godoc,
  no context — a different question, and no candidate in A5 answered it.
- **It does not give the shipping quote an attributes field.** Fulfillment's
  matcher would already read one; the cart's request schema has nowhere to put
  it, and widening that schema is a fulfillment-facing decision.
- **It does not touch the ladder.** The tier, the rule count, the span, the
  amount and the id keep their order and their meanings; this decision changes
  what is in the CONTEXT, not how candidates are ranked.
- **It does not decide what a shop with incomparable segments gets.** That is the
  premise named above as unmeasured, and evidence that such a deployment exists
  is what would reopen this record — not a new preference about sets.
- **It does not make the rank mandatory.** A merchant who never sets one gets the
  default and a stable answer, in the same way a warehouse with no policy row
  sits at priority 0.

## Related

- [ADR 0010](0010-depo-secim-politikasi.md) — coverage is a constraint,
  preference is an ORDER: the same answer given for warehouses, and the source of
  the priority column this rank copies.
- [ADR 0001](0001-modul-arasi-iletisim.md) — the cross-module surface that
  freezes `CustomerGroupIDs` and leaves only its order refinable.
- [ADR 0004](0004-query-veri-erisimi.md) — the read layer whose customer provider
  publishes the same group list under a different order.
- [ADR 0006](0006-workflow-modul-erisimi.md) — why the cart repeats the customer
  surface instead of importing the module, which is what makes adding a method to
  it a two-place edit.
- [ADR 0041](0041-a-price-filter-compares-the-base-price-at-quantity-one.md) —
  the storefront filter that answers the same for everybody, for the same reason
  the provider drops ruled prices.

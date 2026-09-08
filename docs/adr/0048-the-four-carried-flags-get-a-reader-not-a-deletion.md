# ADR 0048 — All four carried flags get a reader: the stock pair in the checkout saga, the product pair in the campaign engine

**Summary:** All four carried flags get a reader and none stops being
published: the stock pair in the checkout saga, the product pair in the
campaign engine. A published flag that nothing reads is a promise nobody keeps.

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A6 was filed against one flag and the sweep of 2026-09-06 turned it into
four. Re-measured 2026-09-07 against HEAD: the repository declares **17 boolean
columns, in nine of its seventeen modules**, and exactly four of them are never
READ — `manage_inventory` and `allow_backorder` on `product_variant`,
`is_giftcard` and `discountable` on `product`. All four belong to ONE module.

"Never read" was checked rather than repeated, and it means that NOTHING BRANCHES
on the value. Outside the generated GraphQL layer, which marshals the field
straight out to the client, every Go mention of the four names is an assignment,
a struct field, a key in the Query record the provider publishes
(`productRecord` and `variantRecord`), a selection string or an assertion in a
test, or prose in a comment. The only `if` statements naming any of them are the
patch shape `in.X != nil`, which tests whether a caller SENT the field and not
what the field says. On the SQL side, outside the four declarations in
`internal/modules/product/migrations/000001_product_init.up.sql`, the names occur
in exactly two INSERT column lists and three `COALESCE(...)` update expressions
— `is_giftcard` has not even that, because the product UPDATE omits it. NO
SELECT list names any of them, for the reason that every read of `product` and
`product_variant` in `internal/modules/product/queries/product.sql` and
`internal/modules/product/queries/variant.sql` is `SELECT *`. And there is no
`WHERE` on any of the four anywhere. Every OTHER boolean column changes what the
system does: `is_active` and `is_internal` cut the category listing in product's
taxonomy query, `requires_shipping` is read in inventory's own queries, and so on
down the list.

**This is the repository's named second class of mistake.** ADR 0009 refused to
add a `TenantID` field with nowhere to read it and gave the class its name — a
capability with no consumer — and ADR 0022 is the worked example of what it
costs: `Service.SetOrderSummaryTotals`, whose godoc in
`internal/modules/order/service/summary.go` argues FOUR rationales under four
headings — why the write is a merge, why a shrinking report is ignored rather
than rejected, why it runs under the order's lock, and why it is not compared
with the order total — with no production caller, and every order in the system
reporting `paid_total` 0 as a result. A column is the same shape with a longer
fuse, because a column accumulates a value per row while it waits.

**The holding action is already in place and it is not an answer.** D2 measured
that the defaults were unpinned: `manage_inventory` true to false,
`allow_backorder` false to true and `discountable` true to false were each
flipped alone and the whole product suite — unit and integration against a real
PostgreSQL — stayed green on every flip.
`TestCreateVariantDefaultsToManagedStockWithoutBackorder`,
`TestCreateProductDefaultsToDiscountableAndNotAGiftcard` and
`TestPartialVariantUpdateDoesNotResetTheFlags` pin them now. Every one of the 17
boolean columns IS written by something — checked column by column against the
query files on 2026-09-07, each named in an INSERT column list or on the left of
a SET. That fact is stated here directly and NOT credited to
`TestEveryColumnIsWrittenBySomething`, because that gate explicitly disclaims it:
`databaseSupplies` in `internal/arch/columns_test.go` reports a column written by
the database when its definition carries the word `default`, `auditColumns` skips
exactly those, and the gate's own godoc says a column the database supplies is
out of scope. All 17 are declared `NOT NULL DEFAULT`, so not one of them is
inside that audit, and it would stay green if a boolean column stopped being
written tomorrow.

What none of that does is make the stored value MEAN anything. A flag nothing
reads has no second line of defence, so the defaults had to be pinned — but
pinning a default is how you stop the catalog rotting while a decision waits, not
the decision.

**Two things changed while it waited, and both point the same way.**

The first is that ADR 0040 SPENT the stock pair. Its definition of "in stock" is
three clauses and two of them are these columns: a variant is in stock when
`manage_inventory` is false, OR `allow_backorder` is true, OR its available
quantity exceeds zero. So two of the four unread flags are now the majority of an
ACCEPTED definition, eight records old. That definition is not built — measured
2026-09-07, no `in_stock` or `InStock` identifier exists anywhere in the Go tree
— which means the pair is currently spoken for by a record and read by nothing.

The second is sharper, and it is the fact that decides this record. **At HEAD the
accepted definition and the checkout DISAGREE, in the direction that badges a
thing and then refuses to sell it.** ADR 0040 says the inventory link is optional
(`LinkVariantInventory`) and that a variant with `manage_inventory` false is
always sellable. The checkout's `Workflows.inventoryItems` refuses EVERY variant
with zero links — `CodeVariantNotStocked`, "a product whose stock cannot be
reserved cannot be ordered" — and it does so before any flag is consulted,
because no flag reaches it. A merchant who unticks "manage inventory" and links
no inventory item therefore gets a storefront that says in stock and a checkout
that will not take the order.

One sentence has to be corrected on the way in, because this decision is often
argued from it. Both `docs/gaps.md` and the file comment of
`internal/modules/product/service/flag_defaults_test.go` say that the stock
pair's "only possible reader is the checkout saga". ADR 0040 landed a second one
in the catalog. The sentence is stale in two places, and the argument below does
not rest on it.

## Decision

**All four flags get a reader. None of the four stops being published.**

**The stock pair is read by the checkout saga.** `manage_inventory` decides
whether a line is reserved at all — an unmanaged variant is not counted by the
merchant, so it is neither linked nor reserved, and `Workflows.inventoryItems`
must stop refusing it. `allow_backorder` decides whether the order is REFUSED
when no warehouse can cover the line.

The reads cost nothing new, and that was measured rather than hoped for.
`Workflows.prepare` already batch-Graphs every variant of the cart for its title
through `Workflows.variantTitles` — entity "variant", filtered by ids,
unconditionally, called immediately beside `Workflows.inventoryItems` — and
`variantRecord` already publishes both flags for that entity, with `project`
serving any published key. Adding two names to that field list is one Fields
entry and ZERO extra catalog round trips.

**The product pair is read by the campaign engine, as line attributes the cart
puts into its discount request.** This needs no change at all inside the
promotion module, which was the surprising half of the measurement and is the
reason the two halves of this decision can land independently. Measured against
the schema and the code:

| what was checked | what it says |
|---|---|
| `promotion_rule.attribute` | `TEXT NOT NULL`, with `CHECK (attribute <> '')` as its only constraint |
| `validateRuleInput` | checks the attribute's length, 1 to 128 bytes, and nothing else |
| `promotion_rule_operator_check` | admits `eq`, `ne`, `in`, `nin`, `gt`, `gte`, `lt`, `lte` |
| `matchRule` and `filterLines` | look the attribute up in the line's map and branch on the VALUE |

The rule attribute name space is open by construction. A rule spelled
`discountable eq true`, or the standard "promotions do not apply to gift cards"
rule `is_giftcard ne true`, runs against the engine exactly as it stands today.
What is missing is the CALLER putting the keys in the map — and
`internal/workflows/cart/discount.go` already names the place it goes, in
`attrVariantID`'s own comment: the attribute map inside
`Workflows.discountRequestFor`.

**The price of that key is per CART CALCULATION, not per line, and the comment
saying otherwise is wrong about that.** `attrVariantID`'s godoc says reading
product ids "would mean an extra round trip per line".
`Workflows.productIDsFor` disproves the per-line half: it resolves variant to
product for the WHOLE cart in a single Graph call, and `Workflows.computeTotals`
runs `Workflows.applyDiscounts` and `Workflows.applyTaxes` back to back in one
pass, so a hoisted read is paid once per calculation.

**How many reads that is depends on which tax path the cart takes, and saying
"one" flatly would be wrong.** `Workflows.applyTaxes` has THREE exits and only
one of them resolves variant to product today: `Workflows.applyModuleTax` calls
`Workflows.productIDsFor`, while the two region-rate exits — the tax module not
wired at all (the Phase 5 path), and a region that does not resolve to a single
country — return through `Workflows.applyRegionTax`, which never makes that hop.
So the hoist is free on one path and is a new read on the other two:

| tax path the cart takes | batch Graphs the discount leg adds |
|---|---|
| module tax, through `Workflows.applyModuleTax` | ONE — the hop is hoisted out of the tax leg and nets zero; only the flag read is new |
| region rate, either exit through `Workflows.applyRegionTax` | TWO — the hop is not paid today, so the discount leg pays it as well as the flag read |

`productRecord` publishes both flags, so the flag read itself is one Graph
against the "product" entity in either case.

**What each flag decides, written here so nobody has to infer it:**

| flag | reader | what it decides |
|---|---|---|
| `manage_inventory` | checkout saga, plan and reserve step | false: the line takes no reservation and needs no inventory link |
| `allow_backorder` | checkout saga, reserve step | true: an uncoverable line does not REFUSE the order |
| `discountable` | campaign engine, via a cart line attribute | whether a promotion may fall on the line |
| `is_giftcard` | campaign engine, via a cart line attribute | whether the line is a gift card, which a rule may then exclude |

**`allow_backorder`'s reader is "do not refuse", not "reserve past zero", and
that bound is deliberate.** Inventory's `Service.Reserve` compares the level's
available quantity against the requested amount and returns
`errors.Conflict` with `CodeInsufficientStock` when it falls short; there is no
path in that module that takes a level negative. The reader this decision
authorises lives entirely in the checkout: where the saga today turns "no
candidate warehouse" into a refusal, a backorder-permitting line is allowed
through with no reservation. Giving inventory a negative level is a different
decision with its own record.

## Rejected alternatives

**Stop publishing all four — drop the columns and the DTO fields.** This is the
other exit A6 named and it is the cheapest thing on this page: four columns stop
accumulating values nobody decided upon, three pinned-default tests stop being
necessary, and the product DTO stops making four promises it does not keep. It is
killed by ADR 0040. Two of the four are not spare — they are two of the three
clauses of a definition this repository ACCEPTED eight records ago, so dropping
them means superseding that record and reopening A17 with nothing to replace it.
The remaining two would then be dropped on the argument that nothing reads them,
which is circular once the measurement shows the reader is one map key away. And
the columns hold a merchant's merchandising decisions: a dropped column throws
away every value a shop has already curated, with no migration able to get them
back.

**Answer only the stock pair, because only it has an accepted definition
pointing at it.** It would halve the work and touch one flow instead of two. It
is refused because it recreates exactly the state A6 was widened to describe: one
module's DTO making four promises, answered two at a time, with two left behind
still accumulating a value per row and still with no second line of defence. It
also gets the economics backwards — the measurement says the LEFT-BEHIND half is
the cheap one, needing no change whatever inside the promotion module, while the
stock pair is the half that touches a saga and its durable record. Deferring the
free half to keep the decision small is not a saving.

**Make `discountable` a first-class field of the discount request and hard-code
the skip inside the promotion engine.** This is the strongest of the rejected
alternatives and it buys something real: a line that is not discountable would
never receive a discount in ANY shop, whether or not a merchant remembered to
write a rule, and the flag would mean the same thing everywhere. It is refused
for the same reason ADR 0040 refused to give inventory an `in_stock` boolean —
it puts a CATALOG concept into another module's vocabulary — and because it
widens promotion's interop schema, which rejects unknown fields, so the two sides
must move together and the compiler cannot see the match (the accepted price of
ADR 0006, provable only by an integration test). The rule-attribute route needs
no schema change on either side. **This is the weakest part of this decision and
it is written down as such**; the cost it leaves behind is the first entry under
the negatives.

**Fold `is_giftcard` into `discountable`, since a gift card is simply not
discountable.** One flag instead of two, one rule instead of two, one fewer
promise. Refused because they are not the same statement: `is_giftcard` says what
the product IS and `discountable` says what may be DONE to it, and a shop that
wants to run a promotion on gift cards is a legitimate configuration rather than
a contradiction. Collapsing a fact into a policy means the day the two must
differ the column has to be split again — and the rows written in between cannot
say which of the two meanings they were carrying.

**Wait for pre-order and give `allow_backorder` its reader then.** The
build-nothing-ahead-of-a-use argument, and it is normally the right one here. It
fails on the specific fact that the flag is not idle: it is being written on
every variant today, so the reader that eventually arrives acts on rows nobody
chose the values for, which is D2's whole argument. And the wait is no longer
free, because ADR 0040 has already committed the flag to a storefront answer
while the checkout ignores it. Waiting does not preserve a clean slate; it keeps
the badge and the till disagreeing.

**Let ADR 0040's catalog answer be the whole reader and leave the checkout
alone.** One place, no saga change, no plan record change, and the flags would
stop being unread the moment `StoreVariant` computes the answer. It is refused
because a BADGE is not an ENFORCEMENT. The catalog answer decides what a shopper
SEES; the checkout decides what the shop SELLS, and with the badge alone the two
disagree in the direction that takes an order the saga then refuses to reserve.
Two readers of one definition, applied at display and at enforcement, are not two
definitions — they are the same rule holding in both places, which is what makes
it a definition at all.

## Consequences

**Positive**

- **The count of carried-but-never-decided booleans goes from four to zero**, and
  with it the last instance of ADR 0009's second class of mistake that the
  boolean sweep could find. Every stored boolean in the repository then changes
  what the system does.
- **The storefront and the checkout enforce ONE rule.** The live disagreement
  measured above closes: a variant that is unmanaged and unlinked stops being
  badged in stock and refused with `CodeVariantNotStocked` in the same afternoon.
- **The campaign engine gains two readers with no change to the promotion
  module.** The open attribute name space, the eight operators and the branch in
  `filterLines` are already there; the whole cost sits on the cart's side of the
  boundary, which is where ADR 0001 puts the translation work anyway.
- **The stock pair costs zero extra catalog reads.** `Workflows.variantTitles`
  already fetches every variant of the cart in one batch call, unconditionally,
  and the flags are two more names in its field list.
- **The pinned defaults finally buy something.** D2's three tests were written to
  protect a value nothing consumed; from here on they protect a value that
  decides whether stock is reserved.

**Negative, and accepted**

- **For the product pair, a reader is a row somebody has to write, and with no
  row the attribute is not even looked at.** `filterLines` in
  `internal/modules/promotion/service/compute.go` guards its call —
  `if len(rules) > 0 && !matchRules(rules, lines[i].attributes)` — so a promotion
  with no target rules never calls `matchRules` at all and takes every line, and
  `matchRule` looks up only the attributes a rule actually names. Putting the
  keys in the map costs the read either way; what turns them into a decision is a
  `promotion_rule` record a merchant creates. A shop that ships without the rule
  has a `discountable` column that changes nothing in that shop,
  which is a weaker guarantee than "the flag means what its name says". This is
  the deliberate trade against the hard-branch alternative above, and it is the
  first thing to revisit: if a shop is found running a promotion against a gift
  card because nobody wrote the rule, the answer is the first-class field and the
  branch in the engine.
- **The cart calculation gains one batch catalog read on the module-tax path and
  TWO on the region-rate path.** Per calculation rather than per line, which is
  the part the existing comment gets wrong — but a cart is recalculated on every
  line change, so the arithmetic is a real addition and not a rounding error. The
  second read is not optional bookkeeping: on the two exits that go through
  `Workflows.applyRegionTax` there is no existing variant-to-product hop to hoist,
  so the discount leg has to make it. A repository that has not wired the tax
  module pays the higher of the two prices, which is the opposite of the usual
  direction and is worth knowing before the work starts.
  ~~`Workflows.discountRequestFor` today takes neither a context nor an error
  return; the flags have to be fetched by `Workflows.applyDiscounts`, which has
  both, and handed in.~~ **Corrected 2026-09-08:** half of that is wrong about
  the code. `Workflows.discountRequestFor` DOES take a context and use it — it
  calls `Workflows.ruleContext(ctx, snap)` and already degrades that read's
  failure with a warning. What it lacks is the ERROR RETURN, and that alone is
  the reason: the flag read has two ways to fail, and a function that cannot
  report a failure would have to swallow one. The flags are fetched by
  `Workflows.applyDiscounts`, which has the error return, and handed in.
- **FOUR new cross-module name pairings with nothing comparing the two
  literals.** Counted over BOTH halves, because both halves create them and the
  cheap half is not the free one here. The cart names the "product" entity and
  `is_giftcard` and `discountable` as strings against the keys `productRecord`
  declares; the checkout adds `manage_inventory` and `allow_backorder` as two
  more strings in `Workflows.variantTitles`' field list against the keys
  `variantRecord` declares. Four field names across two packages that cannot
  import the module declaring them, and nothing compares any pair. This is
  exactly the hazard ADR 0040 wrote down for `available_quantity` and exactly the
  audit it did not add. An unnoticed rename on product's side would silently stop
  every gift-card rule from matching, which is a failure with no error.
- **The saga's plan line grows two fields, and the reasoning that makes this safe
  lives in another package.** The obvious objection is that an execution record
  written before the upgrade decodes `manage_inventory` as Go's zero value
  `false` — the direction that would sell an unmanaged variant without reserving
  it. Checked against the engine: it cannot happen. `internal/core/workflow/recover.go`
  states in its file comment and in the Recoverer godoc that the recovery path
  NEVER calls Invoke, `recoverExecution` does exactly two things (rebuild the
  chain, then compensate), and `Workflows.RecoveryWorkflow` is the only place a
  checkout plan is decoded from JSON in the tree — so a stale plan reaches the
  compensations and never the reserve decision. What makes this a cost rather
  than a non-issue is that the safety is a property of the ENGINE that nothing in
  the checkout package restates: an engine that one day RESUMED an abandoned
  execution instead of undoing it would make the objection true again with no
  change to this decision. The cheap insurance is to encode the inverse, a field
  named for "unmanaged", so that Go's zero value falls on the safe side; whoever
  implements this should take it.
- **An unmanaged line leaves no reservation trace.** `reservationRef` is written
  into the record so an operator can answer the "which warehouse" question by
  hand; for a line the merchant asked not to count there is no answer and there
  should not be. The operator surface for a half-done saga will show lines with
  no reservation, and that is now a legitimate state rather than evidence of a
  fault.
- **`allow_backorder` is the one flag of the four whose full meaning this
  repository cannot yet express.** The reader authorised here stops the refusal;
  it cannot promise the unit, because inventory has no negative level and no
  promised date. A merchant reading the checkbox may expect the second thing.

## What this deliberately does NOT do

- ~~**It does not carry the change.**~~ **BUILT 2026-09-08, both halves.**
  The stock pair: `Workflows.variantTitles` asks for `manage_inventory` and
  `allow_backorder` beside the title — one more Fields entry, no new round trip,
  measured as ONE catalog call for the whole
  checkout; `planLine` gains `Unmanaged` and `AllowBackorder`, and the first is
  the INVERSE the negatives asked for, so a plan written before today decodes as
  COUNTED; `Workflows.inventoryItems` stops demanding a link for an uncounted
  variant and still refuses a counted one that refuses backorder; the reserve
  step skips an uncounted line before asking any module, and forgives an
  uncoverable line only when it permits backorder and only on errors.Conflict.
  Three consequences the record did not name were found while building and are
  held by their own gates. **The first is that reading ONE clause at the link
  check leaves the disagreement half open**, and it is the whole point of the
  record, so it was closed rather than filed: `Workflows.inventoryItems` lets an
  unlinked variant through when it permits BACKORDER as well, because nothing
  counts its stock and therefore no warehouse can ever cover it, which is
  exactly the case that flag forgives — the reserve step skips it without
  putting a question with an empty item identifier to the inventory module. ADR
  0040 also says "a variant with `manage_inventory` true and NO linked inventory
  item is NOT in stock"; that sentence is read here as belonging to the THIRD
  clause, the one that needs a quantity to evaluate, and the reading is written
  down because the other one makes ADR 0040 contradict its own clause two. The
  reading is also the safe one either way: under it the badge and the till
  agree, and under the other the till accepts what the badge refused, which
  never strands a shopper. **The second**: `reserveInventoryStep.Restore` used
  to read an empty reservation list as a corrupt record, which is now a
  LEGITIMATE outcome — so the step's output NAMES every line it deliberately
  left unreserved (`reserveOutput.Unreserved`) and the record has to account for
  every line of the plan. The plan cannot do that job: a backorder-permitting
  line is reserved wherever stock exists, so a plan of such lines is equally
  consistent with "reserved nothing, correctly" and "reserved two and lost
  both", and a guard that asked the plan alone would let a lost trail through
  with compensation reporting "done" having released nothing. **The third**:
  `checkoutPlan.validate` now refuses a line that is counted, refuses backorder
  and carries no inventory item, because the flags and the item come from two
  different reads and a disagreement would make the step silently skip a line
  that had to be reserved. The product pair: `Workflows.discountRequestFor` puts
  `is_giftcard` and `discountable` into each line's attribute map,
  `Workflows.applyDiscounts` fetches them (it has the error return that function
  lacks), a product the catalog cannot answer for leaves the keys OFF the line,
  and a failed read logs and prices the cart without them. Nothing inside the
  promotion module changed, exactly as measured. Mutation-proved eight ways with
  `-count=1`: removing the uncounted-line skip, inverting the conflict guard on
  backorder, swapping the two product flags, never sending them, dropping the
  backorder exemption at the link check, dropping the unlinked-line skip in the
  reserve step, weakening the record's accounting back to the plan's answer, and
  not naming an unreserved line in the output.
  **One measured cost differs from the table above and it is stated rather than
  smoothed over.** The hoist that makes the module-tax path net ONE lives in
  `Workflows.computeTotals` and `Workflows.applyModuleTax`, which the build that
  carried this record did not own, so the discount leg resolves the products
  itself and a discounting cart on that path pays THREE batch catalog reads
  rather than two — the hop, the flag read, and the tax leg's own hop. The
  region-rate path pays the two the table names. The hoist is still the right
  shape and is left open. The assertion that used to pin the module-tax path at
  ONE batch read, `TestTheProductsAreReadInONEQuery` in the cart flow's tax
  tests, MOVED rather than being loosened: it now compares a one-line cart with
  a two-line cart and requires the counts to be EQUAL, which is the property it
  was written for — catalog work that does not grow with the basket — and which
  a literal count could not tell apart from an N+1.
- **It does not build pre-order.** A promised date and a stock level that may go
  negative in a controlled way are a feature with its own decisions. This record
  ends the state in which the flag decides nothing.
- **It does not build ADR 0040's in-stock answer and it does not change it.** The
  three clauses stand as accepted; what this adds is that the same three must
  hold at the reserve as well as at the badge.
- **It does not add the name audit.** ADR 0040 asked for one for the
  catalog-to-inventory field pairing and did not build it; this decision creates
  FOUR more pairings of the same shape — two in the cart, two in the checkout —
  and does not build it either. That debt is now bigger by four and is still
  open.
- **It does not widen the promotion module's interop schema, and it does not
  license a third catalog field into the discount request.** Two flags, named
  above. The day a rule wants a category id it is a separate argument with a
  separate read.
- **It does not decide the rules a shop should write.** `discountable eq true`
  and `is_giftcard ne true` are the rules that make the columns mean what their
  names say; whether a given shop writes them is the merchant's.
- **It does not touch the other 13 boolean columns**, which already have readers,
  and it does not turn the boolean sweep into an arch test. The sweep's own
  measurement argues against that: `automatic_taxes` is read by the cart through
  a primitive interop method that hands it across as an unnamed bool, so the
  value flows while the NAME does not survive the crossing, and a name-based
  reader gate would fail the build on a module obeying ADR 0001.

## Related

- [ADR 0040](0040-in-stock-is-a-catalog-answer-over-an-inventory-fact.md) — the
  definition that spends the stock pair at the badge, and the reason the checkout
  must read the same three clauses at enforcement.
- [ADR 0009](0009-cok-kiracililik-kurulum-siniri.md) — where a capability with no
  consumer was named as this repository's second class of mistake.
- [ADR 0022](0022-the-saga-records-what-was-collected.md) — the worked example of
  what a carefully argued thing with no caller costs in production.
- [ADR 0004](0004-query-veri-erisimi.md) — the read layer both readers travel
  through, and the reason the product module cannot interpret its own flags for
  the checkout.
- [ADR 0006](0006-workflow-modul-erisimi.md) — the workflow-to-module boundary
  whose price is a schema no compiler compares, which is what kills the
  first-class-field alternative.
- [ADR 0001](0001-modul-arasi-iletisim.md) — the interop rule that puts the
  translation on the cart's side, and that makes a name-based reader audit
  unsound here.
- [ADR 0017](0017-recovering-abandoned-sagas-from-the-record.md) — the recovery
  path whose "never calls Invoke" property is what makes the plan record's new
  fields safe, and the property this decision leans on from outside.

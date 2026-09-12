# What a line could not say

Evidence for [ADR 0148](../adr/0148-a-rule-can-ask-what-a-product-belongs-to.md).

Measured 2026-09-12, taking the half ADR 0144 named and left open.

## The half that was left, quoted from the record that left it

ADR 0144's measurement closes with two paragraphs that are this slice's whole
brief, and both turned out to be exactly right:

> For the other two targets the cart could not send the data even if the engine
> took it … `productRecord` on the Query provider publishes sixteen fields, none of
> them a category or tag list … the bulk membership read exists as
> `ListCategoriesByProductIDs` / `ListTagsByProductIDs`, consumed only inside the
> module, on no Query provider.

> Also not measured: whether `any_in` should exist for LINE rules. It is passed
> `nil` there on purpose.

Re-measured against today's tree, nothing had moved: the record still carried
sixteen keys, `filterLines` still passed `nil`, and the two bulk reads still had
one caller each — `Service.attachRelations`, inside the module.

## What a merchant could and could not write

The engine's attribute namespace is open, so the rule ROW could always be written.
What decided the answer was what the cart SENT:

| Rule a merchant writes | Could it match? |
|---|---|
| `product_id eq prod_1` | yes — ADR 0103 |
| `collection_id eq pcol_1` | yes — a column on the product row |
| `customer_group_id any_in [vip]` | yes — ADR 0144, a CONTEXT rule |
| `category_ids any_in [cat_1]` | **no** — the line carried no list at all |
| `tag_ids any_in [tag_sale]` | **no** — same |

The last two are the inert shape at its most expensive: the rule saves, the admin
surface accepts it, the compute endpoint answers 200, and the discount is simply
zero. Nothing in the system reports anything.

## Why the field is conditional, and what it costs

`productRecord` is built from a `models.Product` and nothing else, so membership
had to come from somewhere. Three shapes were compared:

| Shape | Cost | Verdict |
|---|---|---|
| a JOIN in the product query | multiplies product rows by memberships; the page size is applied to PRODUCTS | rejected |
| two batch reads, always | two statements on every catalog page, for fields the panel's grid never names | rejected |
| two batch reads, only when the fields are named | nothing for every existing reader | **chosen** |

The reads are keyed by `product_id = ANY($1)` on tables whose PRIMARY KEY is
`(product_id, …)`, so the lookup is on the key's leading column, and the id list
is the products of ONE cart. The totals path already reads: the variant hop, the
product record, the price sets, the tax rates and the promotion candidates.

**Not measured:** the query PLAN, on a catalog large enough for the planner to
have a choice. The structural fact above is not the same claim as "an index scan
happens", and this repository has already been wrong once about exactly that (the
load measurement found a godoc claiming index use that was false). What is
measured is the statement COUNT and the key shape; a shop that sees a problem has
the escape hatch named in the ADR's Rejected list.

## The empty selection was the quiet case

`project` returns the record as it is when the field list is empty, which means
"the whole record". Fetching membership only for an explicit selection would have
answered a caller who asked for everything with two empty lists for a product in
three categories — a lie that no error accompanies. It is its own test
(`TestAskingForEverythingIncludesTheMemberships`) and its own mutation (56).

## The drift this slice caused, and the gate that now holds it

The promotion module answers this calculation on TWO surfaces:

- `service/interop.go` — what the cart flow posts;
- `api/admin.go` — `POST /admin/v1/promotions/compute`, what an operator posts.

Both godocs say the shapes must be IDENTICAL, and both say why. The field was
added to the interop item first and the admin item was missed; every lane stayed
green. An operator trying the category rule in the panel would have been told it
discounts nothing about a cart the shop discounts — which reads as a broken RULE,
so the operator goes and rewrites a rule that is correct.

`internal/arch/discount_schema_test.go` compares the JSON field names of the three
request shapes now. It prices NAMES, not meanings: a field both sides call "lists"
passes even if one drops it, which is what the module's tests and the e2e discount
proof are for.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 51 | the provider never fills the lists | **bit** (5 unit, 2 e2e) |
| 52 | the cart does not ask for the fields | **bit** (1 unit, 2 e2e) |
| 53 | the line carries no lists on the wire | **bit** (2 unit, 2 e2e) |
| 54 | target rules get `nil` lists again | **bit** (2 unit, 2 e2e) |
| 55 | every product gets the first one's memberships | **bit** (1 unit, 1 e2e) |
| 56 | an empty field selection skips the membership | **bit** (1 unit) |
| 57 | an empty membership is sent as an empty list | **bit** (3 unit) |
| 58 | the tag list is never filled while the category list is | **bit** (2 unit, 1 e2e) |
| 59 | the admin compute body loses the lists | **bit** (the new gate) |
| 60 | a gated pair is renamed so nothing is compared | **bit** (the new gate) |

Mutation 55 is the one a batch read invites and the only one the single-product
tests could not have caught: one map, three products, and an answer indexed by
position rather than by id looks perfect until two products differ. Its witness
reads all three in one call and asserts each separately.

Mutation 58 is the same shape across the PAIR of fields: one mechanism carries two
lists, and feeding one while forgetting the other is silent — a tag rule selects no
line, the promotion produces no discount, and no error is raised. The tag case is
therefore a separate test from the category case at every layer, which is ADR
0144's own rule about fixtures applied again.

## What was not measured

Whether any shop wrote a `category_ids` rule before this and watched it do nothing.
It would show as a promotion rule row whose attribute is one of the two names, on a
deployment running any version before this one, which is reconstructable and was
not attempted.

Also not measured: whether a merchant expects a parent category's campaign to
cover its children. The answer shipped is DIRECT membership, matching the filter,
and it is published as a limit rather than as a decision nobody stated.

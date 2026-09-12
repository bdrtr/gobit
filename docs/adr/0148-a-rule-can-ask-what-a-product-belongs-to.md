# ADR 0148 — A rule can ask what a product belongs to

**Summary:** The product module publishes each product's category and tag ids on
its Query record, and the cart sends them per line, so a promotion rule can say
"half off anything in this category". It costs two batch reads on the totals path
and buys the two rule targets the feature list has asked for since the start.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

ADR 0144 gave the engine an operator that reads a SET — `any_in` — and gave the
cart a set to answer it with: every customer group beside the ranked head. It
closed the customer-group half of the feature list's A2.4 row and wrote down what
it could not: a LINE had nothing set-shaped to offer, because a product's
categories and tags are not columns on the product row and nothing published them.

So the operator existed and half its questions could not be asked. A merchant
could write "20% off this product" and "20% off this collection" — both single
values — and not the one a shop actually runs: half off a category.

The pieces were measured to exist already: `ListCategoriesByProductIDs` and
`ListTagsByProductIDs` are bulk reads, consumed only inside the module and on no
Query provider.

Measurement: [measurements/0148](../measurements/0148-what-a-line-could-not-say.md)

## Decision

The product record publishes `category_ids` and `tag_ids`, filled by two batch
reads and ONLY when a caller names them, and the cart puts each line's lists into
the discount request beside its attributes. `any_in` reads them on a target rule
exactly as it reads the context's set.

## Consequences

The cart's totals path costs two more statements, and they are the shape that can
be afforded: keyed by the products of ONE cart, on the primary key's leading
column, once per calculation rather than per line. Every other reader of the
product record pays nothing, because the lists are fetched only when the field
selection names them — and an EMPTY selection names them, since "the whole
record" has to be true.

Membership is DIRECT, and that is the same answer the provider's `category_id`
filter gives. A rule naming a parent category does not reach the products filed
only under its children, so a merchant running a campaign over a tree names the
subcategories; the limit is in `known-limits.md` beside the filter's, because the
two have to move together.

A shipping method carries NO lists and never will: it is in no category and
carries no tag, so a rule asking a shipping target about a category matches
nothing. That is the right answer rather than a gap — the alternative would be a
"category" campaign making delivery free.

An absent list and an empty one differ on the wire. A line whose product could not
be read sends no key, which the engine treats as "does not match"; an empty list
would be the cart claiming the product is in zero categories, which it cannot know.

Eight mutations bit on the feature and two on the new gate, and the gate is the
part worth naming. The promotion module answers this calculation on TWO surfaces —
the cart's interop and `POST /admin/v1/promotions/compute` — and both godocs said
the shapes must stay identical. This slice added the field to one of them and
nothing failed: an operator trying a category rule in the panel would have been
told it discounts nothing while the shop discounted it, which reads as a broken
rule and sends somebody to rewrite one that is correct. The rule is a test now.

## Rejected

- **Send the lists always, fetched on every product read.** Two statements on
  every catalog page, for a field the panel's grid never reads.
- **Put the memberships in the attribute map as a joined string.** The engine's
  single-value operators would then match on "cat_a,cat_b" as a value, so a rule
  would have to know the join order and a shipped `eq` rule could start matching.
- **Ask the promotion module which attributes its live rules read, and fetch
  only those.** It replaces two indexed reads with one query plus a cache whose
  staleness is a silently non-matching rule — the defect this decision closes.
- **Resolve the category tree here.** A descendant walk belongs in the SQL that
  the filter already uses; writing it in Go for the record would give the same
  filter two answers depending on the path the caller took.

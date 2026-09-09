# ADR 0103 — A rule can name the product and its collection

**Summary:** A cart line carries its product and that product's collection into
the promotion engine, so a rule can name either. Category and tag stay out, and
the reason is the shape of a line attribute rather than the work.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

A discount rule could name the VARIANT of a line and nothing above it. A
merchant discounting a product had to name every variant of it, and name the new
ones as they were added — which is a rule that silently stops covering the thing
it was written for.

The catalog reads the product's row on every totals round already: the engine's
two flags come off it, and since ADR 0101 the tax module's type does too. The
product id and its collection were on that record and were being dropped.

## Decision

`lineAttributes` carries `product_id` and `collection_id`, read from the record
the round already fetches. A category or a tag does NOT, and that is a decision
rather than a next step.

## Consequences

Both keys cost no extra read. That is the whole reason they could be added: the
totals path runs on every cart update, and a second read of a row already in
hand is the N+1 this package's tests are written to keep out.

An attribute is ABSENT rather than empty when the product is in no collection.
An empty string is a value a rule can be stored against, and such a rule would
match every line whose product is in none; the engine's own rule is that a line
missing an attribute does not match, which is the answer this wants.

A CATEGORY and a TAG are lists, and a line attribute is one string
(`map[string]string`, matched in `matchRule`). A product in three categories has
no single category to send, so carrying them means a multi-valued attribute and
an operator to compare against it — a change to the engine's contract, not to
the cart. It is left undone deliberately and this is where the reason is.

## Rejected

- **Sending the first category** — a rule would then match or miss depending on
  a row order nobody chose.
- **Joining the categories into one string** — the engine compares whole values,
  so "a,b,c" matches no rule anybody would write.
- **A second read for the collection** — it is on the row already read.
- **An empty attribute when the product has no collection** — it turns a rule
  stored with an empty value into a match on every uncategorised line.

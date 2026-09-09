# ADR 0094 — A product wears a tax class

**Summary:** A tax class is a set of products taxed the same way, owned by the
tax module, and a rate rule may be written against the class instead of against
one product at a time.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

A rule could name ONE product, or a product TYPE the catalog does not have —
the type key is empty on every request gobit sends today. So a shop taxing books
at one rate and electronics at another had to write one rule per product and
rewrite them as the catalog grew: the classification existed in the merchant's
head and nowhere in the schema.

## Decision

`tax_class` names the vocabulary, `tax_class_member` binds a product to at most
one class, and `tax_class` becomes a fourth rule reference. A product's class is
resolved by the tax service from its own tables, so no caller sends it and no
wire changes.

Rate selection keeps its shape: one more match key, and the class sits between
the product and the type in specificity.

## Consequences

What the merchant said about THIS product still beats what they said about its
class, and a class beats a type — the narrower statement wins, and the class is
the tax module's own word while the type is the catalog's.

The classification lives in tax, not on the product. Putting it in the catalog
would have put a tax word in `models.Product` — and in the storefront body that
model is embedded in — and would have made the cart's tax leg read the catalog a
second time to answer a question this module answers from its own tables.

The class of a whole cart is resolved in ONE query, and in none at all when no
line carries a product id. A product in no class is absent from the answer and
falls through exactly as it did before.

A product is in at most one class, held by a partial unique index. That is what
keeps the selection decidable: two classes would give a line two keys of equal
specificity and which rate applied would fall to row order. Putting a product in
a second class therefore MOVES it, rather than being refused — reclassifying is
the ordinary operator action, and refusing it would leave a window in which the
product is in no class at all.

A class holding products cannot be retired: its rules would go on applying to a
set nobody can name any more.

The rollback DELETES the rules written against a class, and says so. A rate rule
is configuration rather than a record of something that happened, and those rows
are exactly the ones the older vocabulary has no word for; an order's tax is not
read from them, because the line stores the rate it was charged.

Measurement: none. The shape was read off the tree — the rule vocabulary, the
match keys and the one query the calculation already makes per read.

## Rejected

**A `tax_class_id` column on the product.** The catalog would carry a tax word
in a model the storefront publishes, the GraphQL field gate would move, and the
cart's tax leg would read the catalog again for something tax already knows.

**A class per VARIANT.** The rate is a statement about what a thing IS, and two
variants of one product are the same thing in two sizes. A variant-level class
would also multiply the membership table by the catalog's variant count for a
distinction nobody asked for.

**Several classes per product, resolved by priority.** A second ordering rule to
learn and to get wrong, on top of the specificity order that already exists.

**A per-authority class CODE column** (what an external calculator would want).
No external tax provider exists and none is planned, so it would be a column
nothing writes and nothing reads — the shape this repository keeps refusing.
It is an additive migration on the day a provider arrives.

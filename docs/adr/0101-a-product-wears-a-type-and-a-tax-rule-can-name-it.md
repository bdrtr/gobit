# ADR 0101 — A product wears a type, and a tax rule can name it

**Summary:** The catalog gains `product_type` and a product carries one, which
is what a tax rate rule matches on. It costs a table, a column and one more
field on a catalog read the totals path already makes.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

The tax module has matched a rate rule on three references since it was written,
and one of them is the product TYPE: `ReferenceProductType` is a defined
reference, the cross-module request carries `product_type_id`, and the local
calculation turns it into a match key. Every one of those lines is live.

The field has been empty on every request ever made, because nothing in the tree
could name a type. A merchant could not say "books are taxed at 1%" — only "this
product is", one product at a time, and again for every book they ever add.

That is the error ADR 0009 and ADR 0063 refuse, seen from the other end. There a
capability was published with no consumer; here a consumer was published with no
capability. The cart's own field said so in its godoc: "the day the catalog
grows types, this is where the value goes and nothing else changes."

## Decision

`product_type` is a table beside the collection and a product points at one with
a column of its own. The type reaches the tax request through the catalog read
the totals path already makes, so a merchant's rule written against a type
matches.

## Consequences

A product has ONE type and several categories, which is why the type is a column
and categories are a map table. The tax rule needs a single value to compare,
and a product in three categories has no single category to offer it.

Deleting a type RELEASES its products in the same transaction, because
`ON DELETE SET NULL` cannot fire against a soft delete. The collection answers
the same question the same way; what differs is the cost of getting it wrong.
A stale collection pointer is a listing defect. A stale type pointer is MONEY:
the products would keep being taxed by a rule the merchant believes they
removed.

The totals path now reads the product row for TWO consumers — the discount
engine's flags and the tax module's type — and reads it ONCE. The read is
skipped entirely when neither module is installed, which is the same rule the
flags already followed and the same reason: a read with no consumer is a cost
nobody asked for.

A shop that names no types is priced exactly as it was: an empty reference
produces no match key, so every line falls to the rate it fell to yesterday.

The storefront publishes the type and cannot FILTER by it. Reading a fact and
narrowing by it are different requests, and a filter needs an index and a
decision of its own.

## Rejected

- **A map table, like categories and tags** — a rate rule compares one value,
  and a product in three types could not answer it.
- **Leaving the type to `metadata`** — no uniqueness, no handle, nothing to list
  in an admin screen, and a tax rule would match on a string nobody validated.
- **Reading the type in a query of its own** — a second read of the row the
  discount path already fetches, on the path that runs on every cart update.
- **A storefront filter in this change** — it needs an index and its own
  measurement; publishing the field does not.

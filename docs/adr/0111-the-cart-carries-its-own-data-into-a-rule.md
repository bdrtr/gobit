# ADR 0111 — The cart carries its own data into a rule, under a prefix

**Summary:** A cart's `metadata` becomes promotion rule context, prefixed with
`cart.` and bounded at thirty-two string-valued keys. It costs a field on the
snapshot and buys an embedder a rule attribute this framework never has to know
about.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

The discount engine's rule context was built from two names the cart flow
decides: the region and the customer's group. Nothing could add a third.
`internal/app.Options` takes `Modules` and `Plugins`, so an embedder had no seam
at all.

That makes ordinary promotions unwritable. A shop selling two brands from one
installation cannot say "ten percent off, brand A only" without a column in the
cart module for a concept the cart module has never heard of — which is the
opposite of what a framework is for.

The cart already carries a free-form bag. What was missing was the hop, and a
rule for what may travel through it.

## Decision

`Snapshot.Metadata` crosses the boundary, and `ruleContext` writes its
string-valued entries into the rule context under `cart.`, at most
`MaxCartAttributes` of them, taken in sorted key order.

## Consequences

The prefix is not decoration and is not configurable. A cart whose metadata
carried `customer_group_id` would otherwise let whoever writes that bag hand
themselves a segment discount. The dot is deliberate: neither fixed name contains
one, so the two spaces cannot collide by any spelling.

Only STRING values cross. The engine compares whole values, so a number would
need a formatting rule — 1 and 1.0 are the same number and two different
attribute values, and a rule stored against one would silently miss the other. A
merchant who wants a numeric rule writes the number as a string; the numeric
operators parse it. A value of another type is SKIPPED rather than formatted, and
that is the safe direction: an absent attribute does not match, so it narrows a
discount rather than widening it.

The count is bounded. Every attribute is copied into the discount request on
every totals round, and the bag is free-form, so an unbounded one would let a
storefront make its own carts expensive to price. Sorted order makes it
reproducible which keys survive the bound.

It is the CART's bag and not the LINE's. A line's metadata is the shopper's
intent — a gift note — and the shopper must not write the left-hand side of a
discount rule.

## Rejected

**A configuration key on `internal/app.Options`.** It would put the shop's
vocabulary in the binary's setup rather than on the cart it describes, so two
carts in one installation could not differ.

**Carrying every value by formatting it.** It buys numeric metadata and pays with
a second representation of every number, which the engine cannot compare.

**No prefix, with the two fixed names simply written last.** Order is not a
guard: it works until somebody reorders the writes, and nothing would fail.

**The line's metadata as well.** The shopper writes it.

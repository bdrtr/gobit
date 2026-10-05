# ADR 0393 — An add-on is written under its line and asked again when it rises

**Summary:** The checkout and the cart merge write each add-on right after its
line, and a raise asks each add-on of the line its product's list and its
channel again. A shipping quote keeps counting an add-on's units.

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

Three costs ADR 0229 named were still open. The checkout sent every line of its
own before every add-on, so an order of two engraved rings was written, read,
invoiced and disclosed with both engravings after both rings, where ADR 0233
said an order prints its lines as the cart held them; a merge opened a guest's
lines the same way on the target cart (D243). A raise of a line raises its
add-ons, and the raise ADR 0281 made ask the channel asked it of the line's
variant alone, so an add-on taken off its product's list or gone from the
request's channels rose with its line unasked (D244); copies of the channel
rule said a quantity update asks nothing and that adding a line is the only way
into a cart (D245). A shipping quote and a delivery change count
every unit sold, an add-on's among them.

## Decision

The checkout sends, and a merge opens, each line of its own followed at once by
its add-ons, and a raise of a line asks each of its add-ons whether the
product's list still takes it and the request's channels still hold it,
refusing as an add refuses and writing nothing. An add-on stays an ordinary
variant to the quote, the parcel, the merge and the completion, which count it,
pack it and ask nothing of it.

## Consequences

On an order placed from now on, every read ordered by `created_at, seq` prints
an add-on under its line: the order read, the invoice, the dossier and the line
entity, and a merged cart lists them so too. An order placed before keeps its
add-ons after its lines, and its page nests them (ADR 0250). The order module
is unchanged: it writes lines in the order given and holds only that a parent
comes first.

A raise refused for an add-on answers 422 `cart_workflow_add_on_not_accepted`
when the product no longer takes it and 404 `cart_workflow_variant_unknown`
when the channels no longer hold it; the shopper keeps the line, lowers it, or
removes it and adds it again. The raise reads the line's product with the
add-ons' variants in one scoped read and that product's list in another, and a
line without add-ons reads nothing more. An add-on's price at the new quantity is asked by the totals round after
the write, as the line's own is.

A per-item shipping rate charges an engraving or a wrap as an item, and a
parcel takes an add-on line as it takes any line; which units travel together is
the operator's. A merge and a completion move units already in a cart and ask
an add-on nothing.

## Rejected

- **Grouping at read time in the order module.** Three queries, the order read,
  the line entity's cross-order page and the disclosure, would each join a line
  to its parent to sort, where the one sender can write the order once.
- **Renumbering the stored lines.** `seq` is an identity, and an update does not
  choose the order in which its rows take new numbers.
- **A parent field on the invoice line.** The invoicing flow prints flat lines
  and would read nothing it carries.
- **An add-on left out of the item count.** An add-on is an ordinary variant
  (ADR 0228), and one that ships would go uncounted; this reopens when a quote
  can tell a unit that ships from one that does not.
- **Asking the list at completion.** An edit of the list would make a full cart
  unpayable, the cost ADR 0281 refused for the channel.
- **Asking the list at merge.** A merge moves units already in a cart, and would
  ask of them what completing the cart that held them does not.

# ADR 0403 — A cart's metadata chooses no price

**Summary:** A cart's price is chosen from its region, sales channel, customer, company and head group; its metadata reaches promotions alone.
Pricing refuses a new rule whose attribute begins with `cart.`, and carries one written before through every write that keeps it.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0111](0111-the-cart-carries-its-own-data-into-a-rule.md), whose bag now reaches promotions alone, and [0397](0397-a-cart-is-priced-in-the-channel-it-was-opened-in.md), whose list trial no longer says `no_cart_metadata`

Measurement: [measurements/0403](../measurements/0403-the-carts-bag-and-a-price.md)

## Context

ADR 0111 put a cart's metadata into the rule context under `cart.`, and the
cart's line and totals prices were asked with the same context, so a price
rule on `cart.arm` chose what a cart was charged (ADR 0185 recorded the
reach). The wishlist quote (ADR 0216) never carried the bag, and the price
list trial (ADR 0220) carried none and said so only from ADR 0397 on.

ADR 0111 assumed the shopper does not write the bag, and two comments repeated
it. `POST /store/v1/carts` takes the bag from the body, and the browser holds
the key that opens it (ADR 0008). That is D260.

## Decision

**A cart's price is chosen from the region, the sales channel, the customer,
the company and the head group, and the cart's metadata reaches promotion
rules alone. Pricing refuses a rule whose attribute begins with `cart.` where a
caller writes one, and carries one written before through every write that
keeps it.**

## Consequences

- **A cart is priced from what its quote and list trial read, and its
  channel.** The line, the totals, the quote and the list trial take
  `priceContext`, whose subject type has no field the bag could arrive in; the
  discount and the promotion trial take `ruleContext`, which adds it. The list
  trial drops `no_cart_metadata` from its assumptions; the promotion trial
  keeps it.
- **One code is new.** `POST /admin/v1/prices/{price_id}/rules`,
  `POST /admin/v1/price-sets` and `POST /admin/v1/price-sets/{id}/prices`
  answer 422 `pricing_rule_attribute_reserved` for a `cart.` attribute.
- **A `cart.` price rule written before stays and matches no cart.** The panel's
  forms and the catalog import write it back unchanged, and the variant page
  marks it so; the admin calculator still matches it when asked with that
  attribute.
- **Removing it means removing its price.** A price whose `cart.` rule is
  deleted applies to everybody its other rules match, and a list price then
  beats the base for them.
- **A cart an operator opens loses the same reach.** `POST /admin/v1/carts`
  takes metadata too, and no flow prices a telephone order differently.
- **Promotions still read the bag, and the publishable key's holder writes it.**
  That is D261, open.
- **The prefix is spelled twice and held once.** Pricing cannot import the cart
  flow, so an architecture test binds `ReservedAttributePrefix` to
  `CartAttributePrefix`.

## Rejected

- **Dropping the bag without the refusal.** A merchant writing `cart.` means a
  price by the bag; it would be stored, matched by the admin calculator and
  never charged.
- **The refusal inside `validateRule`.** Every write that keeps a set's other
  prices revalidates them, so the import and both panel forms would answer 422
  for a rule the operator did not write there.
- **An allow-list of the names the flow sends.** It would change with every name
  the flow adds, as the company and the channel did (ADR 0185, ADR 0397).
- **Trusting the bag on a cart an operator opened.** The cart knows its opener
  (ADR 0296), but no caller prices by it.
- **A CHECK on `price_rule`.** Old rows would need `NOT VALID` and a soft-delete
  exception, and no cart's context carries the attribute they name.
- **Deleting old `cart.` rules in a migration.** Their prices would lose that
  condition and apply to everybody their other rules match.

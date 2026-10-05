# ADR 0407 — A cart's metadata reaches no rule

**Summary:** A cart's promotions are matched against its price context, and its metadata reaches neither; promotion refuses a new rule under `cart.`.
It costs every promotion ruled on the bag, which stops applying, and the seam ADR 0111 gave an embedder for a fact of its own.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Supersedes:** [0111](0111-the-cart-carries-its-own-data-into-a-rule.md)
- **Amends:** [0403](0403-a-carts-metadata-chooses-no-price.md), whose bag reached promotions alone

Measurement: [measurements/0407](../measurements/0407-who-reads-the-carts-bag.md)

## Context

ADR 0111 put a cart's metadata into the promotion rule context under `cart.`
on the premise that the shopper does not write it. ADR 0403 took it out of every
price, because `POST /store/v1/carts` takes the bag from whoever holds the
publishable key, and left promotions reading it (D261). The order keeps no copy
of the bag, so a sale cannot show why such a promotion applied and the
promotion trial matches its rule on no order. The case ADR 0111 was written
for, a promotion for one brand's storefront, has had a server-decided attribute
since ADR 0397: the sales channel a key opens its carts in.

## Decision

A cart's discount round and the promotion trial take the price context, so a
cart's metadata reaches no price and no promotion. Promotion refuses a rule
whose attribute begins with `cart.` with 422 `promotion_rule_attribute_reserved`,
and one written before stays and matches no cart.

## Consequences

- **The bag is stored and read back.** The routes return it and `cart.interop`
  still carries it; the cart flow's snapshot has no field it could arrive in.
- **An old `cart.` rule holds on no cart.** An absent attribute matches no
  operator, `ne` and `nin` included, so a promotion with such a context rule
  stops applying. The admin computation still matches it when asked with the
  attribute, and the promotion page marks it.
- **Removing it widens the promotion.** The promotion then applies wherever its
  other rules hold, so its condition moves to `sales_channel_id`, a customer
  group or a code first.
- **The refusal sits at promotion's one rule write.** It is kept out of the
  shape validation, so a later write that carries rules it did not take lets
  an old one through. Every other attribute stays open (ADR 0048).
- **The trial sets nothing aside for the bag.** Its `assumptions` drop
  `no_cart_metadata`, which v0.9.0 published.
- **An embedder with a fact only its server knows has no promotion seam.** A
  storefront's concept is a channel, a group or a code; a fact none of them
  holds reopens this record.
- **The prefix is spelled four times and held once.** internal/arch binds
  promotion's and pricing's reserved prefix and the panel's marker to the cart
  flow's `CartAttributePrefix`.

## Rejected

- **Documenting `cart.` as caller-written, like a code.** A code is typed, bounded and kept on the cart; a bag value is none of these, and the order cannot show it.
- **A bag only an operator or in-process code writes.** A column, a second write and a new parameter for no caller in this tree.
- **Reading the bag only on a cart an operator opened.** ADR 0403 refused it for prices: no caller.
- **Dropping the bag without the refusal.** A merchant writing `cart.` means the feature ADR 0111 published, and would store a rule that never holds.
- **The refusal inside `validateRuleInput`.** A write that carries old rules would answer 422 for one it did not take, as pricing found.
- **Refusing the bag on the store route.** The storefront reads back what it stored, and no rule reads it.
- **Deleting old `cart.` rules in a migration.** Their promotions would apply wherever their other rules hold.

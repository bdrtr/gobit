# ADR 0185 — A contract price names its buyer

**Summary:** The cart puts the customer and the company they buy for into the
rule context, and a price ruled on either is a contract price that outranks a
segment price at the same list priority. It costs a b2b read wherever the cart
already reads the customer's groups, and a rung in the pricing ladder.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0185](../measurements/0185-whose-price-it-is.md)

## Context

A B2B shop agrees prices with a customer or with a company, and gobit could not
express either. The rule context the cart builds carried the region, the cart's
own metadata and the customer's groups, so a price rule could name a segment
and nobody in it. A group per contract would work, at the cost of a group for
every customer and a second place to keep who the contract is with. And the
ladder would still separate a contract from the customer's segment price by
amount, which ADR 0049 already refused for groups: the cheaper price is not
the one the merchant meant.

## Decision

The cart writes `customer_id` for a cart with a customer, and `company_id` when
the b2b module says the customer is a company's employee. Pricing ranks a
price whose `eq` or `in` rule names the customer above one that names their
company, and that above one that names neither, below list priority and above
the rule count.

## Consequences

A merchant writes a contract as an override list whose prices carry one rule:
the customer, or the company. Every employee of the company pays the company's
contract, and one of them with a contract of their own pays theirs. A contract
beats the buyer's segment price even when the segment price is cheaper, and a
contract on a sale list still loses to an override, because the list type is
read first.

`ne` and `nin` rules on these attributes name nobody: "everyone but this
customer" is no contract with anyone, and it ranks as any other rule does.

No price a cart charges changes rank. Neither attribute was ever in the cart's
context, so every rule that named one was eliminated, and a price that names
neither ranks at zero on the new rung. The admin calculation endpoint takes its
context from the query string, and a request that passes these attributes is
now ranked as a cart is.

The b2b module is optional: without it no cart carries a company. An unreadable
membership prices the cart without the company, logged, which is what the cart
already does with an unreadable group.

The attributes reach promotions too, since discounts share the rule context,
so a promotion can be written for a customer or a company. They reach neither
the storefront catalog nor the storefront price set: both leave out every
price with a rule.

Both names are exported on both sides, and internal/arch binds the spellings.

`pricing/service/calculate.go`, which holds the ladder, was translated in the
same change.

## Rejected

- **A customer group per contract.** It works today and costs a group for every
  contract customer, and the ladder would still pick the cheaper of a contract
  and a segment price.
- **The lowest applicable price.** A contract is the merchant's decision for
  that buyer, and ADR 0049 refused letting the amount rung override a
  merchant's decision.
- **A new list type for contracts.** A contract is already an override list; a
  third type would rank lists, not buyers.
- **The rung above list priority.** A merchant who writes a contract on a sale
  list has chosen the sale list's rank.

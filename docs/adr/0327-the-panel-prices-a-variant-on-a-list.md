# ADR 0327 — The panel prices a variant on a list

**Summary:** A variant's page lists its prices on price lists with the
customers each is for, read through `pricing.admin`, and adds a price on a
list for every customer or for some customer groups, and removes one, under
`pricing:write`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel writes price lists (ADR 0326) and customer groups (ADR 0323), and
the cart writes a customer's first-ranked group into the price's rule
context (ADR 0049). A list price limited to a group could be written only
through the admin API, and the variant's page could not show one: the read
layer leaves a price with rules out.

## Decision

The pricing module's panel surface lists a price set's prices on lists with
their list's title and rules, and adds or removes one under the set's lock,
keeping every other price. The variant's page shows them, offers the lists,
the currencies and the groups, and writes a price at one unit and up limited
by a `customer_group_id in` rule to the chosen groups, or to none.

## Consequences

- A wholesale price for a trade group is set where the variant is priced,
  and the page says who pays it.
- The rule reads the customer's first-ranked group, the one pricing has always
  read (ADR 0049); a customer in several groups pays the price of the group
  that ranks first. Its attribute is pinned against the cart's.
- The same list, currency and customers twice is refused; a base price is not
  removed here, since the variant would be left with nothing to sell at.
- The groups are named and offered only to an operator who may read the
  customers; one who may not sees their ids.
- A list price limited by a rule the panel did not write is listed with its
  conditions as they stand, and can be removed.
- Quantity tiers on a list stay on the admin API.

## Rejected

- An `any_in` rule over the customer's groups: pricing reads one group by
  design (ADR 0049), and two ladders reading groups differently would price
  one customer two ways.
- Writing the price through the variant's base price form: that write changes
  the base price at one unit and leaves the rest untouched, by contract.

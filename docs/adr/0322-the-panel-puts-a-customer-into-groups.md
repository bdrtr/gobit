# ADR 0322 — The panel puts a customer into groups

**Summary:** A customer's page names their groups through the customer module's
group entity, and an operator holding `customer:write` puts the customer into a
group or takes them out through a new `customer.admin` surface.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A promotion or a price limited to a customer group (ADR 0321, ADR 0049) reaches
a customer through their memberships, and the panel neither showed a customer's
groups nor could change them: membership was written through the admin API
alone, so an operator who granted a customer a wholesale tier had to leave the
panel to do it.

## Decision

The customer's page lists their groups in rank order, named through the
`customer_group` entity under `customer:read`. The customer module registers a
`customer.admin` surface that puts a customer into a group and takes them out,
and the page offers both under `customer:write`.

## Consequences

- An operator sees why a customer gets a segment's price or discount, and
  grants or withdraws it where they see it.
- Putting a customer into a group they are in leaves them there, so a second
  press is harmless; taking them out of one they are not in is refused, as the
  module already does.
- A group that is gone or whose name cannot be read is shown by its id, and
  the customer's page stands.
- The form offers the ninety-nine newest groups the customer is not in, read
  only for an operator who may write them.
- Membership is a write of the customer module, so the panel gains its first
  customer surface; reads stay on the read layer.

## Rejected

- Listing the members on a group's screen: the question an operator brings is
  about one customer, and a group may hold thousands.
- Writing membership through the customer record's group_ids: the read layer
  publishes, it does not write.

# ADR 0217 — A customer group can be a segment

**Summary:** An operator gives a customer group a rule over the customer's
account, age, default shipping country and orders, and an hourly job keeps its
members exactly the live customers the rule takes in. While the rule stands the
group takes no hand edits, and a preview counts a rule before it is set.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0049](0049-one-group-decides-the-price-and-the-merchant-ranks-the-groups.md), whose groups the operator filled one customer at a time

Measurement: [measurements/0217](../measurements/0217-a-group-that-follows-its-rule.md)

## Context

A customer group is how the shop prices and promotes a kind of customer: the
cart writes the head group and every group into the rule context (ADR 0049,
0144). Membership was a plain set the operator wrote customer by customer, and
nothing recorded where a member came from. What a segment tests lives in two
modules that do not import each other (ADR 0006): the customer module holds the
record and the addresses, the order module the orders, and no aggregate of a
customer's orders existed beyond the spending limit's one-customer sum. The user
chose that a segment manages its group, that a rule reads the record and the
order history, and an hourly job with a preview.

## Decision

`PUT /admin/v1/customer-groups/{id}/segment` gives a group a rule of ANDed
conditions over `has_account`, `account_age_days`, `country_code`, `order_count`
and `net_spend`, and while it stands a hand edit of the members is refused with
409 `customer_group_segment_managed`; `DELETE` takes the rule away and keeps the
members. A `customer-segments` job writes every segment's members each hour
through a segment workflow over the live customers and the order module's new
per-customer totals, and `POST /admin/v1/customer-segments/preview` counts what a
rule would take in.

## Consequences

The order totals follow the spending limit's rules: canceled orders out, pending
ones in, refunds deducted, no currency converted. `net_spend` reads the rule's
currency and `order_count` counts every currency.

A pass reads each live customer once, 500 at a time, with one order read per
page for each window the rules use. Each segment's page is written under the
group's lock only while its rule is the one the pass read, and the last page
reaches the end of the ids, so a member whose record is gone leaves. A failed
pass leaves what it wrote, and the next writes every segment again. A segment
follows its rule within the hour, not at an order.

A new rule keeps the old members until the next pass, and a hand edit that races
the rule's arrival is undone by it. At most 50 groups are segments; the writers
that could add one take advisory lock class 6 first.

The rule's words are written twice, where the customer module admits a rule and
where the flow evaluates it, and internal/arch holds the two together. A word
the flow does not know fails its segment and not the pass. The preview reads
every customer while the request waits.

The rule is not personal data: its values are booleans, whole numbers and
country codes, so it names a kind of customer and never one. Rolling back
customer migration 000006 forgets the rules and keeps their members as plain
members.

## Rejected

- **A segment that only adds members.** A customer who stopped matching would
  keep a price meant for others, and the group would drift past its rule.
- **Evaluating on `order.placed`.** Age and windows move without an event, and a
  lost delivery would need a second way in (ADR 0212).
- **A segment membership beside the groups.** Pricing and promotions read groups
  already; a second membership would need its own path into the rule context.
- **Customer metadata in a rule.** Its values are untyped and the shop's own, so
  a rule over it would compare whatever was written.

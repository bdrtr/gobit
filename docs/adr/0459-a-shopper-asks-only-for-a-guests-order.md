# ADR 0459 — A shopper asks only for a guest's order

**Summary:** The storefront's transfer request and its acceptance refuse an order that has a customer, an order takes three requests within seven days, a customer's erasure withdraws the requests waiting for them, and a transfer discloses to each person only their own side.
It costs a shopper the request on a customer's order, which an operator moves instead, and buys an account that keeps what it bought.

- **Status:** Accepted
- **Date:** 2026-10-20
- **Amends:** [0450](0450-an-order-changes-hands-by-its-addresss-consent.md), whose request reached a customer's order through its address

## Context

ADR 0450 asked the order's address for consent, and argued it as the
consent a guest's order has; it took a customer's order the same way. The
address of a customer's order is the one typed at its checkout, a gift's
recipient's as easily as the account's, so whoever read it could take the
order from the account that bought it, unasked and untold (D314). A review
of 0450 found three more: a request withdrawn and asked again mailed the
order's address without end, a receiver's erasure left their request
acceptable into an address that reaches nobody, and the receiver's
disclosure returned the previous holder's address as their own.

## Decision

The storefront's request refuses an order that has a customer with
`order_not_transferable`, its acceptance refuses one too, a fourth request
within seven days of three is refused with 429
`order_transfer_requests_spent`, and an erasure of a customer withdraws
every request waiting for them as its receiver. A transfer's disclosure
keeps the holder's id and address for the holder and the receiver's id for
the receiver, and empties the other side's.

## Consequences

- A customer's order changes hands only by an operator's move, from the customer the operator read.
- A request written on a customer's order before this record is refused at acceptance; nothing in it moved.
- The count is of the requests whose bound has not passed, answered, withdrawn and mailed-and-failed alike; a guest who withdraws three times waits a week or asks an operator.
- An erasure counts no withdrawn request among its rows: the receiver's id is kept, as `orders.customer_id` is.
- A transfer that holds nothing of the person asking, one on their order between two earlier holders, is not disclosed to them.
- The moved order's sale addresses stay with the order, as 0450 decided, and are disclosed to whoever holds it.

## Rejected

- Mailing the token of a customer's order to the account's address on record: the account's holder is proven at the storefront already, and has nothing to ask of themselves.
- A withdrawn request blocking the next until its bound: a mail that failed withdraws the request too, and the shopper could not ask again for a week.
- A per-shopper quota on the route: the mails go to the order's address, so the order is what the count protects.

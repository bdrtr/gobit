# ADR 0450 — An order changes hands by its address's consent

**Summary:** A signed-in shopper who can open an order asks for it, the order's address answers with a token mailed there, and acceptance moves the order's customer and e-mail and withdraws its links; an operator moves an order at once.
It costs a second mail and a guest's token to ask with, and buys a transfer no order id, forwarded link or stale page can make alone.

- **Status:** Accepted
- **Date:** 2026-10-17
- **Amends:** [0439](0439-an-id-gobit-hands-out-opens-nothing-at-the-storefront.md), whose access tokens an accepted transfer withdraws

## Context

depremtek's storefront, a consumer moving off Medusa, let a shopper move an
order into their account: they named it, its address was mailed a token,
and the holder accepted or declined (D305). gobit had no route or service
that changes an order's customer after placement, so the consumer could only
write the order table under gobit's events, audit and erasure. An order id
opens nothing at the storefront (ADR 0439), the order's address is the only
contact every order mail reached, and a token in a mail is a link anyone it
is forwarded to holds.

## Decision

`POST /store/v1/orders/{id}/transfer-requests`, behind the order's access
and from a shopper the identity proves, records a request to the order's
customer and address, mails the address a token with the template
`order.transfer_requested` and waits seven days, one per order; its holder
accepts or declines at `/store/v1/order-transfers/{transferId}`, the shopper
withdraws it, and `POST /admin/v1/orders/{id}/transfers` moves an order at
once from the customer the operator read. Acceptance moves the order and the
orders that add to it, under the order's lock and only while it holds the
customer and address the request recorded, to the receiver and their address
on record, withdraws their access tokens and publishes `order.transferred`
per order with the order, the transfer and the moment.

## Consequences

- The order's customer list, proof, totals, b2b window and review badge follow the order; its addresses, invoice, payment and whose balance paid it, cart and records already opened stay with the sale.
- `order_transfers` keeps each transfer, an operator's closed when written; its customer ids are kept on erasure, as `orders.customer_id` is, and its asked address is emptied wherever the person was the holder.
- The previous holder's links stop opening the order; a mail sent after the move goes to the receiver's address.
- A canceled or erased order, one that adds to another and a guest's order with no address do not change hands; an operator moves the last.
- A request whose mail fails is withdrawn and answered 503; a holder answering it is told it was closed.
- Expiry is read, not swept: a request past its bound stays `requested`, reads `expired` and holds nothing.
- `order.transferred` carries no customer: a subscriber reads the order, which is the truth when two transfers arrive out of order. Nothing in gobit subscribes; the consumer that asked hears it through its plugin.
- The panel offers no transfer screen; the operator's move is the admin route.

## Rejected

- Medusa's request by order id alone: the id goes to every webhook receiver, and the order's address would be asked by anyone holding one.
- Accepting on the order's token alone: a forwarded mail's link would move the order without the mailbox.
- A request offered to an address the holder names: the order's own address is the consent a guest's order has.
- Keeping the order's e-mail: the order's mails, a guest's lookup and an erasure would keep reaching the previous holder.
- A compare-and-set route the consumer drives: every shop would rebuild the token, the mail and the expiry.

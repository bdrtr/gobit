# ADR 0215 — A wishlist item can ask for its stock

**Summary:** A proven customer marks a wishlist item to be told when its variant
is back in stock, and a job mails their own address once when the storefront
shows the variant in stock again after having shown it out. Only the customer
who set the mark is mailed, and the mail clears it.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amends:** [0190](0190-a-customer-keeps-a-wishlist.md), whose items could not ask for anything
- **Amended by:** [0216](0216-a-wishlist-item-can-ask-for-its-price.md): an item can ask for its price too, and the job answers both marks

Measurement: [measurements/0215](../measurements/0215-a-variant-that-came-back.md)

## Context

ADR 0051 refuses the back-in-stock waitlist as specified: a row written by a
party the storefront cannot identify, acted on later toward an address nobody
verified. The wishlist (ADR 0190) is reached by the proven customer alone. No
event announces stock (ADR 0063), and "in stock" is computed by the product
module over the warehouses a request's sales channels serve (ADR 0040). The user
chose proven customers only, a mark on the wishlist, stock alone as the trigger,
and one mail.

## Decision

`PUT /store/v1/customers/{id}/wishlist/{variant_id}/stock-alert` saves the
variant if needed and marks it with the request's sales channels, and `DELETE`
takes the mark off. A `stock-alert` job, every five minutes, arms a mark whose
variant the storefront shows out of stock in those channels and, once an armed
mark's variant shows in stock, mails the customer's own address and clears it.

## Consequences

A mark set on a variant in stock waits for it to run out and come back; marking
again starts the wait again. The in-stock answer is the storefront's own badge,
read through `product.interop`: a variant whose product is unpublished or not
visible in the mark's channels never mails.

The mail's reference names the mark and the moment it was armed, so a pass
stopped between the mail and the clear sends nothing twice; a mail that fails
keeps the mark, and the notification module sends a reference once, so the
next pass clears it without mailing. A notification provider has to know
`wishlist.back_in_stock` to deliver it.

The mark is declared personal data: a person's file shows it and an erasure
deletes it with the item. The channels and the arming moment are the shop's
working state. A deleted customer's marks are not read. Rolling back customer
migration 000004 forgets the marks.

The storefront gains two routes that require a proven customer, and the
installation without a bound identity refuses them, as it refuses the wishlist.

## Rejected

- **A guest's address with a double opt-in.** The user chose proven customers,
  and it needs a token, a confirmation mail and an eraser for strangers.
- **A price-drop trigger.** A later slice; the price a shopper saw depends on a
  region and a currency the mark does not carry.
- **Mailing at every return to stock.** One mail per mark, as chosen; a shopper
  who wants another marks again.
- **Asking the stock at the moment of the mark.** The customer module cannot
  read the catalog, and arming reaches the same answer on the next pass.

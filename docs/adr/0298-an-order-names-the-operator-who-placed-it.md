# ADR 0298 — An order names the operator who placed it

**Summary:** An order placed through the admin cart API's completion or the
panel's telephone order keeps the identity of the operator who placed it. The
admin order record, the read layer, the admin listing and the panel's order
list and page read it; the storefront's order does not carry it.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0296 had a cart name the operator who opened it, for finding unfinished
telephone orders. A placed order kept no such mark: the shop could not list
the orders its operators took, nor say who took one when the caller rang
back. The cart's opener is not the order's placer: a colleague may complete a
cart handed over, and a shopper may complete an operator's cart through its
link (ADR 0295). The completion already ran under the operator's identity,
which the guard ring proves on the admin route and the panel.

## Decision

The operator's completion passes the caller's identity to the checkout, which
writes it to the order's `placed_by`, and a storefront's completion passes
none. The order provider offers `placed_by` and a `placed_by_operator` filter,
the admin listing takes the same filter, the admin order record carries the
field, and the panel's order list and page offer them.

## Consequences

- A shop lists the orders its operators took and sees who took each, whoever
  opened the cart.
- An operator's cart a shopper completes on the storefront is the shopper's
  order and names no operator.
- The value is free text and written once, as the cart's opener is; a CHECK
  refuses a blank and a partial index holds the operators' orders, the cart's
  measured shape (measurements/0296).
- The storefront's order record leaves the field out, as it leaves out the
  addresses: a shopper holding the order's id would read the operator.
- The panel's two list boxes narrow together, and the paging keeps both.
- Orders placed before this record name no operator and count as shoppers'.

## Rejected

- Carrying the cart's opener into the order: a cart handed over or completed
  by the shopper would name the wrong person.
- Recording the operator in the order's metadata: the read layer and the
  listing could not filter on it, and a caller could write it.
- Showing the operator on the storefront's order: the record is read by
  anyone holding its id, with a key that names the shop (ADR 0008, ADR 0193).

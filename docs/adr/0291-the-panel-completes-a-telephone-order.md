# ADR 0291 — The panel completes a telephone order

**Summary:** The panel's telephone order writes the cart's shipping address,
chooses its shipping option and completes it with an offline method against the
total the page shows, through three more methods of the cart module's surface.
The cart provider offers a cart's shipping address and shipping methods, so the
page shows what the forms wrote.

- **Status:** Accepted — amended by [0292](0292-a-cart-lists-the-shipping-options-it-can-take.md)
- **Date:** 2026-10-01

## Context

ADR 0290 let an operator open a cart and add its lines in the panel. The
address, the shipping method and the completion stayed on the admin API, so a
telephone order still ended in an API client. The admin API's completion
compares the total the operator read to the caller (ADR 0286) and refuses a
method whose money moves at the checkout. Its address write reprices the cart,
because the tax follows the address. The cart provider offered neither the
address nor the chosen methods.

## Decision

The cart module's surface is built over the handler the admin API runs and
gains the address write, the shipping option and the completion, each the API's
own act. The cart's page shows the shipping address and methods from two new
provider fields, and its completion form carries the total the page was drawn
with and sends the operator to the order it places.

## Consequences

- An operator takes a telephone order from the first line to the placed order
  in the panel, and the order owes its total until the shop records the money
  (ADR 0287).
- The panel refuses what the API refuses. A total that moved since the page was
  drawn, or a method whose money moves at the checkout, is printed on the cart
  with what was typed.
- The address form writes the shipping address only; the billing address stays
  on the API.
- The operator types the shipping option and the offline method by id, as the
  channel and the variant; the page offers no list of eligible options.
- The two fields each cost one read for a page of carts, made only when asked
  for. A cart with no address answers nil, and one with no method an empty list.
- A completion form sent twice places one order: the second finds the cart
  completed and is refused.

## Rejected

- A copy of the address and completion logic in the surface: two
  implementations of one act drift, and the handler already holds the
  repricing and the completion's encoding.
- A list of eligible shipping options on the page: the fulfillment module
  prices an option against facts the cart workflow assembles, and no cart-level
  read of them exists yet.
- Taking the total from the cart at the moment of completion: the operator
  would place an order for a figure nobody read to the caller.

# ADR 0290 — The panel opens a telephone order

**Summary:** The panel gains a telephone order screen. An operator opens a cart
for a country and a caller and adds priced lines to it through the cart
module's `cart.admin` surface, the admin API's own acts. The cart query
provider offers a cart's lines and an `id` filter, so the cart's page reads it
as the order page reads an order.

- **Status:** Accepted — amended by [0293](0293-a-telephone-order-finds-a-variant-by-its-title.md)
- **Date:** 2026-10-01

## Context

ADR 0146 and ADR 0286 put the whole telephone order on the admin API: open a
cart, add priced lines, write the address and the shipping method, complete it
with an offline method. The panel had no screen for any of it, so an operator
taking an order by telephone needed an API client. The panel writes through a
module's surface and reads through the query layer (ADR 0013, ADR 0271). The
cart provider offered neither the lines, which its godoc had refused as an
unbounded set before ADR 0227 capped a cart at 100 lines, nor the `id` filter
the order provider had gained for the order page.

## Decision

The cart module registers `cart.admin`, whose two methods open a cart for a
country and add a line in a named sales channel through the flows the admin API
holds. The panel's telephone order section opens a cart and draws its page from
the cart provider's new `lines` field and `id` filter, with a form that adds a
line.

## Consequences

- An operator opens the cart and adds its lines in the panel. The address, the
  shipping method and the completion stay on the admin API until the panel
  offers them.
- The panel refuses what the API refuses: the region comes from the country,
  the price and the title from the catalog of the named channel, and a line
  with no channel is refused rather than priced against the products assigned
  to none. The one function that writes the API's channel claim writes the
  panel's.
- Each write sends the operator back to the cart's page, so a reload does not
  add the line twice. A refusal is drawn on the page with what was typed.
- The form that opens a cart asks for `cart:write`. The cart's page asks for
  `cart:read` and offers its form only to an operator who also holds the write.
- The `lines` field costs one read for a page of carts, made only when asked
  for; a cart with no line answers an empty list.
- The operator types the channel and the variant ids; the screen offers no
  search.

## Rejected

- A screen served from `/admin/v1` in the browser (ADR 0030): its behavior
  would carry no test this repository can run, and every write the panel gained
  since ADR 0266 is a form through a surface.
- A cart line entity in the read layer: the page reads one cart's lines, which a
  field carries.
- Taking the channel from the operator's identity: an operator holds no channel,
  and the claim is made per sale (ADR 0146).

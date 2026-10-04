# ADR 0332 — The panel opens a parcel on the delivery chosen

**Summary:** An order sold several deliveries is asked on its page which one
a parcel goes on; the order module's surface lists the order's deliveries as
they stand and sends the parcel on the option the chosen delivery stands on
when the form is sent. Closes D207.

- **Status:** Accepted
- **Date:** 2026-10-02
- **Amended by:** [0388](0388-the-order-page-credits-changes-a-delivery-and-corrects-the-address.md): a delivery is put on another option in the panel; a parcel on an option no delivery stands on stays on the admin API

## Context

The panel opens a parcel through the fulfilling flow (ADR 0324), and its
form named no delivery, so the flow took the one the order was sold. The
flow defaults only on an order sold exactly one (ADR 0198); a cart may hold
several deliveries, one per shipping profile, and on such an order every
press of the panel's form was refused with no way to name one (D207).

## Decision

The order module's surface lists an order's deliveries as they stand after
their changes (ADR 0199) and opens a parcel on the delivery the panel names,
on the option that delivery stands on when the parcel is opened. The order's
page asks which delivery only when the order was sold several, and says an
order sold none has nothing to open a parcel on.

## Consequences

- An order whose goods ship from two profiles is packed in the panel, a
  parcel per delivery.
- The form carries the delivery, not its option, so a delivery changed
  between drawing the page and pressing the button ships on its new option.
- An order sold one delivery is drawn and opened as before, the flow taking
  it; the page reads the deliveries only for an operator who may open.
- Deliveries the surface cannot read leave the choice to the flow, which
  refuses an order it cannot default, as before.
- Shipping on an option the order was not sold stays on the admin API.

## Rejected

- The form carrying the option id: a delivery changed after the page was
  drawn would ship on the option it was changed from.
- Asking on every order: a single delivery has nothing to choose, and the
  flow's default is the one the order was sold.

# ADR 0287 — The panel records an offline payment

**Summary:** The order page names each offline method's session that still
awaits its money, and an operator who may write payments records it as
received; the payment module captures the session whole and refuses a session
whose money moves at the checkout.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0284 left an offline method's session authorized until
`POST /admin/v1/payment-sessions/{id}/capture` records the money, and ADR 0286
let an operator place a telephone order that owes its total. The operator who
took that order works in the panel. The order page printed the payment's
totals but not which session awaited money or by which method, and recording
it took a call to the API with a session id that no page printed. The page's
other writes are forms that reach a module's panel surface (ADR 0271).

## Decision

The payment query provider offers `awaiting`, every authorized session of a
provider whose money comes later with its method and amount, and the order page
prints each one. An operator holding `payment:write` records one as received
through the payment module's `payment.admin` surface, which captures the
session whole and refuses one whose provider moves its money at the checkout.

## Consequences

- An operator closes a telephone order in the panel: the page says what is
  awaited and by which method, and the record raises the order's paid total
  through `payment.captured`, as the API's capture does.
- A card's session is refused by the surface, not only left off the page: an
  operator's word is not a capture of money a provider holds. The API's capture
  is unchanged and takes either.
- The form needs `payment:write`; the page shows the payment block only under
  `payment:read` (ADR 0251), so the order's privilege alone opens neither.
- The record is the whole session. A transfer that arrives short is captured
  through the API with an amount; the panel offers no amount.
- A second record returns the first capture, so a form sent twice captures
  once.
- The route acts on the session it names, as the after-sales acts do on their
  record (ADR 0271); the order in its path is the page it returns to.
- Reading `awaiting` costs one read of the collections' sessions, made only
  when the field is asked for.

## Rejected

- A query entity for payment sessions: the page needs three fields of the
  awaited ones, and the module would answer for a whole entity to serve them.
- An amount on the form: a short transfer is the exception the API's capture
  already takes, and every record would ask for a figure.
- The page posting to the API's capture through the panel's session (ADR
  0030): that capture takes a card's session as well, and the page's other
  acts are forms through a surface.

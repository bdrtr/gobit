# ADR 0312 — An operator switches a promotion's status from the one read

**Summary:** The Promotions screen publishes a draft, pauses an active
promotion and resumes an inactive one. The switch carries the status its row
was drawn in, and a promotion another operator moved first is refused.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0311 gave the panel a list of the promotions in each status, with nothing
to do on it. The admin API changes a promotion's status only by rewriting the
whole promotion with `PUT /admin/v1/promotions/{id}`, which carries every
field and no version: a panel form built on it would write back whatever it
had read, over an edit another operator saved in between. Pausing a coupon
that is being abused, or publishing a prepared one, is the act an operator
needs most often, and it changes one field.

## Decision

The promotion module switches a status with a single update that matches the
status the caller read, and refuses with `promotion_status_moved` when the
promotion is no longer in it. The `promotion.admin` surface offers the switch,
and the Promotions screen gives each row its one move under `promotion:write`.

## Consequences

- A draft is published, an active promotion paused and an inactive one
  resumed from the list, each in one form that returns to the list it left.
- Two operators pausing the same coupon are not both obeyed: the second is
  told the coupon is inactive now, and a draft withdrawn while another
  operator published it stays withdrawn.
- The update writes the status and the stamp alone, so an edit of the code,
  the limit or the campaign made meanwhile is kept.
- A deleted promotion is not found, as an unknown one is.
- The screen offers no other move, such as returning an active promotion to
  draft; the surface accepts any pair of statuses for a caller that asks.
- An operator who may switch and not read sees a refusal's reason alone, as
  on the cart page (ADR 0260).
- The admin API gains no status door; its PUT still writes the status with
  the rest of the promotion.

## Rejected

- Building the form on the full update: it would write back every field the
  list never showed.
- A version column on the promotion: the status is the one field the switch
  writes, and it is its own guard.
- A status endpoint on the admin API now: no integrator has asked, and it
  would be a second contract for the same write.

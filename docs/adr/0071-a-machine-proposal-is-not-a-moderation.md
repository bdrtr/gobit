# ADR 0071 — A machine's proposal is stored beside a review, and it is not a moderation

**Summary:** A model may write a proposal about a waiting review into columns of
its own; it moves nothing, and the human decision keeps meaning what it says.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

The review module rests on one sentence: a review is invisible until an operator
approves it. A15 admits the storefront write only because a human stands between
it and its effect, and the schema says it twice — the storefront read filters on
`'approved'` in SQL, and `reviews_moderation_mirror` ties `moderated_at` to the
status beside it.

A model reading a review can say something useful about it, and the queue is
where an operator spends their time. The question is not whether that is worth
having; it is where the answer goes, because the obvious place — the `status`
column — is the one place it must never go.

Measurement: [measurements/ai-subsystem](../measurements/ai-subsystem.md).

## Decision

**A proposal is stored in four columns of its own on `reviews`**:
`suggested_status`, `suggested_at`, `suggestion_note`, `suggestion_model`. It
touches neither `status` nor `moderated_at`, so the mirror keeps meaning what it
said — a HUMAN decided — and no proposal produces `'approved'`.

**A proposal exists whole or not at all.** Three mirrored CHECK constraints say
so, in the same shape as the moderation mirror, and they forbid the residue as
well as the partial write. The Go model is a POINTER for that reason: four zero
values would be a state the type permits and the table refuses.

**It is refused about a review somebody has already decided**, by the statement's
own `status = 'submitted'` literal rather than by a read-then-write. A job
racing an operator is normal, not exceptional, and a caller told "recorded"
about a dropped proposal would report a run that handled everything.

**The reason is required for an approval too**, which is where this differs from
moderation. A human approval explains itself because a person took
responsibility for it; a machine approval does not, and the operator is being
asked to take that responsibility on the strength of the reason alone.

**A proposal never reaches a shopper.** The storefront DTO has no field for one,
and the test reads the ENCODED body rather than the struct.

## Consequences

- **The guarantee is enforced in three places** — the statement names neither
  human column, the mirror would refuse the row, and a test asserts both after a
  proposal is stored.
- **The personal-data declaration grew by one column.** `suggestion_note` is
  free text a model wrote about a stranger's free text, and a reason for
  rejecting a review quotes the review.
- **Two audits were found blind on the way in, and both were fixed.** The
  module's declaration audit read ONE migration BY NAME, so a column added by a
  second was outside the population — measured by removing the new holding and
  watching the audit stay green. The repository-wide schema reader took only the
  FIRST `ADD COLUMN` of an `ALTER` naming several, and its blindness control had
  the same hole: every planted `ALTER` added one column.
- **A later proposal replaces an earlier one.** It records nothing that
  happened, so there is no history to keep; `suggested_at` and
  `suggestion_model` are what tell a stale one apart.
- **Nothing writes a proposal yet.** This record is the storage decision; the
  producer is a separate one, and until it lands the field is absent from every
  response.

## Rejected

- **Letting a model write `status`.** It is the whole sentence the module
  exists for, and A15's answer would change with it.
- **Computing the proposal when the queue is opened.** The operator pays for it
  on every page, and it changes under them between two loads with nothing to
  point at.
- **A `review_suggestions` table.** One row per review at most, read only with
  the review; it would buy a history nobody consults and a join on the queue.
- **A bare proposed status, with no reason and no model.** An operator cannot
  weigh it, so the sound response is to ignore it — cost with no benefit.

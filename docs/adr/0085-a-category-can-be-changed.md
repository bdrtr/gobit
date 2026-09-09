# ADR 0085 — A category can be changed, and a move that would close a ring is refused by the statement

**Summary:** The category tree had no write but the soft delete; a PATCH adds
one, and the cycle rule lives in the UPDATE rather than beside it.

- **Status:** Accepted
- **Superseded by:** [0091](0091-a-reparent-takes-the-trees-lock.md) for the concurrency half; the PATCH and the guard stand.
- **Date:** 2026-09-09

## Context

Measured on 2026-09-09, while comparing this catalog against a platform's
capability list: the only `UPDATE product_category` in the whole tree was the
soft delete, the admin surface bound POST, GET and DELETE and no PATCH, and
`is_active` was written by the INSERT and by nothing else.

So a category could not be renamed, could not be moved, and — worst — one
created with `is_active` false stayed off for the life of the installation.

**A document said otherwise.** The listing's own godoc read: "the admin surface
passes false and sees everything, which is the only way the merchant can turn a
category back on." Seeing is not writing. It described a mechanism that did not
exist — the class this repository keeps finding in itself (D29).

## Decision

**`PATCH /admin/v1/product-categories/{id}`** changes name, handle, description,
parent, both flags and the rank. A field that is not supplied is preserved.

**`clear_parent` is its own word**, because nil already means "leave the parent
alone" and "make this a root" needs a way to say itself. Sending both it and
`parent_id` is refused rather than resolved.

**The cycle rule lives in the STATEMENT.** The UPDATE carries a recursive term
that walks UP from the new parent and will not write if the category being moved
is on that path — which also covers a category being made its own parent, since
the walk starts at the parent.

**Reaching the walk's bound is itself a refusal.** The walk must be bounded or
an ancestry that already holds a ring would not terminate, and a bound alone
would be worse than none: an ancestor past it would go unseen and the ring would
be written. Sixty-four is far past any catalog a person maintains.

**The service resolves the id first**, so "no such category" and "that move is
refused" stay two different answers.

## Consequences

- **A category switched off can be switched back on**, asserted through the
  storefront's predicate rather than the column.
- **Two concurrent reparents cannot close a ring between them.** This is why the
  guard is in the statement: read-then-write leaves a window where each caller
  sees a clean tree and the pair closes the ring. Raced, asserting the TREE.
- **Five mutations, and two of them corrected the work.** Dropping the guard
  fails both cycle tests; dropping the depth half fails the depth test; a
  no-op `is_active` write fails the reactivation test; removing the service's
  refusal of a double answer fails its test. The fifth mutation found the
  in-memory fake LYING — it answered NotFound for an unknown id where the real
  statement produces the refusal, so removing the service's id resolution
  changed nothing in the unit tests while it would have turned every missing
  category into "that move is refused" against a database. The fake was
  corrected to answer what the repository answers.
- **The depth bound's cost is asserted, not described**: in a tree deeper than
  the bound a legitimate move is refused too.
- **A description still cannot be cleared**: the COALESCE pattern's known limit,
  the same one `UpdateProduct` documents.
- **Nothing walks the tree yet.** Every category read goes one level, so a ring
  loops nothing today. The guard is written for the read that does not exist
  yet, because by the time it does the rings would already be stored.

## Rejected

- **Checking the cycle in the service.** Two copies of one rule, and the copy
  that runs first is racy: two moves can pass their checks and close the ring
  together.
- **Refusing reparenting altogether.** A cheap write, and a shape nobody could fix.
- **A materialised path or closure table.** It answers the ancestry question
  without recursion and would serve a future descendant filter too, but it is a
  schema with its own write path to keep consistent — a bigger decision than the
  one this record makes.

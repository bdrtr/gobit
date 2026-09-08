# ADR 0010 — Warehouse selection: coverage is a CONSTRAINT, preference is an ORDER

**Summary:** Warehouse coverage is a CONSTRAINT and warehouse preference is an
ORDER; the location model lives in fulfillment's own schema. A location that
cannot cover the order is never ranked, so preference can never override
availability.

- **Status:** Accepted
- **Date:** 2026-09-02
- **Phase:** after 10 (the multi-warehouse round)

## Context

Multiple warehouses have been supported since Phase 6: an order's lines can be
split across different warehouses. The seam is divided across two modules and
the division was deliberate — "which warehouses have enough stock" is a **fact**
(the stock module), "which one do we ship from" is a **decision** (the
fulfillment module).

The decision had a problem, and `README.md` wrote it down among the known
limits:

> **Warehouse selection carries no POLICY.** Multiple warehouses are supported
> (the candidates come from the stock fact, the fulfillment module makes the
> choice) but today's rule is "the candidate with the smallest identifier":
> proximity, cost and stock distribution CANNOT BE EXPRESSED, because the module
> has no location model.

That is, the decision was in the right place but there was no data to decide
with. The operator could not say "ship from the Istanbul warehouse first" or
"send European orders out of the Germany warehouse"; the lexicographic order of
the identifiers settled the outcome.

This ADR records the decision that brought that data, and the three new traps
that came with it.

## Decision

### 1. The location model is in fulfillment's OWN schema

Two tables were added (`shipping_locations`, `shipping_location_regions`). The
warehouse identifier belongs to the stock module, it is **opaque and is not a
foreign key** (Principle 2.2). Holding a foreign identifier this way is not new
— `shipping_options.region_id` does the same — but that identifier being the
**primary key** is a new pattern, and its justification stands at the head of
the migration: a policy row has no existence independent of its warehouse.

The module **does not copy names or addresses**. Where the warehouse is is the
stock module's data; what stands here is only the shipping quality.

### 2. The policy's input is the cart's REGION

`checkoutPlan` already carried the region. Because of that:

- The storefront contract (`POST /store/v1/carts/{id}/complete`) **did not
  change** — the ban on letting the customer choose a warehouse was preserved.
- **No personal data entered the execution record**; the delivery address was
  deliberately not carried (plan Section 8).
- The cart snapshot did not change.

"Proximity" in this system is **not** geographic distance, it is shipping region
coverage. Warehouses have no coordinates and none were made up.

### 3. The surface returns a PREFERENCE ORDER, not a single location

`SelectLocation(ctx, candidates) (string, error)` was removed;
`RankLocations(ctx, destinationRegionID, candidates) ([]string, error)` took its
place. There are two changes at once and each has its own reason.

**The region parameter** breaks a promise written in the godoc: the old text
said "the policy grows richer INSIDE this method; the signature the caller sees
does not change". The promise was wrong, and where it was wrong is measurable:
what was missing was not only the warehouse itself, it was WHERE the shipment
was going. The second cannot be obtained by growing richer inside the module —
it is in the caller's hands.

**Returning an order** is a cost decision. The caller tries the next one after a
warehouse runs out; with the old surface that meant asking the policy again on
every stock-out and reading the same policy records again — N queries instead of
one for a line with N candidates. Because the order is deterministic, those N-1
calls were already producing the same answer.

A side gain: the termination of the cart flow's loop no longer depends on what
the module returns. Termination used to depend on the chosen candidate being
removable from the list — that is, on the module not stepping outside the
candidate set; now it is bounded by the length of a finite slice.

### 4. The rule: ELIMINATE, RANK, BREAK THE TIE

1. **Elimination** — if at least one region is bound to a warehouse and the
   destination region is not among them, the candidate drops. A warehouse with
   no binding at all serves **every** region.
2. **Ranking** — the remaining ones are lined up by priority; the smaller one
   goes first. A warehouse with no record is at priority zero.
3. **Tie breaking** — at equal priority the smaller identifier goes first.

If there is no policy record at all, the result is the third step alone: **the
selected warehouse** is exactly the same as the behavior before this change. The
strict alternative (a warehouse with no policy cannot be a candidate) would, the
day it was switched on, stop every order of every existing installation.

The two things that do not stay the same are written in "Consequences", and both
of them affect an installation with no records too: one SQL query per line, and
the code of stock reservation failures.

### 5. If elimination produces an empty set the kind is Conflict, the code is SEPARATE

The new code is `fulfillment_no_serviceable_location`; it is separate from the
code for the "no candidates at all" state because the work the operator has to
do is separate too — in one there is no stock, in the other the region coverage
has been set up wrong.

The justification for the kind being Conflict is **not** the caller's branching:
the cart flow passes a selection error upwards without looking at its kind. The
real ground is twofold and both halves are measurable:

- While wrapping a step failure the cart flow **inherits the kind** and the HTTP
  status comes from there. Had Invalid been chosen, a fault caused by the state
  of the world would have become a 422 telling the customer "fix your body".
- The engine's default retry predicate **does not retry** `KindConflict`, it
  **does retry** `KindInternal`. Had Internal been chosen, a configuration error
  the operator has to fix by hand would be mistaken for a transient fault and
  repeated the day compensation retries were switched on.

### 6. The cart flow preserves the underlying error's CODE

The place that wraps a step failure was overwriting the code with its own
constant; now the underlying error's code is inherited and
`checkout_workflow_reservation_failed` is only a fallback for an error with no
code.

This is a second decision taken in this round and it is a **precondition** of
this feature. The transport layer writes a single machine-readable field into
the body; had the code been overwritten, a wrongly set up region binding would
be reported as "stock could not be reserved" with full shelves, and the operator
would not find the place they have to look at. The pattern is not new: the
engine had fixed the same fault in its own wrapping a round earlier, and its
justification is written there, measured against the B2B spending limit.

### 7. The declared-location path DID NOT CHANGE

If the caller declares a location the policy does not run at all and no module
is asked. A declared location is not a preference but an instruction.

## Consequences

**Positive**

- The operator can express the preference order and the service coverage; the
  lexicographic order of the identifiers does not settle the outcome.
- Reading the policy is a single query per line and the order is computed once.
- An order that drops because of elimination **can be told apart** from
  insufficient stock. What carries the distinction is THE CODE and that is the
  only thing that reaches the storefront; the message is the same in all three
  cases, because the transport layer writes only the outermost message into the
  body. The text containing the candidates' region breakdown **is in the server
  log and in the execution record**, that is, its reader is the operator.
- Backward compatibility is complete for the SELECTED WAREHOUSE on installations
  with no records, and it is pinned by a test. It is not complete for the error
  CODE (see Decision 6), and one SQL query per line has been added — the old
  selection was a pure function and did not touch the database at all, so even
  an installation with no records can now see a fault on this path.

**Negative — accepted prices**

- **A wrong region binding closes the shop.** Binding a region identifier that
  does not exist (or deleting a region and reopening it under the same name —
  the new record gets a new identifier) eliminates that warehouse on every cart.
  On a single-warehouse installation the result is that every completion is
  refused with a full catalog.

  The weight of the price does not end there: the completion flow's idempotency
  key derives from the cart identifier and a failed execution cannot run again
  with the same key. So a cart that drops because of elimination is
  **permanently** burned; the customer has to open a new cart. This burning
  existed before too, but its trigger was a stock fact; now a single admin write
  can trigger it as well.

  In exchange: the fault is visible and the way back is a single admin write.
  But the LIMIT of that visibility has to be written down too: the storefront
  client sees only the code (`fulfillment_no_serviceable_location`), and the
  message containing the candidates' region breakdown stays in the server log
  and in the execution record. So the way back depends on the operator being
  able to reach the log or the execution record.

- **A binding is not a PREFERENCE but a CONSTRAINT, and it narrows the fallback
  set.** An operator who binds two warehouses to separate regions has accepted
  that the order drops when the first warehouse's stock runs out in the race —
  whereas without a policy that order would have gone out of the other one.
  "Prefer Istanbul but ship from Ankara if it runs out" is not written with a
  region binding, it is written with **priority**. A region binding is correct
  only for "this warehouse cannot ship there".

- **Deleting the last binding does not hide the warehouse, it opens it to every
  region.** The rule is the same as the sales channel scope's and it carries the
  same trap; the asymmetry is this: there a wrong scope hides a product, here it
  drops the order.

- **The admin listing can show orphan rows.** A policy row left behind for a
  warehouse deleted in the stock module can never be **selected** but is
  **visible** in the listing; because it carries no name and no address, it
  stands on the screen as an opaque identifier that cannot be resolved.

- **`fulfillment:write` can now stop the order path.** The scope vocabulary did
  not change; the justification is in "Rejected alternatives".

- **A breaking change.** One method of the `fulfillment.interop` surface changed
  in name and in signature; it affects code that embeds it. The one seam the
  compiler does not check is where the interface is resolved from the container
  **by name**, and its only proof is the end-to-end test.

## WHAT CANNOT BE EXPRESSED

What the surface does not guarantee is as important as what it does:

- **Stock distribution.** "Put the warehouse with the most stock first" cannot
  be written. There are two reasons: the sellable count in the location
  breakdown is not on the stock module's primitive surface, and adding it there
  touches the decision not to leak the location breakdown to the storefront; the
  second and heavier one is that it changes what determinism rests on — the
  policy is **the operator's setting** and its changing is an expected
  consequence, whereas stock is a fast-changing fact and the same defense does
  not work there.
- **Cost.** There is no tariff model between a warehouse and a carrier; had one
  been written, the data it rested on would be made up.
- **A decision at the order level.** The order is asked for per line and the
  surface does not see the whole cart; "take all the lines out of a single
  warehouse" or "reduce the number of shipments" cannot be expressed.
- **A preference per (warehouse, region) pair.** Priority is per **warehouse**.
  "A first for R1, B first for R2" cannot be written; the only thing writable
  per region is exclusion.

## Rejected alternatives

**Adding the location detail to the stock module's primitive surface.** This was
the path that needed the least code and it would have preserved the single
source of truth. Rejected because that surface's godoc has closed the door in
writing: it does not carry the question "which warehouse do we ship from", that
is a shipping decision. Opening the door would have made the stock query depend
on the shipping policy — the very division this ADR is trying to protect.

**Opening the warehouse as a second entity in the Query layer.** The read path
would have gone through a single point and filtering would have come for free.
Rejected because the stock module's admin surface puts the boundary "no location
breakdown leaks to the storefront" in writing, and a second entity would have
opened a new read path touching that boundary.

**Making the region binding a ranking key and putting the strict cut behind a
flag.** The serving warehouses would be lined up first and the non-serving ones
last; the "no serviceable warehouse" error would never occur while there is
stock. Rejected because it breaks the concept: "the regions it serves" is not a
preference but the carrier's coverage area, and shipping outside the coverage is
not a graceful fallback but an impossible shipment. Preference can already be
expressed — with priority. Loading two concepts onto one field would make it
impossible to ask the operator which of them they had written. Its price is
written by name under "Negative".

**Giving policy writing a third scope (`fulfillment:policy`).** This endpoint's
blast radius really is wider than the module's other write endpoints. Rejected
because the scope vocabulary derives from a single rule (`<module>:read` /
`<module>:write`, `admin` as the super scope) and hundreds of admin endpoints
are checked with that rule. A name special to one single endpoint would make the
rule unlearnable and unauditable; the gain would be limited, because an identity
carrying `fulfillment:write` is already an admin identity.

**An environment variable that turns the policy off.** A precedent can be looked
for in the repository, but what is found is not exactly this: why a switch
turning the b2b module off was NOT ADDED is written in `CHANGELOG.md` ("a switch
accidentally set to `false` would remove the spending limit without producing
any error"), and that justification **cannot be carried here directly**: the
flag there would make a protection fail open, whereas the one here would remove
a rule that drops orders — the opposite direction. ADR 0007 speaks not of a flag
but of BEHAVIOR under failure; ~~in ADR 0009 the subject does not come up at
all~~ **Corrected 2026-09-07: it comes up there in exactly this form.** ADR
0009's rejected alternatives carry an env-var-toggled "multi-tenant mode",
refused in the same words as the b2b switch and falling to the same SIDE as it —
an installation left "off" would read a database full of tenant columns without
a filter, a protection failing open. (ADR 0009 credits those words to ADR 0007
rather than to the `CHANGELOG.md` entry they are in; the reading above is the
one that checks out.) So the hunt finds a second precedent rather than a gap,
and the second lands where the first did: neither of them covers a flag that
would remove a rule which DROPS orders.

The flag was still not added, and the reason comes from its own criterion: the
way back is already in the admin API and the fault carries its own code.
A flag opens a second path doing the same job, and which of the two paths holds
gets asked in the next round. **The precondition of this rejection is "Decision
6"**: saying "it is undone from an admin endpoint" for a fault whose cause is
invisible would be an empty phrase.

**Soft delete.** The module's rule is soft delete. Rejected because the effect of
a soft-deleted policy row is exactly identical to that of a row that never
existed (both mean "the default") and the distinction would carry no meaning;
furthermore, because the primary key is the warehouse identifier, a dead row
would block a new policy from being written for the same warehouse.

## Reopening the decision

Three pieces of data reopen this decision:

1. **If warehouses get coordinates**, proximity really becomes computable and
   "coverage" and "distance" become two separate rules. Today's elimination
   could turn into a ranking input on that day.
2. **If the sellable count in the location breakdown enters the stock module's
   surface**, stock distribution becomes expressible; the question to be
   answered that day is what determinism will mean.
3. **If a "a wrong binding closed the shop" incident actually happens** — its
   measure is `fulfillment_no_serviceable_location` being seen in production —
   validating the region identifier on the write path (asking the region
   module's surface) gets reconsidered. It was not done today because breaking
   the module's structure of never asking another module anywhere, for a single
   validation, is a decision that does not make itself pay its own price.

## Related

- [ADR 0001](0001-modul-arasi-iletisim.md) — narrow interface + resolution by
  name; where this ADR's signature change is left without a compiler.
- [ADR 0004](0004-query-veri-erisimi.md) — the ground of the second rejected
  alternative.
- [ADR 0006](0006-workflow-modul-erisimi.md) — how the cart flow reaches the
  modules.
- [ADR 0007](0007-sertlestirme-arizada-davranis.md) — the decision that behavior
  under failure is not uniform. The environment variable rejection IS NOT
  DERIVED from there; ADR 0007 does not speak of flags, it only decides failure
  behavior per component.
- [ADR 0009](0009-cok-kiracililik-kurulum-siniri.md) — the installation boundary
  decision; this ADR's assumption that "every installation is single-tenant" is
  what makes it possible for warehouse policy too to live at installation level.

# ADR 0033 — Erasure is a synchronous sweep that returns an answer

- **Status:** Accepted
- **Date:** 2026-09-06
- **Phase:** after the roadmap

## Context

[ADR 0029](0029-the-embedder-is-the-data-controller.md) made the embedding
application the data controller and left gobit three obligations: a contract
whose outcomes are `DELETED`, `ANONYMIZED` and `RETAINED`, hooks so a holder of
personal data can be asked, and a declaration of what each holder keeps.
[ADR 0032](0032-an-issued-invoice-refuses-erasure-in-the-schema.md) supplied the
first module's answer. What was left was the mechanism, and `docs/gaps.md` said
the only thing standing in front of it was B8 — "the customer module publishes
no event at all, so nothing can learn an erasure happened".

**That framing was one step off, and measuring it is what produced this
record.** The question is not how a holder LEARNS about an erasure. It is how
the controller gets an ANSWER: ADR 0029's first obligation is an outcome per
holder with a reason attached, because a controller who has been asked to erase
somebody has to reply to that person. An announcement carries no reply.

Three measurements decided the rest.

**The bus cannot carry an answer.** `core/eventbus`'s own package doc records
that `Publish` does not wait, that a handler's error never reaches the
publisher, that the in-memory backend is at-most-once and loses the event if the
process dies, and that no backend retries. Whatever an event-driven erasure did,
the caller would learn none of it.

**The audit that would police such a topic counts to one.**
`TestTheEventTopicsHaveASubscriber` requires a published topic to have a
subscriber and is satisfied by the first one it finds. A topic named
`customer.erased` with a single listener is green while every other holder of
personal data never hears it. The gate cannot make the event honest.

**The subject is not a customer row.** The `invoices` table has no `customer_id`
and no `order_id` column at all — the only handle on a person is the address
printed on the document — and an order placed by a guest carries an `email` with
a NULL `customer_id`. Any design keyed on a customer identifier is unable to
reach either.

## Decision

**An erasure is a synchronous sweep across every holder of personal data, and it
returns one report.**

**1. The vocabulary is published as `core/erasure`.** ADR 0029 deferred this
question to whoever built the mechanism; the amendment on
[ADR 0026](0026-the-published-surface-is-fourteen-packages.md) carries the
argument and the fifteenth table row. Only the vocabulary is published —
`Subject`, `Outcome`, `Result`, `Report`, `Holding`, `Declaration` and the two
optional one-method interfaces `Eraser` and `Declarer`. The sweep, the
transactions, the HTTP surface and the schema refusals stay internal.

**2. `Subject` carries several identifiers and requires none in particular.**
A holder uses whichever one it can resolve. This is forced by the measurement
above, not chosen for generality.

**3. No erasure event ships, and B8's literal wording is not delivered.** The
hook ADR 0029 asked for is the interface, and it is a better hook than an
announcement because it returns an answer. Wiring an event to the customer
module's delete path would also have made gobit choose POLICY: a shop deleting a
customer record is bookkeeping, an erasure is a legal answer to a request, and
ADR 0029 puts that judgement with the embedder. `docs/gaps.md`'s B8 row is
rewritten rather than left looking satisfied.

**4. The surface is bound at the composition root**, as `POST /admin/v1/erasure`
and `GET /admin/v1/erasure/personal-data`, under the scopes `erasure:write` and
`erasure:read`. It is not on the customer module. No module owns this sweep;
binding it there would make the customer module publish the invoice module's
retention decision, and the path `/admin/v1/customers/{id}/erasure` would assert
something measurably false — that the subject has a customer record.

**5. Each holder's answer, and the reasoning is per module rather than one
rule.**

| holder | outcome | why |
| --- | --- | --- |
| customer | `ANONYMIZED` | it cannot answer `DELETED`: orders and carts carry the customer id as a bare TEXT column with no foreign key (Principle 2.2), so removing the row would orphan them silently |
| order | `ANONYMIZED`, and `RETAINED` while the order is unsettled | ADR 0032's three reasons for the invoice are measurably absent here — see below |
| invoice | `RETAINED` | ADR 0032, and the refusal is now enforced by the schema |

**6. gobit never rewrites a free-form column, and an `ANONYMIZED` result must
say so.** ADR 0029 places the judgement of whether a `metadata` jsonb holds
personal data with the controller, and a framework that read customers' free
text in order to classify it would have taken that judgement back. So the named
columns are overwritten, the open ones are left, and `Result.Kept` lists what
was left with `Result.Why` explaining it. Without that rule the word
"anonymized" would cover a field nobody looked at.

**7. The three stores outside the module tree answer too.** ADR 0029's
declaration obligation is written in terms of modules, and taking it literally
would have produced a report that is true of every module and false about the
installation. `internal/core/workflow/pgstore`, `core/audit` and `core/link`
hold personal data and cannot be reached by walking the module registry. The
saga store answers for ITSELF — it is resolved from the container by name and
erases its own rows (see the amendment below); `core/audit` and `core/link`
answer `RETAINED` from a static holder, each with its reason. Every erasure report therefore carries the
gap in writing rather than leaving it to a document nobody reads while answering
a data subject.

**8. A holder that DECLARES personal data and offers no erasure still appears in
the report, as `RETAINED`.** This is what makes the answer complete by
construction rather than by diligence: adding a declaration to a module makes it
visible in every sweep from that moment, with no second list to maintain and
nothing to forget. Without it the mechanism would produce exactly the failure it
exists to prevent — a report that is true about everything it mentions and
silent about the rest. It is also why `Declarer` is worth implementing on a
module that can never erase: the review module cannot resolve a subject, and
declaring is how it stops being invisible.

**9. A failure is not a fourth outcome.** The sweep does not stop at the first
error — stopping would leave MORE of the person's data behind — so the rest are
asked, the successful answers stay in the report, and the returned error names
every holder that failed. Folding a fault into `RETAINED` would make a broken
query indistinguishable from a lawful refusal, which is the exact confusion the
third outcome exists to end.

## The declaration text is English even in a Turkish file

ADR 0012 makes language a property of the FILE, and three of the modules that
declare their holdings are still Turkish files. Their declaration strings are
nevertheless English, and the rule is worth stating because it is the first
place the language ratchet meets a published contract: **the `Why` text is DATA
that crosses into `core/erasure` and is read by the embedder, not prose about
the code.** One report assembles the sentences of every holder, so a per-file
rule would produce an answer to a data subject written in two languages. The
existing precedent points the other way — the OpenAPI descriptions are emitted
in the file's language — and it is not followed here for that reason: a schema
description is read by a developer looking at one module, while this text is
read by a controller answering a person.

## Why the order module is not the invoice

This is the part a future reader is most likely to get wrong, because the
invoice precedent looks like it should transfer. It does not, and each of
ADR 0032's own three reasons fails to reach the order.

- **ADR 0032 rejected "redact the person in place" because it would supersede
  ADR 0024's clause that an issued document has "no update path for its amounts,
  its parties or its lines".** No ADR gives an order such a clause, and the
  schema says the opposite in both directions: the order's address columns are
  nullable TEXT with no CHECK, while `invoices.buyer_name` is `text NOT NULL`.
- **ADR 0032 rejected "keep it whole for a window" because that needs a
  retention setting, and under ADR 0029 the window is the embedder's number.**
  `RETAINED` has to carry a WHY. For the invoice that why is a fact gobit owns —
  the numbered series ADR 0024 protects. For a shipping address the only why on
  offer is "an order is a commercial record", which is a retention judgement and
  the FIRST item on ADR 0029's list of what gobit does not owe.
- **ADR 0032 rejected "refuse, and leave the refusal a policy" because it leaves
  the live hazard open.** `RETAINED` for the order is that, with no compensating
  guard anywhere.

The evidentiary argument also runs the other way now. ADR 0032 has made the
invoice undeletable, so the document a court reads survives the order's
anonymization by construction; the order's address was never the accounting
record's source.

## The one carve-out: an unsettled order

While an order is still moving, the order module answers `RETAINED` and names
which fact made it so — a `pending` status, an open return, exchange or claim,
or a non-zero outstanding amount. That is a fact gobit OWNS, held in the order
module's own tables, and it is not a period: it ends when the order settles.
It is the honest reading of the migration header's own concern that an order
without its address cannot be shipped or invoiced.

## Rejected alternatives

**An event-driven cascade.** Rejected on the three measurements in the Context.
It is worth naming what it would have looked like on a good day: every holder
subscribes, each erases its own rows, and nobody can say whether it worked.

**Adding the interfaces to `core/module`.** Cheaper on paper — that package is
already published, so no new directory and no ADR amendment — and that is
exactly what disqualified it. The published-surface audit compares DIRECTORIES
under `core/` with a hand-written list, so this route would have created a
permanent compatibility promise while touching no entry in that list and
requiring no justification anywhere. Publishing is meant to be an edit that
shows up in the diff next to its reason. Secondarily, `module.Module` is the
mandatory four-method contract every module satisfies and an optional capability
does not belong on it.

**One interface instead of two.** `Eraser` and `Declarer` are separate because a
holder can honestly have one and not the other. The review module stores the
byline an author typed and deliberately stores nothing that says which person
that is (the argument is in gap A15) — so it can declare what it keeps and
cannot resolve a subject. A single interface would force it to lie or stay
silent.

**Blanking every `metadata` column.** It would make `ANONYMIZED` unconditionally
true and would destroy embedder data on a guess about its contents, which is the
controller's judgement, not gobit's. Naming what was left is the honest form.

## What this deliberately does NOT do

- ~~**It does not prune `workflow_executions.input`.**~~ **Built 2026-09-07, and
  the sentence this replaces was wrong about the reason as well as the fact.**
  It said a RUNNING execution needs its input to compensate and a terminal one
  does not, so pruning would have to be status-scoped. Measured: the engine
  copies the stored input into the recovery step context and NOTHING reads it
  back (zero uses of `StepContext.Input` across `internal/workflows`,
  `internal/modules`, `plugins` and `core`), the five compensations of the only
  workflow that runs here read `cart_id`, `amount` and `currency_code` and
  nothing else, and terminal executions are answered from `output` and `failure`
  without touching the input at all. The ONE genuine reader is
  `gobit recover <id> -confirm`, which rebuilds the compensation chain from the
  input and refuses a plan with no cart id.
  So the erasure EDITS the input instead of dropping it: `email` becomes empty
  and both addresses become JSON null, and everything a recovery reads stays.
  A status filter is not needed and would have been expensive to get right —
  measured, there is no `IsTerminal` anywhere in the engine and `status` is free
  text with no enum behind it, so "terminal" would have been a hand-kept list in
  Go that the database does not enforce. The store answers `ANONYMIZED` now,
  from `internal/core/workflow/pgstore`, which owns those two tables and
  therefore owns the statement.
- **It does not give the review or auth modules an eraser.** Review cannot
  resolve a subject at all. Auth's subject is a staff member, a different class
  of data subject from a shopper, and sweeping it from a shopper's request would
  delete an administrator.
- **It does not set a retention period.** Under ADR 0029 that number is the
  embedder's, and `RETAINED` means "kept" until the embedder says otherwise.
- **It does not make an unwired embedder compliant.** ADR 0029 already accepted
  this: gobit ships a mechanism that can be left uncalled, and a shop that never
  calls it has a legal problem the framework cannot see.

## Consequences

**Positive**

- **The controller gets something to answer with.** A report naming every holder,
  what it did, and — where something was kept — what and why.
- **The declaration is reachable before there is any data.**
  `GET /admin/v1/erasure/personal-data` answers from the code, so an embedder can
  write its privacy notice from it on an empty installation.
- **An embedder's own module joins the sweep for free.** The capabilities are
  found by type assertion over the module registry, and `core/plugin`'s
  `Host.AddModule` puts a plugin's module into the same registry — so gobit's own
  modules, the ones plugins bring and the ones the embedding application adds are
  all reached by one walk.

**Negative, and accepted**

- **The declaration is only as true as the modules keep it.** ADR 0029 wrote this
  down as an audit the repository did not have; it still does not have it in the
  form that would close it — nothing yet proves that a module which grows a
  personal column also declares it.
- **A partial sweep is possible and is reported as an error.** An operator who
  ignores the error will tell a data subject the work is done when it is not.
- **`RETAINED` now has two flavours in practice** — a refusal somebody decided on
  (the invoice) and a copy nobody has built the pruning for (the saga store) —
  and only the `Why` text tells them apart. That was accepted because the
  alternative was to leave the second one out of the report entirely.

## Amendment: the saga store erases its own rows (2026-09-07)

The Consequences below recorded the saga store's copy of the checkout input as
the first thing to close. It is closed, and three things were measured on the
way that are worth more than the change itself.

**The reason the original entry gave was wrong.** It said a RUNNING execution
needs its input to compensate, so pruning would have to be scoped to terminal
executions. The engine's recovery path copies the stored input into the step
context and nothing reads it back — zero uses of `StepContext.Input` across
`internal/workflows`, `internal/modules`, `plugins` and `core` — and the five
compensations of the only workflow that runs here read `cart_id`, `amount` and
`currency_code`. Terminal executions never touch the input at all.

**The one real reader is an operator command, not the engine.**
`gobit recover <execution-id> -confirm` rebuilds a half-finished saga's
definition FROM the record's input and refuses a plan with no cart id
([ADR 0017](0017-recovering-abandoned-sagas-from-the-record.md)). That single
fact is what chose the shape: the erasure EDITS the input rather than dropping
it. `email` becomes empty, both addresses become JSON null, and every field a
recovery reads survives. Trading a recoverable checkout for a privacy answer
would have been a bad bargain in both directions, since the person is gone
either way.

**A status filter is not needed, and would have been a hand-kept list.** There
is no `IsTerminal` anywhere in the engine and `status` is free text with no enum
behind it, so "terminal" could only have been a slice in Go the database does
not enforce. Once the fields a recovery actually reads were measured, the
distinction stopped buying anything.

**Where it lives.** In `internal/core/workflow/pgstore`, which owns those two
tables — the same rule that keeps a module's SQL inside the module. The
coordinator resolves it from the container by name, which is what its
`*container.Container` parameter is for; when the store is absent the
declaration-only stub still answers, and
`TestTheSagaStoreIsReachedFromTheRealCompositionRoot` fails the day the real
root stops reaching it, so the stub cannot ship silently.

**A correction to the declaration, too.** The coordinator used to declare both
`output` columns as possibly personal. They are not: the execution's output and
every step's output are identifiers and amounts. Declaring a column that holds
nobody sends a controller looking in the wrong place, so the store's own
declaration names `input` and the two free-text `failure` columns and stops.

**And one self-correction worth keeping.** The first version of the statement
opened with `jsonb_typeof(input) = 'object'`, defended by the sentence "the `||`
operator raises on a scalar". Measured against PostgreSQL 16, it does not — and
the guard could not fire anyway, because `->>` on a non-object returns SQL NULL
so the row is never selected. The guard was removed rather than kept as
harmless: a protection that cannot fire, defended by a claim that is not true,
invites the next reader to trust it. It was found by a mutation that failed to
bite.

## Reopening the decision

Reopen if an erasure ever needs to run without a caller waiting for it — a sweep
over a backlog, or a holder whose erasure is genuinely long-running. The
synchronous shape is chosen for a request made by a person and answered to that
person; it is not a claim that no erasure could ever be asynchronous. The answer
would then be a job that writes a durable report, not an event, for the reason in
the Context.

## Related

- [ADR 0029](0029-the-embedder-is-the-data-controller.md) — the controller
  decision this one implements, and the source of the three obligations.
- [ADR 0032](0032-an-issued-invoice-refuses-erasure-in-the-schema.md) — the
  invoice's answer, and the three rejected alternatives that decide the order's.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the
  published-surface rule, amended by this work with a fifteenth package.
- [ADR 0024](0024-an-invoice-number-comes-from-a-row-not-a-sequence.md) — the
  immutability clause that binds the invoice and not the order.
- [ADR 0006](0006-workflow-modul-erisimi.md) — why a decision spanning modules
  lives in a flow.
- [ADR 0017](0017-recovering-abandoned-sagas-from-the-record.md) — why the saga store keeps an
  execution's input.

# ADR 0032 — An issued invoice refuses erasure, and the refusal lives in the schema

- **Status:** Accepted
- **Date:** 2026-09-06
- **Phase:** after the roadmap

## Context

[ADR 0029](0029-the-embedder-is-the-data-controller.md) makes the embedder the
data controller and gives gobit the job of publishing an erasure contract whose
outcomes are `DELETED`, `ANONYMIZED` and `RETAINED`. This ADR answers what the
invoice module returns when that contract reaches it, and it exists as a
separate record because the answer is not a policy — it is a constraint.

**What the invoice holds about a person, measured.** `invoices` carries six
`buyer_*` columns: `buyer_name` (NOT NULL, no default) and five siblings that
default to the empty string — migration `000003` has since added a seventh, the
derived `buyer_email_folded`. On the SAME row sits a `metadata` jsonb the caller
fills and nothing validates, and a `status_reason` an operator types when a
document is canceled or re-sent, and every `invoice_lines.description` is caller
text too. ~~So the person is in eight places, not six, and two of them are
free-form.~~ **Corrected 2026-09-07: TEN places, and THREE of them are
free-form.** The two the count of eight missed arrived by different routes.
`status_reason` was overlooked: `000001_invoice_init` created it with the table
and the admin API fills it from the caller's own `reason`, so it was there to be
counted on the day this was written. `buyer_email_folded` is a genuine later
arrival — migration `000003` added it on 2026-09-07 with
[ADR 0038](0038-the-storage-form-of-an-e-mail-is-one-rule.md), and `module.go`'s
own declaration has not caught up with it yet. The module's erasure code has put
`status_reason` where it belongs, beside `metadata` and
`invoice_lines.description`, in the list of columns whose content the EMBEDDER
controls.

**What stops a deletion today is the absence of code, not a constraint.** A
case-insensitive search for "delete" over the module returns exactly one line,
and it is the `ON DELETE CASCADE` on `invoice_lines`. The nine named queries in
`queries/invoice.sql` and the five in `queries/series.sql` contain no DELETE; the
repository exposes eight methods and none deletes; the five admin routes contain
no DELETE. There is no trigger and no rule in the migration — only CHECK
constraints, two foreign keys and indexes, and no procedural code anywhere in
any migration in this repository.

**So the hazard is live rather than hypothetical.** An erasure implementation
that issues `DELETE FROM invoices WHERE ...` succeeds, and puts the hole in the
numbered series that [ADR 0024](0024-an-invoice-number-comes-from-a-row-not-a-sequence.md)
exists to prevent — that ADR's own words are that a canceled document "keeps its
number and stays in the table. Deleting it would put the hole in from the other
end."

## Decision

**An issued invoice is outside erasure, and the database says so.**

The erasure contract returns `RETAINED` for an invoice, naming what was kept.
The embedder, as controller, decides how long "retained" lasts and answers the
data subject; gobit's job is to make the retention TRUE rather than merely
intended.

**The refusal is enforced in the schema and not only in the module**, because a
refusal that lives in Go stops the caller while a refusal that lives in the
database stops the STATEMENT. The distinction is the whole finding above: the
module has no delete path today and the hazard exists anyway, because the next
implementation is one statement away and nothing is watching.

## The open sub-question: which schema mechanism

This ADR fixes WHERE the refusal lives and deliberately leaves HOW to whoever
writes the migration, because the two candidates differ in a way this repository
should weigh rather than default:

- **A `BEFORE DELETE` trigger that raises.** It carries a message, so an
  operator sees why. Its price is that it would be the FIRST procedural code in
  any migration in this repository — measured, there is none today — and
  `plpgsql`, while installed by default on every supported image and needing no
  `CREATE EXTENSION`, is nonetheless an extension by the catalog's own reckoning.
  ADR 0015's "extensions: none" row is evidenced by a grep for `CREATE
  EXTENSION` returning nothing, which a trigger does not change; the contract
  survives, but the claim should be re-read by whoever writes it.
- **`REVOKE DELETE ON invoices` from the application role.** No procedural code,
  no new construct — a grant. Its price is that the error is a bare permission
  denial with no explanation, and that the repository does not manage database
  roles today, so the migration would introduce role management as a concept.

Neither is obviously right. The trigger buys a readable failure; the revoke buys
no new kind of code. Whoever implements it says which, and why, in the migration.

## The three free-text fields

~~`metadata` and `invoice_lines.description` are caller-supplied and
unvalidated, so gobit cannot know whether they hold personal data in a given
deployment.~~ **Corrected 2026-09-07: there are three.** `status_reason`,
`metadata` and `invoice_lines.description` are caller-supplied and unvalidated,
so gobit cannot know whether they hold personal data in a given deployment.
Under ADR 0029 it does not have to. The rule is one sentence and it is published
rather than enforced:

> An embedder that writes personal data into an invoice's `status_reason` or
> `metadata`, or into a line's `description`, retains it under the same refusal
> as the named columns, and answers for it as controller.

Validating those fields was rejected: a framework that inspects free-form
customer data to guess whether it is personal has taken on exactly the
controller's judgement that ADR 0029 places elsewhere.

## Rejected alternatives

**Refuse, and leave the refusal a policy.** Costs nothing in the module and
leaves the live hazard open — the next erasure implementation still succeeds.
Rejected for that reason alone.

**Keep the number and the amounts, redact the person in place.** It would
supersede ADR 0024's clause that an issued document has "no update path for its
amounts, its parties or its lines", and it must answer what happens to the three
free-form fields, which is the part it cannot answer without inspecting them. It
also gives up the property that makes an invoice evidence: a document whose
buyer can be rewritten is a document a court reads differently.

**Keep it whole for a window, then expire the whole `(prefix, year)` cohort.**
The only candidate that needs a retention setting, and under ADR 0029 the window
is the embedder's number rather than gobit's — so this is a mechanism gobit
could publish later, not a policy it can adopt now. It also expires documents
belonging to people who never asked for anything, which is a different act from
answering an erasure request.

## Consequences

**Positive**

- **The hazard closes.** The series ADR 0024 protects cannot acquire a hole by
  accident, and the protection no longer depends on nobody writing a query.
- **The contract has something true to return.** `RETAINED` is enforced rather
  than promised, which is what lets an embedder answer a data subject honestly.

**Negative, and accepted**

- **An invoice is now undeletable by the application, including in situations
  nobody has thought of** — a test fixture, a botched import, a wrong-tenant
  document. The escape is a DBA acting deliberately, which is the right shape
  for this class of act but is friction that did not exist before.
- **The refusal has no end date.** This ADR says an issued invoice is retained
  and does not say for how long; under ADR 0029 that number is the embedder's,
  and until an embedder sets one, "retained" means "kept".

## Amendment: the sub-question is answered, and two sentences above were wrong (2026-09-07)

**The mechanism is the `BEFORE DELETE` trigger.** Migration
`000002_an_issued_invoice_is_retained` carries it, on `invoices` AND on
`invoice_lines`, with a `BEFORE TRUNCATE` companion on each and a custom
SQLSTATE rather than `restrict_violation` — 23001 is also what a genuine
`FOREIGN KEY ... ON DELETE RESTRICT` raises, so anyone mapping it onto "this is
the retention refusal" would misclassify a real constraint failure.

**`REVOKE DELETE` was not merely weaker, it does nothing at all here, and that
is a measurement rather than an argument.** gobit's shipped role is a SUPERUSER
— the compose file sets `POSTGRES_USER` to `gobit` and the configuration carries
exactly one `DATABASE_URL`, so the migration role and the runtime role are the
same account. Run against that role: `REVOKE DELETE ON invoices FROM gobit`
succeeds, `pg_class.relacl` visibly changes, and the very next
`DELETE FROM invoices` still reports `DELETE 1`. It leaves a catalog artefact
that LOOKS like protection and stops nothing, which is worse than no guard.

**A second hole was measured and closed.** With `invoices` guarded,
`DELETE FROM invoice_lines WHERE invoice_id = ...` still succeeded, taking the
retained `description` text and leaving the invoice's stored totals standing
against lines that no longer existed. Both tables are guarded now.

**A trigger CAN be bypassed by the role that owns it, and the first draft of
this migration was open to it.** Measured against the applied migration as the
same superuser role: `SET session_replication_role = 'replica'` followed by
`DELETE FROM invoices` removed the document, because ordinary user triggers do
not fire in replica mode. So the escape was a single session-level `SET`, not
the deliberate `ALTER TABLE ... DISABLE TRIGGER` the header had called friction.

**That hole is closed, and closing it was measured both ways.** The four
triggers are declared `ENABLE ALWAYS`, which makes them fire in replica mode
too. On a throwaway table against the shipped container, as the shipped role:

| trigger | `session_replication_role = replica` |
| --- | --- |
| plain | `DELETE 1` — the row is gone |
| `ENABLE ALWAYS` | `ERROR: refused` — the row stays |

What remains is the honest limit and it is worth stating plainly: **the refusal
stops the ordinary statement and every session-level override; it does not stop
the role that OWNS the table from dropping or disabling the trigger.** Both
remaining escapes are deliberate DDL, which is the shape this refusal was meant
to have. A guard the application's own account can still drop is a ROLE
SEPARATION problem, and this repository does not separate roles today — that is
a gap against [ADR 0015](0015-postgresql-cluster-contract.md)'s privileges row,
filed here rather than fixed, because fixing it means the migration role and the
runtime role stop being one account and every deployment path has to say which
is which.

Two lessons, and the second is the one worth carrying:

- **A guarantee stated absolutely is the kind of sentence the next person builds
  on.** The original overstatement was caught because the claim was tested
  rather than read.
- **The argument for NOT doing something has to be true as well.** The draft
  that left `ENABLE ALWAYS` out defended the omission with "this migration has
  already been applied to live databases". The file had never been in a commit.
  A false premise had almost bought a permanent weakening of the guard, and it
  was the cheaper claim to check.

## Reopening the decision

Reopen if a jurisdiction the framework targets requires erasure to override the
retention of a commercial document — that would make the refusal itself illegal
in that deployment, and the answer becomes the redaction candidate above with
ADR 0024's clause amended alongside it.

## Related

- [ADR 0029](0029-the-embedder-is-the-data-controller.md) — the controller
  decision this one implements.
- [ADR 0024](0024-an-invoice-number-comes-from-a-row-not-a-sequence.md) — why a
  hole in the series is the fault being prevented.
- [ADR 0015](0015-postgresql-cluster-contract.md) — the cluster contract the
  trigger candidate has to be read against.

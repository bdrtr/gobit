# ADR 0034 — A person may see what a person may erase

**Summary:** A person may SEE what a person may erase: disclosure is published
beside erasure and assembled by the same sweep. One mechanism serves both
rights, so neither can drift from the other.

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

[ADR 0029](0029-the-embedder-is-the-data-controller.md) makes the embedder the
data controller and gives gobit three obligations: an erasure contract, hooks so
a holder can be asked, and a declaration of what each holder keeps.
[ADR 0033](0033-erasure-is-a-sweep-that-returns-an-answer.md) built the erasure
half. `docs/gaps.md` row B17 has always read "KVKK erasure, export and
retention", and the export third was left open with the reason written down.

**Nothing in the record assigns export to either party.** ADR 0029's list of
what gobit owes is closed and does not contain it; its paired list of what gobit
does NOT owe — the retention period, the lawful basis, the consent text, the
identity of the data subject — does not contain it either. So the question was
open rather than answered, and it was answered by measuring what the code
already does.

**The measurement is one sentence long. gobit can already find this person, and
every one of those searches is wired to a destructive verb.**

- `pgstore` finds her by `input->>'customer_id'` and `input->>'email'`, in order
  to empty the fields.
- The customer module resolves both handles, deliberately including guests, in
  order to anonymize them.
- The order and cart modules resolve both handles, in order to null their
  address columns.
- The invoice module's own godoc calls its buyer-address lookup "the ONLY way
  this module can resolve a person", and it uses it to count what it will
  refuse to erase.

So the framework will destroy this person's rows on request and will not show
them to her. That asymmetry is not a policy the embedder chose. It is a fact
about gobit's code, and every one of ADR 0029's three obligations is a fact
about the code too — which is the side of the division that belongs here.

## Decision

**gobit publishes a disclosure capability beside the erasure one, and a person's
dossier is assembled by the same sweep that erases them.**

**1. A third optional interface**, `personaldata.Discloser`, with one method:
`PersonalDataOf(ctx, Subject) (Disclosure, error)`. It is optional and found by
type assertion, like `Eraser` and `Declarer`, so a holder with no personal data
implements nothing.

**2. THREE states, for the reason the three outcomes exist.** A holder answers
`Disclosed`, `Nothing` or `Unresolvable`. Two states could not tell the two
kinds of empty apart: a holder that searched and found nothing has told the
controller something true, and a holder that CANNOT search has not. The review
module is the case that forces it — it stores the byline an author typed and
deliberately nothing that says which person that is, so a dossier that simply
omitted it would read as "you have written no reviews", which nobody checked.
`Why` is required whenever the state is not `Disclosed`.

**3. A holder that declares and cannot disclose is entered as `Unresolvable`
with the columns it declared.** This is the same construction the erasure sweep
uses, and it is what makes the answer complete without a second list: the gap is
written into the document the person receives rather than kept in a backlog.

**4. A disclosure lists exactly the DECLARED columns.** Reaching past the
declaration would hand over data the declaration told the controller was not
there; stopping short of it would contradict the same document. The two are one
statement made twice, and a holder should derive one from the other rather than
maintain both.

**5. `Field.Kind` travels with the value.** A value from an `Open` column is one
gobit has never inspected, and whoever reads the dossier has to know which
values the framework can vouch for. It rides on the field rather than being
looked up, so it cannot drift from what it describes.

**6. The surface is three endpoints under one root**, bound at the composition
root, with three scopes:

| endpoint | scope | what it is |
| --- | --- | --- |
| `GET /admin/v1/personal-data` | `personal-data:read` | the declaration — a map of tables and columns, about nobody |
| `POST /admin/v1/personal-data/disclosure` | `personal-data:disclose` | one named person's file |
| `POST /admin/v1/personal-data/erasure` | `personal-data:erase` | destroy |

Three scopes rather than two, because these are three different powers: reading
the map is not permission to read a person, and reading a person is not
permission to destroy them. The subject travels in a BODY and never in a query
string, because `core/audit` records `audit_log.path` and declares it personal
data — a subject in the path would write the person into the audit log on every
request made about them.

**7. A partial dossier comes back, and says so.** This is the deliberate
opposite of the erasure handler. A partial erasure reported as a whole one is a
false statement made to a person, so that handler refuses the answer; a partial
disclosure still contains the parts that worked, and those are the person's own
data. Discarding them helps nobody and invites an operator to retry until they
get a green light they will then trust. So the dossier returns with an
`incomplete` field naming what is missing, and a document carrying that field
says out loud what is not in it.

## It is a dossier and not an export

The word matters and it is chosen against the easier one. What comes back is
organised and complete about the columns gobit declared — and it still contains
free-form values the framework never inspects, which may name third parties or
carry anything the embedder put there. It is material a controller REVIEWS
before sending, not a document that can be forwarded unread. Calling it an
export would promise the second, and the promise would be broken by the first
`metadata` blob that mentions somebody else.

## Rejected alternatives

**Build nothing; the declaration is enough.** It says where the data is and the
controller can read it with surfaces that already exist. Measured, they cannot:
a guest — the subject `Subject` was shaped around — is not reachable by the
order, cart or invoice admin surfaces, which take ids rather than a person. The
declaration is a map with no way to walk it. This repository HAS reclassified a
gap into a decision before (B8, in ADR 0033), and the licence for that move was
a measurement showing the obligation was met another way. Nothing meets this
one another way.

**One interface with a verb parameter**, or folding disclosure into `Eraser`.
Refused for the reason `Declarer` is already separate: a holder may honestly
have one capability and not another, and a combined interface forces the ones
that cannot into lying or staying silent.

**Publish the per-holder capability and no coordinator.** It would hand an
embedder an interface they can never iterate: `module.Registry` does not reach
them from outside, and `plugin.Host` keeps its registry unexported. A capability
whose only consumer is impossible to write is not a capability.

**A status code for a partial dossier.** A 207 or a 500 both lose to a named
field: the first is ignored by most clients, and the second throws away the
person's data to report a fault about somebody else's holder.

## What this deliberately does NOT do

- **It does not verify who is asking.** Neither does the erasure endpoint. Both
  sit behind an admin scope and trust the controller, which is exactly where
  ADR 0029 puts the identity of the data subject.
- **It does not disclose from every holder yet.** The first implementers are
  customer, order and cart — the three that already resolve a person cleanly.
  Every other declaring holder appears as `Unresolvable`, so its absence is
  LOUD rather than silent, and closing each one is its own change.
- **It does not redact the free-form values.** ADR 0029 leaves the judgement of
  what a `metadata` blob holds with the controller. This ADR reduces the promise
  to match: a dossier is reviewed, not forwarded.
- **It does not set a format.** There is no CSV, no archive, no machine-readable
  portability schema. The dossier is JSON on an admin endpoint; wrapping it in a
  format a regulator names is the embedder's product decision.

## Consequences

**Positive**

- **The asymmetry closes.** What gobit can find in order to destroy, it can find
  in order to show.
- **The gaps are in the document.** A holder that cannot answer says so inside
  the dossier, with what it holds, so the incompleteness travels with the answer
  instead of living in a tracker.
- **Adding a holder is one method.** The coordinator, the endpoint and the scope
  already exist; a module joins by implementing one interface, and until it does
  it is visibly `Unresolvable`.

**Negative, and accepted**

- **The most concentrated personal data this system can produce now has an
  endpoint.** That is the point of it, and it is why the scope is its own and
  why the subject never appears in a URL. An installation that hands
  `personal-data:disclose` to everyone has made a mistake the framework cannot
  see.
- **`Unresolvable` will be common at first**, and a dossier full of it is a
  weak answer. It is still a truer one than a short dossier that looks complete.
- **The declaration and the disclosure can drift** in a holder that maintains
  two lists. The contract says to derive one from the other; where a module
  cannot, it owes a test that holds them equal.

## Reopening the decision

Reopen if a jurisdiction gobit targets requires a specific machine-readable
portability format, which would turn the dossier from a review artefact into a
deliverable and would move the redaction question from the controller back to
the framework. That is the change this ADR's word choice is guarding against.

## Related

- [ADR 0029](0029-the-embedder-is-the-data-controller.md) — the controller
  decision, and the closed list this obligation is not on.
- [ADR 0033](0033-erasure-is-a-sweep-that-returns-an-answer.md) — the erasure
  half, whose sweep this one reuses.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the published
  surface, and the amendment that renamed this package.

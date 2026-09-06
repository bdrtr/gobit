# ADR 0029 — The embedder is the data controller; gobit publishes the mechanism

- **Status:** Accepted
- **Date:** 2026-09-06
- **Phase:** after the roadmap

## Context

KVKK gives a person a right to erasure. Nothing in this repository says who owes
that right, and the absence has been blocking every privacy item on the list:
B17 (erasure, export, retention), C4 (consent records and data-subject
endpoints), and — measured on 2026-09-06 — the whole shape of A4, because what
an invoice module should do with a person's name depends entirely on whether
gobit is the party that owes the answer.

**What the repository actually has today, measured rather than assumed.** There
is no erasure capability anywhere: no symbol, no endpoint, no table. There is no
retention setting outside the two idempotency stores in `core/http` and
`core/http/redisguard`. What there is instead is three REDACTORS, and they are
worth naming because they show the posture the code already took without saying
so: `core/db` keeps the password out of a logged DSN, `core/errorreport` strips
attributes before a report leaves the process, and `core/http` carries a list of
query keys whose values never reach a log — a list whose own godoc says it
covers "not only credentials but PERSONAL DATA" and which names `mail`, `posta`,
`telefon`, `tckn` and `iban`. The repository has been careful with personal data
in transit and has written nothing about personal data at rest.

The one place a privacy decision IS in the schema is the notification module,
whose delivery log deliberately has no column for a recipient address. That is
the shape this ADR generalises: a decision that binds tomorrow's writer, not
just today's.

## Decision

**gobit is not the data controller. The embedding application is.**

ADR 0025 already decided that gobit is a library rather than a template, and a
library that ships inside somebody else's product does not choose that product's
retention periods, its lawful basis, or the wording of its consent. The
controller is whoever decides why and how the data is processed, and that is the
shop, not the framework it imports.

What follows from that is not "gobit does less" but "gobit owes something
different". The obligation changes from IMPLEMENT CONSENT to PUBLISH THE HOOKS
AND THE ERASURE CONTRACT.

**Concretely, gobit owes three things and no more:**

1. **An erasure contract with THREE outcomes, not two.** A module asked to erase
   a person answers `DELETED`, `ANONYMIZED` or `RETAINED`. The third one is the
   whole reason the contract needs designing rather than assuming: an invoice
   module that is legally required to keep a document cannot answer "done", and
   a caller that receives only done/failed has no way to tell a refusal from a
   fault. `RETAINED` carries WHAT was kept and WHY, because a controller
   answering a data subject has to be able to say so.
2. **Hooks, so the embedder can act.** Erasure is a cross-module event and
   Principle 2.2 forbids the cascade that would otherwise carry it, so the
   customer module has to SAY that a person was erased. That is B8, and this ADR
   is the reason it is not optional.
3. **A declaration of what each module holds.** A module that stores personal
   data says so, in a form the embedder can enumerate. Without it the embedder
   cannot answer a data-subject request even in principle, because nothing tells
   it where to look.

**What gobit does NOT owe:** the retention period, the lawful basis, the consent
text, the identity of the data subject, or the decision about whether a given
field is personal data at all in a given deployment.

## Rejected alternative

**gobit is the controller and implements consent, retention and erasure.** It
was rejected on the same argument ADR 0025 used to reject the template model,
and one more that is specific to this subject: a retention period is a legal
choice that varies by jurisdiction, by industry and by the shop's own counsel,
and a framework that picks one is either wrong for most of its installations or
carries a configuration surface that is a policy engine in disguise. The
framework would also have to know which of its columns are personal in a given
deployment, which it cannot: a `metadata` jsonb is personal data exactly when
the embedder puts personal data in it.

## What this deliberately does NOT do

- **It does not make gobit indifferent to personal data.** The three redactors
  stay, the notification module's missing column stays, and a new column that
  stores a person still has to justify itself. A framework that hands the
  controller a schema full of unnecessary personal data has made the
  controller's job harder, which is a design failure even when it is not a legal
  one.
- **It does not answer A4.** What the invoice module does when the contract
  reaches it is a separate decision, and this ADR only fixes which of its
  candidates are available: under this answer gobit publishes the mechanism and
  the embedder sets the window, so "keep it for N years" is a value the embedder
  supplies rather than a number gobit picks.
- **It does not make the declaration surface public yet.** Whether the "what I
  hold" declaration is a published package under ADR 0026 or stays internal is a
  question for whoever builds B17, and publishing it is a compatibility promise
  that should be made once, deliberately, with its first consumer.

## Consequences

**Positive**

- **Every privacy row now has a shape.** B17 becomes "publish a contract and
  wire the modules to it" rather than "implement KVKK", and C4 becomes the
  embedder's endpoint calling that contract.
- **A4's candidate set narrows to the ones a mechanism can express.** The
  invoice module answers `RETAINED` and says why; how it enforces that is A4's
  own decision.
- **B8 stops being optional.** Under this ADR the customer module's silence is
  not a missing nicety, it is the hole the contract cannot be built over.

**Negative, and accepted**

- **An embedder that does nothing is not compliant.** gobit will ship a
  mechanism that can be left unwired, and a shop that never calls it has a legal
  problem the framework cannot see. The README has to say this in the words a
  shop owner reads, not only in an ADR.
- **The declaration is only as true as the modules keep it.** A module that
  grows a personal column and does not declare it makes the contract lie. That
  is an audit this repository does not have yet, and it belongs with B17.

## Reopening the decision

Reopen if a deployment model appears in which gobit itself holds the data — a
hosted or multi-tenant offering where the shop is the framework operator's
customer rather than its own controller. ADR 0009 keeps multi-tenancy out of
scope today and that is what makes this answer safe; the day that changes, the
controller question changes with it.

## Related

- [ADR 0025](0025-gobit-is-a-library-not-a-template.md) — the library decision
  this one follows from.
- [ADR 0024](0024-an-invoice-number-comes-from-a-row-not-a-sequence.md) — why an
  issued document cannot simply be deleted.
- [ADR 0009](0009-cok-kiracililik-kurulum-siniri.md) — multi-tenancy is out of
  scope, which is what makes a single controller per installation coherent.

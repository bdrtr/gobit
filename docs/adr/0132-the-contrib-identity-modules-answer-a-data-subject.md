# ADR 0132 — The contrib identity modules answer a data subject

**Summary:** `contrib/identity-session` and `contrib/identity-passkey` implement
erasure, declaration and disclosure, and the personal-data audit walks `contrib/`
as well as `plugins/`. It costs two optional store capabilities and buys a
data-subject request that no longer steps over the modules holding a person's
credentials.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

ADR 0029 gives gobit three erasure obligations and ADR 0034 adds disclosure, each
answered by an optional capability the sweep finds by type assertion.

The two contrib identity modules implemented none of them while holding an e-mail
address, an argon2id password hash, a customer id, a per-device credential id and
timestamps. A shop honouring a deletion request deleted the customer and left the
credentials that sign that customer in.

The audit that should have said so could not see them: it walks `plugins/`, the
tree it was built for when gap D30 found a plugin invisible, and `contrib/` did not
exist then. A separate go.mod does not make a table less personal.

## Decision

Both modules implement `personaldata.Eraser`, `Declarer` and `Discloser`, delegating
to an optional `PersonalRecords` capability on their credential store, and the
personal-data audit walks a list of roots that is itself checked against disk.

## Consequences

The store capability is optional for `Credentials`'s own reason: it is bound by
installations keeping keys elsewhere, which must not be asked to delete rows out of
a table they do not own. Such a store leaves the module answering `Retained` with
the reason — what a controller needs to hear instead of "deleted, 0 rows".

The passkey module's erasure and disclosure are NOT scoped by the relying party,
unlike every other statement it runs (ADR 0131): those answer "which keys can sign
this person in", these answer "what is held about her", and a row left behind by a
domain move is held about her.

The erasure does not go through the removal endpoint's last-way-in rule. That rule
defends somebody KEEPING their account; an erasure is that person asking for the
account to stop existing, and routing one through the other would refuse it for
exactly the person it serves.

The password hash is declared and its value is not reproduced: the dossier carries
the field with a sentence in place of the hash, because dropping the column would
make the answer false about what is held and printing it would put a secret derived
from the person's own password into whatever carries the answer. It is not read out
of the database either.

An address alone resolves in the session module and not in the passkey module, which
stores none — so that module answers `Retained` and `Unresolvable`: a holder that
could not search has not told the controller what one that searched has.

That the sweep reaches a module in a separate go.mod is TESTED rather than pinned:
Go's internal rule is about the import path, so `contrib` can import the
coordinator and drive it. The cost is a test depending on one of gobit's internal
packages, which is the signal wanted — the sweep this module relies on has moved.

Measurement: [measurements/0132](../measurements/0132-a-credential-is-personal-data.md)

## Rejected

**Three methods on `Credentials`.** It is bound by installations keeping keys
elsewhere; erasing gobit's own table is not their business.

**Scoping the erasure by relying party.** It would leave somebody who asked to be
forgotten with rows on disk and a report saying they were deleted.

**Reporting the password hash, and omitting its column.** The value tells the person
nothing they do not know and its escape hurts them; dropping the column would make
the dossier false.

**Installing every unit to read its declaration.** The audit is static for the
reason D30's record gives: a gate that must satisfy every unit's configuration
breaks the day one adds a required setting.

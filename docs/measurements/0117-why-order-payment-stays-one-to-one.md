# Why `order_payment` stays one-to-one — measured 2026-09-10

Serves ADR 0117.

Until this morning the answer was "because it cannot be widened". ADR 0116
removed that answer by making a widening run, so the question became the one
worth asking: it can be done now, and should it be done to THIS link?

Three of the four obstacles measured before ADR 0116 are gone. One remains, one
became live rather than hypothetical, and one is new. This file states each with
the command that measured it.

## What was claimed, and the search that hides it

Two link definitions promised that widening their cardinality would be free, in
sentences copied one from the other. `payment/service/links.go` for
`order_payment` and `invoice/service/links.go` for `order_invoice` both ended:

> That day this becomes OneToMany and nothing else changes.

Both are corrected as of this morning — the first by the commit that built the
widening, the second by a commit three later, after an independent reading found
it still standing.

**The population cannot be sized with a phrase search, in either direction.**
The sentence wraps across a comment line in the invoice file, so a plain
`grep 'OneToMany and nothing else changes'` returns NOTHING and reads as proof
that the class is closed. And after the correction the phrase survives in the
payment file as a QUOTATION of the withdrawn claim, so the same grep reports a
live claim where there is none. A pattern cannot tell a promise from a promise
being withdrawn.

What sizes it is stripping the comment markers, flattening the wraps, and then
reading the hits:

```
python3 - <<'PY'
import re, io, subprocess
files = subprocess.run(['grep','-rl','--include=*.go','Cardinality: link.',
                        'internal/','core/'], capture_output=True, text=True).stdout.split()
for f in files:
    flat = re.sub(r'\n\s*//', ' ', io.open(f, encoding='utf-8').read())
    if re.search(r'nothing\s+else\s+changes', flat):
        print(f)
PY
```

### How many link definitions there are, and the first wrong method

**Wrong method, recorded because it undercounts silently:**

```
grep -rn 'Cardinality: link\.' --include='*.go' internal/modules/ | grep -v _test.go
```

`Cardinality` is an OPTIONAL field — `core/link`'s godoc says an undeclared one
defaults to the strictest — so a definition that omits it is invisible to this
command. Deriving a population from an optional field is the same defect as
deriving it from the property under audit.

**Right method** — one `Name:` per entry in each module's `Definitions()`:

```
grep -rln 'func Definitions()' --include='*.go' internal/modules/ | xargs grep -h 'Name: *Link' | wc -l
```

Nine today: `product` 4, and one each in `payment`, `fulfillment`,
`inventory`, `b2b`, `invoice`.

## The two obstacles ADR 0116 removed

**The declaration would not start.** The ledger compared an incoming definition
against the stored row for equality, so a changed cardinality was a conflict at
startup, and no repair could be carried by a migration: `core/module`'s
bootstrap runs `validateNames` → `registerAll` → `migrateAll`, `Define` runs
inside `registerAll`, and a process that cannot get past the conflict never
reaches `migrateAll`. `core/link` had no migrations directory and was not among
the four owners in `coreMigrationSources()`.

That measurement was correct and it is now beside the point. ADR 0116's answer
was not to write a migration but to make `Define` BE the migration: it applies a
widening in the transaction that already holds the declaration lock.

**The obsolete index would survive.** The DDL was `CREATE ... IF NOT EXISTS`
throughout with no `DROP` anywhere, so a widened declaration added the index the
new cardinality needs and left the old one enforcing the old rule, while
`verifySchema` — which iterated only the indexes the cardinality REQUIRES —
reported success. ADR 0116 closed this with `obsoleteIndexes()` and
unconditional `DROP INDEX IF EXISTS`, and gave `verifySchema` the other half of
its question.

The unconditional drops also close a path that mechanism never targeted: a
ledger row abandoned by DELETE rather than UPDATE. On that path the upsert finds
no conflict, the insert carries the new cardinality, `RETURNING` hands back what
was just written, and the comparison compares a row against itself — so nothing
ever objects, on that startup or any later one. Running the drops on every
startup rather than only on a detected change is what makes the schema converge
anyway.

## The obstacle that stands: the row cannot carry the distinction

This one does not depend on any mechanism and no release can remove it.

A link row holds `from_id`, `to_id` and `created_at`, and `created_at` is
selected by no read statement in the package. Under a widened `order_payment`,
nothing in the row distinguishes the collection the checkout opened from a
collection that settles an exchange difference. The distinction has to live
somewhere; with no field for it, the only place left is the link NAME.

Three readers show the cost. All three take the first record, and all three want
the SALE's collection:

- `order/service/payment_view.go`, `firstExpanded` — "the expansion is declared
  OneToOne on the payment side, so at most one record comes back".
  `order/service/timeline.go` calls the same helper and does not repeat the
  reasoning, so one godoc covers two readers.
- `workflows/returns/refund.go` — "the definition is one to one, so more than
  one is a data fault rather than a choice."

The refund reader is the expensive one, because its comment is an ARGUMENT
rather than a description: `collections[0]` is justified BY the cardinality.
Widen the link and the premise evaporates while the behaviour is unchanged —
what the code calls a data fault becomes a legitimate second collection, and a
refund the operator already approved is taken from the wrong collection with no
error raised. No test can see it: nothing about the behaviour changed, only the
reason for it stopped being true.

Which collection loses is ordered but not deterministic. Reads are
`ORDER BY to_id` and `ORDER BY from_id, to_id`, and payment collection
identifiers are ULIDs whose 48-bit prefix is `UnixMilli`, so lexicographic order
is chronological AT MILLISECOND RESOLUTION. Two collections opened inside one
millisecond order by their random tail. The resolution does not matter for this
decision — the two collections are opened days apart — but it is why the
sentence cannot say "always the oldest".

`workflows/invoicing/issue.go` carries the refund reader's sentence verbatim for
`order_invoice`. That link stays one-to-one, so the sentence stays TRUE; it is
this defect's waiting twin rather than a second instance of it.

## The obstacle that became live: widening SPENDS a guarantee

`ddl()`'s `OneToOne` branch creates two unique indexes; the `OneToMany` branch
creates one, and ADR 0116 now DROPS the other. So widening removes `from_uniq` —
the index that makes a second binding on the same left-hand record impossible,
and the only STRUCTURAL guard against two concurrent writers binding one order
to two collections. Nothing else holds it: the flows read the link and then
create one, with no lock, and `core/link` takes its advisory lock only around
`Define`.

`checkout/authorize_payment.go` shows what that guard is worth. It binds the
order to the collection BEFORE the authorization, and says why in its own
godoc — "nothing has been held on the customer's card yet". Under `OneToOne` a
second concurrent binding fails there, with no money moved. Under `OneToMany`
both bindings are accepted, both callers receive a collection identifier, and
each can be carried through the payment module's published session, authorize
and capture endpoints.

Before ADR 0116 this cost was unreachable, so it was theory. It is now the
ordinary consequence of an edit any release can make, which is why it belongs in
a record rather than a footnote. `payment/service/links.go` states the principle
it is an instance of: a constraint that is too tight "fails LOUDLY while one that
is too loose lets a wrong row in silently".

## The obstacle that is new: the one-way door has two locks

ADR 0116 records that a release which widens a link cannot be rolled back
through this path, because the older binary declares the narrower cardinality
and is refused at startup. That lock can be picked: an operator with the runtime
role can edit the ledger by hand.

The second lock cannot. Read ADR 0116's own spine sentence backwards — *every
pair a narrower cardinality admits is admitted by a wider one* — and it says
that the reverse does not hold: pairs written AFTER the widening need not
satisfy the narrower constraint. If a widened `order_payment` has bound one
order to two collections, recreating `from_uniq` fails on `23505`, and the only
way through is to delete a row. The rollback is then not a rollback but a data
loss.

So the safety argument that makes widening cheap in one direction is the same
sentence that makes it irreversible in the other. Both readings are true and
they are the same fact.

## What no gate sees

Re-measured after ADR 0116's three commits:

```
grep -rn 'Cardinality\|OneToMany\|OneToOne' internal/arch/
```

Five hits, all in `internal/arch/testdata/published-names.txt`, all of them the
NAMES of exported symbols. No architectural gate reads a link's cardinality. The
reason is recorded in `internal/arch/module_sql_test.go`: link tables appear in
no migration, so no module owns them and the SQL audits never see them.

`core/link`'s own lanes do test cardinality, thoroughly and now including a
widening, but they open a FRESH postgres container per run. What they cannot
observe is a tree where a cardinality constant CHANGED between releases against
one database — which is the whole subject here.

## What actually blocks the difference, and it is not the link

ADR 0117 names the link and stops, because three rounds of design against this
tree found the blocker somewhere else: **a payment collection cannot be
abandoned once it has taken anything.**

Three facts, each read out of the payment module:

- `service/session.go` refuses a new session on a collection whose
  `CapturedAmount` is above zero, and says so: a collection whose capture has
  begun is closed to further sessions.
- `service/capture.go` writes a refund by raising `RefundedAmount` and leaves
  `CapturedAmount` where it is.

Read together, the second explains the first: `CapturedAmount` is MONOTONIC, so
the gate is not asking whether the collection holds anything now — it is asking
whether it ever took anything. After a full refund the balance is zero,
`RefundedAmount` equals `CapturedAmount`, and the collection is still closed for
good. Had the gate been written on the net figure, a fully refunded collection
would be reusable and most of what follows would not arise. It was not, and
changing it is the payment module's decision rather than this record's.
- The admin surface binds three verbs to collections: create, list, read. There
  is no delete and no patch, so a collection cannot be removed and its
  `reference` cannot be re-aimed.

Put together with a provider that may authorize PARTIALLY, they produce a state
with no exit. A collection opened for a difference and short-captured can never
take another session, cannot be topped up, cannot be dropped, and cannot be
replaced under a one-to-one link. Any record that stamped "settled" from it
would be stamping a partial payment, and any record that waited would wait for
ever — and `order_exchanges` with status `requested` is what
`queries/erasure.sql` reads to refuse erasing an order's personal data.

The same three facts make a stamp a one-way latch. Capture in full, stamp,
refund in full: the stamp survives, `CapturedAmount` is unchanged, the order
summary never learned the figure, and nothing in the tree can contradict the
row. The record then says a balance was handled when the money went back, which
is the sentence ADR 0114 refused and migration 000017 repeats.

Three independent designs were written against these constraints and three
independent readings broke all three, on these points and not on others. That
convergence is the evidence for deferring rather than the absence of a design:
the missing piece is a collection that can be withdrawn or re-aimed, and it
belongs to the payment module.

## A count this change moves

`docs/security.md` says the runtime role owns "the eight link tables and
`link_definitions` it creates". The tree declares nine today.

It was true when it was written. Counted against the tree of 2026-09-06:

```
git grep -c 'Name: *Link' <commit> -- '*/service/links.go'
```

reports eight. The same number in `docs/measurements/0060-two-roles.md` is left
alone — measurements are dated observations by the rule stated in their own
index, and on its date it was right. `docs/security.md` is a living guide and is
corrected.

The number rotted unnoticed because "link definitions the tree declares" is not
one of the populations in ADR 0070's count vocabulary. The enumeration to price
it already exists in the same package — `internal/arch/consumers_test.go` walks
`link.LinkDefinition` literals through `qualifiedLiterals` — so the size function
would be cheap. Admission is not: the count gate admits a claim by the
POPULATION'S PATH appearing on the line, and this sentence names a table rather
than a path. Gating it would mean rewording a security document to suit an
audit, which is a decision of its own and is not taken here.

# ADR 0038 — The storage form of an e-mail address is one rule, and it is folded in Go

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Six modules store an e-mail address: `auth`, `b2b`, `cart`, `customer`,
`invoice`, `order`. Five of them folded it in Go with
`strings.ToLower(strings.TrimSpace(email))`, each from its own `NormalizeEmail`,
because a module may not import another module's models (Principle 2.1).

Nothing compared them. That is the whole problem, and the two failures it
permits are both INVISIBLE:

- A guest order carries an e-mail and a NULL `customer_id`. The day that person
  registers, the order and the account meet only if both surfaces folded the
  address to the same bytes. Folded differently, the person's history is simply
  not there and the shop concludes they are new.
- A data-subject erasure resolves one person across every holder at once
  (ADR 0033). A holder that folded differently answers "nothing found" for rows
  it is holding — a green report, a complete-looking answer, and personal data
  still in the database.

Neither raises an error. Both look exactly like an absence, which is why
the disagreement would be found by a customer rather than by a test.

**The sixth module was worse than uncompared: it folded somewhere the comparison
could not look.** `invoice` stores `buyer_email` VERBATIM — the column copies
what the document said, which ADR 0024's immutability requires — and matched in
SQL instead, `WHERE lower(buyer_email) = lower($1::text)`, with the Go side doing
only a `TrimSpace`. The reasoning written into migration 000002 is sound as far
as it goes: this table has no `customer_id` and no `order_id`, the address is the
only handle on a person, and a case-sensitive match "would let one capital letter
answer 0 rows retained about a person whose invoice is sitting in the table".

What it assumed is that `lower()` folds letters. It folds the ones the
CLUSTER's ctype knows. Measured 2026-09-07 against the development database,
which reports `datcollate=C, datctype=C, datlocprovider=c`:

| expression | result |
|---|---|
| `lower('ALI@X.COM')` | `ali@x.com` |
| `lower(<capital O with diaeresis>)` | unchanged |
| `<capital C cedilla> ILIKE <small c cedilla>` | false |

The two non-ASCII rows are described rather than printed: this file is English,
the language ratchet's diacritic lane reads the source, and a letter written out
here would need a ledger entry for what is plainly data (ADR 0012). They are
U+00D6 and U+00C7.

Reproduced with the module's own predicate on that cluster: a row holding the
address as the document recorded it, matched against the folded form every OTHER
holder of that person's data stores, counted **0**. Matched against the
document's own casing it counted 1. The ASCII control counted 1.

Zero is not a quiet branch in that module. `Service.Erase` returns
`whyNothingHere` for it, whose entire purpose is to distinguish "nobody looked"
from "we looked and you are not here" — and the second sentence is the one a
controller repeats to a data subject. On that cluster it was false while the
document was held.

`core/db/casefold.go` exists for exactly this class of defect and probes two
paths, `ILIKE` and `to_tsvector`, because those two can disagree. `lower()` is a
third and is not probed. The probe also only WARNS, and that decision is argued
in its own godoc for the case it was written for: "a catalog written entirely in
ASCII works perfectly on a C-locale cluster." True of a product search. Not true
of an erasure, where the cost is not a missed search result but a false statement
to a person exercising a legal right.

## Decision

**The storage form of an e-mail address is one rule: trimmed, then lower-cased
by Go, and never by the database.**

**`invoice` folds in Go like the other five.** Migration 000003 adds
`buyer_email_folded`, written by `models.NormalizeEmail` at insert and indexed;
the erasure count is a plain equality against it. What the document PRINTS is
untouched — `buyer_email` still holds what was said — because an invoice is a
snapshot and ADR 0024 makes it immutable. The folded value is a handle beside the
document, not an edit to it.

**The six copies stay, and an audit is what holds them together.**
`internal/arch/email_test.go` compares all six over inputs chosen for the ways
two "lower-case and trim" implementations drift apart, and it has two blindness
guards: one walks the tree for an exported `NormalizeEmail` that is not in the
comparison, and one takes the SCHEMA as ground truth — a module declaring a
column named `*email*` must either fold in Go or carry a WRITTEN exemption. All
three fail under mutation, which was checked rather than assumed.

## Rejected alternatives

**Hoist the fold into `core/`.** The strongest candidate, and it is what this
repository usually prefers: one implementation makes the agreement true by
construction instead of by audit, which is the same reasoning ADR 0036 used when
it made a component name unique by construction. It also has a real second
argument — an out-of-tree module implementing `personaldata.Eraser` receives
`Subject.Email` and today has no exported way to fold it the way gobit did on the
way in, which is precisely the blind spot `core/personaldata`'s own godoc
describes for `Describer`.

Rejected because the published surface is a promise kept forever (ADR 0026) and
this is a two-line function; a package added there cannot be withdrawn before
`1.0.0` without breaking an embedder who named it. The cost of NOT hoisting is
paid by an audit that exists, runs on every build and has been shown to fail when
the copies disagree. The cost of hoisting would be paid every release from now
on. **This is the weakest part of this decision and it is written down as such:**
if a second out-of-tree holder appears, or if the audit is ever found green on a
real disagreement, the trade flips and this should be revisited.

**Fold the subject harder in Go before the invoice query.** It does not help.
The side the cluster fails to fold is the STORED one, and no amount of folding
the argument makes `lower(buyer_email)` fold.

**Use an explicit collation, `lower(buyer_email COLLATE "und-x-icu")`.** It
trades a dependency on the cluster's ctype for a dependency on ICU being present,
which is the same KIND of dependency this is escaping. It also still would not
agree with the Go copies: Go maps the Turkish dotted capital I to a single rune
and ICU's full case mapping produces two, so the one letter this was found on
would still divide the holders.

**Add a CHECK tying the two invoice columns together.** The mirror this
repository reaches for — `CHECK (buyer_email_folded = lower(btrim(buyer_email)))`
— is written in the very expression being escaped. On a C-locale cluster it would
REJECT exactly the rows the migration exists to store correctly, so the
constraint would refuse the fix and accept the defect. What holds the pairing
instead is the INSERT naming the column, plus the column audit that requires it
to.

**Extend the case-folding probe to `lower()` and leave the rest alone.** It
would surface the problem on every affected cluster, which is worth doing on its
own, but it does not make the erasure correct — it tells an operator their
erasures are wrong and offers them a dump and restore. It is a smaller step, not
a different answer, and it is still open.

## Consequences

**Positive**

- **A data subject's documents are found by the same handle every other holder
  finds them by**, and the answer `whyNothingHere` now means what it says.
- **The agreement is checked rather than hoped for.** Six implementations, one
  comparison, two blindness guards, all mutation-proved.
- **~~A seventh module cannot join quietly. A column named `*email*` in a
  migration puts its module in the audit the day it lands, whether or not anybody
  remembers this decision.~~ Corrected 2026-09-07: it could, in two shapes, and
  the guard has since been widened to cover both.** As shipped, the schema guard
  matched a column only where it was DECLARED inside a `CREATE TABLE`, and it
  walked `internal/modules` alone. So a column arriving by
  `ALTER TABLE ... ADD COLUMN` was invisible to it — the shape THIS decision's
  own migration 000003 used, which left `buyer_email_folded` outside the audit it
  was written for, with 000001's `CREATE TABLE` the only reason `invoice` was in
  that audit at all — and a module brought by a PLUGIN was outside the walk
  entirely, while four plugins ship migrations of their own.
  `internal/arch/email_test.go` now matches `ADD COLUMN` as well and walks
  `plugins` beside `internal/modules`. What it still rests on is the column's
  NAME and its type: an address kept in a column without "email" in its name, or
  in a type outside `text`, `varchar` and `citext`, is seen by neither pattern.
- **The column audit got smarter for everyone.** Teaching the replay
  `ALTER COLUMN` — which this migration was the first to need — closed a shape it
  had been reporting as unreadable since D16.

**Negative, and accepted**

- **The backfill in 000003 is correct for ASCII and only for ASCII.** A migration
  is SQL, and SQL is the thing that cannot fold reliably here, so the rows the
  defect is about are exactly the rows the backfill gets wrong. The remedy is a
  Go pass, `gobit refold-invoices`, and it is a COMMAND rather than a hidden step
  at boot: it is needed once per installation and only by one that has non-ASCII
  buyer addresses, and putting a full table walk in every shop's startup path to
  fix a row most of them do not have is the wrong trade. An operator who never
  runs it keeps the defect for those rows.
- **Six copies of one function are still six copies.** The audit makes a
  disagreement loud; it does not make one impossible, and that is a weaker
  guarantee than this repository usually accepts.
- **~~`lower()` is still unprobed. Nothing in the tree now depends on `lower()`
  folding non-ASCII, so the hole is not live.~~ THE SECOND SENTENCE WAS FALSE and
  it was checked the same day, 81 minutes later (2026-09-07).** Three modules
  depend on exactly that: `auth`, `customer` and `b2b` each guard their e-mail
  column with `CHECK (email <> '' AND email = lower(email))`. Measured on a
  `--locale=C` cluster, that constraint REFUSES the unfolded ASCII address
  `Ada@Example.com` and ACCEPTS an unfolded Turkish one, because `lower()` leaves
  those capitals alone and the value therefore equals its own `lower()` — so the
  guard that is meant to be the last defense behind the Go fold stops guarding at
  the ASCII boundary, and two rows for one person is what the unique index on
  that column then cannot prevent. `promotion_code_check` has the same shape and
  is SOUND, for a reason that had never been written down: its validator admits
  only `A-Z`, `0-9`, `-` and `_`.

  `lower()` is now the probe's third path, the constraints carry written
  declarations of what they hold, and `internal/arch/case_folding_test.go`
  refuses any SQL in a migration that folds case without one — default-deny,
  across CHECK constraints, unique indexes, DEFAULT and GENERATED expressions,
  data migrations and plpgsql trigger bodies alike. D28 has the reproduction, and
  also the record of that audit shipping with a false scope justification of its
  own before it was inverted.

  **The mistake is the part worth keeping.** This bullet was written while
  removing the last `lower()` from a query, and it generalized from "the
  predicates are clean" to "the tree is clean" without looking at constraints at
  all. A claim about what a whole repository does not do is a claim that has to
  be searched for, not inferred from the thing just fixed.

## What this deliberately does NOT do

- **It does not change what an invoice prints.** `buyer_email` holds what the
  document said, and `seller_email` is untouched for a different reason: the
  seller is not a data subject of this framework's erasure, and `module.go`
  already says an erasure naming the seller's own address counts zero invoices.
- **It does not make the storefront's search locale-independent.** ADR 0015's
  hazard stands for `ILIKE` and `to_tsvector`; this decision is about the storage
  form of an identity, not about matching text a shopper typed.
- **It does not touch the validation rules, which differ on purpose.** `auth`,
  `customer` and `b2b` require a dot in the domain and `cart` and `order` do not,
  because an address on a cart is CONTACT for one transaction while an address on
  an account is an IDENTITY. What must not differ is the storage form, and that
  is what this decision fixes.

## Related

- [ADR 0033](0033-erasure-is-a-sweep-that-returns-an-answer.md) — the sweep that
  resolves one person across every holder, which is what a disagreement breaks.
- [ADR 0032](0032-an-issued-invoice-refuses-erasure-in-the-schema.md) — why the
  invoice answers RETAINED, and the migration this one extends.
- [ADR 0024](0024-an-invoice-number-comes-from-a-row-not-a-sequence.md) — the
  immutability that keeps `buyer_email` verbatim and forces the folded value into
  its own column.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the promise
  that makes hoisting into `core/` expensive.
- [ADR 0015](0015-postgresql-cluster-contract.md) — the cluster's case folding,
  measured there for search and found here on a third path.

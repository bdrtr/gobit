# What a count claim is, and what auditing one costs — measured 2026-09-08

Evidence for [ADR 0070](../adr/0070-a-count-is-audited-against-a-closed-vocabulary.md).
Three questions: is the route gate's 393-of-491 a gap or a decided boundary, is
the cross-reference class already closed, and can the count class be mechanized.

## 1. The 393 of 491 is a decided boundary, not a residue

`docs/gaps.md` D29 read the exclusion as an open residue. Re-measured against
today's tree by running the route collector with the dated-record filter turned
off:

| | Addresses |
|---|---|
| Written anywhere in the tree | 508 |
| In scope for `TestTheRouteAddressesInTheProseExist` | 410 |
| In `CHANGELOG.md` and the ADR records, not read | 98 |
| Of those 98, naming a route the tree no longer binds | 17 |

The 98 and the 17 are exactly what the row says. The 393 and the 491 are the
same measurement taken one day earlier: the tree gained 17 addresses since, all
of them in scope. So the row's own two numbers had gone stale while the numbers
describing the exclusion had not — which is the count class biting the sentence
that reports it.

The exclusion itself is argued in the route gate's own godoc, with its price
written out: 17 addresses in 10 file-and-claim pairs, and the one sentence that
reads as present tense rather than history is named there. That is a boundary
with a stated cost, not an unclosed gap. The repair is to the ROW.

## 2. Cross-references are closed

Every reference kind and the gate that resolves it:

| Reference | Gate |
|---|---|
| `[Symbol]` links in Go comments | `TestTheGodocLinksResolve` |
| A document section a godoc names | `TestTheGodocSectionReferencesResolve` |
| "ADR NNNN" and links to records | `TestTheADRReferencesResolve` |
| A repository path in a comment | `TestThePathReferencesInCommentsResolve` |
| A test FILE named in a comment | `TestTheTestFileReferencesInCommentsResolve` |
| A test NAME named in a document | `TestTheTestNamesInTheDocsResolve` |
| Paths and symbols in documents | `TestTheReferencesInTheDocsResolve` |
| A plugin name in a document | `TestThePluginNamesInTheDocsAreReal` |
| A `-run` pattern in a build file | `TestEveryRunPatternInABuildFileNamesARealTest` |
| Every record reachable from the index | `TestTheADRIndexNamesEveryRecord` |
| A route address next to its verb | `TestTheRouteAddressesInTheProseExist` |
| A file:line reference, which is FORBIDDEN | `TestTheDocsCarryNoLineNumberReference` |

Six of them carry a blindness control of their own. What stays open is stated in
those godocs and is not a missing gate: a reference that resolves to the WRONG
place, and a mention written without brackets. No gate was added.

"Closed" was the wrong word for that table and it has been withdrawn. The table
is an INVENTORY, and one reference kind the prose writes is on nobody's list: a
`table.column` named in a document is resolved against no schema by any of the
twelve. The tree writes many of them and they all resolve today, so this is a
blind spot rather than a defect — but it is the class's, not an omission from
the class, and the earlier sentence claimed a closure that was never measured
from the prose side.

## 3. The count class: the forms, the noise, and the vocabulary

**The form.** A count under twenty is written as a WORD more often than as a
digit, and the two Turkish documents write theirs in Turkish. All four forms
occur: `82` (digit), `seventeen` (English word), `fifty-one` (English hyphenated
compound), and the Turkish compound, which is a ten and a unit written as two
separate words. A reader that took digits alone would have found none of the six
defects below.

**The noise.** The bare shape "a number followed by a plural noun", scanned over
every `.md` and `.go` file:

| Noun | Occurrences | Totals among them |
|---|---|---|
| modules | 185 | 2 |
| tables | 129 | ~3 |
| plugins | 26 | 3 |

Almost every occurrence is a SUBSET — "two modules share a schema", "the two
tables this store owns", "two plugins each believing they own the reporting" —
and no article rule separates them: "the four core packages" is a subset and
"the ten plugins" is a total. An exemption list over this population would carry
its own false positives, which is the shape `docs/gaps.md` D16 records.

**What separates them.** A total names the population BY ITS PATH on the same
line; a subset does not. Anchoring on the path takes the modules from 185
occurrences to 2, both of them totals. This is the vocabulary that follows:

| Population | Anchor | Tree says |
|---|---|---|
| commerce modules | `internal/modules` | 17 |
| published packages | `core` | 17 |
| in-tree plugins | `plugins` | 10 |
| workflow packages | `internal/workflows` | 6 |
| decision records | `docs/adr` | 69 |
| measurement reports | `docs/measurements` | 32 |
| entries of the limits document | `docs/known-limits.md` | 23 |
| groups of the limits document | `docs/known-limits.md` | 4 |

A single-segment anchor — `core`, `plugins` — is admitted only in the
directory-layout column of a README, because "core" and "plugins" are ordinary
words as well as directories. Without that rule three subsets in the arch
godocs anchor as totals.

**What it found.** Ten claims in scope, in two files, SIX of them false:

| Document | Said | Tree held |
|---|---|---|
| `README.en.md`, the layout block | sixteen packages | 17 |
| `README.md`, the layout block | sixteen packages, in Turkish words | 17 |
| `README.en.md`, the reading table | fifty-one records | 69 |
| `README.md`, the reading table | fifty-one records, in Turkish words | 69 |
| `README.en.md`, the reading table | twenty-one items | 23 |
| `README.md`, the reading table | twenty-two items, in Turkish words | 23 |

The two READMEs disagreed with each other as well as with the tree: one said the
limits document held twenty-one entries and the other twenty-two. Both READMEs
also listed the published packages by name and the list was missing
`core/jobreport`, published the day before.

**Two more, outside the vocabulary.** The route gate's own godoc priced the
described surface at 276 of 328 routes; the tree binds 330 and describes 278.
Corrected by hand. Nothing in the vocabulary reaches it, because no path names
the route population — that is the gate's largest named blind spot.

## 4. The dated records, and why the boundary is wider here

The route gate freezes the records up to 0058 and audits every record written
after. For counts the exclusion is ALL of them, and there is exactly ONE reason:
a route address in a new record should still be true tomorrow, while a count is
stale the day the tree grows — and `CLAUDE.md` amends a dated record by adding,
never by editing, so a gate holding one to today's tree asks for a repair that
does not exist.

A second reason was written here first and is struck, because it was checked and
found false. It said ADR 0069's "`core/` goes from sixteen packages to
seventeen" is a transition whose halves fall on different lines, which a
line-anchored scanner would read as a claim about today. Measured with
`countDatedRecord` switched off: the records yield ZERO claims, ADR 0069's
sentence included — `core` anchors only in a README's layout column, and that
sentence does not open one. The argument was for a misreading that cannot
happen.

THE PRICE, measured the same way rather than reasoned about: ONE claim, and it
is not in a record. `CHANGELOG.md` prices the reports under `docs/measurements/`
at fifteen, in the entry announcing that directory, against a tree now holding
thirty-two. The sentence states what was moved out of `gaps.md` THAT DAY. It is
historical — which is the argument for excluding the changelog, not a cost of
having excluded it.

## 5. Universal negations are not mechanizable as a class

Scanned for the shape across the documents: 91 negation phrases in
`docs/gaps.md`, `docs/known-limits.md`, `docs/mimari.md`,
`docs/api-surfaces.md`, `docs/security.md` and the two READMEs. Their objects:

- **a predicate, not a name** — "nothing depends on `lower()` folding
  non-ASCII" (ADR 0038, and three CHECK constraints did), "nothing emits
  `Vary`", "no index whose leading column is `title`", "No JSON response
  anywhere carries `Cache-Control`". Each needs a different oracle: the schema,
  a response header, an index definition, a live server;
- **a category, not a literal** — "No table named for a suggestion exists in any
  migration". A machine cannot decide what is named "for a suggestion";
- **a name the tree could hold** — the only decidable sub-shape, and the tree
  carries essentially none of it in that literal form.

There is no closed vocabulary of predicates, so there is no population, so there
is no gate. The trigger is in the ADR.

The trigger's condition is ALREADY MET, which is worth saying plainly so the
deferral does not read as an indefinite one. Measurement 0066 writes "No table
named for a suggestion exists in any migration — HOLDS, 82 tables, none", and
tables are a population `internal/app`'s schema reader already enumerates. That
sentence is where the repair belongs the day somebody wants it: an assertion
inside the gate that owns the table list, not a scanner over this document.

Of the 91 negations, the great majority are of the first two shapes above and
name no enumerated population at all. The count that DO is not stated here,
because it was not measured — the scan classified the shapes, not the objects,
and a number written from an impression is the exact defect this report opens
with.

## 6. Not built, and reported instead: the measurements index

`docs/measurements/README.md` carries a Lines column, one number per report.
Counted against the files: of the 31 rows that predate this report, 19 are
wrong, most by exactly four lines — the length of a header block added to each
of them and never carried back. It is a real instance of the class, and it is
deliberately outside the
vocabulary — the number is a document's LENGTH rather than a population's size,
so it goes stale on the same commit that corrects any report, including a commit
that only adds a strike-through. Whether the column should be corrected or
dropped is the owner's call.

## 7. Left to the owner

1. **The Lines column of `docs/measurements/README.md`** — correct it or drop
   it. Section 6 has the reasoning; it is not gated either way.
2. **Where the Turkish number words live.** They are in
   `internal/arch/testdata/turkish-numerals.txt`, and the `.txt` extension is
   what puts them outside the language scan — the words are DATA for a scanner,
   not prose, and spelling them in a Go file would add lines to a ledger that
   may only shrink (ADR 0012). The existing alternative was
   `diacriticDataExemptions`, which would have kept them in the source and
   named them as data there instead. The file was chosen because the ledger
   stays untouched either way and a table in its own file can be read without
   reading the gate; the exemption list is the smaller change and remains
   available if the owner would rather have one place for that kind of
   declaration.

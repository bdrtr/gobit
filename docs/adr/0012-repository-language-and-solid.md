# ADR 0012 — English is the working language, and SOLID is enforced only where it can be measured

- **Status:** Accepted
- **Date:** 2026-09-03
- **Supersedes:** none
- **Related:** ADR 0001, ADR 0006, ADR 0011

## Context

Two rules were added to the development rules: code follows the SOLID
principles, and the repository contains no Turkish — the working language is
English.

Both rules arrive with the same problem. The repository was written in Turkish
and the Turkish is not a leftover; it is the majority. Measured on 2026-09-03:

| What | Count |
| --- | --- |
| Hand-written files the scan reads | 804 |
| …of which contain Turkish | 784 |
| Files whose PATH is Turkish | 41 |
| Test function names | 2852 |
| …of which carry a Turkish letter | **0** |

That last row is the whole difficulty. The repository already writes Turkish
without Turkish letters: `TestKayitBayatlamiyor`, `hatayolu_test.go`,
`bayatMuafiyetleriDenetle`. A rule stated as "no Turkish characters" would be
satisfied today by a single transliteration pass and would then certify a fully
Turkish repository as English. This was not reasoned about, it was run: folding
every Turkish letter to ASCII across the whole tree drops a letter-based
detector from 724 files to 0 while the repository stays exactly as Turkish as
it was.

SOLID has the mirror problem. Four of the five principles are already enforced
here — some very strongly, some not at all — and a blanket "follow SOLID" rule
would mostly restate what `.golangci.yml` and `internal/arch` already prove,
while implying enforcement for the parts nothing checks.

This repository's answer to an unmeasurable rule is always the same: write the
rule as a test, or write down that it is not enforced. Neither rule gets to sit
in the middle.

## Decision

### 1. English is the working language; the switch is incremental and ledgered

New code is written in English: identifiers, comments, test names, assertion
messages, documents, and the messages that reach a user.

Existing code is translated file by file, as it is touched. The transition is
governed by a **ledger**: `internal/arch/testdata/turkish_ledger.txt` names
every file that still contains Turkish, and `internal/arch/testdata/turkish_paths.txt`
names every file whose own path does. Files outside the ledgers must be
English, and `TestNoTurkishOutsideLedger` enforces it.

The ledger may only shrink. Removing a line requires the file to actually be
translated — `TestLedgerIsNotStale` refuses a line whose file is already clean
— and adding a line requires editing a checked-in file, which a reviewer sees.
A new file is born clean because nobody adds it to the ledger.

The reason for a ledger rather than a deadline: a rule that fails on 784 files
on its first day gets switched off, and a rule that is off enforces nothing.

### 2. The detector has three lanes, because one lane is a `sed` away from lying

`internal/arch/language_test.go` runs three independent scans, and a file is
Turkish if any of them fires:

1. **Diacritic** — the letters `çğıöşüÇĞİÖŞÜ` anywhere in the file. Cheap,
   precise, and defeated by transliteration.
2. **Word** — Turkish function words that survive transliteration, in comments
   and string literals only. The list is short on purpose: every candidate was
   measured against the 7711 files of the Go standard library and only the ones
   with zero hits were kept. `ve` matched 300 standard-library files, `bu`
   matched 14, and `var` is a Go keyword.
3. **Identifier** — Turkish stems matched as whole camelCase or snake_case
   parts of an identifier. Whole-part matching is what makes the lane usable:
   substring matching reads `module` as `modul`, `rollback` as `rol` and
   `reason` as `son`.

The word lane is what gives the ratchet its teeth. Measured today it finds the
same files the diacritic lane finds and adds none — its value is not coverage,
it is immunity to the cheapest possible fake translation.

The letter class deliberately excludes `â î û å é ô`. Those carry no Turkish
signal and do appear in this repository as legitimate reference data: the ISO
3166 seed holds Åland, Barthélemy, Côte d'Ivoire and Réunion. A class that
covered all non-ASCII would turn correct reference data into a permanent
violation.

### 3. A file is translated whole, in a change that does nothing else

Translating a file and changing its behaviour never share a commit.

A half-translated file is worse than either language: the ledger cannot express
it, and a reviewer looking at the remaining Turkish cannot tell whether it is
left over or newly added. And a translation is large — the four files this
decision was written alongside total 6070 lines — so a behavioural change
buried inside one is a change nobody reads.

The practical consequence is not "never touch a Turkish file". It is: fix the
behaviour in the file's existing language, then translate the file in its own
commit. Touching one line of a 1655-line Turkish test does not oblige
translating it in that change.

Language is therefore a property of the FILE, not of the line. A new row added
to the ADR table in `README.md` is written in Turkish for as long as `README.md`
is Turkish, because a table with one English row in it is harder to read than
either language and tells the reader nothing about which one is intended.

This rule is **not enforced by a test**. It is a property of a commit, not of a
tree, and every test here reads trees.

### 4. Error codes never translate; error messages do

`internal/core/errors` already writes this down: the code is a machine-readable
identifier and part of the API contract, the message is for a human. So
`product_not_found` stays exactly as it is forever, and the message beside it
becomes English.

The same holds for every other name a machine reads: database table and column
names, JSON field names, container service names, event and status codes,
OpenAPI operation ids. These are wire contracts. Translating one is a breaking
change disguised as a language change, and it would break clients that this
repository cannot see.

The consequence is uncomfortable and is accepted: some machine-readable
Turkish is permanent. `internal/core` carries event codes that read as Turkish
and will keep reading as Turkish.

### 5. SOLID is enforced where a violation is mechanically detectable

The five principles are not in the same state here, and the rule reflects the
measurement rather than flattening it:

| Principle | Today | Decision |
| --- | --- | --- |
| **DIP** | Strongly enforced: 211 deny entries in `.golangci.yml` plus four AST tests that keep the module trees from importing each other | Keep; it is the strongest rule in the repository |
| **OCP** | Enforced on the plugin/provider axis by `TestEklentiCekirdegeDokunmadanSaglayiciEkler` | Keep |
| **ISP** | Guaranteed **structurally** across module boundaries — a consumer that may not import the provider is forced to declare a narrow interface in primitive types — but unchecked inside a module | Enforceable; a test is worth adding |
| **SRP** | Enforced at the macro level only (module isolation, the error-path rules). Nothing at the level of a single type | Review-level; see below |
| **LSP** | No check at all | Review-level; see below |

For SRP-micro and LSP the decision is to write down that they are **review-level
rules with no test**, and to say so out loud rather than let the word "SOLID"
imply otherwise.

Size linters stay **off**. Turning on `interfacebloat` repository-wide produces
20 violations today, and the largest, `product/repository.Store`, has 53
methods. Splitting it into six interfaces to satisfy a threshold would change
no dependency and would leave every caller depending on the same code — metric
satisfaction, not design. When that type is genuinely split, it will be split
because a consumer needed less, not because a linter counted.

### 6. The detector is a floor, not a fence

Named here so nobody mistakes a green suite for a translated repository. The
scan cannot see:

- **Calques.** English words in Turkish sentence order — "the record is not
  found returns" — carry no signal whatsoever.
- **The agglutinative tail.** Turkish suffixes words, and whole-part matching
  sees only the bare stem: `akis` is in the stem list, `akisi` is not. A stem
  list cannot be completed, only extended.
- **Machine-readable Turkish**, which decision 4 has already made permanent.
- **Git history and the CHANGELOG.** History is not rewritten, and the
  CHANGELOG describes releases that were written in Turkish. Both stay.
- **Fragmented text.** Messages assembled by concatenation, format-string
  pieces, and Turkish inside a regular expression.

`TestDetectorIsNotBlind` and `TestDetectorFindsPlantedTurkish` pin the floor so
the lanes cannot quietly stop working, but they cannot raise it.

## Consequences

- Every new file is English, and the first pull request that adds a Turkish one
  fails on `TestNoTurkishOutsideLedger` with the file named.
- The remaining work is a number that only goes down: 784 files and 41 paths on
  2026-09-03. Progress is visible in a diff instead of being asserted in a
  status meeting.
- `internal/adminui`, the panel tree from ADR 0011, is the first fully English
  package and doubles as the detector's negative control: a lane that started
  flagging correct English would be caught there before it was caught in a
  review.
- Shrinking a ledger by hand across hundreds of lines is where mistakes get
  made, so the maintenance path is written down rather than improvised. Setting
  `GOBIT_UPDATE_TURKISH_LEDGER` rewrites both ledgers from the current tree and
  then **fails** — a run with the flag on can never be green, so CI cannot
  rewrite the debt it is supposed to be measuring.
- One file is exempt from the content scan: the detector itself, which carries
  the letter class, the word list and the stem list as data.
  `TestDetectorExemptsOnlyItself` proves the hole is exactly one file wide and
  that it is still needed.

## What this cannot express

- **How good the English is.** The scan proves the absence of Turkish, not the
  presence of readable English. A file translated word by word passes.
- **Whether a translation preserved meaning.** The godocs in this repository
  carry the reasoning, not just the description; a translation that keeps the
  sentences and loses the argument passes every test here.
- **SRP and LSP.** Decision 5 says so explicitly rather than implying coverage.
- **The commit rule of decision 3**, which is a property of history.

## Rejected options

**A single big-bang translation.** Translating 784 files at once produces a
diff nobody reads, and it lands on top of a working tree that is being changed
by feature work at the same time. Every conflict would be resolved by someone
choosing between two languages under time pressure. Rejected on reviewability.

**A detector that only looks for Turkish letters.** Simpler, faster, and
measurably wrong: the transliteration run described in the context turns it
green over an untouched Turkish repository. It would have made the ratchet a
liability, because a false "done" is worse than no signal.

**A spell-checker (`cspell`, `misspell`) instead of a custom scan.** Neither
carries a Turkish dictionary that survives contact with Go identifiers, both
would need a per-word ignore list larger than this test, and neither ratchets:
they report, they do not hold a floor that can only rise.

**Translating the CHANGELOG and git history retroactively.** The CHANGELOG
describes releases that shipped; rewriting it would decouple the notes from the
tags they belong to, and `README.md` already forbids retroactive edits to
released entries. History is not rewritten at all.

**Turning on the size linters as SOLID enforcement.** `interfacebloat`,
`funlen` and `gocognit` would produce a violation list that gets closed by
splitting types along thresholds rather than along dependencies. The 53-method
`Store` split into six interfaces still has one implementation and the same
callers. Rejected because it converts a design principle into a counting
exercise and then reports the counting as design.

**Keeping Turkish in comments and moving only code to English.** This is where
the repository already is by accident, and it is the worst of the three states:
the reasoning — which is the valuable half of these godocs — stays unreadable
to a contributor who cannot read Turkish, while the identifiers around it
promise that they can.

## Reopening the decision

Reopen when the content ledger reaches zero. At that point
`TestDetectorIsNotBlind` fails by design — its lanes will find nothing — and
the assertion must be replaced by one that says the migration is complete. That
failure is the intended end of this ADR, not a bug.

Reopen decision 5 if a mechanical check for SRP at the type level or for LSP is
found that does not reduce to counting. Until then the row stays honest.

## Related

- ADR 0001 — cross-module communication; the narrow consumer-side interfaces
  that make ISP structural here.
- ADR 0006 — workflow access to modules; the same rule on the workflow axis.
- ADR 0011 — the admin panel tree, which is the first package written under
  this decision.

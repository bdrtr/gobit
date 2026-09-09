# ADR 0084 — The defect ledger's numbers are checked, and the rule it already stated is now enforced

**Summary:** `docs/gaps.md` numbers must be unique and dense; three rows had
collided with existing ones before anything looked.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

`docs/gaps.md` opens by saying its numbers "never move and a closed row is never
deleted", because files across the tree cite one as the argument for their own
shape. Nothing checked it.

On 2026-09-09 three rows were appended carrying D33, D34 and D35 — all three
already taken. The mistake was reading the TAIL of the file and assuming it was
ordered. It is not: the ledger groups rows by subject, so the last row is not
the highest number. A number is an ADDRESS, and two rows answering to one make
every citation of it ambiguous.

## Decision

**The ledger's numbers are unique and DENSE**, checked by
`internal/arch/gap_ledger_test.go`.

Dense rather than merely unique, because a skipped number reads exactly like a
deleted row and a deleted row is what the ledger's first paragraph forbids. With
1..N complete, an absent number means either a row was removed or one was
misnumbered, and both are worth stopping for.

**The three rows are renumbered to D36-D38** rather than left in place with a
note. The alternative would leave two rows sharing an address, which is the
defect itself.

## Consequences

- **The commit messages that introduced those rows still name D33, D34 and
  D35**, and that is left standing: a commit message is history. A reader who
  follows one of those numbers lands on an unrelated row and finds the story in
  the gate's godoc.
- **A number cannot be reused or skipped by accident again.** Proved three ways:
  a row taking an existing number, a row skipping one, and a blinded row
  pattern — the last one because an empty ledger has no duplicates by
  definition.
- **The gate reads the ledger's SHAPE, not its content.** It says nothing about
  whether a row is true, closed, or worth keeping.
- **The ledger stays grouped by subject.** Sorting it would make appending safe
  and would move numbers, which is the one thing the file forbids.

## Rejected

- **Sorting the ledger by number.** It would make the tail meaningful and the
  mistake impossible, at the price of moving rows the file says never move.
- **Uniqueness without density.** It permits the deletion the ledger forbids and
  cannot tell a removed row from a misnumbered one.
- **Leaving the collision and adding a note.** Two rows at one address is the
  defect, and a note does not resolve a citation.

# gobit — working agreement

## An ADR records a decision. It is not the argument that produced it.

**Four sections, in this order, and nothing else:**

```
## Context       — what forced a choice
## Decision      — what was chosen
## Consequences  — what it costs and what it buys
## Rejected      — what else was on the table, one line each
```

**An ADR may not exceed 80 lines.** `internal/arch/adr_shape_test.go` enforces
it from 0052 onward.

**The decision goes in two sentences.** If it will not fit, it has not been made
yet, and what is missing is the decision rather than the room.

**Measurements do not go in an ADR.** Numbers, tables, probe output, cluster
settings and reproductions go to a file under `docs/measurements/`, named for
the record it serves. The ADR links to it in one line:

```
Measurement: [measurements/0049](../measurements/0049-group-pricing.md)
```

The measurement file may be as long as it needs to be. Nobody has to read it.
Everybody has to read the ADR.

**The opposition narrative goes in the commit message.** "The draft said X, the
opposition pass found Y, so the decision became Z" is the process of deciding.
It is worth keeping, and git is where it is kept.

**Write a declaration, not a defense.** *"We chose X, because Y. It costs Z."*
Not *"X is the strongest candidate and this is written down as such."* A sentence
that defends itself is the same mistake as a measurement in an ADR: it confuses
the record with the argument.

**Amend by adding, not by editing.** A decision that changes gets a new ADR and
a `Superseded by NNNN` line on the old one. Strikethrough corrections inside a
Consequences section belong to the era before this rule.

## ADRs 0001–0051 are the historical record

They predate this agreement and are NOT rewritten. Each carries a two-line
`**Summary:**` under its title, and that is the only edit they take.

`docs/adr/README.md` is the index: number, title, the decision in one sentence,
status. Read it first and descend into a file only when the one sentence is not
enough.

## docs/gaps.md is the defect ledger

One row per fault this repository has found in itself: number, one sentence, and
what closed it. The reproduction is in the commit, the reasoning in the ADR the
row names. Numbers never move and a closed row is never deleted — code cites
them as the argument for its own shape.

The gap inventory it used to carry closed on 2026-09-08; new work is decided in
an ADR, not queued here.

## Verification

`make lint`, `make vuln`, `go test ./...`, `make test` (race),
`make test-integration` (Docker), `make smoke` and `go mod tidy` are separate
lanes; `go test ./...` is not the suite. A gate is not trusted until a mutation
makes it fail, run with `-count=1`.

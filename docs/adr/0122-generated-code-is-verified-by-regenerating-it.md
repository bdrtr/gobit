# ADR 0122 — Generated code is verified by regenerating it

**Summary:** CI runs the generators and fails on any difference, the way it
already asks whether go.mod is up to date. It costs a pinned tool build in the
lint job and buys the end of a drift no lane could see.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

This repository commits generated code: 75 sqlc files written from 75 query
files, and 14 gqlgen files. Nothing compared a generated file with its source.

Gap D60 is what that costs when the drift is a comment: a sentence corrected in
a `.sql` file without regenerating stood against its own copy for an hour, on
main, with every lane green. Measuring the class turned up the worse half. The
source file is not the query the database runs — the string in the generated file
is — so an edit to the source alone is executed by nothing. Multiplying the B2B
spending window by a thousand in the source and not regenerating leaves `go
build`, `go vet`, the unit lane and the integration lane against a real
PostgreSQL all clean, because they run the old query, which is the correct one.
The edit is invisible exactly while it is wrong.

## Decision

CI regenerates the tree in the lint job and fails on any difference, exactly as
it already does for `go mod tidy`. The generated files stay committed.

## Consequences

The comparison is the whole file rather than a part of it, which is what makes it
cover the measured drift as well as the found one.

It costs one pinned tool build. sqlc is installed from the version the Makefile
pins: 27 seconds on an empty build cache and 4 on a warm one, in a job that
already spends minutes in golangci-lint. `make gen` itself is 2 seconds. gqlgen
needs no install — it runs through `go tool` from the version in `go.mod`, so the
generator cannot drift from the library it generates calls into.

Nothing changes for a developer without the tools. The tree still holds the
generated files, so a stack trace still lands in a file that is in the
repository, and building gobit still needs only Go. The gate is CI's, and a
contributor learns from it rather than from a rule they had to know.

The gate is only as good as the pin. A tool version bumped in the Makefile
rewrites the tree, and that diff is the change rather than a failure — it has to
be committed with the bump.

Measurement: [measurements/0122](../measurements/0122-what-a-stale-generated-tree-hides.md)

## Rejected

**A test comparing each generated function's comment with its source block.** It
catches the drift that was found and none of the one that was measured: a query
multiplied by a thousand leaves every comment byte-identical.

**Not committing generated code.** A stack trace would land in a file that is
not in the tree, and every consumer would need the generators to build.

**Trusting `make gen` to be run.** That is the state that produced D60, and the
drift it produces is silent by construction: the generated file is the copy a
reader lands in and the copy nobody edits.

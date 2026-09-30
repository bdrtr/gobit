# ADR 0254 — A program calls itself by its own name

**Summary:** `gobit.App.Name` sets what the usage text and every command line an
operator subcommand prints call the program; empty is gobit, and a generated
project names itself.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

The operator subcommands print command lines an operator is meant to copy: the
rollback plan, the dead-letter refusals and footer, the seed reset plan and the
usage text. They named the program with a constant, `gobit`, which is right for
this repository's binary and wrong for every program built on it. A project
`gobit new` wrote as `shop` told its operator to run `gobit migrate down`, a
binary that operator does not have. The constant was chosen over `os.Args[0]`
because that is whatever path the process was started with.

## Decision

The facade takes `Name(string)`, and every text an operator subcommand prints
names the program by it, empty meaning `gobit`. The project `gobit new` writes
calls `Name` with the last element of its module path, which is what `go build`
names its binary.

## Consequences

- A command line an operator copies names the program they run. The reason
  for a name over `os.Args[0]` stands: the name is the embedder's, not the
  launch path's.
- The name reaches each text function as a parameter rather than through a
  package variable, so two installations in one test binary cannot overwrite
  each other's. Sixteen functions take it.
- A test calls every one of those texts with another name and refuses `gobit`
  before any of the program's subcommands; the generated project's lane runs
  its binary and holds its help to its own name.
- The example programs under `examples/` build against this tree and call
  themselves by their directory's name.
- `App.Name` joins the published names, a promise kept until 1.0.0
  (ADR 0026).
- The known limit keeps its other half: a generated project serves guests only.

## Rejected

- `os.Args[0]`: under `go run` it is a build cache path, and a plan carrying it
  cannot be copied.
- A package variable set by `Main`: a second installation in the same process
  would rename the first one's text.
- Printing no program name in the plans: an operator would have to assemble the
  command, and the plans exist so they do not.

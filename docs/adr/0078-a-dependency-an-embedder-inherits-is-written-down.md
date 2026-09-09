# ADR 0078 — A dependency an embedder inherits is written down, and a known vulnerability fails the build

**Summary:** Every direct require carries a written reason, every indirect one a
line, and `govulncheck` runs on the root and both example modules in CI.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0046 added eight modules to the graph and recorded the gap in its own
Consequences: "nothing in this repository audits third-party requires ... It is
being taken on judgment, with no automated second opinion." That was still true.

gobit is imported (ADR 0025), so a line of its `go.mod` is a line in the
embedder's module graph, their `go list -m all`, their vulnerability scan and
their legal review. Twenty-one direct requires bring seventy-five indirect ones.
`gosec` was checked rather than assumed and has been enabled all along.

## Decision

**Every DIRECT require carries a sentence saying why an embedder inherits it**,
in `internal/arch/dependency_allowlist_test.go`. A new one cannot land without
somebody writing that sentence, which is the second opinion ADR 0046 asked for.

**Every INDIRECT require is a line in `testdata/indirect-dependencies.txt`**,
names only. Seventy-five sentences about somebody else's transitive graph would
be seventy-five sentences nobody maintains.

**The reason for auditing the indirect set is not the obvious one**, and the
obvious one was checked and is wrong: ADR 0046's seven transitive modules
arrived BECAUSE two direct lines were added, so a direct-only ledger would have
stopped that change anyway. The real case is the version BUMP of a module
already on the direct list: it can pull a new transitive module into every
embedder's graph while the direct line's path does not change, and nothing about
that appears in a diff of direct requires.

**An unrecognized top-level directive in `go.mod` FAILS the audit.** A scan not
scoped to the require blocks reads a line inside `exclude (` as a require, so a
module deliberately kept OUT of the graph would be demanded in the allowlist —
the opposite of what it means.

**`govulncheck` runs on the root and both example modules and fails the build**,
the examples because they are the closest thing here to what an embedder
compiles. **No exemption mechanism ships with it:** an advisory with no fix turns
the build red and somebody decides that day — pin, patch, or write the exemption.
Building the escape hatch first is a capability with no consumer (ADR 0009).

## Consequences

- **Adding a direct dependency now costs a sentence**, and that is the intended
  friction: it is paid by whoever chooses the dependency rather than by the
  embedder who discovers it. A transitive module entering the graph appears in a
  diff, whatever pulled it in.
- **The audit reads `go.mod`, not the build.** It sees the graph MVS computed
  and not what is linked, and says nothing about licenses or maintenance.
- **`make vuln` had a FALSE GREEN in its first draft and the guard is the
  lesson.** A shell `for` loop exits with the status of its LAST iteration, so a
  red root with green examples returned zero. `|| exit 1` is inside the loop and
  a `found` counter refuses a run that scanned nothing — "no vulnerabilities"
  and "I looked nowhere" cannot share an exit code. Both were proved by running
  the loop with and without them.
- **Three advisories are reported today and none fails the build**, because
  `govulncheck` reports what is REACHABLE and none of them is called. That
  distinction is why this can be blocking at all.

## Rejected

- **A single floor over both require blocks.** With 21 direct and 75 indirect, a
  floor of 60 passes when every direct line is lost. Two floors, separately.
- **A reason per indirect module.** It would rot, and a ledger that rots is worse
  than a list that is merely long.
- **A separate CI job for the scan.** It needs no database and finishes in
  seconds; a job would spend more on its own startup.
- **Running `govulncheck` on the root only.** The example modules' graphs can
  differ from the root's, and they are what an embedder's build looks like.

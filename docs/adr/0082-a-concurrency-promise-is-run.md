# ADR 0082 — A promise of concurrency safety is RUN, and Bootstrap's ordering guarantee is about routes

**Summary:** Every type that promises concurrency safety and holds a primitive
now has a named test that runs it from two goroutines.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

The race detector only reports races on code that ACTUALLY RAN concurrently, so
a lock nothing exercises from two goroutines is one the `-race` lane never sees.

**Measured on 2026-09-09.** Five identical provider registries — where every
payment, notification, file, fulfillment and tax plugin extends the system —
each promise in their godoc to be safe for concurrent use, and not one had a
test running them from two goroutines. Deleting the mutex from payment's
`Register` left the whole package GREEN under `-race`.

The coverage was not absent, it was SELECTIVE: `core/container`, `core/link`,
`core/http`'s idempotency store and limiter and the workflow memory store all
had one. The core was covered; the extension points were not.

**The same measurement found a contradiction that turned out not to be one.**
Two lazy container wrappers refuse `sync.Once` and say why: a resolution that
failed while the other module was not yet registered "would leave search dead
for the life of the process". Ten `sync.Once` wrappers say the opposite —
"resolving it again would only reproduce the same answer". Both are correct, and
the rule separating them was written nowhere: `Bootstrap` guarantees no module
binds a ROUTE before every module has registered, and a subscriber is not a
route. On the Redis bus `Subscribe` starts its consumer inside `registerAll`,
with the group at the head of the stream, so a backlog arrives while later
modules are still registering.

## Decision

**Every type that carries a concurrency primitive inside a package that promises
concurrency safety has one NAMED test that runs it from more than one
goroutine**, kept by `internal/arch/concurrency_promise_test.go`.

**The population comes from two independent directions**: the prose alone could
be evaded by deleting the sentence, and the primitive alone would demand a test
for every lazily built cache on a single-threaded path. **It is read at PACKAGE
granularity**, because the promise is not always on the type — `core/container`
puts it in the package doc, `workflow` on the constructor, `core/http` on the
interface.

**The witness is a written map**: whether a test exercises a promise is a
judgment, and judgments are not inferred from substrings here. **`Bootstrap`'s
godoc now says what its guarantee does not cover.**

## Consequences

- **Ten tests were added**, every one mutation-proved: deleting a lock, or
  swapping a retry for `sync.Once` or an `atomic.Value` for a plain field, turns
  the matching test red.
- **What a concurrent test buys over its single-threaded sibling was measured**
  on the searchpg catalog: swapping the mutex for `sync.Once` turns BOTH red and
  separates nothing, while deleting the LOCK leaves the single-threaded test
  green and makes the concurrent one report a data race.
- **A promise cannot be withdrawn to escape the gate.** Deleting the sentence
  from a whole package turns the gate red from the other direction: the witness
  then names a subject that no longer exists. Proved.
- **The written map exists because the searched version over-credited**: it
  reported `core/http` covered on the words "has to go through" in a message
  string, next to an unrelated constructor call.
- **The gate holds the population, not the quality**: it does not check that a
  witness really contends, or asserts anything worth asserting.
- **The lazy wrappers' two camps are explained rather than opposed**, at the
  guarantee they depend on.

## Rejected

- **Deriving the witness by search.** It over-credited on its first run.
- **Reading the promise on the type's own godoc.** Three subjects would leave
  the population — the unit mistake ADR 0026's first gate made.
- **Demanding a concurrent test for every mutex.** A lock on a single-threaded
  path would be forced into a test that proves nothing.
- **Moving subscriber delivery after `registerAll`.** A real option and a
  different decision. What was missing here was the sentence, not the ordering.

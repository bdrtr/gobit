# ADR 0079 — A benchmark carries a ceiling, and the ceiling is allocations

**Summary:** Every benchmark in the tree is priced by a `benchbudget.Budget`
whose ceiling is allocations per operation, asserted in the ordinary test lane.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

Five benchmarks have measured the Go side since 88bec82, and not one of them
could fail. `make bench` is a lane a person runs by hand and reads with their
eyes; between two runs of it, a change could double the allocations of the
promotion arithmetic and every gate in this repository would stay green.

A benchmark that nobody can fail is a measurement, and this repository already
knows what an unread measurement is worth: ADR 0075 found eighteen of thirty-four
rows of the measurements index wrong.

## Decision

**Every benchmark carries a `benchbudget.Budget`**, and
`internal/arch/benchmark_budget_test.go` walks the tree to make sure of it. The
population is the DECLARATION, not the filename: a benchmark written beside the
tests it belongs to is in the population exactly like one in a `*_bench_test.go`.

**The ceiling is allocations per operation and never nanoseconds.** A timing
threshold measures the machine, and would have to be loose enough to pass on the
worst runner — which is loose enough to miss the regression. Allocations per
operation are a property of the code.

**The budgets run in the ordinary test lane**, with no build tag, no separate
make target and no CI job of their own. A lane nobody runs is the problem this
record exists to fix.

**Four of the five ceilings are the measured figure, exactly.** Zero for the two
cart functions, 18 and 2 for the promotion ones. A small number is exact because
one more allocation in it means something.

## Consequences

- **A per-request allocation added to a priced path now fails a test**, and it
  fails with the sentence that says what the number is for. Proved: cloning one
  string in `assembleTotals` moves it from 0 to 1 and turns the lane red.
- **The assumption behind the single lane was checked and is FALSE for one
  benchmark.** Four counts are identical with and without `-race`. The fifth —
  the only one driving an HTTP surface — is 8,432 without the detector across
  five runs and 8,702–8,705 with it across six. So `Budget` carries a second
  ceiling used only under race, and a single number would have had to be the
  looser of the two.
- **Three ways of measuring nothing fail instead of passing**: an empty table, a
  benchmark that skipped (a zero result is under every ceiling there is), and a
  `-benchtime` given as a fixed iteration count, where a per-operation figure
  stops meaning what is asserted.
- **A budget cannot name one benchmark and price another.** The entry holds the
  function VALUE and `Check` asks the runtime for its name, so the arch gate and
  the assertion cannot be talking about different functions.
- **8,432 allocations per storefront listing is left standing.** Stopping a
  number from rising and lowering it are different decisions, and only the first
  is made here.
- **What is not caught: a change that gets slower without allocating.** A sort
  that turns quadratic over buffers it already owns is invisible to every
  ceiling here. The benchmark still prints the time and a person still reads it.

## Rejected

- **A build tag and a lane of its own.** It was the first draft. The reason for
  it — that race numbers would be different — is true of one benchmark out of
  five, and a second ceiling costs a field where a second lane costs a CI job
  and the certainty that somebody eventually stops running it.
- **A ns/op ceiling beside the allocation one.** It would be red on a busy
  laptop and green on a fast one, which is a gate that trains people to ignore it.
- **Selecting the population by the `*_bench_test.go` suffix.** The obvious
  reading, and it would report a clean tree it had never looked at the day
  somebody wrote a benchmark in an ordinary test file.
- **Deriving the ceiling automatically from a stored baseline.** A number that
  updates itself asserts nothing; the point of the sentence beside each figure is
  that raising it is a decision somebody makes and signs.

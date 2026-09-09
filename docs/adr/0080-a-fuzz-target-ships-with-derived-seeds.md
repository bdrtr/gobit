# ADR 0080 — A fuzz target ships with seeds, and the seeds are derived where the fuzzer cannot reach

**Summary:** Three fuzz targets ship, every one carries at least three seeds, and
the seeds that matter were computed rather than discovered.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

Nothing in this repository fuzzed. Two places wanted it most: the 128-bit
multiply-divide the promotion allocation is built on, whose inputs are the ones
nobody thinks of, and the storefront GraphQL endpoint, the only surface where an
anonymous caller decides the SHAPE of the work.

The lane matters as much as the targets. `go test` does not generate inputs — it
runs a target's SEEDS as ordinary sub-tests and nothing else. A target with no
seeds runs on the day somebody remembers to fuzz it, and never in CI.

## Decision

**Three targets ship**, and each asserts a property rather than an example:
`FuzzMulDivMod` against `math/big`, `FuzzAllocateAcross` against the sentence the
largest-remainder method exists for, and `FuzzStorefrontDocument` against the
three things a storefront endpoint owes any document off the wire.

**Every target carries at least three seeds**, enforced by
`internal/arch/fuzz_seed_test.go`, because seeds are what the ordinary lane runs.

**The generated lane is `make fuzz` and it is NOT a CI job.** A fuzz run's value
grows with its length, and thirty seconds on every push buys what the seeds
already bought.

**The seeds that matter are DERIVED**, and that is the finding this record
exists for.

## Consequences

- **Two mutations that should have fired did not, and both were the fuzzer's
  reach rather than the target's assertions.** Changing `hi >= d` to `hi > d` in
  the overflow guard survived 25 seconds and ten million generated inputs: the
  case that separates them needs the 128-bit product to land in one exact window
  of width 2^64. Mutating every storefront limit to "unlimited" survived forty
  seconds and 3.7 million documents: a generated document is nonsense long
  before it is large.
- **Both were then caught in 0.00s by a hand-computed seed.** `2^32 x 2^32 / 1`
  puts `hi` exactly on `d`, and the mutated guard panics inside `bits.Div64`. A
  77 KB document reaches the request-size refusal, and writing that refusal with
  `net/http.Error` answers plain text while building it from an internal error
  answers 500 — one seed, two assertions proved.
- **A fuzzer covers the space it can reach, which is not the space that is
  interesting.** The rule this leaves: after writing a target, mutate the code it
  guards; if generated inputs do not find it, compute the boundary and seed it.
- **The order-independence property earns its place.** Deleting the identity
  tie-break from the allocation's sort is caught by a SEED — three lines of one
  kurus each — and no table test in the tree had that cart.
- **One assertion is a backstop and is written down as one.** No document reaches
  the response-byte ceiling against this catalog: the field-repetition limit
  caps a request at twenty pages of about 14 KB.
- **`make fuzz` repeats a shell trap this repository already paid for.** A `for`
  loop exits with its LAST iteration's status, so a red first target returned
  zero until `|| exit 1` moved inside the loop; a `found` counter refuses a run
  that enumerated nothing. Both proved by running the loop both ways.
- **The `-fuzz` pattern is anchored, and the reason was measured rather than
  assumed.** `go test` REFUSES to run when the pattern matches more than one
  target, so an unanchored loop would stop dead the day a second target shared a
  prefix — and every target after it would go unfuzzed.
- **An existing gate caught the new lane and had to learn it.** `-run '^$'` was
  allowed to match nothing only beside `-bench`; `-fuzz` selects work the same
  way and is now the second EXACT spelling, never a prefix, because `-fuzztime`
  starts with it exactly as `-benchmem` starts with `-bench`.

## Rejected

- **A fuzzing job in CI.** Bounded to seconds it repeats the seeds; unbounded it
  is a cost with no ceiling. The corpus belongs to whoever is hunting.
- **A target over `computeDiscounts` end to end.** Most of it would be an input
  generator, and the generator is where the assumptions would hide.
- **Committing the generated corpus.** `go test` runs `testdata/fuzz` as seeds,
  so a corpus checked in becomes a suite everybody pays for on every build.

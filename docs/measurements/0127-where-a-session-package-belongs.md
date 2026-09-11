# Where a session package belongs — measured 2026-09-11

Serves [ADR 0127](../adr/0127-a-working-identity-ships-outside-the-module.md).

## The three shapes, and the one measurement that decides between them

Only one question separated them, and the dependency gate had already answered
it in its own words: *"gobit is imported (ADR 0025), so every line of its go.mod
is a line in the embedder's module graph, their `go list -m all`, their
vulnerability scan and their legal review. A dependency here is not a private
choice."*

```
find . -name go.mod
./go.mod
./examples/plugin/go.mod
./examples/starter/go.mod
```

`plugins/*` are in the MAIN module. So an in-tree plugin's requires are every
embedder's requires, and a WebAuthn library — which this package grows towards —
would land in the graph of a shop that wanted a product catalogue. Only a
separate `go.mod` keeps it out.

| Shape | Passkey library lands in | Turned on by |
|---|---|---|
| an in-tree plugin | every embedder's graph | one environment variable |
| a published core package | every embedder's graph, plus a 1.0.0 promise | an import |
| a separate module, the shape taken | only the graph of whoever imports it | an import |

The first two are written as descriptions rather than as paths on purpose: they
were not built, and a path naming an option a record rejected sends a reader
grepping for something nobody wrote.

## What the first slice costs in dependencies: nothing

argon2id needs `golang.org/x/crypto/argon2`, and that module is already a DIRECT
require of gobit:

```
go list -m golang.org/x/crypto     # golang.org/x/crypto v0.55.0
```

So the cookie-and-password slice adds no module to anybody's graph, in any of the
three shapes. It is the passkey slice that would, which is why the two are
separate records.

## What the separate module costs

Four gates and two lanes had to learn about the new tree, and each of them found
something rather than merely accepting a new entry.

**The out-of-tree compilation gate** (`TestTheOutOfTreeExamplesCompile`) already
compiles each separate module from outside, which is exactly the proof this
package needs: a verifier an embedder writes reaches `core/http`,
`core/container`, `core/module`, `core/db` and `core/identitytest`, or it does
not exist. Renaming that test and its list to stop saying "examples" was tried
and REVERTED: ADR 0025 and ADR 0043 name the test in their prose and both are
below 0052, the line where this repository stops rewriting its own records.

**The language detector's blindness check** refused the tree before any content
was scanned: *"contrib/ holds 8 file(s) the scan reads but is not in
scannedRoots, so the whole tree is excused without a single ledger line."* That
is the check earning its keep — a new root is exactly how a population grows
past a gate.

**The response-writing gate** rejected the module's first draft in five places:
it wrote its own JSON error envelope. `core/http` is published, so the module
uses `corehttp.WriteError` and `coreerrors` like every route beside it, and the
envelope shape, the masking and the request id stay in one place. The
hand-written version had claimed, in its own godoc, to want exactly that.

**The tests nobody ran.** `go test ./...` from the root does not reach a separate
module, so this package's twenty-eight tests would have been coverage that only
looked present. `make test-modules` runs every separate module and carries the
same FLOOR the vuln target already had, for the same reason: "no failures" and
"I ran nothing" must not share an exit code.

## The suite catching a real defect on its first real consumer

ADR 0126 published `identitytest.Contract` with no in-tree consumer. This module
is the first, and the suite was mutation-proved against it rather than only
against its own fixtures — making `CustomerID` read the request body fails with
*"the verifier consumed the request body"*, which is the rule whose symptom lands
furthest from its cause.

Three mutations, three deaths: the MAC check, the expiry check, and the body.

## The SQL nothing ran, and the floor that was argued away and measured back

The unit tests replace the store with one the test wrote, which proves the
session half and nothing about the storage half. What shipped unexecuted was two
statements, three CHECKs, one unique index and one migration. A mistyped column
compiles; a CHECK refusing a row the store writes is invisible until the first
sign-in in production.

`make test-modules-integration` runs them, and the mutations say it is real:

| Mutation | What fell |
|---|---|
| the read stops folding the address | the folding test and the whole-chain test |
| the conflict target becomes the address, not the customer | one-row and one-address |
| the folding CHECK is dropped from the migration | the schema witness and the raw-SQL test |

The folding pair is the one worth naming: the store folds and the column CHECKs
that it is folded, which is two rules for one fact. In a unit test the same
function writes both sides and they always agree. Only the database can say the
CHECK is the floor the store thinks it is.

The target's own floor was argued against in its first draft — "which module has
integration tests is today's fact, and a per-module floor imposes an unwritten
rule" — and then measured. Breaking the scan so it matched nothing made the
target run nothing and exit 0. The floor is ONE, in total: it puts no test-writing
debt on any module and still refuses a scan that quietly found nobody.

## Describing itself, and the loop that closes only when somebody asks

A module outside gobit can write itself into the OpenAPI document an
installation serves — that is what ADR 0035 published the schema vocabulary for,
and `examples/starter`'s loyalty module exists partly to prove it compiles from
a separate Go module.

Skipping it is silent in the direction that costs most: three endpoints an
integrator can call and cannot find. The core reports both halves —
`UndescribedRoutes` for a route nobody described, `UnmatchedDescriptions` for a
description matching no route — and reports them to whoever asks. Nothing asks
unless a test does, so one does.

Two mutations, two deaths: deleting one description fails the loop from the
route side, and misspelling a described path fails it from the other. A third
case neither can see is a path spelled the same WRONG way in both places, so a
second test matches every described path against the real router.

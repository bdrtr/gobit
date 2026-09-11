# A credential is personal data — measured 2026-09-11

Serves [ADR 0132](../adr/0132-the-contrib-identity-modules-answer-a-data-subject.md).

## What the two modules held, and what the sweep did about it

Between them, per person: an e-mail address, an argon2id password hash, a customer
id, a per-device credential id, and four timestamps. The sweep reported neither
module, because neither implemented any of the three capabilities — and the audit
built to catch exactly that could not see either one.

`internal/arch/plugin_personaldata_test.go` walks `plugins/`. It was written when
gap D30 found a plugin invisible to the app-level audit, and `contrib/` did not
exist then. This is the fourth time in this repository that a hand-kept population
deciding what gets verified has been found short: the language detector's roots,
the documentation scan's trees, the separate-module list, and now this.

So the roots are a list again, and the list is checked against DISK:
`TestThePersonalDataRootsCoverEveryTreeOfUnits` walks the repository root for
directories holding `<unit>/migrations/*.up.sql` and compares. Removing `contrib`
from the list fails it.

## The assumption that was nearly written into the record

The first draft of ADR 0132 said the sweep's reach could not be tested from
`contrib`, on the grounds that `internal/workflows/datasubject` is not importable
across a module boundary. The consequence would have been a record claiming a
compile-time pin was the strongest possible evidence.

It is wrong. Go's internal rule is about the import PATH, not the module:
`github.com/bdrtr/gobit/contrib/identity-passkey` shares the
`github.com/bdrtr/gobit/` prefix, so the internal tree is reachable. Probed rather
than reasoned — a file importing the coordinator compiled and ran:

```
ok  github.com/bdrtr/gobit/contrib/identity-passkey  0.004s
```

So the end-to-end test exists. It builds a registry, adds the module, constructs
the coordinator and runs the sweep. The cost is accepted: a contrib test now
depends on one of gobit's internal packages, and a refactor there breaks it —
which is the signal wanted.

## The pin and the test catch different things

| Mutation | What happens |
|---|---|
| `Erase` renamed on the module | COMPILE failure, from `var _ personaldata.Eraser = (*Module)(nil)` |
| the coordinator stops asserting `personaldata.Eraser` | `TestTheSWEEPReallyReachesThisModule` fails |

The first is not a bite by this repository's discipline — a compile failure proves
Go noticed, not that a gate did. But turning a silent skip into a build failure is
the pin's entire job, so it is recorded as the mechanism working rather than as a
mutation survived. The second is what the pin cannot see: a module that satisfies
the interface perfectly and is never asked.

The sweep also reported something the test had to be corrected for. A coordinator
built from a bare container answers with three holders from outside the module tree
— the saga store, the audit log and the link tables — each `Retained` with its
reason. The first version of the test required exactly one result and failed on
four; it now FINDS this module among them, because those three are the mechanism
refusing to pass over a holder in silence.

## The shape of the two erasures

The passkey module's erasure is deliberately unlike every other statement it runs.

| Question | Scoped by relying party? |
|---|---|
| which keys can sign this person in | yes (ADR 0131) |
| what is held about this person | NO |

Measured as a fixture: a row from an abandoned relying party, a row from the
current one, and a row written before the `rp_id` column existed. All three go, and
a scoped delete takes one — which would leave somebody who asked to be forgotten
with rows on disk and a report saying they were deleted.

It also does not go through the removal endpoint. That path refuses to take
somebody's last way in (ADR 0130), and an erasure routed through it would be
refused for exactly the person it serves. The guard defends an account being kept;
an erasure is the account ending.

## The hash is declared and not reproduced

Nothing else in this repository puts a password hash in a dossier, and the reason
is not squeamishness. The value tells the person nothing they do not know — they
chose the password — and it is the one field whose escape hurts THEM, because a
dossier travels by e-mail, ticket systems and support tools.

Dropping the column was rejected too: it is declared, so a dossier without it would
be false about what is held. The field carries a sentence instead, and the hash is
not selected from the database either — a secret that is never going to be reported
should not be in the process's memory.

The test asserts BOTH halves: that the field is present, and that no field anywhere
in the dossier carries the stored value.

## The mutation table

| Mutation | Bitten by |
|---|---|
| passkey: erasure scoped by relying party | `TestAnErasureTakesEVERYPasskeyOfThePerson` |
| passkey: dossier scoped by relying party | `TestADossierCarriesEveryPasskeyRowAndTheWholeCredential` |
| passkey: an address answered "deleted, 0 rows" | `TestAnAddressAloneCannotBeResolvedHere` |
| passkey: a store that cannot look answers `Nothing` | `TestAStoreThatCannotEraseSaysSOAndSaysWhat` |
| passkey: what is kept is not listed | `TestAStoreThatCannotEraseSaysSOAndSaysWhat` |
| passkey: the declaration is one column short | `TestADeclarationNamesEveryColumnOfTheTable` |
| session: the erasure ANDs its two handles | `TestAnErasureByADDRESSAlsoFindsThePerson` |
| session: the address is not folded | `TestAnErasureByADDRESSAlsoFindsThePerson` |
| session: a store that cannot look answers `Nothing` | `TestAStoreThatCannotEraseSaysSOAndSaysWhat` |
| session: the declaration is one column short | `TestTheDeclarationNamesEveryColumnOfTheTable` |
| session: the hash reaches the dossier | `TestADossierNamesTheHashAndDoesNotReproduceIt` |
| the audit's roots drop `contrib` | `TestThePersonalDataRootsCoverEveryTreeOfUnits` |
| the session module stops declaring its e-mail column | `TestEveryPersonColumnInAPluginIsDeclared` |

Thirteen, each run with `-count=1`, each restored from a scratchpad copy rather
than with `git checkout`.

The last two are the pair worth naming together: the first proves the audit looks
at `contrib`, and the second proves that looking there actually decides something.
A widened root with no failing case behind it would be a gate that walks further
and asks nothing.

# What an erasure keeps — measured 2026-09-25

The evidence behind [ADR 0172](../adr/0172-a-holding-says-what-erasure-does-to-it.md).

## 1. Two lists per holder

Before this record, each eraser's `Result.Kept` came from somewhere of its own:

| Holder | Kept came from |
|---|---|
| order, cart | a private `personalColumn{holding, erased}` row, so declaration and report shared a list |
| customer | a hand list of three columns beside the declaration |
| saga store | a hand list of two columns beside the declaration |
| audit, link, saga fallback (`datasubject`) | a `kept` field beside a `holdings` field on the same struct |
| invoice | a buyer list and an open list beside the declaration |
| identity-session, identity-passkey | built from the declaration on refusal |

One pair had already disagreed. The link holder reported
`<link table>.from_id` and `<link table>.to_id` as kept, and declared only
`to_id`. `from_id` is the b2b module's employee id and `to_id` the customer id
(`b2b/service/links.go`, `LinkEmployeeCustomer`), so the declaration was right
and the report named a column no controller had been told about (**D128**). No
test read the static holders' kept lists; the datasubject tests passed with
either.

## 2. The population

148 `Holding` literals in 14 declarers, found by type in the source of
`internal`, `plugins` and `contrib`. Nine units have an `Erase` method: the
saga store, customer, order, cart, invoice, `datasubject`, both contrib
identity modules and web-push. The other five declare only (auth, inventory,
b2b, review, settings), and every column they declare is `Kept`.

| Declarer | Holdings | Emptied | Kept |
|---|---|---|---|
| order | 26 | 11 | 15 |
| cart | 17 | 11 | 6 |
| customer | 15 | 12 | 3 |
| auth | 17 | 0 | 17 |
| invoice | 15 | 0 | 15 |
| inventory | 10 | 0 | 10 |
| identity-session | 10 | 10 | 0 |
| b2b | 9 | 0 | 9 |
| datasubject (audit, link, saga fallback) | 7 | 0 | 7 |
| review | 5 | 0 | 5 |
| settings | 5 | 0 | 5 |
| identity-passkey | 5 | 5 | 0 |
| web-push | 4 | 4 | 0 |
| saga store | 3 | 1 | 2 |

The order and cart values are their former `erased` flags. The customer and
saga-store values reproduce their hand lists exactly, in the same order. The
contrib modules and web-push delete rows, so their open columns go with them.
That is why "every open column is kept" is not a rule the gate imposes.

## 3. An answer about nobody

The production sweep, asked about a subject no holder has, shows that the
holders disagree about what "kept" means for a person who is not there:

- order and cart answer `anonymized` with rows 0 and an **empty** kept list,
  and their comment says why: those columns hold nothing about somebody who
  never bought anything;
- customer answers with its **full** kept list whatever it found, and its
  comment says why: kept describes the module, not one person's data.

Both are argued. This record does not choose between them, and the end-to-end
check asks for the declaration's exact kept list only from an answer that
touched rows. It is an open question.

## 4. The mutations

All were run with the baseline check and the build check.

| # | Mutation | Red test |
|---|---|---|
| C1 | a holding without `OnErasure` | `TestEveryHoldingSaysWhatErasureDoes` |
| C2 | a declarer-only module promises `Emptied` | same |
| C3 | a static holder reports an undeclared column as kept (D128 again) | e2e `TestAnErasureOfNobodyStillKeepsOnlyWhatIsDeclared`, `TestOneSweepForgets…` |
| C4 | an order column the SQL keeps declared `Emptied` | order `TestErasureAnonymizesASettledOrder`, `TestErasureLeavesTheCancellationReason` |
| C5 | the core helper keeps everything | `TestKeptOnErasureIsWhatAnErasureLeaves` |
| C6 | the endpoint drops `on_erasure` | `TestTheDeclarationSaysWhatAnErasureDoesToEveryColumn` |
| C7 | the customer returns to a hand list that lost a column | e2e `TestOneSweepForgets…` |
| C8 | a customer column the SQL empties declared `Kept` | customer `TestErasureOverwritesEveryNamedColumn` |

The first draft of the source gate matched a literal by its field names and
flagged the declaration endpoint's DTO as a holding with no `OnErasure`. It
now matches the type, `personaldata.Holding`, on the literal or on its slice.
That first failure is also what showed the endpoint was not publishing the
new field.

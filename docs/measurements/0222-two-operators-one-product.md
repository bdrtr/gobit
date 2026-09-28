# Two operators, one product — measured 2026-09-28

The evidence behind [ADR 0222](../adr/0222-a-product-write-names-the-version-it-read.md).

## 1. What there was

| Part | State |
|---|---|
| a product's version | `product.version`, stamped under the row lock by every write that revises it (ADR 0221); read by nothing on a write |
| the panel's product form | title, handle, status and the schedule; ADR 0013: "Two operators saving the same product overwrite each other" |
| If-Match, ETag, 412 | none in `core/http`; an ETag only on asset responses |
| error classes | eight; none maps to 412 |
| writes that revise | thirteen admin routes, found by the ADR 0221 gate's population |

## 2. The core and the service

`core/errors` gains `KindPreconditionFailed`; `TestStatusForMapsEveryKind` and
`TestWriteErrorPassesTheMessageThroughForOtherKinds` answer it 412 with its
code, and `TestNoErrorIsOfAnyKind` holds `nil` out of it.
`TestAWriteAskedOnAnOlderVersionIsRefused`: at version 2, a write asked on 1 is
refused and the title stays; a variant write asked on 2 reports 3; an edit that
changes nothing reports the 3 it found. `TestAProductWrittenBeforeRevisionsIsAtVersionZero`:
asked on 0, written as 2. `TestThePanelsSaveIsAskedOnTheVersionItWasReadAt`,
`TestTheReadLayerCarriesTheVersion`.

## 3. The surface

`TestEveryRevisingWriteIsAskedOnTheVersionItNames`: each of the thirteen routes
hands `"7"` to the service, and `*` and no header hand nothing.
`TestAnIfMatchOutOfShapeIsRefused`: `W/"7"`, `7`, a list, a word and `"-1"`
refused 422 before the service; the schedule's PUT reads no header.
`TestAHeaderOnSuccessGoesOutOnlyWithASuccess`, in `core/http`: no ETag on a
500 after a commit, one on a written and on an implied 200. The writer that
sets it began in the product's API and moved into the core when the error path
gate refused a module keeping the response writer in a value of its own. `TestEveryRevisingWriteTakesTheVersion`, an
arch gate, derives the routes from the service methods reaching `revise` and
holds each handler to the `revising` group; moving the attribute route back to
`write` failed it.

## 4. The panel

`TestTheEditFormIsSavedAtTheVersionItWasReadAt`: the hidden field holds 4 and
the save sends 4. `TestAStaleEditComesBackWithWhatWasTyped`: 422, the message,
the typed title, the hidden field at the stored 4, and no schedule call.
`TestAnEditThatNamesNoVersionIsRefused`: an empty and a worded version 400, no
write. The panel tests' form helper gained a version where a test gave none.

## 5. On a real PostgreSQL

`TestAVersionIsComparedUnderTheLock`: a transaction holding the product's lock
moves it to version 2; a variant rename asked on 1 waits, is refused with 412
after the commit, appends nothing and leaves the variant's title.

## 6. On the production wiring

`TestTwoOperatorsSavingOneProductDoNotOverwriteEachOther`: created with ETag
`"1"`, read with `"1"`, the first PATCH asked on it answered `"2"`, the second
refused 412 `product_version_mismatch` with no ETag and the first title kept, a
variant created on `"2"` answered `"3"`, `*` answered `"4"`, and `W/"4"`
refused 422.

## 7. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | no version check | the service, lock and end-to-end tests |
| P2 | the check made without the row lock | the lock test |
| P3 | no version reported | the service and end-to-end tests, once rewritten |
| P4 | an unchanged write reporting 0 | the service test |
| P5 | If-Match not handed on | the route and end-to-end tests |
| P6 | `*` read as a version | the end-to-end test |
| P7 | a weak tag taken | the shape and end-to-end tests |
| P8 | a negative version taken | the shape test |
| P9 | the attribute route off the revising group | the arch gate and the route test |
| P10 | no ETag on a read | the end-to-end test |
| P11 | no ETag on a creation | the end-to-end test |
| P12 | an ETag on a failure after the commit | the core writer test, once written |
| P13 | the form without its version | the panel tests |
| P14 | a stale save shown as a failure | the panel stale test |
| P15 | the panel's surface dropping the version | the service test |
| P16 | the read layer without the version | the read layer test |
| P17 | the panel reading no version | the panel tests |
| P18 | a save with no version taken | the panel refusal test |
| P19 | 412 answered as 409 | the core tests, the end-to-end test |
| P20 | an ETag with no revision | the core writer test |
| P21 | no ETag on an implied 200 | the core writer test |

P12 survived the first run: a refused write sets no version, so the two halves
of the guard only part when a committed write fails to answer, and no test made
that happen; the writer test does now. P3 did not compile in its first form.
P12, P20 and P21 were run against the writer in the product's API and again
after it moved into `core/http`.

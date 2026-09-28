# A line past the ceiling — measured 2026-09-28

The evidence behind [ADR 0227](../adr/0227-the-cart-counts-the-lines-it-opens.md).

## 1. What there was

| Part | State |
|---|---|
| the ceiling | the cart workflow's `MaxLineItems` (100), asked by `checkLineLimit` before the catalog read |
| what it asked | a snapshot read outside the cart's lock: at 100 lines, is the added variant on any line |
| a line's identity | since ADR 0223, the variant and its trimmed properties (`GetLineItemByVariant`, the unique index on `md5(properties::text)`) |
| the paths that create a line | the cart service's `AddLineItem` and the merge's `foldLines`, each calling the store's `CreateLineItem` |
| the merge | no line ceiling; the promotion code ceiling held on it |
| pricing's bulk ceiling | 1,000 items (`MaxCalculateItems`); the totals round sends every line in one request |
| what the comments said | `unitPrices`: past 1,000 lines "is an unreachable state: the only path that opens a line is subject to the ceiling"; `known-limits.md`: two concurrent additions "can exceed it by a few lines" |

The ADR 0223 commit changed neither `checkLineLimit` nor its tests, which added
lines of one variant without properties.

## 2. The reproduction

`TestAStorefrontCartStopsAtItsLineCeiling`: a cart holding one variant under a
hundred engravings, and the storefront adds the same variant with a new
engraving. With the ceiling not asked (M1 below), which is what the workflow's
check answered for a variant already in the cart, the add is answered `201` and
the cart holds 101 lines. With the fix it is answered `422` with
`cart_workflow_line_limit_reached`, and the same variant with an engraving the
cart holds is raised.

## 3. The service

`TestAFullCartOpensNoLine`: the same variant with new words and another variant
are refused with the code, the class and the ceiling in the message; the words
`" Engraving ": "7 "` raise the line `"Engraving": "7"`.
`TestTheLastLineUnderTheCeilingOpens`: at 99 lines one opens, the next is
refused. `TestAMergePastTheCeilingIsRefusedWhole`: a target at 99 lines and a
source with one line the target holds and two it does not; the merge is refused
with the code, the target's line is not raised and the source keeps its three.
`TestAMergeIntoAFullCartRaisesItsLines`: a full target and a source whose two
lines it holds; the two are raised, nothing opens.
`TestEveryLineIsOpenedUnderTheCeiling`: every `CreateLineItem` call in the
service package is inside `openLine`.

## 4. On a real database

`TestConcurrentAdditionsStopAtTheLineCeiling`: eight racers each opening a line
on a cart at 99; one opens, seven are refused, the cart holds 100.
`TestARemovedLineFreesItsPlace`: a full cart with one line removed opens one
more and refuses the next, so the count reads living lines.

## 5. The workflow

`TestAddLineItemAnswersTheCartsLineCeiling`: the cart's refusal reaches the
caller as it was returned, and no totals round runs.
`TestCalculateTotalsPricesEveryLineItHolds`: a snapshot of 105 lines is priced
line by line.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| M1 | the ceiling not asked | the service tests, both database tests, the storefront reproduction |
| M2 | the ceiling one line past | the service tests, both database tests |
| M3 | the ceiling one line short | the full cart, last line and full merge tests |
| M4 | the merge not counting the lines it opens | the refused merge test |
| M5 | the merge creating a line past `openLine` | the refused merge test, the population gate |
| M6 | the add counting nothing | the full cart and last line tests, the storefront reproduction |
| M7 | the merge counting the source's lines | the refused merge test |
| M8 | the refusal a conflict | the full cart test, the storefront reproduction |
| M9 | the count reading removed lines, regenerated | the removed line test |
| M10 | the ceiling asked on a raise | the full cart test, the storefront reproduction |

Ten mutants, all killed on the first run. M1 is the defect as a shopper met it:
the storefront answered the hundred-and-first line `201`.

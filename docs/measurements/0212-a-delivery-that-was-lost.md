# A delivery that was lost — measured 2026-09-27

The evidence behind [ADR 0212](../adr/0212-a-sweep-issues-the-cards-a-lost-delivery-did-not.md).

## 1. How a capture's delivery is lost

| Where | What happens |
|---|---|
| a handler's error | logged; no backend delivers the event again, and Redis acknowledges it whatever the handler returned (docs/extending.md) |
| the in-memory bus | runs each handler in its own goroutine after `Publish` returns; a process that stops mid-handler loses the event |
| a command's assembly | subscribes nothing (ADR 0160), so a capture it makes reaches no handler |
| a sold card | named by its sale (`<line>:<unit>`) and issued once (ADR 0210), so a second way in cannot make one twice |
| the job runner | hands a job a context and nothing else; a pass keeps no state for the next |

Before this record an order whose flow failed before its cards were made had
none, and nothing issued them.

## 2. The window's read

The sweep reads the lines with `is_giftcard = true` whose order was placed in
the last seven days, newest first, a hundred at a time. It was measured on
PostgreSQL 16 over 300,000 orders spread over 90 days, three lines each, one
line in two hundred a gift card: 900,000 lines, 4,445 of them cards, 352 in the
week. Each plan below is the second of two runs, so the cache is warm.

| | first page | last page |
|---|---|---|
| without an index on the flag | nested loop over 13,530 orders through `order_line_items_order_idx`, 81,272 buffers, 99.7 ms | parallel sequential scan of the line table, 55,296 buffers, 86.2 ms |
| with `order_line_items_giftcard_idx` | nested loop through the partial index, 27,343 buffers, 14.2 ms | bitmap scan of the partial index, 4,133 buffers, 10.3 ms |

The partial index takes 152 kB beside the 61 MB of `order_line_items_order_idx`.
Without it every pass would walk every line of the week's orders, or scan the
whole table, every five minutes; migration 000029 adds it.

## 3. On the production wiring

`TestTheSweepIssuesACardNoCaptureDeliveryDid` opens an installation in a
command's role, places an order of two 2,500 TRY gift cards, pays it through
the manual provider and links the collection as the checkout does: the capture
reaches no handler and no card exists. A second paid order is canceled. The
`gift-card-sweep` job, taken from the registry `registerJobs` builds, reports
`issued 2 gift cards on 1 orders; 0 orders wait for their capture`; the two
cards exist, each with one `gift_card.issued` delivery, and the canceled order
has none. A second run reports nothing issued and changes nothing.

`TestEveryJobTheRootDeclaresCanBeBuiltAgainstARealInstallation` now names the
job, and `gobit jobs` against a migrated database lists `gift-card-sweep` every
five minutes.

## 4. The flow and the reads

The flow's tests hold the sweep to: a card no delivery made is made and mailed;
an order whose cards exist is found by one lookup and not issued again; an
order half issued gets the rest; an order not captured in full waits; a
canceled order gets nothing by either way; an order before the window is not
read; the window's hundred-and-first line is read; six hundred sales are looked
up in two batches; and an order that fails does not stop the others.

`TestLineItemFilterSelectsTheGiftCardLines` runs the filter on the real query:
true selects the card line, false every other line, and the window applies
with it. `TestTheSoldReferencesAreReadOnTheRealSchema` names the sales that made
a card and no other.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| S1 | every order of the window taken as missing a card | the flow's tests |
| S2 | every order taken as complete | the flow's tests, the production test |
| S3 | an order not captured in full issued | the flow's test |
| S4 | a canceled order issued | the flow's test, the production test |
| S5 | the window ignored | the flow's test |
| S6 | every line of the window read as a card | the flow's tests |
| S7 | the window's first page only | the flow's test |
| S8 | the sales looked up in one unbounded read | the flow's test |
| S9 | a failed order stopping the sweep | the flow's test |
| S10 | a failure swallowed | the flow's test |
| S11 | the job looking back nothing | the job's test, the production test |
| S12 | the job built and not registered | the job registration gate once fixed (D147), the production tests |
| S13 | the provider dropping the filter | the provider's test, the production test once it held a mug |
| S14 | the service dropping the filter | the provider's test, the production test once it held a mug |
| S15 | the repository dropping the filter | the real query's test, the production test once it held a mug |
| S16 | `false` read as `true` | the real query's test |
| S17 | every sale read as having a card | the real schema's test, the production test |
| S18 | the index without its predicate | the index test |
| S19 | `canceled` spelled apart from the order module | the production test |
| S20 | the order's collection not read | the flow's tests, the production test |

S12 left `TestEveryJobIsRegisteredInTheCompositionRoot` green: it counted any
call to a job's `Definition` as a registration, and the mutant called it and
threw the job away. The production tests caught it only because they name the
job. The gate now counts a `Definition` handed to the registry's `Add` and
kills it (D147); the old gate, run against the mutant, passed.

S13 to S15 first left the production test green: its order held gift card
lines only, so a sweep that read every line read the same ones. A mug beside
the cards now makes a dropped filter issue a card for the mug.

S19 passes the flow's tests, whose fake order module spells the status with the
flow's own constant; the production test reads the order module's status.

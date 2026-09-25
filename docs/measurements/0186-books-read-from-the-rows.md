# Books read from the rows — measured 2026-09-25

The evidence behind [ADR 0186](../adr/0186-the-payment-module-keeps-derived-books.md).

## 1. The money the payment module already records

| Movement | Rows | Written |
|---|---|---|
| capture | `payments` | appended, `captured_at` from the application clock |
| refund | `refunds` | appended, `created_at` from the database, no currency of its own |
| store credit | `payment_store_credit_entries` | appended: issue, hold, release, refund |
| loyalty points | `payment_loyalty_entries` | appended: earn, reverse, hold, release, refund |

No table records a movement twice, and none of them is deleted (000003). No
document in the tree discussed double-entry, a journal or a general ledger.

## 2. What the journal reads, and what it leaves out

A tender's hold, release and refund rows are its own mechanics: `Authorize`
writes a hold, a partial capture a release of the rest, a cancel a release, a
refund a refund. The money is the capture and the refund row, which the journal
reads with the provider of their session. So for one customer:

| Count | Store credit |
|---|---|
| the ledger's sum | issues − holds + releases + refunds |
| the journal's account | issues − captures through store credit + refunds of them |

With no session pending, a hold was either captured (the capture) or released,
so the two are equal. `TestTheJournalAgreesWithTheLedgersItIsReadFrom` runs one
customer through a grant, a card payment that earns points, a partial refund
that reverses some, and a payment with store credit and one with points, on the
real schema. For that customer the journal's store credit account equals
`StoreCreditBalance` (2,000), its loyalty account equals `LoyaltyBalance`, the
manual provider's clearing for these collections is 8,000, every entry
balances, and so does the trial balance.

## 3. The window, with and without the indexes

A scratch PostgreSQL 16 with the module's migrations, 50,000 captures two years
apart in 21-minute steps, a refund for every tenth, and 100,000 rows in each
ledger of which a quarter are the kinds the journal reads. One month's window,
in TRY, `EXPLAIN ANALYZE`:

| Read | Without the indexes | With them |
|---|---|---|
| captures (2,125 rows) | Seq Scan on payments, 20.7 ms | Bitmap Index Scan on `payments_captured_at_idx`, 16.4 ms |
| refunds (213 rows) | Seq Scan on refunds, 1.6 ms | Bitmap Index Scan on `refunds_created_at_idx`, 1.4 ms |
| store credit issues (1,116 rows) | Seq Scan, 6.1 ms | Bitmap Index Scan on the partial index, 0.5 ms |
| loyalty grants (1,116 rows) | Seq Scan, 9.2 ms | Bitmap Index Scan on the partial index, 0.6 ms |

The captures read spends its time in its two joins: at this volume the planner
hash-joins the window's 2,125 captures against sequential scans of sessions and
collections. The refunds read, with a tenth of the rows, joins by primary key.
The indexes take the window off a scan of the whole table; how the joins are
done is the planner's, and it was not forced.

## 4. Mutations

| Mutation | Result |
|---|---|
| a capture's sides swapped | red: `TestTheChartOfAccounts` |
| the store credit tender booked to clearing | red: `TestTheChartOfAccounts` |
| a loyalty reverse not negated | red: three service tests |
| the time order removed | red: `TestTheEntriesAreInTimeOrder` |
| the 93-day bound removed | red: `TestTheWindowIsChecked` |
| a window over the bound served cut | red: `TestAWindowOverTheBoundIsRefusedNotCut` |
| the trial balance summed across currencies | red: `TestEveryEntryAndTheTrialBalanceBalance` |
| holds and releases read as loyalty grants | red: the integration test |
| the points tender booked to clearing | red: the integration test |

## 5. A test that passed by file order (D135)

The integration test first failed on another test:
`TestMigrationVeriVarkenGeriAlinabilir` rolled the module back in the shared
database, and 000006's down refuses a point ledger holding a spend row, by its
own design. The journal test spends points, and its file sorts before
`payment_integration_test.go`, so for the first time a test spending points ran
first. The migration test now runs in a database of its own, as pricing's
already did, and the two pass in that order.

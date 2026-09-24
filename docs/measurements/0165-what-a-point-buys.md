# What a point buys — measured 2026-09-13

The evidence behind [ADR 0165](../adr/0165-a-customer-can-pay-with-their-points.md).

ADR 0164 left one sentence for the next record: "Points are not spendable yet:
the tender that spends them is a provider in this module, where the published
contract already says a person's funds are spent from." Seven read-only reports
were taken against HEAD `c0fa59d` before anything was written — one per
question the sentence raised — and what follows is what they found, in the order
the decision needed it. Where a number below came from a probe rather than a
reading, the section says so.

## 1. What a point was worth: nothing, and the tree said so on purpose

The tree held ONE number about points, `PAYMENT_LOYALTY_EARN_BASIS_POINTS` —
"how many points a minor unit of captured money earns, in ten-thousandths" — and
had declined three times to say what a point buys. The three refusals, as they
stood on the day:

| Where | The sentence |
|---|---|
| `internal/modules/payment/api/describe_loyalty.go`, the file's godoc | "There is no amountNote anywhere here, and its absence is the decision. That note says a number is in MINOR UNITS, and a point is not money: a shop decides what a point is worth, and telling a client that these are the smallest coin of a currency would be a false sentence about the one field this document exists to explain." |
| the same file, the balance operation's published description | "It is a COUNT and not an amount of money — what a point is worth is the shop's decision and this installation does not hold it." |
| `LoyaltyEntry.CurrencyCode`'s godoc and migration 000005's column comment | "Points are unitless but the money is not, and a customer who paid in two currencies holds two balances rather than one meaningless sum." |

The balance DTO's field is named `points`, which is why the amount-note audit
(its field pattern matches `amount`, `*_amount`, `balance` and `*_balance`) never
asked for the note.

The provider contract, meanwhile, speaks MONEY and nothing else:
`CreateSessionInput.Amount` is "an INTEGER in minor units", and so are
`Session.Amount` and every field of `SessionInspection`. A provider whose ledger
is in points has to convert at its boundary, and the contract gives it no field
for a second unit. So the first question was not "how is a point spent" but
"what is a point worth", and every shape below is an answer to it.

Under the shape chosen, the first two sentences became their opposite in the
same file, and the "unitless" sentence stays true of the COLUMN — a point is
not money — while the tender gives it a price: one minor unit of the currency it
was earned in. The earn rate then reads as the cashback in basis points, and the
existing godoc "100 is one point per hundred minor units — a point per lira, per
euro, per dollar" gains its second half for free: 100 points is one lira.

## 2. The four shapes, and why A

Each shape was judged against the sentences already written — that the tender
is a provider (ADR 0164, the feature list, `CreateSessionInput.CustomerID`,
`PaymentCollection.CustomerID`, all four naming "store credit today, loyalty
points tomorrow") — and against what a shop needs: a customer who can spend
without a phone call, a refund that gives the points back, a shop that cannot be
drained, and a number an operator can explain.

| Shape | What it is | What it costs beyond A | What it contradicts |
|---|---|---|---|
| **A** — a `loyalty_points` provider, one point = one minor unit | store credit's verb table on the point ledger: hold at authorize, release at cancel, refund at refund, no spend at capture; a session table of its own; three new kinds | — | the two `describe_loyalty.go` sentences, and ADR 0164's "writes every row" |
| **B** — A plus a redeem value setting | a second setting v; points for an amount a are ceil(a·10 000/v) | the whole configuration wire (field, validation, `.env.example`, composition root, a ceiling with a symmetry assertion); the value FROZEN on every session, or a refund months later returns a different number of points than the hold took; a target function with ceil where the earn uses floor, or two half-refunds return one point more than was taken | the same two sentences, plus the spirit of ADR 0164's "the rate is one number for every currency" — two knobs for one economic quantity |
| **C** — convert points into store credit | one service method, an admin endpoint, a conversion value; spend through `store_credit` | everything B costs minus the provider; and the loop of §2.1 survives through the credit hop while store credit earns | all four "the tender is a provider" sentences, and ADR 0164's own rejection of a write endpoint ("in this module a write means money moved") |
| **D** — a discount at checkout | the cart's discount round produces a points discount; a saga step holds and releases | a new cross-module read promotion → payment; a promotion type whose value is a customer's balance; a hold written from OUTSIDE the module; a refund path that does not exist (`RefundPayment` knows the collection and the session, not a discount) | ADR 0164's rejection of the promotion module ("it owns no customer, no event and no money type") |

C has a second defect that decides it alone: only an operator can trigger the
conversion, because there is no proven customer at the storefront, so every
redemption is a support ticket. D buys the one thing no tender can — partial
redemption at the storefront — and §3 shows that to be a checkout decision and
not a payment one.

A was chosen. No second number, no rounding anywhere (partial capture, partial
refund and release are integer differences of one unit), and the state machine,
the lock, the decline-not-error rule and the registration guard inherited from a
provider with fourteen recorded mutations. It does not foreclose B: a redeem
value added later with a default of one is additive, and a shop that changes it
revalues its balances — which real programs do.

### 2.1 The earn loop

`Service.earnLoyaltyPoints` reads the collection's customer and its captured
and refunded totals and nothing about how the money came in; it is called from
`Service.writeCollectionTotals` for every capture and refund. A store-credit
session requires a customer, so every store-credit capture is a named capture,
so it earns. This was proved rather than read, with an overlay probe against the
real `storecredit.Provider` at 100 basis points:

```
loyalty balance after a 10,000 capture through "store_credit" at 100bp = 100 points
store credit balance after capture = 0
after refund 2,500 -> loyalty=75 credit=2500
```

No test pinned that either way — every loyalty scenario ran through a fake
provider, and the store-credit fixtures constructed the service with no rate.

A points tender on the same slot would therefore earn points on points. Let
q = r/10 000. Spending P points captures P minor units and earns ⌊P·q⌋;
spending that earns ⌊⌊P·q⌋·q⌋; and so on. The value a customer can extract
from P points is bounded by the geometric series:

  V(P) = P + ⌊Pq⌋ + ⌊⌊Pq⌋q⌋ + … ≤ P / (1 − q) = P · 10 000 / (10 000 − r)

| r (basis points) | multiplier 10 000 / (10 000 − r) | extra over face value |
|---|---|---|
| 100 (1 %) | 1.0101 | 1.01 % |
| 500 | 1.0526 | 5.3 % |
| 1 000 | 1.111 | 11.1 % |
| 5 000 | 2 | 100 % |
| 9 000 | 10 | 900 % |
| 10 000 (the ceiling) | ∞ | unbounded: ⌊P·1⌋ = P, every spend earns itself back |

Truncation ends the chain when P·r < 10 000: at 100 basis points a
10 000-point balance yields 100, then 1, then 0 — three steps. At the ceiling
truncation never bites. ADR 0164 chose one point per minor unit as a ceiling
whose written reasons are overflow and "a program this record has not thought
about"; this is that program. Below the ceiling it is a bounded leak rather
than an exploit, and still cashback on a liability being extinguished rather
than on revenue, so an operator's "1 %" is not 1 %.

Three exclusions were weighed. (a) A target computed from the collection's
captures MINUS those whose session is at the points tender keeps every sentence
of the earn path's godoc true: still a target, still idempotent, a refund of
card money lowers it, a refund of points money never counted. (b) "A collection
with any points session earns nothing" is a target function too, but a
discontinuous one — captures on one collection run in any order at the module
level, so a card capture that earned +70 followed by a points capture would
flip the target to zero and write a `reverse` on money that was card money.
(c) Accepting the loop forces a STRICT ceiling below 10 000 and changes two
published constants. (a) was taken, as `CollectionNetCapturedExcludingProvider`:
one join from `payments` to `payment_sessions`, summing
`amount − refunded_amount` where the provider is not `loyalty_points`, read
under the collection lock the caller already holds, after the capture or
refund it is earning for has been written. The service names a provider id for
the first time, `LoyaltyTenderID`, kept beside the ledger in `payment/models`
because the earn path and the tender both depend on it.

Store credit still earns, by decision: credit is money the shop owes at face
value and points are the program's own currency. The norm real programs follow
is the same — award redemptions earn nothing, cash equivalents do.

## 3. Split tender: the module splits, the storefront and the saga do not

At the MODULE a collection is paid by two sessions today, and it is documented
and tested: `CreateSessionInput.Amount` "exists only to SPLIT the payment
across more than one session", `Service.remainingToOpen` subtracts what is
captured and what live sessions hold, and `TestTwoConcurrentCapturesEarnTheTargetOnce`
already opens two half-amount sessions on one collection. A probe with the real
store-credit provider and a fake card provider on a 10 000 collection:

```
second session (card, amount 0 = remaining) got amount 7000
third session refused with code "payment_collection_closed"
after both authorizations collection status="authorized" authorized=10000
after credit capture status="partially_captured" captured=3000
after card capture status="captured" captured=10000
loyalty after split tender (3,000 credit + 7,000 card) = 100 points (target counts BOTH)
refund 7000 against payment ... (session provider "card")
refund 3000 against payment ... (session provider "store_credit")
after full refund credit=3000 loyalty=0
```

"Pay 30 with credit and 70 by card" works end to end at the service, including
`Service.RefundCollection` fanning the refund out to both providers. The admin
session body carries an amount, and its godoc says so: splitting the payment
across several sessions is administrative work.

At the STOREFRONT it does not, twice over and by decision:

| Where | What is single |
|---|---|
| the store session endpoint | the body has no amount field, deliberately — a client-chosen amount was a 1-unit session on a 50 000 order — so the first pending session reserves the whole remainder |
| the cart's completion body | one `payment_provider_id`; unknown fields refused |
| the checkout plan | one provider id, written to the execution record and replayed by recovery |
| `Interop.OpenSession` | no amount parameter: "the session is opened for the whole of the collection's NOT YET HELD amount … partial payment flows are the business of the admin API" |
| the authorize step | ONE session, ONE authorize, and the rule the checkout calls its most serious finding: `authorized < plan.Amount` fails the step and releases the hold |
| the capture step | ONE capture of the plan's amount |

So at the storefront any tender — card, credit, points — covers the whole
order or the checkout fails and compensates. A provider that partially
authorized (the contract allows it) would be released by the saga all the same.
"Part in points, the rest by card" needs a body naming two tenders and an
amount, a plan carrying a list, an interop that takes an amount, a full-payment
rule that sums across sessions, N captures and N compensations. That is a
checkout decision, and the record leaves it one. One small mercy for whoever
makes it: the module's idempotency index is `(provider_id, idempotency_key)`, so
the saga's single execution id could already open one session per provider.

## 4. The ledger gate's second door

ADR 0164's gate was a map of one allowed caller per function, three links:
`InsertLoyaltyEntry` → `AppendLoyaltyEntry` → `earnLoyaltyPoints` →
`writeCollectionTotals`. It matches caller and callee by BARE NAME, so a
provider's `Authorize` calling the repository's `AppendLoyaltyEntry` fails with
"…and the only function allowed to is earnLoyaltyPoints … or make the case for
a second door in a record that supersedes ADR 0164". That sentence is the
record being measured for, and there was no room in the map's type for it.

Two facts decided the door's shape:

- **The gate's own reason for refusing a second writer does not apply to a
  spend row.** Its message says a second writer "adds to a number the rest of
  the module derives". The number is `LoyaltyPointsForReference(col.ID)`, a sum
  over rows whose reference is the collection. A spend row written with the
  provider's OWN session id as reference — store credit's convention,
  `StoreCreditEntry.Reference` is the session — never enters that sum. So the
  arithmetic survives a second door; only the gate's sentence does not. The
  corollary is a rule, now on `Entry.SessionID`'s godoc and in the migration: a
  spend row's reference is never a collection id. The query gained
  `kind IN ('earn', 'reverse')` regardless, so the arithmetic states its subject
  instead of resting on a convention kept in another package. Without the
  filter, one hold row referencing the collection followed by a second capture
  would re-earn the spent points — inferred from the arithmetic, not executed.
- **A list of function names would be wrong, not merely inelegant.** The manual
  and PayTR providers have methods called `Authorize`, `Capture`, `Refund` and
  `Cancel` too, and a name-only matcher cannot tell them apart. The property the
  ADR names is "the tender is a provider in this module", and a provider has
  exactly one mechanically checkable identity: the string its `ID()` returns,
  which `TestEveryProviderRunsTheComplianceSuite` already walks.

So `TestEveryPaymentLedgerWriteEntersThroughANamedDoor` resolves the second
door at test time: it finds the one package whose provider returns
`loyalty_points`, admits calls from that package, and fails when zero packages
answer (the tender is gone and the door is open to nobody), when two answer, or
when either door is never seen opening — the admitted package never calling, or
the named function never calling. The first draft counted the entry's callers
as a whole, and the verification pass found that the tender's calls then kept
the count positive while the named function had stopped writing at all. A
tender that is renamed or removed turns the audit red rather than opening the
door.

**The credit ledger had no gate at all.** Searched `internal/arch/` for
`AppendStoreCreditEntry`, `InsertStoreCreditEntry` and `IssueCredit`: nothing.
The credit ledger's writers, from a production grep, were `Service.IssueCredit`
and the four provider verbs — two writers, and a sixth would have been caught by
review or by nobody. The loyalty decision was the first moment an instrument for
a two-writer ledger had to be designed, and the same map now carries
`InsertStoreCreditEntry` → `AppendStoreCreditEntry` → `IssueCredit` or the
`store_credit` tender's package (D116).

What the call graph still cannot see is said rather than implied: it sees
callers, not KINDS. Nothing in the gate stops the provider appending an `earn`
row or the earn path appending a `hold`; the schema's sign check bounds the
sign, and the kind filter in the target query bounds the arithmetic.

## 5. The negative balance and the lock's precondition

Store credit has three positive kinds and one negative, and the negative one is
written only under the customer's balance lock, so its balance can never be driven below
zero by a writer that did not read it. The point ledger's negative kind is
`reverse`, written by a refund under the COLLECTION lock without reading the
customer's balance. So this sequence, with no race at all:

1. order 1 captures 10 000 → earn +100;
2. order 2 spends 100 points (hold −100, captured);
3. order 1 is fully refunded → reverse −100 → balance −100.

Three reports agreed on the mechanism and disagreed on the remedy. One proposed
that the earn path take the ledger lock before writing a reverse, to close the
interleaving where a spend reads its balance under the ledger lock while a
refund's reverse INSERTs unlocked (`FOR UPDATE` on existing rows does not block
a concurrent INSERT, and no lock of any kind blocks one that does not ask). Another said the balance below zero is reachable with no
race and must be declared, not prevented. The critic's verdict: both right on
their fact, and the lock oversold — it removes one interleaving and leaves the
sequential case untouched, so it cannot be written up as closing the negative
balance; it only decides whether the race is ALSO a way there, and the state
the race produces is a state a legal order also produces. The record declares
the state and takes no lock on the reverse: a refund is never refused because
the points it reverses were spent, money goes back before points do, the next
earn fills the hole first, and the tender declines against a negative balance
until it is filled.

The spend side keeps store credit's lock-and-sum shape for store credit's
reason. Two authorizations for one customer without it both read 100, both
decide 100 ≥ 100, both insert a hold of −100; the inserts do not conflict
(different keys, an append-only table, no shared unique index) and the balance
is −100 — a lost update on a DERIVED value the database cannot see because
nothing was updated.

### 5.1 What the lock was, and the hole the verification pass found in it

The first draft of this record kept ADR 0152's lock as it stood: `SELECT … FOR
UPDATE` over the customer's rows in that currency, with the sentence "a customer
with NO rows locks nothing, and that is not a hole: their balance is zero" on
both ledgers' queries. The verification pass, an opposition read of the whole
change before commit, refuted it with three psql sessions against the six
migrations, and this record repeated the probe on `postgres:16-alpine` on
2026-09-24 — a stripped ledger table, T1 and T2 as two authorizations, an earn
as the third session, the interleaving forced by `pg_sleep`:

| Case | T1 locked | T2 waited | T2 read | Final balance |
|---|---|---|---|---|
| row lock; the customer had NO rows at T1's lock, an earn of 100 commits while T1 holds it | 0 rows | no (0.98 ms) | 100 | **−100** |
| row lock; the customer had one row (the control, and the shape every lock test proved) | 1 row | 1.52 s | 0 | 0 |
| advisory lock on the balance; no rows at T1's lock, the same earn | the key | 1.46 s | 0 | 0 |
| advisory lock; T2 at REPEATABLE READ, its first statement before the wait | the key | 1.51 s | 100 | **−100** |

The balance is zero at the LOCK. The sum is the next statement and, under READ
COMMITTED, a fresh snapshot, so money committed between the two is a row nobody
locked; the second authorization locks that row without waiting, both read the
same balance and both hold it. Under ADR 0152 the racing writer was an
operator's `IssueCredit`; under ADR 0165 it is the automatic earn, which runs on
every named capture. Every lock test in the tree passed through it, because each
one funded the customer before the competitor locked (D118).

The order module had written the answer down for a different total: "SELECT …
FOR UPDATE locks the rows that exist, not the one NOT YET WRITTEN" is the godoc
of its spending lock, a `pg_advisory_xact_lock` keyed on the customer. The two
ledgers now take the same kind of lock, keyed on the customer AND the currency
because a balance is per currency, under classes 4 (store credit) and 5 (points)
so that `TestAdvisoryLockClassesAreUnique` holds them apart from the classes
already taken. The first draft had named this lock as a future
optimization for a long ledger; it is the correctness fix instead.

### 5.2 The precondition nobody had written: READ COMMITTED

The waiter's sum is "a fresh statement, a fresh snapshot" only at PostgreSQL's
default level; the fourth row of the table is the same lock with the waiter at
REPEATABLE READ. Searched `core/db/` and the payment repository for
`TxOptions`, `BeginTx`, `IsoLevel`, `RepeatableRead`, `Serializable` and
`default_transaction_isolation`: nothing. The pool ran at the server's default,
which a role or a database can set to anything, and the lock tests proved the
default and only the default. The payment repository's `WithTx` now begins at
READ COMMITTED by name (D119). The other modules whose locks rest on the same
level are §8's.

### 5.3 The witness

`TestAnAuthorizationWaitsOnTheBalanceLock` replaces the two per-tender lock
tests and runs three shapes for each tender. A competitor takes the balance
lock through the repository's own function, on a pool of exactly one
connection so that its backend is known before it begins — the earlier
competitors took the lock with a copy of its SQL, and a copy goes on proving a
lock's old shape after the lock has changed. The authorization under test has
to be seen waiting through `pg_blocking_pids`, then read the balance the
competitor left and decline:

- the customer had rows when the competitor locked — the shape both earlier
  tests proved, and the only one a row lock passes;
- the customer had NO rows until the competitor held the lock, and the money
  arrived while it did — D118;
- the service's pool starts every connection at REPEATABLE READ — D119.

Each mutation in the section below turns exactly the shape it names red.

Lock ordering was not in dispute: every path takes the customer's balance LAST
(collection → module session → provider session → balance), and the earn path
and the reverse take no balance lock at all, so no cycle.

## 6. Two reconciliation precedents that disagreed

`Service.Reconcile` lists the module's sessions that sat `authorized` past the
settling window, fetches each provider from the registry and type-asserts it to
`coreprovider.SessionInspector`; a provider without it is counted `Unaskable`,
and the hourly job logs "their ledger is unverified by anything" and reports
the pass as not clean.

`grep -rn 'func .* InspectSession'` over production Go found two
implementations: the manual provider's and the PayTR plugin's. The store-credit
provider did not implement it, and nothing said why — `grep -rn -i
'inspect|reconcil'` over its package, ADR 0152 and measurement 0152 found only a
migration name. Two facts had to be weighed:

- The hole reconciliation closes — "the provider took the money and the module's
  commit failed" — CANNOT occur for a provider that writes into the same
  transaction; the hold rolls back with the module. So for `manual`,
  `store_credit` and a points tender the pass is structurally empty.
- The manual provider implements the inspector anyway, with a godoc arguing that
  its ledger and the module's are "really separate tables" and that this is
  what makes the comparison meaningful. Store credit — the same shape, the same
  separate table — did not. The two in-tree precedents disagreed, and the third
  provider had to pick one.

Picking "unaskable" would have meant that every credit or points session
sitting `authorized` past the window makes the hourly report say a ledger is
unverified by anything — a sentence that is false for a same-transaction
ledger. The shared machine answers `Machine.InspectSession` from the tender's
own session table for both tenders, and the reconciler is untouched: it reaches
a provider only through the contract and knows no provider's table by name,
which is also why adding `payment_loyalty_sessions` cost it nothing (D117).

Related and inherited: an `authorized` points session whose saga died between
the module's commit and the step record has no step record, recovery stops
there by decision, and no job expires holds. The hold sits on the balance until
an operator finds it — through three existing endpoints, none documented as the
recipe. Shared with store credit.

## 7. The stale sentences found on the way

None of these has a gate, and the reason is the one D111 recorded: the count
audit admits a number only when the population's PATH sits on the number's
line, and the route audit needs a method word before an address.

| Sentence | Where | Stale since | What it says now |
|---|---|---|---|
| the manual provider is the only one in the box | `internal/modules/payment/module.go`, package godoc | ADR 0152; wrong by two with the tender | the providers `Module.Register` registers, and under which option |
| "Twelve providers ship in this tree today" | `internal/arch/provider_compliance_test.go` | ADR 0152 (thirteen packages, fourteen with the tender) | a population the floor stays under, with a note that the count went stale twice; the test's own godoc carried the number twice more, which the first repair missed and the verification pass found |
| this is the only endpoint that reads the query string | `internal/modules/payment/api/describe.go`, package godoc | ADR 0152 (five readers); the sibling copy in the collection listing was repaired by measurement 0164 | the readers are described in their own files — the collection listing among them, which the first repair said was not described at all |
| the same cart cannot be retried after a failed attempt | `internal/workflows/checkout/doc.go`, "# Idempotency" | the engine's `StatusFailed` godoc, its test and `docs/known-limits.md` all said the opposite | what the engine does: a failed execution releases its key (D114) |
| "has no store credit at all" | `docs/known-limits.md` | narrower than the truth once two tenders share the option | both person-bound tenders |
| "A customer cannot see their own store credit" | `docs/known-limits.md`, which carried no loyalty entry at all | measurement 0164 said the same of points | store credit or points |
| a hand-listed map of five identifier prefixes for a file declaring eight | the models package's prefix test | ADR 0152 and 0164 added three, the test gained none | the population is read from the source (D112) |
| "Points are not spendable" | nowhere but ADR 0164 and its measurement | — | the limit was never listed, so closing it changed nothing there |

Two more were found. `service.Store` declared the credit ledger's lock and the
service never called it — only the provider did, through its own narrower
interface — so it was removed: a method the service declares and never calls
is a door it holds open for nobody. And the repository's typed-error map names
no constraint of either ledger or of either provider session table, so a CHECK
violation on those tables is a 500 — an inherited silence, left, and correct in
that no client input reaches those columns.

## 8. What this record does not close

- **Split tender at the storefront.** §3: a checkout decision, five places
  single-valued by design. A person-bound tender pays for the whole order or for
  none of it, and a shortfall is a decline the customer answers with another
  tender for the same cart — which the e2e now walks, and which the checkout's
  own godoc said was impossible until this record (D114).
- **A customer reading their own balance.** Still ADR 0152's identity bill:
  `corehttp.ProvenCustomer` exists and cart, b2b and customer bind it; the
  payment module does not, and this record does not pay that bill.
- **The dossier and the erasure.** The payment module declares nothing to
  `core/personaldata`, `customer_id` is on neither person-column audit's list by
  decision, and the module now holds two per-person ledgers and two per-person
  session tables. After an erasure both ledgers keep every row under the
  still-valid pseudonymous id, and the disclosure dossier lists neither balance,
  the holds, nor the `reference` column pointing at the person's collections.
  Inherited from store credit, grown by one table, and named here rather than
  fixed.
- **A store-credit-paid capture still earns**, by decision (§2.1). The
  exclusion names one tender.
- **The storefront provider list is not per-customer.** `Handler.listProviders`
  writes the registry's ids to both surfaces and nothing filters by the request's
  customer, so a guest is offered both person-bound tenders and gets a 409 on
  choosing one (D113) — a conflict rather than a server error, and still an
  offer that cannot be taken.
- **A collection refund on a split collection picks its tender by capture
  order.** `Service.RefundCollection` walks the collection's captures newest
  first, so a partial refund of "30 in points, 70 by card" gives back points or
  money depending on which session captured last. The earn target is right
  either way — a card refund reverses what it earned, a points refund never
  counted — and the unit and integration tests refund per payment so that the
  test, not the order, chooses. Which half a shop owes first is a policy
  question the admin surface answers today by refunding a payment directly.
- **The other modules' locks rest on the same unpinned level.** §5.2 pins READ
  COMMITTED in the payment repository only. The order module's spending lock,
  the promotion budget's row lock and the auth module's guards argue from READ
  COMMITTED in their own godocs, and every one of those `WithTx` begins at the
  server's default. It is the same fault in other modules, named here rather
  than fixed inside a payment record.
- **Migration 000006's down is data-dependent.** The narrowed CHECK fails on a
  table holding a spend row, which the precedent (fulfillment's 000004) records
  as the decision — a rollback that stops is recoverable, one that rewrote rows
  is not — and which the rollback lane cannot prove, since it round-trips on an
  empty schema and says so. The module's rollback-with-data test passes because
  it runs BEFORE the points tests in source order on the shared database; under
  `-shuffle` a spend row written earlier would stop it, as designed. No lane
  shuffles.

## The mutations

Run on 2026-09-24, after the verification pass, each one applied to the working
tree alone, the named lane run with `-count=1`, and the file restored and its
checksum compared before the next. "Before" is what the same mutation did to
the first draft, as the build and verification passes of 2026-09-13 recorded
it, and "not recorded" where neither ran it.

| # | Mutation | What went red | Before |
|---|---|---|---|
| 1 | the balance lock back to `SELECT … FOR UPDATE` on the ledger's rows | `TestAnAuthorizationWaitsOnTheBalanceLock`, the two "no rows until the lock was held" shapes only | the shape had no test |
| 2 | the repository's `WithTx` begins without naming a level | the two "server defaults to repeatable read" shapes only | the level was named nowhere |
| 3 | `Machine.Authorize` takes no balance lock | all six shapes | red |
| 4 | the balance read BEFORE the balance lock | `TestTheLedgerIsLockedBeforeItIsRead`, both tenders | survived the unit lane: the fakes recorded locks, not reads |
| 5 | the guard on a capture above the hold removed | `TestTheForbiddenTransitionsAreConflictsAndWriteNothing` | survived every lane |
| 6 | the guard on a refund above the capture removed | same | survived every lane |
| 7 | a captured session canceled answers nil | same | survived every lane |
| 8 | the captured-status guard on a refund removed | same | survived every lane |
| 9 | a second capture no longer answers with the current state | `TestARepeatedVerbAnswersWithTheCurrentStateAndWritesNothing` | survived every lane |
| 10 | a repeated refund with nothing left writes a zero row (`given <= 0` → `< 0`) | same | survived every lane; the schema would have refused the row as a 500 |
| 11 | `InspectSession` reports authorized and captured swapped | the points tender's inspection test (unit) and `TestBothTendersAnswerReconciliation` (both subtests) | survived the unit lane: the fixture inspected only a captured session |
| 12 | a session with no customer refused as Internal rather than Conflict | the no-customer tests of the machine and of BOTH tenders | store credit's own test survived: it pinned the code only (D113) |
| 13 | the earn base back to the collection's provider-blind totals, in the service | `TestMoneyCapturedThroughThePointsTenderEarnsNothing` (unit) | survived the unit lane; only Docker saw it |
| 14 | the same, in the query (`<> $2` → always true) | `TestACapturePaidWithPointsEarnsNothing` | red |
| 15 | the query excludes store credit too (`NOT IN ($2, 'store_credit')`) | `TestCreditThatIsSpentStillEarns` | survived every lane: "credit that is spent still earns" had no witness |
| 16 | the earn path stops writing reverses | `TestABalanceBelowZeroIsAState` | red |
| 17 | 000006's sign CHECK admits any sign | `TestTheLedgerRefusesASignThatContradictsItsKind`, exactly the five sign cases | red |
| 18 | 000006's down keeps `payment_loyalty_sessions` | `TestMigrationVeriVarkenGeriAlinabilir` | red |
| 19 | `earnLoyaltyPoints` stops calling its door (the named door inert) | `TestEveryPaymentLedgerWriteEntersThroughANamedDoor` | survived: the tender's calls satisfied the consumer count |
| 20 | `IssueCredit` stops calling its door | same | survived, same reason |
| 21 | `LoyaltyTenderID` renamed | same | not recorded |
| 22 | a third writer: `IssueCredit` also appends a point row | same | not recorded |
| 23 | the history endpoint loses its unit sentence | `TestTutarTasiyanUclarBirimiYaziyor`, that endpoint | survived: `points` was outside the audit's field population |
| 24 | the composition root passes the trust setting to `PersonBoundTenders` without its negation | both smoke processes' provider-list subtests | survived every lane: unit and arch, and the app, e2e and payment integration packages, all green |
| 25 | the e2e harness earns at half the ceiling | `TestPointsPayForAnOrder`, at its first assertion | the guard compared the rate with itself, and the test failed later, at the balance, with a message about the wrong thing |

Row 24 is witnessed only in the smoke lane, because only there does the
composition root exist: every other lane builds its own root and hands the
option in itself. Both processes of `internal/smoke/b2b_test.go` now read the
storefront's provider list, one with the setting on and one without (D120).

# Proving an address — measured 2026-09-11

Serves [ADR 0133](../adr/0133-a-shopper-opens-their-own-account.md).

## What was already there, and what was measured about it

Nothing here needed building from scratch, and the measuring was about which
published seam fits.

| Needed | What exists | Fits? |
|---|---|---|
| a rate limit | `corehttp.RateLimit` + `NewMemoryLimiter` | yes, and the default's weakness is named below |
| a way to send mail | `core/provider.NotificationProvider` | the INTERFACE is published; the registry that holds one is `internal` |
| a way to create a customer | `customer.service`'s `RegisterGuestCustomer` | no — see below |
| a way to find a customer by address | the query layer's customer provider, `email` filter | yes |

`RegisterGuestCustomer` is the one that does not fit, and its own record says why:
"A guest record already existing with the same e-mail is NOT an obstacle." It
always creates. That is right for a guest checkout, where each checkout may be its
own record, and wrong for a sign-up, where a second record for an address that has
one is the defect.

So the installation binds the seam. The alternative was a new write on
`customer.service`, whose own godoc says "every method added here is a contract
customer can never change again" and that a mismatch "is caught not at compile
time but at the moment of resolution from the container". A permanent cross-module
contract to serve a contrib module is a price, and it was the user's call to
refuse it.

## The concurrency question, and why this one needed no forced barrier

A token must work once. `DELETE … RETURNING` makes reading and consuming one
statement, so two requests carrying one token cannot both be answered.

That claim was tested the way ADR 0130's was — 20 rounds, two goroutines — and
then the test was checked against the mutation it exists for: `SELECT` the row,
then `DELETE` it. The mutation was run three times and bit three times; the real
code was run three times and stayed green three times.

This is worth recording because ADR 0130's measurement says the opposite about a
different shape. There, a simple barrier produced a FALSE NEGATIVE five runs out
of five and the overlap had to be forced with a third transaction holding a lock.
The difference is the size of the window: `SELECT … FOR UPDATE` then `DELETE`
inside one transaction leaves microseconds between the two, while `SELECT` then
`DELETE` as two round trips leaves a whole network hop. Twenty unforced rounds
find the second and cannot be relied on to find the first.

The lesson is not "barriers are unnecessary" but "the window has a size, and
whether a test needs forcing depends on it" — measured per shape, not assumed
from the last one.

## The typed nil, found by accident

The unmounted-state test was written with a table whose fields were `*fakeSender`
and `*fakeAccounts`. For the "no way to send the proof" row the sender field was
left at its zero value, and the endpoint was MOUNTED anyway.

An interface holding a nil pointer is not nil. So an installation writing

```go
Verification: shop.Mailer()   // returns (*mailer)(nil) when unconfigured
```

passed the `== nil` check, mounted both endpoints, and would have panicked inside
the handler on the first registration — after taking somebody's password. The
check is `reflect`-based now and runs once, at `Routes` time. Both typed-nil rows
are in the table, because a test that found a hole by accident should keep looking
on purpose.

## The gate widened one commit earlier caught this commit's own table

ADR 0132 widened the personal-data audit from `plugins/` to `contrib/`. The first
run after `customer_registrations` was created failed:

```
the identity-session unit holds customer_registrations.email and declares no such
holding
```

And the audit was right about more than the declaration. A pending registration
holds somebody's address and a hash of the password they chose; an erasure that
took the account and left the claim would have reported a complete deletion to a
person who asked to be forgotten. The erasure takes it now, and the dossier
reports it — without the token hash, which is sharper than a password hash: it is
not a record OF something, it is a working link.

Reached by ADDRESS only, because the table has no customer id — there is no
customer yet, which is the point of it. That limit is stated in a test of its own
rather than left to be discovered.

## An edit that silently did nothing

The five registration holdings were added by a string replacement whose anchor
was:

```
				Why: "when they last changed it, ...
```

`gofmt` had already aligned that field as `Why:  "when they last changed it, ...`
with two spaces. The anchor did not match, the replacement wrote the file back
unchanged, and the declaration test went on passing — because the thing it would
have caught had never been added.

It surfaced one command later, when the audit still failed. The habit that follows
is cheap: every edit script in this session now asserts its anchor matched before
writing.

## The starter's verification is a stand-in and says so

The example binds `accounts.LogOnlyVerification`, which writes the sign-up link to
the log. A log is read by more people than a mailbox, kept longer, and shipped
onward; a link in one is an account anybody with log access can take. So the type
carries the warning in its NAME and logs at WARN on every send, rather than in a
comment somebody has to find.

The alternative was to bind nothing, leaving the flow unmounted in the example —
rejected because then no example shows the wiring at all. The registry that holds
a real notification provider is `internal`, so an example cannot reach it without
naming an internal type, which is worse than a stand-in that announces itself.

## The mutation table

| Mutation | Bitten by |
|---|---|
| an address that already has an account gets a different status | `TestTheSameAnswerForAnAddressThatHasAnAccount` |
| it gets a verification token instead of the other message | `TestTheSameAnswerForAnAddressThatHasAnAccount` |
| the token is stored as sent | `TestATokenWorksONCE` |
| verification opens a second customer | `TestAnAddressThatGainedACustomerInBetweenKeepsIt` |
| verification does not sign the person in | `TestAProvenAddressOpensAnAccountAndSignsIn` |
| the address check refuses nothing | `TestAnAddressThatCannotBeAnAddressIsRefused` |
| the address check allows commas and newlines | `TestAnAddressThatCannotBeAnAddressIsRefused` |
| a typed nil counts as bound | `TestTheEndpointsDoNotEXISTWithoutTheSeams` |
| the store's capability is not asked for | `TestAStoreThatCannotHoldAPendingRowLeavesItUnmounted` |
| the endpoints run unlimited | `TestTheRegistrationIsRateLIMITED` |
| they are described when not mounted | `TestEveryRouteIsDescribedAndEveryDescriptionMatchesARoute` |
| `DELETE … RETURNING` becomes SELECT then DELETE | `TestATokenIsSingleUseBecauseTakingItIsONEStatement` |
| asking again does not replace the pending row | `TestAskingAgainREPLACESThePendingRegistration` |
| an unfinished sign-up is not erased | `TestAnErasureTakesAnUNFINISHEDSignUpToo` |
| the pending row is not counted in the report | `TestAnErasureTakesAnUNFINISHEDSignUpToo` |
| a blank address erases every pending row | `TestAnErasureByCustomerIDCannotReachAPendingRow` |
| the dossier omits an unfinished sign-up | `TestADossierCarriesAnUnfinishedSignUpWithoutItsSecrets` |

Seventeen, each with `-count=1`, each restored from a scratchpad copy rather than
with `git checkout`.

Two more were written, found to be unable to bite, and are recorded rather than
quietly dropped. One mutated a memo of the limiter — and the memo could not
matter, because `Routes` is its only caller and runs once; the field was deleted,
which is the right answer to a mutation that cannot fail. The other used a fixture
with a comma AND two `@`, so the character check it was aimed at was never reached:
the other rule answered first. The fixture is `some,one@example.test` now, and this
is the same defect the chain tests keep producing — a wholly-wrong case is refused
by whichever rule fires first.

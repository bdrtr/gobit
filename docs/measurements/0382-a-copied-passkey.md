# A copied passkey — measured 2026-10-04

The evidence behind
[ADR 0382](../adr/0382-a-passkey-whose-counter-does-not-advance-is-suspended.md).

## What the library does with a counter

go-webauthn v0.18.1, the authenticator's `UpdateCounter`, called by the login
validation with the counter of the assertion's authenticator data:

| Stored | Presented | Returned credential |
|---|---|---|
| 0 | 0 | count 0, no warning |
| s | p > s | count p, no warning |
| s > 0 | p ≤ s | count s, `CloneWarning` set |
| 0 | p > 0 | count p, no warning |

The sign-in is never refused over the counter, and on a warning the returned
credential carries the STORED count, so the count the key presented is read
from the parsed assertion. Nothing in the library clears `CloneWarning`, so a
credential written back whole would carry it for good. The backup-eligible flag
is compared with the stored one at every login and cannot change after
registration; the user-verified flag is latched by the credential flags' `Update`.

## What a copy can present

virtualwebauthn v1.0.5 writes the credential's `Counter` field into the authenticator data
that the private key signs, and never increments it. Whoever holds the private
key therefore signs any count it chooses, ahead of the owner or behind.

## The reproduction, at af10f0fd before the change

One key registered from zero on the in-memory store, then three sign-ins
through `POST /store/v1/auth/passkey/sign-in/finish`:

| Count presented | Answer | Session cookie | Stored count after |
|---|---|---|---|
| 5 | 204 | set | 0 |
| 3 | 204 | set | 0 |
| 5 | 204 | set | 0 |

The handler dropped the credential the library returned and called `Used`,
which stamped `last_used_at` alone and did not count the rows it touched, so a
key removed between the library's read and the stamp also signed in.

## The race

`TestTwoSignInsWithOneCountRecordExactlyOne`: twenty rounds, each forcing two
`SignedIn` calls with count 7 over a stored 6 to queue behind a third
transaction holding the row, released once PostgreSQL reports two backends
waiting on a lock. With the row lock every round recorded exactly one and
answered `ErrCountRepeated` to the other; without it both recorded.

## Mutations

Each was applied, the module's package run (unit, or with `-tags integration`
against one PostgreSQL 16 container), and the file restored. Fifty-five, all
killed. The three over the flags the handler passes to the store, and the one
that asks when two keys sign in, survived until the tests that kill them were
written. A row-count check after each locked UPDATE survived as an equivalent
mutant, since the row is held from the SELECT to the commit, and was removed.

| Mutant | Killed by |
|---|---|
| handler never calls `SignedIn` | `TestASignInRecordsWhatTheKeyReported` and others |
| `ErrKeyCopied` answered 401 | `TestACountThatGoesBackSuspendsTheKey` |
| session issued before the store answers | `TestACountThatGoesBackSuspendsTheKey`, `TestASignInThatCannotBeRecordedIsRefused` |
| library's count passed instead of the presented one | `TestACountThatGoesBackSuspendsTheKey` |
| a later sign-in of a suspended key not answered 403 | `TestACountThatGoesBackSuspendsTheKey` |
| the documented 403 dropped | `TestACountThatGoesBackSuspendsTheKey` |
| a repeat answered with a session | `TestARepeatOnlyTheStoreSawSignsNobodyIn` |
| handler's own check removed | `TestTheHandlerDoesNotBelieveAStoreThatRecordsAnything` |
| handler's check without the backup-eligible exemption | `TestASyncableKeyIsNeverRefusedOverItsCount` |
| a store failure fails open | `TestASignInThatCannotBeRecordedIsRefused` |
| a key gone during the ceremony answered 500 | `TestAKeyRemovedDuringTheCeremonySignsNobodyIn` |
| a key gone during the ceremony answered with a session | `TestAKeyRemovedDuringTheCeremonySignsNobodyIn` |
| listing counts a suspended key as a way in | `TestASuspendedKeyIsNotAWayIn` |
| a suspended key not listed removable | `TestASuspendedKeyIsNotAWayIn` |
| removal counts rows, not ways in, before asking | `TestASuspendedKeyIsNotAWayIn` |
| removal of a suspended key asks about other ways in | `TestASuspendedKeyIsNotAWayIn` |
| the account not told of a suspension | `TestAnAccountIsToldOnceThatAKeyWasSuspended` |
| the account told at every later sign-in | `TestAnAccountIsToldOnceThatAKeyWasSuspended` |
| a notice sent on the request's context | `TestASuspensionNoticeOutlivesTheCallerHangingUp` |
| the nil seam guard dropped from the suspension notice | `TestAnEqualCountLaterIsACopy` |
| `suspended_at` undeclared as personal data | `TestADeclarationNamesEveryColumnOfTheTable` |
| WARN line's stored count read from the returned credential | `TestASuspensionIsLoggedWithTheCountTheLibraryRead` |
| user verification never passed to the store | `TestASignInRecordsTheFlagsThisAssertionCarried` |
| user verification always passed to the store | `TestASignInRecordsTheFlagsThisAssertionCarried` |
| backup state always passed as synced | `TestASignInRecordsTheFlagsThisAssertionCarried` |
| listing asks when no key signs in | `TestASuspendedKeyIsNotAWayIn` |
| listing asks when two keys sign in | `TestAListingWithTwoKeysAsksNobody` |
| a failed check withholds a listing that holds a suspended key | `TestASuspendedKeyIsNotAWayIn` |
| a failed check lists a one-key account | `TestARemovalThatCannotBeCheckedChangesNothing` |
| an unchecked key given the last-way-in reason | `TestASuspendedKeyIsNotAWayIn` |
| the failed check behind a listing not logged | `TestASuspendedKeyIsNotAWayIn` |
| `FOR UPDATE` dropped | `TestTwoSignInsWithOneCountRecordExactlyOne` |
| an equal count recorded | `TestTheRealStoreSuspendsACountThatDidNotAdvance`, `TestTheRealStoreRefusesARepeatOnce` |
| the suspension not written | `TestTheRealStoreSuspendsACountThatDidNotAdvance` |
| the suspension not committed | `TestTheRealStoreSuspendsACountThatDidNotAdvance` |
| a suspended key not checked | `TestTheRealStoreSuspendsACountThatDidNotAdvance` |
| repeat window dropped | `TestTheRealStoreRefusesARepeatOnce` |
| repeat window inverted | `TestTheRealStoreRefusesARepeatOnce` |
| window of one ceremony lifetime | `TestTheRealStoreRefusesARepeatOnce` |
| a never-used key's registration not a record | `TestTheRealStoreRefusesARepeatOnce` |
| a syncable key's count lowered | `TestASyncableKeyKeepsItsHighestCount` |
| the backup-eligible exemption dropped | `TestASyncableKeyKeepsItsHighestCount` |
| zero over zero not recorded | `TestSigningInRecordsTheCountInsideTheCredential` |
| the absent count not defaulted | `TestSigningInRecordsTheCountInsideTheCredential` |
| merge into a missing `authenticator` object | `TestSigningInRecordsTheCountInsideTheCredential` |
| the count written to the wrong path | `TestSigningInRecordsTheCountInsideTheCredential` |
| the user-verified latch dropped | `TestSigningInRecordsTheCountInsideTheCredential` |
| the moment not stamped | `TestSigningInRecordsTheCountInsideTheCredential` |
| the backup state not written | `TestSigningInRecordsTheCountInsideTheCredential` |
| the public-key guard dropped | `TestTheCountLandsOnlyOnTheKeyThatSigned` |
| the relying-party scope dropped | `TestAnAbandonedKeyCannotSignAnybodyIn` |
| `Remove` counts suspended rows as ways in | `TestTheRealStoreDoesNotCountASuspendedKeyAsAWayIn` |
| a suspended target guarded like any other | `TestTheRealStoreDoesNotCountASuspendedKeyAsAWayIn` |
| the listing does not read `suspended_at` | `TestTheRealStoreDoesNotCountASuspendedKeyAsAWayIn` |
| the dossier does not read `suspended_at` | `TestTheRealStoreDoesNotCountASuspendedKeyAsAWayIn` |

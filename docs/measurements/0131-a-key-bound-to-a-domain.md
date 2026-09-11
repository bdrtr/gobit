# A key bound to a domain — measured 2026-09-11

Serves [ADR 0131](../adr/0131-a-passkey-belongs-to-one-relying-party.md).

## What was measured, and what was not

The claim being chased was that changing `Options.RPID` turns existing rows into
things that are no longer ways in, while the guard shipped in ADR 0130 goes on
counting them. Two halves, and only one of them is measurable here.

**Measured.** With a credential registered under `example.test` and a second
module configured for `moved.test` over the SAME store, the module answered a
sign-in with **204**:

```
PROBE 4: the SERVER answers 204 for a row registered under "example.test"
         asserted under "moved.test"
```

So the module itself had no opinion about which relying party a row belonged to.
Whatever presents such a row is signed in.

**NOT measured, and it is spec rather than evidence.** That a real authenticator
will not offer a credential for a relying party it was not created for. This is
WebAuthn's own scoping rule, and the software authenticator in these tests does
not model it — see below. The record says "cannot be offered" on the strength of
the specification, not of a probe in this repository.

The distinction matters because the two halves point at different fixes. The
measured half says the module must refuse a foreign row. The spec half is what
makes an abandoned row worthless, and therefore what makes counting it a defect.

## A probe that almost produced false evidence

The first version asked the software authenticator whether it would offer the old
credential under the new relying party id, and it answered **false** — which reads
exactly like a confirmation of the spec half.

It is not. `virtualwebauthn`'s check is:

```go
func (c *Credential) IsAllowedForAssertion(options AssertionOptions) bool {
	encodedID := base64.RawURLEncoding.EncodeToString(c.ID)
	for _, allowedID := range options.AllowCredentials {
		if allowedID == encodedID {
			return true
		}
	}
	return false
}
```

It reads the ALLOW LIST and never the relying party. This module's sign-in is
discoverable and sends no allow list, so the answer is `false` for every
credential, in every ceremony, including the ones that work. The probe would have
reported RP scoping and measured an empty slice.

Found by reading the function instead of the return value. It is the same lesson
this repository records about quoting somebody else's godoc as evidence: the
sentence has a SUBJECT, and here the subject was not the relying party.

## The relying party cannot be recovered from the stored value

A column was not the first option considered — the stored credential is the whole
thing the library serialises, and the RP ID hash travels in attestation
authenticator data. So the stored JSON was inspected:

```
PROBE 1: customer=cust_06G8PASSKEYCUSTOMER00000
         credID="HAfKtZkTwAoc5CR9d68PU4Qz5ozNh3-yrNFp-UaHF-k"
         attestationType="basic_surrogate" attestationLen=0
```

`attestationLen` is the length of the credential's attestation authenticator data
(a field of the library's own type, not one of this repository's), and it is zero.
There is nothing to read the relying party out of, so recording it separately is
the only option and any store that keeps credentials has the same obligation.

## What the guard did with an abandoned row

The sequence, as an integration test against a real PostgreSQL: a key registered
under `before.example`, a key registered under `after.example`, and a removal of
the SECOND one attempted by a module configured for `after.example`.

| | before the column | after |
|---|---|---|
| keys the listing shows | 2 | 1 |
| rows on disk | 2 | 2 |
| removing the only usable key | permitted | `ErrLastWayIn` |

The middle row is the one worth keeping: nothing is deleted. A configuration
change is not consent to destroy somebody's registered credentials, and an RPID
edited by mistake would otherwise be unrecoverable.

## The mutation table

| Mutation | Bitten by |
|---|---|
| every row belongs to every relying party (`TRUE OR …`) | `TestAKeyOfAnAbandonedRelyingPartyIsNotAWayIn`, `TestAnAbandonedKeyCannotSignAnybodyIn` |
| a NULL row belongs to no relying party (`FALSE OR …`) | `TestARowFromBeforeTheColumnStillWorks` |
| the conflict update does not claim the row | `TestReRegisteringAKeyClaimsItForTheCurrentRelyingParty` |
| the store is constructed with no relying party | `TestAKeyStoredInPostgresStillSignsIn`, `TestOneKeyStaysOneRow` |

The third survived the first run. No test re-registered the same credential id
under a different relying party, because a credential id is minted per relying
party and the case is rare — rare enough that the conflict update was left alone
and nothing noticed. What it produces is the shape this module refuses everywhere
else: a 204 for a registration whose row the configured relying party cannot see,
so somebody is told they registered a passkey and has not. The test was written
and the mutation then bit.

## Why there is no startup warning

An operator who moved domain would be helped by a line at boot naming how many
rows belong to another relying party. The query is
`SELECT 1 FROM passkey_credentials WHERE rp_id IS NOT NULL AND rp_id <> $1 LIMIT 1`,
and its cost lands on exactly the wrong installation: `LIMIT 1` stops at the first
match when there ARE abandoned rows, and scans the whole table when there are none.
The healthy case pays at every boot for a diagnostic only the moved case needs, and
the new index cannot help — it leads with `customer_id`, which this question does
not have.

## A column count was a measure of its own day

`TestTheSchemaIsWhatTheModuleWrites` asserted that the table has five columns, and
`rp_id` made it fail with `expected: 5, actual: 6` — a number, with no word about
which column was unexpected. It compares the column NAMES now. The class is one
this repository already records: a numeric constant in a test is the measure of the
world on the day it was written, not of the claim being made.

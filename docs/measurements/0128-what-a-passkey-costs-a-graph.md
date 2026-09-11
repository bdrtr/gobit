# What a passkey costs a graph — measured 2026-09-11

Serves [ADR 0128](../adr/0128-passkeys-ship-in-a-module-of-their-own.md).

## The nine modules

Importing `github.com/go-webauthn/webauthn` into an empty module and tidying:

```
go list -m all | wc -l     # 23, one of them the probe itself
```

Of those, nine are not already in gobit's graph:

```
github.com/fxamacker/cbor/v2      github.com/philhofer/fwd
github.com/go-tpm                 github.com/tinylib/msgp
github.com/go-tpm-tools           github.com/x448/float16
github.com/go-webauthn/webauthn   go.uber.org/mock
github.com/go-webauthn/x
```

TPM support and a msgpack codec are there because WebAuthn attestation covers
hardware that attests with them. A shop that wanted a product catalog should not
carry either, and `contrib/identity-session` is imported by shops that want a
password.

So the split is not tidiness. It is the only thing that keeps a password-only
installation from carrying an attestation-format parser: a separate `go.mod`, one
level deeper than the split ADR 0127 already made for the same reason.

## What the TEST dependency costs, measured separately

`github.com/descope/virtualwebauthn` is a software authenticator that signs the
way a real one does. Its graph overlaps go-webauthn's almost entirely — the one
module it adds beyond it is `github.com/fxamacker/webauthn` — and it is a test
dependency of a module an embedder may never import.

That is what made the ceremonies testable rather than merely wired, and the
difference is not academic: every mistake this module could make is about a
challenge, an origin or a user handle, and no assertion about a handler can see
any of them.

## Three things the real ceremonies found

**A discoverable sign-in needs the user handle on the DEVICE.** The first version
of the test built the virtual authenticator without one and the library refused
the assertion: *"Client-side Discoverable Assertion was attempted with a blank
User Handle"*. That is the library refusing exactly the sign-in this module
offers — the handle is what a discoverable ceremony resolves the account by, and
setting it in the registration options is not the same as the authenticator
storing it.

**A second identity cannot be bound.** The test's first harness provided its own
`corehttp.Identity` beside the session module's and the container refused it:
*"a service is already registered under the name core.identity"*. The container
was saying what the composition says — there is one answer to "who is this
request" — and the fix made the test more faithful rather than less: a
registration is now proven with a real session cookie, which is the only proof
this arrangement accepts.

**Comparing stored fields was the wrong assertion.** The storage test first
compared credentials field by field and failed on `attestationType`, which this
library writes into its JSON and, for some shapes, does not read back. Chasing it
would have been chasing the wrong thing twice: the field is registration metadata
no assertion consults, and a list of fields to compare is a copy of the library's
schema that goes stale the first time it grows one.

What the storage decision actually promises is narrower and testable: a key
registered, written to Postgres and read back out of it still signs its owner in.
The test performs both ceremonies with the real store between them and knows
nothing about which fields the library needed.

## The mutations

| Mutation | What fell |
|---|---|
| the ceremony cookie is not cleared on finish | the replay test |
| the account that BEGAN the ceremony is not checked | the takeover test |
| a registration does not ask who the caller is | the not-signed-in test |
| the credential is stored as an empty object | the store-and-sign-in test |
| the conflict target stops replacing a key | one-key-one-row, and three more |
| the object CHECK is dropped from the migration | the raw-SQL test |

Six, each on the case written for it, all with `-count=1`.

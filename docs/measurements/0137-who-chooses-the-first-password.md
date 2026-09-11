# Who chooses the first password — measured 2026-09-11

Serves [ADR 0137](../adr/0137-a-colleague-sets-their-own-first-password.md).

## The two ways to add a colleague, both wrong

Read off `internal/modules/auth/service/user.go`:

| `CreateUser(..., password)` | What the shop ends up with |
|---|---|
| password given | the creating administrator knows a colleague's secret, and the record of who can act as that user is false from the first minute |
| password empty | the whole password block is skipped, `identity` stays nil, and the repository returns right after `InsertUser` — a user with NO `auth_identity` row, who cannot log in, with nothing anywhere saying why |

`SetPassword` was the only way out of the second state and it is an admin endpoint,
so the first person to use it is again somebody other than the account's owner.

## What blocked the obvious fix

An invitation needs a message to go out, and gobit could not send one that was not
an event.

| | |
|---|---|
| the notification module's cross-module surface | **none** — one of two modules without an `interop.go` |
| how anything caused a notification | publish an event; the module subscribes |
| what an event is | an outbox row, a durable Redis stream entry, delivered at least once |
| what happens to every published topic | forwarded by `plugins/webhookout`, and `TestTheForwardedTopicsAreEveryPublishedTopic` fails the build in BOTH directions |

A one-time invitation token in a durable stream that is forwarded to an operator's
third-party endpoints is not a design with a redaction rule missing; it is the wrong
transport. So the surface had to exist, and the invitation is its first consumer —
which is what keeps it from being a contract with nobody on the other end
(`TestTheInteropSurfacesHaveAConsumer`).

## What was already there, and was reused rather than reinvented

- **The token shape.** `api_key` holds `token_hash TEXT` with
  `CHECK (token_hash ~ '^[0-9a-f]{64}$')`, minted from 32 bytes of `crypto/rand` and
  digested with SHA-256. The invitation table carries the same column, the same
  CHECK and the same digest. A second convention for "a hashed token in a table"
  would be a second thing to get right.
- **The unprotected-path mechanism.** One exists and had exactly one user: a module
  publishes the full path as a constant, registers the route on the bare router, and
  the composition root puts that constant into `GuardOptions.AdminExempt`. The
  accept endpoint is the second, written the same way — so the exemption follows the
  path rather than being hand-copied beside it.
- **`SetPasswordHash` INSERTS when there is no identity**, taking the login address
  from the LOCKED user row. That is what makes "invite a user created without a
  password" work at all, and it was measured rather than assumed: had it only
  updated, accepting an invitation would have written nothing and answered success.

## Where the rules differ from the storefront's, and why

ADR 0133 built the same shape for a shopper one commit earlier. Three things are
deliberately not the same:

| | storefront (ADR 0133) | admin (here) |
|---|---|---|
| link lifetime | 1 hour | 72 hours |
| on success | signs the person in | 204, and an ordinary login to make |
| who may ask | anyone, rate limited | an administrator with the write scope |

The lifetime is the person's situation rather than a security dial: a shopper who
registers is at the keyboard and uses the link in minutes; a colleague may be away
for a weekend, and a link that expired before they read it turns a one-step flow into
a support request. The session is the sharper difference — proving an address IS the
account on the storefront, while here handing back an admin token from the one
endpoint that cannot ask who is calling would be a second way to obtain one.

## The order of the two checks in `AcceptInvitation`

The password policy is checked BEFORE the token is taken. Both orders "work"; the
difference is what a typo costs. Taking the token first spends the invitation on a
request that wrote nothing, and the colleague has to ask for another — so the test
that holds it is `TestARefusedPasswordDoesNotSPENDTheInvitation`, and the mutation
that swaps the order fails it.

## The mutation table

| Mutation | Bitten by |
|---|---|
| the token is stored as sent | `TestAnInvitationCanBeSpentONCE` |
| the token is not hashed on acceptance | `TestAnInvitationCanBeSpentONCE` |
| the password policy is checked after the token is taken | `TestARefusedPasswordDoesNotSPENDTheInvitation` |
| an invitation opens with nobody bound to carry it | `TestAnInvitationNOBODYCanCarryIsNotOpened` |
| a failed send is swallowed | `TestAFailedSENDDoesNotLeaveASilentRow` |
| the sender leaves the token out of the message | `TestTheSenderCarriesTheTOKENAndNothingElseIdentifying` |
| the sender fixes the reference to a constant | the same test |
| the surface carries the template where the reference belongs | `TestTheSurfaceCarriesEACHFieldToItsOwnPlace` |
| the surface carries the reference where the address belongs | the same test |

Nine, each with `-count=1`, each restored from a scratchpad copy rather than with
`git checkout`.

### Two survived the first pass, and both are the same gap one layer apart

The service's tests bind a FAKE sender, so a mutation blanking the token inside the
real `invitationSender` passed all of them. And nothing touched the notification
module's new surface at all, so swapping its template and reference — which would
make every invitation to one user look like a repeat of the first — passed too.

That is what a fake always leaves: it proves the caller's decision and says nothing
about the wrapper beneath it. The fix is the same both times — a test in the package
that owns the wrapper — and it is the second time in two days that the seam between
a fake and the real producer was the uncovered part (the other: the order module's
dispatchable-lines surface, ADR 0135).

# A factor nothing asked for

Evidence for [ADR 0147](../adr/0147-a-second-factor-is-demanded.md).

Measured 2026-09-12, taking the remaining half of the feature list's A6.4 row.

## What was there, and what asked for it

ADR 0143 shipped the record: `auth_mfa_credential` (a sealed secret, a
`confirmed_at` stamp), `POST /admin/v1/auth/mfa`, `POST /admin/v1/auth/mfa/confirm`,
RFC 6238 written out and checked against Appendix B's vectors.

Measured against the tree, the method that answers "has this person proven a
factor" had ONE caller, and it was a test:

```
$ grep -rl HasConfirmedMFA --include=*.go .
internal/modules/auth/service/mfa.go        ← the definition
internal/modules/auth/service/mfa_test.go   ← the only caller
```

Its own godoc said so plainly — "Nothing calls it yet, and the endpoint that will
is a separate decision" — which is the honest version of the same fact, written by
the record that shipped it.

So the whole of the feature, from a shop's point of view, was: enrol, scan,
confirm, and then sign in with the password exactly as before.

## The single session mint

The demand has to attach where a token is born, so the population had to be
measured rather than assumed:

| Path | Mints a session? |
|---|---|
| `Service.Login` | yes — `issueToken` |
| `Service.AcceptInvitation` | no; it sets the first password and returns nothing |
| `POST /admin/v1/users/{id}/password` | no |
| api key verification | no token; the key IS the credential |
| `adminui` sign-in | calls `Login` |

One mint, one place to attach. That is also why the gate this slice adds is
structural rather than behavioural: today's population is one, and the risk is the
SECOND one — a magic link, an OAuth callback, an impersonation verb — which would
compile beside it and pass every existing test.

## The three questions ADR 0143 left, answered against the code

**A machine.** `personOrRefuse` already refuses an api_key enrolment, and the
demand hangs off the password login, which a key never touches. Nothing to decide;
what was needed was a test whose subject is a key, because "the demand did not
reach machines" is exactly the sentence a future change would break silently.

**An enrolment nobody proved.** `confirmed_at IS NULL` reads as "no factor" and the
demand respects it. Measured cost of getting this wrong: everybody whose scan died
half way is locked out with a secret that is on no phone, and the way out is a
login they cannot make.

**A lost phone.** This is the one that changed the design. The old shape was a
single row replaced on re-enrolment, and the table's own comment described the
remedy: "they ask again, scan the new code". That remedy requires SIGNING IN, which
is precisely what a person with no phone cannot do — so it stopped working the
moment the demand landed, and it took a second defect with it:

> a re-enrolment cleared `confirmed_at`, so starting one and walking away turned
> the demand OFF for the next login.

A way out of a second factor that needs no secret at all. `pending_secret` is what
closes both: the proven secret keeps signing in, the new one waits, and the swap is
one statement so no moment accepts two phones.

## What the panel turned out to need

`internal/adminui` has its own `Session` interface (it may import no module) and
its own login form. Widening `Login` by one parameter:

- broke nothing that compiles — `go build ./...`, `go vet`, every unit lane green;
- would have failed at BOOT with `container_type_mismatch`, in the smoke lane.

The panel resolves FIVE names by hand (`auth.service`, `core.query`,
`product.admin`, `pricing.admin`, `inventory.admin`) and the pin file priced none
of them: its derivation walks the `.interop` family. Measured while fixing it, the
tree resolves TWENTY-THREE provided names from outside the module that owns them,
nineteen of which are that family. The five panel names are pinned now; the rest
are named in the pin file as a decision still to make, because "the" pin for a core
service is a choice per consumer rather than one assignment.

The form needed a code box and the handler needed two messages. The messages are
not decoration: every other refusal collapses into "email or password is
incorrect", and printing that to somebody holding the right password and an open
authenticator app sends them to change a password that is correct.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 41 | the demand is skipped entirely | **bit** (6 service tests) |
| 42 | a second mint written beside `Login` with no demand | **bit** (the gate) |
| 43 | an unconfirmed enrolment is demanded too | **bit** |
| 44 | a wrong code does not count as an attempt | **bit** (2) |
| 45 | an absent code counts as an attempt | **bit** |
| 46 | the demand runs after the counter is cleared | **bit** |
| 47 | the confirm compares against the old secret while one waits | **bit** (2) |
| 48 | the demand's own "no key" branch is disabled | **survived** |
| 48b | the secret box's keyless refusal is disabled | **bit** |
| 49 | the panel drops the code | **bit** |
| 50 | the panel prints the generic line for a factor refusal | **bit** (3) |

Mutation 46 is the ordering claim and it is the one a reviewer would not think to
look for: `RegisterLoginSuccess` CLEARS the failed-attempt counter, and that counter
is the only bound on guessing six digits. Demanding the factor after the counter is
cleared leaves the login refusing correctly and the lockout unreachable — every
guess resets the count — so the test that holds it counts attempts rather than
statuses.

Mutation 48's survival is the useful one. The branch it disabled was mine, added
for fail-closed behaviour the secret box already provides: `open` refuses a keyless
call with the same code. Two guards, one decision, and the test could not tell them
apart because both produce the same answer. The branch was deleted and 48b proves
which guard the test actually holds.

## What is NOT closed

Nothing requires a factor. An installation that wants every administrator covered
has no setting, and the reason is measured rather than principled: nobody has
enrolled anywhere yet, so the flag's first effect would be locking out every
administrator at once. It belongs to a later decision, with an enrolment grace
period in it.

Also open, and named in ADR 0143 already: no gate checks constant-time comparison
(five in the tree), and the reset command has no audit row of its own beyond the
service's log line.

## What was not measured

Whether any installation enrolled a factor believing it protected them. It would
show as a confirmed `auth_mfa_credential` row on a deployment running any version
before this one, which is reconstructable from a backup and was not attempted.

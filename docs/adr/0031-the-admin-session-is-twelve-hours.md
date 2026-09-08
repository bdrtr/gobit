# ADR 0031 — The admin session is a fixed twelve hours, and the trade is written down

**Summary:** The admin session is a fixed twelve hours and never renews; the
operator sets the number and gobit publishes what each end of the range costs.
A shared environment is bounded at twenty-four hours.

- **Status:** Accepted
- **Date:** 2026-09-06
- **Phase:** after the roadmap

## Context

An admin session token lives `JWT_TTL`, twelve hours by default, and there is no
renewal path. The lifetime was therefore an unstated trade: a long one leaves a
stolen token useful for longer, a short one logs an operator out mid-task, and
nothing in the repository said which side it had picked or why.

**The half everyone expects to be missing is already built, and that inverts the
cost.** Verifying an admin token is not merely cryptographic. The identity path
reads the user and then a SESSION ANCHOR on every request, and refuses a token
issued before a logout or a password change. So revocation exists — coarse,
per-identity, but real: even a long-lived token dies the moment somebody logs
out or changes their password.

That makes "a long lifetime plus revocation" a configuration act rather than a
build, and it makes the refresh answer the expensive one rather than the obvious
one.

**Two more measurements shaped this.** `Config.Validate` accepts any positive
`JWT_TTL` at all — it refuses zero and negatives and nothing else, while the
same file enforces a minimum secret length only in shared environments. And
`JWT_TTL` appears nowhere in the README; it is a bare line in `.env.example`
under a comment block about the secret.

## Decision

**gobit ratifies the clock. The admin session ends at a wall-clock deadline and
never renews.** The default stays twelve hours, the operator sets the number
with `JWT_TTL`, and the framework publishes what each end of the range costs.

Twelve hours is chosen deliberately rather than inherited: it is longer than a
working shift, so an operator who starts a task finishes it, and short enough
that a token taken from a machine at the end of a day is dead by the next one.

**`Config.Validate` gains an upper bound in shared environments**, beside the
existing `<= 0` check and following the precedent a few lines below it, where
the minimum secret length is enforced only when the environment is shared. A
month-long admin session on a shared deployment is a configuration mistake the
framework can see, and refusing it at boot is cheaper than discovering it in an
incident.

**The trade is documented where an operator reads it** — the README's settings
table and a real comment in `.env.example`, not only in this ADR.

## What this costs, and it is not the cost people expect

**The mid-task logout stays**, and it is worse than "the user signs in again":
the message they see is BLANK.

The cookie's lifetime is deliberately tied to the token's own expiry, so that a
cookie never outlives the credential it carries. The consequence is that a
NATURALLY expired session sends no cookie at all, and the panel's guard takes
its `token == ""` branch, which renders the login page with an empty message.
The helpful sentence — "Your session has expired. Please sign in again." — sits
on the other branch, the one reached when a cookie exists but its token does
not, which the cookie design makes unreachable in the ordinary case.

**So this ADR accepts one defect and names it rather than hiding it:** the
twelve-hour deadline is the moment an operator most needs an explanation, and
today they get a bare login form. Fixing that is panel-local and available under
every candidate — it is a message and a return-to, not a mechanism — and it
should ship with this decision rather than after it.

## Rejected alternatives

**A separate refresh credential.** The only candidate needing both a new table
and a new route, in a module whose single migration already carries five tables.
It buys the property none of the others have — a leaked access token dies in
minutes because the renewal credential is separate, single-use and revocable —
and that property is worth paying for the day an admin token is actually stolen.
It is not worth paying for today, before anybody has complained about the
twelve-hour logout, and the file head of the auth service already argues against
a per-token store on the VERIFICATION path.

**Re-issuing the live token at one identity endpoint.** Cheaper than the refresh
row — no table, and both halves already exist as private methods — but it makes
the access token its own renewal credential, so a leaked token renews for as long
as an absolute cap allows. This is the candidate to reach for FIRST if the
twelve-hour logout turns out to hurt: it needs no schema change, and an absolute
cap comes free by carrying the original issue time forward across re-issues.

**The panel renewing silently while the JSON surface keeps the raw clock.** Void
under [ADR 0030](0030-the-panel-becomes-an-admin-api-client.md): once the panel
is a client of `/admin/v1`, there is no panel-only path left to renew on, and
the answer collapses into one of the two above.

## Consequences

**Positive**

- **No new table, no new route, no ADR superseded.** The decision is a number, a
  bound and two sentences of documentation. ~~The blank message is a message and
  a return-to, not a mechanism.~~ **Corrected 2026-09-07 while building it: the
  message half NEEDED a mechanism, and the sentence above was written without
  measuring it.** See the amendment below.
- **A7 can be written.** [ADR 0030](0030-the-panel-becomes-an-admin-api-client.md)
  turns on the words "short-lived", and this ADR is where that number now lives.

**Negative, and accepted**

- **An operator is logged out at the deadline**, mid-task if the deadline lands
  there. ~~and today with no explanation.~~ **The explanation shipped on
  2026-09-07**; the logout itself stays, and it is still the price of this
  decision.
- **Revocation stays coarse.** Logout and password change kill every session of
  that identity; there is no per-device revocation and this ADR does not add
  one. `auth/service/session.go` already refuses that surface "until it is
  really needed", and this decision does not make it needed.

## Amendment, 2026-09-07 — what building the three obligations changed

All three shipped: the bound, the documentation, and the message. Two of them
turned out differently from what this ADR predicted, and both differences are
the ADR's error rather than the implementation's.

**The bound is twenty-four hours, and the number is an argument rather than a
round figure.** `config.MaxSharedJWTTTL`, enforced only when `APP_ENV` is not
`development`, beside the signing-secret rule and for the same reason. It is
twice the default deliberately: the case for twelve hours above is that "a token
taken from a machine at the end of a day is dead by the next one", and a day is
the LAST value that keeps that sentence true. Anything longer survives a night,
and a week survives a holiday. Local development is left unbounded because it is
already the one environment where the secret and TLS rules are relaxed.

**"It is a message and a return-to, not a mechanism" was wrong, and the way it
was wrong is the interesting part.** The claim treated the blank login page as a
missing string. It is not: the guard's empty-token branch is reached by TWO
different people — somebody whose session just lapsed, and somebody who has
never signed in — and the session cookie cannot tell them apart, because this
ADR's own cookie design deletes it in both cases. Printing the sentence
unconditionally does not fix the defect, it moves it: the first-time visitor is
then told their session expired. The distinction needs state that OUTLIVES the
credential, so the fix is a second cookie (`gobit_admin_seen`) carrying a
constant, no credential, and a lifetime a week past the token's. The credential
still dies exactly on the deadline, which is the property this ADR wanted to
keep; what gained a longer life is the MESSAGE, which is not a secret.

It is dropped on a deliberate sign-out — "you signed out" is not "your session
expired" — and dropped again as the sentence is printed, because the message is
about a transition and a reload should show a form rather than repeat an expiry
already explained.

**The return-to is an open-redirect guard, and its first draft had a dead branch
hiding a live hole.** The draft compared raw strings: refuse a leading `//`,
then require the panel prefix. Mutation testing showed the `//` branch could be
deleted with no test noticing — a protocol-relative URL cannot also start with
`/admin/ui`, so the prefix check had already refused it. Looking for a case
where that branch DID matter found the hole facing the other way:
`/admin/ui/../../admin/v1/orders` carries the prefix as a raw string, so the
draft ACCEPTED it, and a browser resolves it outside the panel. The shipped
version parses the target, refuses any scheme or host (an attacker's URL can
carry the panel's exact path — `https://evil.example/admin/ui/orders` — and only
the host says otherwise), resolves `..`, and refuses a backslash because
`path.Clean` does not see it while some browsers fold it. Each of the four is
mutation-proved to be load-bearing.

**What this cost that the ADR did not price:** one constant, three cookie
helpers, a sanitiser, a hidden form field, and eleven tests. It is still small,
and it is not "a message".

## Reopening the decision

Reopen when an operator complains about the twelve-hour logout — that is the
signal, not a schedule — and the first thing to try is the identity-endpoint
re-issue above, because it needs no migration. Reopen immediately, and go to the
refresh credential instead, if an admin token is ever known to have leaked: at
that point the separate, revocable credential is the property worth its price.

## Related

- [ADR 0030](0030-the-panel-becomes-an-admin-api-client.md) — decided in the
  same sitting; each was an input to the other.
- [ADR 0008](0008-musteri-kimligi-guven-siniri.md) — customer sessions are the
  embedder's, so this decision is about the ADMIN token only.

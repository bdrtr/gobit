# ADR 0147 — A second factor is demanded

**Summary:** An administrator who has proven an authenticator does not get a
session token from a password alone. It costs a way back that only an operator at
the machine can walk, and it buys a second factor that means something.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

ADR 0143 gave an administrator somewhere to keep a second factor: a sealed TOTP
secret, two endpoints, and `HasConfirmedMFA` to answer whether it was ever proven.
Nothing called that method. A person could enrol, scan the code, confirm it — and
sign in with their password as if none of it had happened.

That is this repository's own recurring defect rather than an unfinished feature:
a mechanism nothing feeds — worse here, because what it pretends to do is protect
the administration, and an installation that turned it on believed it had.

Three questions had to be answered before the demand could ship, and none could be
answered before people could enrol: what happens to a machine, to somebody who
starts an enrolment and walks away, and to somebody whose phone is gone.

Measurement: [measurements/0147](../measurements/0147-a-factor-nothing-asked-for.md)

## Decision

`Login` asks for the account's proven factor before it signs a token, and takes
the code in the login body — `auth_mfa_required` when none came,
`auth_mfa_code_wrong` when the wrong one did, both only after the password
matched. A confirmed factor is removed by its OWNER over HTTP or by an operator
running `gobit mfa-reset`, and by nobody else.

## Consequences

The three questions are answered where the shapes make them safe rather than by
policy. A MACHINE is untouched: the demand hangs off the password login, a secret
key has no authenticator, and enrolling with one was already refused. An
ENROLMENT NOBODY PROVED demands nothing, so a failed scan locks nobody out. And a
re-enrolment no longer clears the confirmation — the new secret waits in
`pending_secret` beside the proven one and the confirmation moves across in one
statement — because clearing it would have been a way out of the second factor
that needs no secret at all.

A LOST PHONE cannot be fixed by its owner any more, and no endpoint fixes it for
them: an administrator who could remove a colleague's factor would make one stolen
admin session enough to reach every other account with a password. What is left is
shell access, the privilege that case deserves; the command names the account twice
and says plainly that it leaves it protected by a password alone.

Nothing is required shop-wide, so an account with no factor signs in exactly as
before and an upgrade locks nobody out — and an installation that wants every
administrator covered has no way to insist. That is in `known-limits.md`.

The panel had to learn two sentences, and that is a cost with a reason: it
collapses every refusal into "email or password is incorrect" deliberately, and
doing so here would tell an operator holding the right password and an open
authenticator app that their password is wrong.

Ten mutations bit and one survived, and that one is worth the space: an explicit
"this installation has no key" branch in the demand broke no test, because the
secret box already refuses a keyless open with the same code. It was DELETED
rather than given a test — it decided nothing, and a second guard is a second
place to get the answer wrong.

Widening `Login` also broke `adminui.Session` with every lane green: the panel's
five resolved surfaces were unpinned, so the failure waited at boot. Pinned now.

## Rejected

- **A challenge token exchanged for a session.** The usual shape, and it needs a
  second secret type that must be refused everywhere a session is accepted; a code
  in the login body needs no new credential at all.
- **Recovery codes.** A second readable secret to store, show once and re-issue,
  to answer a case the operator's command already answers in one act.
- **An endpoint that resets a colleague's factor.** One stolen admin session
  would then be enough to strip every other account down to its password.
- **Require a factor of every administrator.** Nobody has enrolled yet, so the
  flag's first effect would be locking out every administrator at once.

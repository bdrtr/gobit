# ADR 0264 — A second factor is changed only with itself

**Summary:** Anyone who can sign in may enrol, confirm and remove their own
second factor, and replacing or removing a confirmed one over HTTP takes the
code it shows now; a wrong code counts toward the account's sign-in lock.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amends:** [0143](0143-an-administrator-can-hold-a-second-factor.md) for who may enrol, and [0147](0147-a-second-factor-is-demanded.md) for how the owner removes one

## Context

The three endpoints that act on the caller's own factor were guarded by
`auth:read`, the privilege to read other people's accounts, and their comment
said anybody who can sign in may protect their account. An operator granted
the catalog alone could not (D182).

ADR 0147 made a stolen session not enough to sign in, and its owner's removal
over HTTP asked for the session alone. A stolen token could switch the factor
off, or park the thief's authenticator beside it and confirm it with the
thief's own code, and the owner would then be refused at sign-in (D183).

## Decision

Enrolling, confirming and removing one's own factor need an identity and no
privilege. When the account holds a confirmed factor, replacing it and
removing it over HTTP take the code it shows now, a wrong one is counted as a
wrong sign-in code is, and a locked account is refused.

## Consequences

- Refusals are 403 with `auth_mfa_required`, `auth_mfa_code_wrong` or
  `auth_mfa_locked`: the caller is signed in, and a 401 would read as an
  expired session.
- The first enrolment and a change to one nobody confirmed take no code, since
  nothing proven protects the account yet. The code travels in an optional
  JSON body, so the removal moved from a `DELETE` on the enrolment's address
  to `POST /admin/v1/auth/mfa/remove`: a `DELETE` in this API reads no body.
- A client that removed or replaced a confirmed factor with the session alone
  now has to send the code. The changelog says so.
- `gobit mfa-reset` at the machine removes a factor without a code, as before;
  it is the way back for a lost phone.
- The routes carry no guard, so the OpenAPI document names no privilege for
  them (ADR 0263), and the end-to-end list of identity-only operations names
  them with why.
- The known limit that no installation can require a factor is unchanged.

## Rejected

- Keeping the enrolment behind a privilege of its own: every operator would
  need a grant to protect their own account, which is the defect.
- Asking for the password rather than the code: the session thief may know
  it, and the factor exists for the case where they do.
- Letting an unconfirmed enrolment's removal also take a code: nothing proven
  stands behind it, and a half-scanned phone would lock its owner out.

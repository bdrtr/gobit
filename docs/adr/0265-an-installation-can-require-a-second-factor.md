# ADR 0265 — An installation can require a second factor

**Summary:** From the moment `ADMIN_SECOND_FACTOR_REQUIRED_FROM` names, a person
who has not proven an authenticator holds no privilege until they do, while
every endpoint that asks for identity alone, enrolling included, stays open;
the user listing says who still owes one.

- **Status:** Accepted; amended by [0266](0266-the-panel-enrolls-a-second-factor.md) for the panel, which now enrolls a factor and sends an owing person to it
- **Date:** 2026-09-30

## Context

ADR 0147 demanded a factor of whoever held one, and the known limits said an
installation could not insist on one: the first effect of a switch would be
locking out every administrator at once, so a requirement needed a grace
period and a way to see who was still without one. ADR 0264 made enrolling an
act of identity alone, which is what a person without privileges can still do.

## Decision

The installation names a moment, and from it every session of a person with no
confirmed factor resolves, on each request, to an identity holding no
privilege, so only the identity endpoints answer them, enrolling included. The
user listing takes `second_factor=true|false`, every user record says
`second_factor`, and `/admin/v1/auth/me` says `second_factor_owed`.

## Consequences

- The moment is the grace period: set ahead, it lets people enrol while
  `GET /admin/v1/users?second_factor=false` shows who has not.
- Deciding on each request rather than at sign-in holds a session signed
  before the moment to it, and gives the privileges back to the next request
  after the factor is confirmed.
- Nobody is locked out: an owing person still signs in with a password, gets a
  session, and is refused every privileged endpoint with 403 until they enrol.
- An enrolment nobody confirmed does not pay the debt.
- A secret key is a machine and is untouched, as ADR 0147 left it.
- Every admin request of a person costs one more read of their credential
  while a moment is set.
- The panel has no enrolment screen, so an operator who owes a factor enrols
  through the API; the panel shows them the refusal it shows anyone holding no
  privilege. The known limit now says so.
- A moment that is not RFC 3339 stops the process at start.

## Rejected

- A switch instead of a moment: its first effect is the lockout this waited
  for.
- Refusing the sign-in of a person who owes a factor: they could then never
  reach the enrolment, which needs a session.
- Stripping the privileges when the token is signed: a session opened before
  the moment would keep them for the rest of its life.

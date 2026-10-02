# ADR 0364 — A credential that could not be checked is not refused

**Summary:** The admin and store guards and the panel answer an
authenticator that could not reach a verdict, its error classified
unavailable or internal, with that class's status rather than 401, and the
panel keeps the session cookie when it cannot check it.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

`RequireAdmin` and `RequireStore` answered every error from the
authenticator 401, and the panel's guard met every error by clearing the
session cookie and asking for a sign-in. When the database holding the
identities was out of reach, every storefront request was told its
publishable key was invalid and every operator in the panel was signed
out. The auth module already told the two apart: a credential not on file
is refused as unauthorized, and a lookup the database could not answer
keeps the database's class.

## Decision

An authenticator error classified by `core/errors` as unavailable or
internal is a failure to check the credential, answered with that class's
status under the core's own sentence, `auth_unchecked`, and the panel
answers it with a page that keeps the session. Every other error,
unclassified included, is a refusal, answered 401 as before.

## Consequences

- A database outage answers a storefront 503 or 500, which it retries or
  reports, rather than 401, which says its key was revoked.
- An operator signed into the panel through an outage finds the session
  where it was once the database answers.
- The reason stays in the log for a failure as for a refusal: the client
  reads the class and the core's sentence, never the authenticator's.
- A failure asks for no other credential: it carries no
  `WWW-Authenticate` header.
- An embedder's authenticator that returns a bare error for a wrong
  credential still answers 401; one that classifies its outages gets the
  same as the auth module.
- `corehttp.AuthenticatorFailure` is published, so a guard of an
  embedder's own tells the two apart the same way.

## Rejected

- Answering every failure 503: a broken query is a fault of the server,
  and reporting it as a passing outage hides it from whoever watches the
  5xx.
- Treating every error that is not unauthorized as a failure: an
  authenticator that returns a bare error for a wrong password would
  answer 500 to it.
- Retrying the authenticator in the guard: a request held while the
  database is out holds a connection the pool is short of.

# ADR 0267 — A session can be closed alone

**Summary:** Every admin sign-in writes a session row its token names, the
verification refuses a token whose row was closed, and a person lists their
open sessions and closes one or all the others; logging out everywhere and a
password change still move the anchor.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

A session token carried no state. Logging out moved the identity's anchor and
dropped every token signed before it, and the known limits said no endpoint
closed a single device: an operator whose laptop was left signed in somewhere
could only close every session, the one in hand included, and had no way to
see how many there were.

## Decision

A sign-in writes an `auth_session` row and signs a token whose `jti` names it,
and the verification refuses a token whose row is missing, another person's
or closed. `GET /admin/v1/auth/sessions` lists the caller's open sessions, and
two identity-only endpoints close one or every other.

## Consequences

- Every admin request reads its session row beside the user and the anchor.
- The listing applies the anchor with the verification's own rule, so a
  logout or a password change empties it without closing rows one by one.
- A token signed before this change names no row and is judged by the anchor
  alone until it expires, at most one token lifetime.
- `corehttp.Principal` carries `SessionID`, which is how an endpoint tells the
  caller's own session from their others; it is empty for a key.
- A person's expired rows are forgotten at their next sign-in, so no job
  sweeps the table; a person who never signs in again keeps theirs until the
  account is deleted.
- A session is shown by when it began and ends, not by a device; the known
  limit now says so.
- Rolling migration 000005 back forgets which sessions were closed alone.

## Rejected

- A deny list of closed token ids: the same read per request, and no list of
  what is open to show the person.
- Closing rows when the anchor moves: four statements move it, and a fifth
  added later would leave the listing and the verification disagreeing.
- Naming the device from the request: the sign-in reaches the service through
  the panel and the API alike, and the panel may not pass a module's context.

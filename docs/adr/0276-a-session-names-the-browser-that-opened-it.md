# ADR 0276 — A session names the browser that opened it

**Summary:** An admin sign-in keeps the browser's `User-Agent` on its session
row, and the API's and the panel's session listings show it beside when the
session began and ends.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

Since ADR 0267 a person lists their open sessions and closes one, and since
ADR 0268 the panel does it on a screen, but the known limits said a session is
shown by when it began and ends and names no device. A person closing the
session a lost laptop holds had to guess which of three sign-ins from the same
afternoon it was. ADR 0267 rejected naming the device because the sign-in
reaches the service through the panel and the API alike, and the panel may not
pass a module's context; both doors hold the request, and each can hand the
service a string.

## Decision

`Login` takes the request's `User-Agent`, and the session row keeps it trimmed,
without control characters and cut at 512 bytes on a character. The API lists
it as `user_agent` and the panel's Sessions screen as a Browser column that
says "not recorded" for a session opened before it was kept.

## Consequences

- A person tells their laptop from their phone by the label their own browser
  gave, and closes the one they mean.
- The label is the browser's own word and nothing decides on it: a client can
  send any string, and the screen prints it escaped as text.
- The column is personal data of the staff member, declared `Named` and kept on
  erasure with the rest of the session row.
- A key's request opens no session, so nothing about keys changes.
- A session opened before migration 000006 has an empty label until it expires,
  at most one token lifetime.
- Migration 000006 of the auth module adds the column and a CHECK on its
  length; rolling it back forgets every label and keeps the sessions.
- `adminui.Session.Login` and the auth API's `Auth.Login` take the argument, so
  a fake of either gains it.

## Rejected

- The client's address: the rate limiter resolves it through
  `TRUSTED_PROXY_HOPS`, the auth module would need that resolution handed
  to it, and an address names a network rather than the device a person is
  looking for.
- A place derived from the address: it needs a geolocation database gobit does
  not ship, and it is wrong behind every mobile carrier and VPN.
- Parsing the label into a browser and a system: a table of user agents to keep
  current for a label the person already recognizes as written.

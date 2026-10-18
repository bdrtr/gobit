# ADR 0455 — A secret key's privileges are revised in place

**Summary:** `PUT /admin/v1/api-keys/{id}/scopes` writes a live secret key's privileges under `admin` only while they are, as a set, the ones the caller read, and its secret stays the same.
It costs a leaked key's holder whatever is added to it, and buys a privilege added to a deployed key without minting and redeploying a secret.

- **Status:** Accepted
- **Date:** 2026-10-17

## Context

depremtek's backend, a consumer, reaches gobit's admin surface with one
secret key (D310). A key's privileges were fixed at creation, so each slice
that needed one more privilege meant minting a key, redeploying the secret
and revoking the old one; and narrowing a key was the same work, so a key
kept privileges nothing used. A user's privileges were already revised in
place (ADR 0347), and a key's are read on every request.

## Decision

`PUT /admin/v1/api-keys/{id}/scopes` takes the privileges read and the new
ones and writes them, under `admin`, while the key is a live secret key
holding, as a set, those read, and refuses with 409
`auth_api_key_scopes_revised` otherwise. The new set holds at least one
privilege and none the caller lacks, as on creation.

## Consequences

- Every copy of the secret holds the new privileges from its next request;
  narrowing a key needs no redeploy either.
- A publishable key is refused (`auth_api_key_type_mismatch`), and a revoked
  one is not revised (409 `auth_api_key_revoked`).
- The write is an admin write: the audit log names the caller and the key,
  and the service logs the set before and after.
- Rotating the secret is still minting a key and revoking the old one.

## Rejected

- Keeping keys immutable: narrowing a key would stay a redeploy, so keys would keep privileges nothing uses.
- Writing the list sent over whatever the key holds: two operators would overwrite each other, which ADR 0347 refused for users.
- An `auth:write` privilege for it: whoever writes privilege can grant themselves admin.

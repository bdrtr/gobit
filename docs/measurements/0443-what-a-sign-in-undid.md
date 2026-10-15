# What a sign-in undid — measured 2026-10-15

Evidence for [ADR 0443](../adr/0443-a-credential-is-replaced-only-over-the-one-read.md)
and gaps D297 and D298.

## 1. The consumer's reproduction (D297)

depremtek's backend carries Medusa passwords over: it keeps the old
scrypt-kdf hash, writes a placeholder credential, and at the customer's
first sign-in checks the old hash and writes an argon2id one. Its security
review (commit 449124d, `inceleme-migrate-3.md`) started that sign-in and an
operator's `PUT /admin/v1/customer-credentials/{customer_id}` together, at
offsets of 0 to 130 ms. At a 0 ms offset the operator's password was refused
afterwards (401) and the Medusa password signed in (204): the embedder's
`Put`, about 70 ms after its read because of the scrypt check, wrote the old
password back. The same `Put` replaced a password the customer held under
another address with the embedder's placeholder.

## 2. The module's own window (D298)

The first draft's review (`inceleme-0442.md`, P01) ran gobit's own routes in
the unit lane: a sign-in with the old password, with a reset requested and
confirmed during its argon2id check. Afterwards the old password was refused
and its cookie answered `GET /store/v1/auth/session` with 200. In the
integration lane a swap placed between the reset's anchor and its password
returned true, and the session issued after it survived the reset.

## 3. After

- `TestASignInIsDatedAtItsRead` (unit): the store moves the anchor the moment
  after the sign-in's read; the cookie answers 401. Issued at its own time it
  answered 200.
- `TestACredentialIsReplacedOnlyOverTheOneRead` (integration): an empty
  expectation writes into an empty slot and not over a credential; a mixed-case
  address read is folded; a `Put` landing between the read and the swap stands;
  a proven address change landing there stands; another customer's address is
  refused.
- `TestAReplacedPasswordMovesTheAnchorInTheSameWrite` (integration): the hash
  and the anchor change in one row write, and an earlier moment never moves
  the anchor back.
- `TestTwoSwapsOverOneReadWriteOnce` (integration): of eight writers that
  read one credential, one wins.
- `TestAStoreThatCannotSwapIsSaidSo` (unit): `ErrNotRegistered` before
  `Register`, `ErrSwapUnsupported` for a store that cannot swap.

# ADR 0257 — A secret is compared in constant time

**Summary:** A file that imports a MAC, digest, derived-key or randomness
package compares no two values with `==` or a byte-by-byte call unless the
comparison is listed as holding no secret.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

Seven comparisons in the tree check a secret: a TOTP code, an argon2id derived
key, two cookie MACs, a token digest and two signed callbacks. Each used
`hmac.Equal` or `subtle.ConstantTimeCompare` because somebody wrote it that way.
A mutation that turned the TOTP comparison into `==` broke no test, and none
could observe timing without being the flakiest test in the suite. A scan by
operand name reached one of the seven: the TOTP check compares `expected` with
`typed` and the token check `a` with `b`.

## Decision

In every Go file that imports `crypto/hmac`, `crypto/subtle`, a SHA package,
`crypto/rand` or argon2, a comparison of two values with `==`, `!=` or a
byte-by-byte call fails `internal/arch` unless it is listed, by file and
source text, as comparing no secret. A literal, nil, a boolean or a length on
either side is not such a comparison.

## Consequences

- Turning any of the seven into `==` or `bytes.Equal` fails the gate, which
  was tried on five of them; the gate also fails if the import-derived
  population stops reaching any of their files.
- Forty-six files are in the population today, and twelve comparisons in eight
  of them are listed as holding no secret: enum and constant checks, a length,
  URL schemes, an argon2 version and two request fingerprints. Each carries its
  reason, and a listed comparison that disappears fails the gate too.
- The check is syntactic and reads no types, as the other scans in
  `internal/arch` do.
- A secret compared in a file that imports none of those packages, such as one
  read from storage and compared where it is looked up, is not seen. The known
  limit now names that.
- Timing itself is still observed by no test; the gate holds the code's shape.

## Rejected

- Matching operand names against secret-sounding words: it reached one of the
  seven comparisons and flagged three comparisons of constants and one of
  configuration.
- Type-checking the tree to find byte and string comparisons: `internal/arch`
  stays syntactic so that its scans do not depend on the build graph.
- A list of the seven sites alone: a new MAC or digest check would arrive
  unchecked, and the import-derived population catches it where it is written.

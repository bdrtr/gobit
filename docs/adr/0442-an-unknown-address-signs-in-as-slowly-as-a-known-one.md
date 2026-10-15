# ADR 0442 — An unknown address signs in as slowly as a known one

**Summary:** `contrib/identity-session` answers an address with no account in a known address's time: its sign-in checks a decoy argon2id hash, and its reset request answers before it records and sends the link.
It costs an argon2id check per unknown sign-in and a reset whose failed mail is logged rather than answered, and buys the end of the timing oracle those two routes were.

- **Status:** Accepted
- **Date:** 2026-10-15
- **Amends:** [0127](0127-a-working-identity-ships-outside-the-module.md), whose sign-in answered an unknown address before any hashing, and [0373](0373-a-shopper-resets-a-forgotten-password.md), whose reset request answered a known address after its mail

Measurement: [measurements/0442](../measurements/0442-what-a-wrong-password-took.md)

## Context

`POST /store/v1/auth/sign-in` and `POST /store/v1/auth/password-reset` give
one answer for an address with an account and one without, and the module
calls telling them apart "the oracle this module refuses to be". The answer
was the same but its time was not. The sign-in ran an argon2id check for a
known address and returned after a primary-key read for an unknown one. The
reset request answered an unknown address after the read, and a known one
after minting, recording and sending its link. Through a storefront key both
gaps read address by address which addresses have an account (D296). A
consumer's security review found the first in an embedder's route built the
same way, and the review of this record's first draft found the second.

## Decision

A sign-in whose address has no credential checks the password against a
decoy, an argon2id hash of a random secret drawn in `New` with the
parameters `HashPassword` writes. A reset request answers 202 after its read
for either address, and records and sends a known address's link after the
answer, bounded by the notice timeout.

## Consequences

- Measured after, the sign-in answers an unknown and a known address in one time, and so does the reset request once the work after a previous request has settled.
- A link that cannot be recorded or sent is logged: an error answer would say the address has an account. The person asks again.
- The reset's work after its answer shares the connection pool with whatever request comes next, so requests to known addresses sent back to back answer slower than unknown ones; a test pins that the answer does not wait for the mail, and the module's shutdown drains the work within its budget.
- The decoy has the parameters this module writes. A row written through `Credentials().Put` with other argon2id parameters is checked at its own cost and still answers apart.
- A test pins the decoy's parameters to `HashPassword`'s and counts the check; a cheaper decoy and a dropped check both fail.

## Rejected

- A fixed sleep for an unknown address: it guesses the check's time on every machine and drifts when the parameters change.
- A constant decoy hash in the source: it would have to follow every parameter change by hand.
- Answering the reset after a decoy mail: a shop cannot send mail to an address nobody owns.
- Leaving it to the rate limit: the limit slows guessing passwords, not reading one answer's time per address.

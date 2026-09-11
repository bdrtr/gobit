# ADR 0133 — A shopper opens their own account

**Summary:** `contrib/identity-session` ships storefront self-registration behind a
proven address: two rate-limited endpoints, a pending row that is not an account,
and a single-use link. It costs two seams the installation binds, and buys a shop
where a person signs up without an operator.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The module could sign somebody in and let an operator write a credential, and a
shopper could not open an account — so ADR 0127's "a real shop's starter stays
correct" held everywhere except the first thing a shopper does.

Three things stood between: an address has to be proven, the endpoint has to be
bounded because one request makes the shop send mail to an address a stranger
chose, and somebody has to decide who may create a customer. The third is not this
module's: a customer record belongs to whatever owns customers, and gobit does not
produce identity (ADR 0008/0043). The customer module's cross-module surface offers
`RegisterGuestCustomer`, whose own record says an existing address "is NOT an
obstacle" — it always creates — right for a guest checkout and wrong for a sign-up.

## Decision

`POST /store/v1/auth/register` records a pending registration and sends a single-use
link; `POST /store/v1/auth/register/verify` proves the address, opens the account
through seams the installation binds, writes the credential and signs the person in.
Both are rate limited and both are mounted only when those seams are bound.

## Consequences

Registration creates NOTHING about the person: one row in this module's own table
holding the address, the password as an argon2id hash, and the hash of the token. A
customer per address typed into a form lets anybody fill the shop's table.

It answers the same 202 for an address that already has an account, because anything
else answers "does this person shop here" for any address a caller tries. What
differs is the message, which is why `Verification` has a second method: answering
the same thing and doing nothing leaves somebody who forgot their account staring at
a form that appeared to work.

The token is consumed by `DELETE … RETURNING`, one statement, so single-use needs no
lock and holds whatever the timing. It is spent BEFORE the account is opened,
because the other order leaves a replayable link.

An address that gained a customer between the two halves — a guest checkout while the
message sat in a mailbox — keeps that record rather than a second one.

The endpoints are ABSENT rather than failing when a seam is missing, and a typed nil
counts as missing: an interface wrapping a nil pointer is not nil, so a constructor
returning one mounted an endpoint that panicked on the first registration.

The default rate limit is in the process's own memory, so an installation behind
several instances gets that many times the bound until it binds a shared one. Said
in the published description, because a limit that is a fraction of itself still
looks like one.

The pending table is personal data, declared and erased as such — an unfinished
sign-up holds somebody's address and password hash (ADR 0132) — and reached by
address only, because it has no customer id.

Measurement: [measurements/0133](../measurements/0133-proving-an-address.md)

## Rejected

**Creating the customer at registration.** Anybody could fill the customer table with
addresses that are not theirs, and every row would look like a shopper to every
report that counts them.

**A new write on the customer module's interop.** Its own record says every method
there is a contract that can never change again; the installation binds a seam
instead, and one with its own users table binds something else.

**Telling the caller the address is taken.** It answers, for any address, whether
that person shops here.

**Deleting the pending row last.** It leaves a link that can set a changed password
back.

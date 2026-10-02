# ADR 0377 — A shopper moves their account by proving the new address

**Summary:** `contrib/identity-session` mounts `POST /store/v1/auth/email` and
`.../confirm`: given the current password, a link goes to the new address, and
following it moves the shop's record and then the credential there.

- **Status:** Accepted
- **Date:** 2026-10-03

## Context

ADR 0376 took the e-mail address out of the storefront's profile update,
because nothing there proved a shopper owned a new one, and left an operator as
the only way to change it. The address lives in two places: the shop's customer
record, which the shop mails, and identity-session's credential, which the
shopper signs in under.

## Decision

A request whose session proves a customer gives the current password and a new
address, and a single-use link is sent there; following the link moves the
customer's record through a new `AddressChanges` capability of `Accounts`, then
the credential. Nothing moves until the link is followed, and an address another
account holds is sent nothing.

## Consequences

- The answer is 202 whether the new address is free or another account's, so
  the endpoint tells no account holder which addresses have accounts.
- The current password is asked for, as a password change asks for it.
- The confirmation needs no session, signs nobody in and nobody out: the link
  proves the address, and the password did not change.
- Another account that took the address after the link was sent is told apart
  before anything is written, and one that takes it at the write answers 409
  too, from the customer module's conflict.
- The record moves before the credential, so a failure between the two leaves
  the person signing in where they always did.
- Migration 000005 adds `customer_address_changes`, cascading from the
  credential. It is personal data: an erasure takes the person's own pending
  change with their credential and any change that would move somebody else's
  account to the person's address by that address, and a dossier shows both.
- The customer module gains `ChangeCustomerEmail`, a primitive method on its
  cross-module surface; the starter binds it and logs the link.
- The three flows that mail a link share one quota per client.

## Rejected

- Writing the new address at request time and proving it after: the account
  would be mailed at an address nobody proved.
- Telling the shopper that an address is taken: an enumeration oracle for
  anybody who has an account.
- Signing in on the link, as a reset does: the link proves an address, not the
  person.

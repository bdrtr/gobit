# ADR 0115 — The shop says who it is

**Summary:** A settings module holds ONE store profile — the identity printed on
the documents the shop issues — and the invoicing flow reads the seller from it
instead of from its caller. It costs a required setup step and buys documents
that cannot disagree about who issued them.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

The invoicing flow took both parties from its CALLER, and its own godoc said why
the seller was there: "the seller's legal details are the shop's own
configuration, which lives in no module here."

Two things followed. Two documents issued from one shop could name two different
sellers, because nothing compared one request with the next. And the identity
printed on every invoice was the one thing an operator could not edit — the
person who can change a price, a product and an order had to ask for a redeploy
to correct the shop's own tax office.

The invoice module already treats the seller as data: it copies the party onto
the document at the moment of issue and declares those columns as personal data.
What was missing was the record they were copied FROM.

## Decision

A `settings` module holds one `store_profile` — legal name, tax number, tax
office, e-mail, address, country — written through `PUT /admin/v1/store-profile`.
The invoicing flow reads the seller from it, and the seller is no longer part of
any request.

## Consequences

Issuing before the profile is written is REFUSED, with a message naming the
endpoint. The operator issuing a shop's first invoice is exactly the person who
has not filled it in, and "the seller's name is required" from three layers down
would not tell them where to go.

The admin body loses `seller`. That narrows a published surface, and it is the
point rather than a side effect: a field a caller could set is a field two
callers could set differently.

PUT and not PATCH. The record is an identity, and a partial write would let a
shop carry a legal name from one edit beside a tax number from another.

There is ONE profile per installation. ADR 0009 puts multi-tenancy at the
installation boundary, so a second row would be a second answer to "who is
selling" and every reader would need a rule for choosing between them.

The module declares its columns as PERSONAL DATA and implements no eraser. A
sole trader's shop is a person, so the names are declared; but a data subject who
asks to be forgotten is a CUSTOMER, and erasing the controller's own identity
would empty the shop from its own installation while the issued documents go on
printing it.

The e-mail is folded NOWHERE, and the exemption says so with its cost. Nothing
compares this address — it is printed, never matched — and if a reader that
matches on it is ever added, the match will be case-sensitive and the exemption
has to go rather than grow a caveat.

Only what is PRINTED is stored. A settings module is one unread key away from
being a bag, and a bag is a table nobody can answer a question about.

## Rejected

**A field on `internal/app.Options`.** It puts the shop's identity in the
binary's setup, which is what made it un-editable in the first place; ADR 0111
refused the same shape for a cart's vocabulary.

**A generic settings table keyed by name.** Nothing could validate a key nobody
declared, two installations would spell the same setting two ways, and no reader
could rely on any of it.

**Keeping `seller` in the request as a fallback.** Two sources for one fact, with
the wrong one winning silently whenever a caller still sends it.

**Seeding a placeholder row.** A guessed name on a real invoice is the one thing
a document must never carry.

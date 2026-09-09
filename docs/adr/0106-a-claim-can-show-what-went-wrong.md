# ADR 0106 — A claim can show what went wrong

**Summary:** A claim can carry uploaded files as evidence, bound by the upload's
id alone. It costs a table and a resolve through the file module at read time,
and it buys an operator who looks at the damage instead of reading about it.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

`order_claims` held a reason and a note, so "the box arrived crushed" was a
sentence and never a picture. An operator deciding whether to replace goods was
reading a description of evidence rather than the evidence, and the photographs
the customer had sent lived in a mailbox.

The file module has stored uploads since ADR 0021 and the catalog has bound them
to products since migration 000002. Nothing bound one to a claim.

Two shapes were possible for that binding, and `product_image` had already
chosen the other one: it carries the upload's id AND its address, so a storefront
page renders without asking. A claim's evidence is not read that way.

## Decision

`order_claim_evidence` binds a claim to uploads: several rows or none, unique on
the pair, hard-deleted when detached because the row is a binding and not a
record of something that happened.

It stores the upload's ID and no address. The address is asked of the file module
at the moment it is needed.

## Consequences

Reading evidence costs a resolve through the file module. That is the price of
the record staying true: an object store's address is signed and expires, a claim
is opened months after it was filed, and a stored address would be right about
the file and wrong about where to get it. Nothing here renders on a hot path —
one operator, one claim at a time — so the resolve is affordable in a way
`product_image`'s would not be.

The upload id is NOT verified. This module cannot ask another one whether a
record exists (Principle 2.2), so an id naming no file is stored and shows up as
evidence that does not resolve. What is refused is the empty string.

Binding the same file to one claim twice is a conflict rather than a second piece
of evidence; the same file may be evidence on two claims, because the uniqueness
is of the pair.

The order module's error classifier gained a second foreign-key suffix.
`order_claim_evidence_order_claim_id_fkey` does not end in `_order_id_fkey`, and
without the new suffix a claim that vanished between the lookup and the insert
answered 500 instead of 404.

The delete endpoint is the module's first answering `204`, which the endpoint
shape table could not describe: every row named a record, so a body-less response
had to claim one. The table now takes a row with no response.

## Rejected

**A column on `order_claims`.** A damaged parcel is rarely one photograph, and a
column could hold exactly one — the second would be a reason to overwrite the
first.

**Carrying the address beside the id, as `product_image` does.** It buys a render
with no resolve, and pays for it with a record whose half rots.

**A foreign key to the file module's table.** Banned by Principle 2.2; the two
modules must stay separable.

**An index on `upload_id`.** Nothing reads "which claims use this upload"; an
index with no reader costs every write and advertises a use that does not exist.

**Soft-deleting a detached binding.** The row never said something happened. A
file attached to the wrong claim should leave no trace.

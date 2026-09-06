-- Erasure queries: finding a PERSON's carts and rewriting what identifies them.
--
-- # Why these three statements are one file and not three
--
-- Every other file here is named after a table, and these statements touch two
-- (carts and cart_addresses). They are kept together because they are ONE
-- ANSWER: what this module holds about a person and what it rewrites when asked
-- to forget them. Somebody answering a data-subject request has to read the
-- whole of that answer, and splitting it across the table files would hide the
-- fact that forgetting a shopper means writing to two tables in one
-- transaction. The service that runs them is service/erasure.go, which also
-- carries the argument for anonymizing rather than DELETING the cart, and the
-- declaration of every column involved is cart.Module.PersonalData.
--
-- # What is NOT here
--
-- No statement touches a free-form column: carts.metadata,
-- cart_line_items.metadata, cart_addresses.metadata and cart_shipping_methods.data
-- are left exactly as the caller wrote them. ADR 0029 puts the judgement of
-- whether such a field holds personal data in a given deployment with the
-- CONTROLLER, not with the framework, and a framework that rewrote a shop's own
-- notes would have taken that judgement back. What the module owes instead is
-- to SAY so, which the Erase result does through erasure.Result.Kept.
--
-- No statement touches revision either, and that is not an omission. The
-- counter is the cart's SHAPE, incremented by everything that changes what is
-- in the cart; bumping it here would make the totals look stale
-- (totals_revision < revision) and MarkCompleted would then refuse to close a
-- cart whose checkout is already past its payment. Forgetting the shopper does
-- not change what is in the basket.

-- ListCartsForErasure returns the identifiers of the person's carts and LOCKS
-- them.
--
-- # Why the ids are read before anything is written
--
-- The second statement finds its rows THROUGH the cart, and the first statement
-- destroys the handle it would have used: once carts.email is NULL a guest's
-- carts can no longer be found by e-mail, so a query that re-derived the set
-- would update the header and then miss the address in the same transaction.
-- Reading the ids once and passing them to both writes is what keeps the two
-- tables in step.
--
-- # Why the rows are locked
--
-- Every flow in this module that changes a cart starts by taking the cart's row
-- lock, and this one changes carts (see the service package doc on the lock
-- order: the cart first, its children after). Without the lock a line addition
-- or a completion could land between the moment this query reads the cart and
-- the moment the anonymizing statements write.
--
-- ORDER BY id is not for the reader, who does not care in which order the
-- person's carts come back; it is the lock order. Two sweeps running at once
-- take their locks in the same sequence, so no cycle can form.
--
-- # Why soft-deleted and completed carts are NOT filtered out
--
-- This is the one read in the module that omits `deleted_at IS NULL`, and it is
-- also the one that ignores completed_at. Every other query filters the first
-- because a soft-deleted cart is not part of the business any more, and every
-- write refuses the second because a completed cart is the record an order
-- rests on. Here the question is neither: it is what the DATABASE STILL HOLDS
-- about a person, and a hidden or closed row holds an address exactly as an
-- open one does. What the immutability of a completed cart protects is its
-- SHAPE and its amounts — the lines, the totals and the counters, none of which
-- these statements touch; the buyer's name was never part of what made the cart
-- a record of the sale, and where the law needs the buyer named the invoice
-- keeps them (ADR 0032).
--
-- # Why either identifier finds a row
--
-- The two handles are OR-ed rather than AND-ed. A guest cart carries an e-mail
-- and a NULL customer_id, a cart opened for a signed-in shopper may carry the
-- customer id and no e-mail, and the same person may have both; requiring both
-- would find neither. A subject with no identifier at all matches nothing here,
-- and the service refuses it before the query is reached — erasing "everyone"
-- is not an erasure request.
-- name: ListCartsForErasure :many
SELECT id
FROM carts
WHERE (sqlc.narg('customer_id')::text IS NOT NULL AND customer_id = sqlc.narg('customer_id')::text)
   OR (sqlc.narg('email')::text IS NOT NULL AND email = sqlc.narg('email')::text)
ORDER BY id
FOR UPDATE;

-- AnonymizeCartContacts drops the person out of the cart headers.
--
-- Only the e-mail goes. customer_id STAYS, and that is a decision rather than
-- an oversight: it is the handle a repeated sweep finds these rows by
-- (carts_customer_idx), so nulling it would leave the second call — the one the
-- idempotence rule requires to answer the same thing — searching for a cart it
-- has already anonymized. The id reaches the person only through the customer
-- module's record, and that module answers the same request on its own account;
-- the cart result names the column in erasure.Result.Kept so the controller is
-- told rather than left to assume.
--
-- The statement takes an ARRAY rather than one id: a shopper who has opened a
-- cart on every visit for a year is a round trip per cart otherwise, inside a
-- transaction that is holding a lock on every one of those rows.
--
-- There is no guard skipping the rows that are already NULL. Skipping them
-- would make the affected-row count shrink on the second call, and that count
-- is what the report tells the controller; the write is idempotent in its
-- VALUES, so doing it again costs one row rewrite and keeps the answer stable.
-- name: AnonymizeCartContacts :execrows
UPDATE carts
SET email      = NULL,
    updated_at = now()
WHERE id = ANY (sqlc.arg('cart_ids')::text[]);

-- AnonymizeCartAddresses empties the address of the cart without deleting it.
--
-- # Why the row survives
--
-- An ABSENT address already means something in this module: a cart that has not
-- reached the address step of the checkout has none, and the unique index
-- allows at most one living address of each type. Deleting the row would
-- therefore make "this cart's address was erased" unreadable as anything but
-- "this cart never got that far", and the two are different facts about a
-- session.
--
-- address_type and country_code stay for the same reason: they say what the
-- address was for and roughly where the parcel would have gone, which is what
-- keeps the surviving row readable AS an address, and neither identifies a
-- person on its own. The country is jurisdiction, not identity.
--
-- # Why source_address_id goes
--
-- It is a pointer INTO the person's address book in the customer module — the
-- one column here that reaches back at the person's own record rather than
-- describing this session. Its single reader in this repository was measured
-- rather than assumed (2026-09-07): service.Interop's cart snapshot carries it
-- to the checkout saga, which hands it to the order module so an order can
-- record where its address came from. That reader cannot want it after an
-- erasure — a snapshot whose name, street and phone are NULL is not a snapshot
-- an order can be placed from — so nulling it costs nothing and removes a
-- handle back to the person.
--
-- metadata is NOT touched, for the reason stated at the top of this file.
-- name: AnonymizeCartAddresses :execrows
UPDATE cart_addresses
SET source_address_id = NULL,
    first_name        = NULL,
    last_name         = NULL,
    company           = NULL,
    address_1         = NULL,
    address_2         = NULL,
    city              = NULL,
    province          = NULL,
    postal_code       = NULL,
    phone             = NULL,
    updated_at        = now()
WHERE cart_id = ANY (sqlc.arg('cart_ids')::text[]);

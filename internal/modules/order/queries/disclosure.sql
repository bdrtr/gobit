-- Disclosure queries: showing a PERSON everything this module holds about her.
--
-- # Why this is a second file beside erasure.sql
--
-- The two are the same resolution pointed at opposite verbs. queries/erasure.sql
-- finds the person's orders in order to REWRITE them; these statements find the
-- same rows in order to READ them, and they are kept apart for one reason that
-- is visible in every line below: not one of them takes a lock, none of them
-- writes, and none of them may ever grow a statement that does. A disclosure
-- that locked the rows it reports would make answering a person's question
-- block the shop's own checkout, and a file that mixes the two verbs is a file
-- where the next statement is added on the wrong side of that line.
--
-- The service that runs them is service/disclosure.go and the declaration of
-- every column they carry is order.Module.PersonalData.
--
-- # Why they select the whole row
--
-- The disclosure emits ONLY the declared columns, and the filtering happens in
-- Go, where the field list is derived from the declaration itself
-- (service.PersonalDataHoldings). Narrowing the SELECT to the declared columns
-- as well would look tidier and would buy nothing: it would be a SECOND copy of
-- the declaration, written in SQL where no test can compare it with the first,
-- and the day a column was added to one of them the other would go on answering
-- as if the column did not exist. What the wide SELECT costs is a few unread
-- columns per row; what it saves is that list.
--
-- # Why soft-deleted rows are NOT filtered out
--
-- The same reason queries/erasure.sql gives, read in the other direction. The
-- question a data subject asks is what the database still HOLDS about her, not
-- what the shop's own screens can still see, and a soft-deleted order holds her
-- address exactly as a live one does. Answering "we hold nothing" while a hidden
-- row carried her name would be the same false report the erasure contract warns
-- about — with the difference that here she is the one being told.
--
-- # Why the after-sales rows are read at all
--
-- Their declared columns are free text somebody typed: the reason a parcel came
-- back, the note an operator wrote about what the customer said on the phone. It
-- is exactly the material a person is entitled to see, and it is exactly the
-- material gobit never inspects (ADR 0029 leaves that judgement with the
-- controller). The disclosure therefore carries it out with its Kind attached
-- rather than deciding what is in it.

-- ListOrdersForDisclosure returns every order that carries the person's
-- customer id or e-mail address, soft-deleted ones included.
--
-- # Why either identifier finds a row
--
-- The two handles are OR-ed, as they are in ListOrdersForErasure, and for the
-- same measured reason: a guest order carries an e-mail with a NULL customer_id
-- and an order opened by administration may carry a customer id and no e-mail,
-- so requiring both would find neither. A subject with no identifier at all
-- matches nothing here, and the service refuses it before the query is reached —
-- disclosing "everyone" is not a subject-access request, it is a data breach.
--
-- # What this query cannot reach, and why it is said out loud
--
-- An order that was already anonymized no longer has an e-mail. For a GUEST
-- order that address was the only handle, so such a row can never be found
-- again by anybody, and no statement here can change that. The service says so
-- in the Why of its answer rather than reporting a confident "nothing found":
-- see service/disclosure.go.
--
-- ORDER BY id is chronological as well as stable — the identifier is
-- time-sortable (models.Order.ID) — and it is the SAME order queries/erasure.sql
-- returns, so the two answers a controller may hold about one person list her
-- orders in one sequence rather than in two.
--
-- There is no FOR UPDATE and there must not be one. The lock in the erasure
-- query protects the gap between reading a status and writing on the strength of
-- it; nothing is written here, so a lock would buy nothing and would make a
-- person's question wait behind — and hold up — the shop's own transactions.
-- name: ListOrdersForDisclosure :many
SELECT * FROM orders
WHERE (sqlc.narg('customer_id')::text IS NOT NULL AND customer_id = sqlc.narg('customer_id')::text)
   OR (sqlc.narg('email')::text IS NOT NULL AND email = sqlc.narg('email')::text)
ORDER BY id;

-- ListOrderLineItemsForDisclosure reads the lines of the given orders.
--
-- The line's only declared holding is its metadata — what was sold is a fact
-- about the sale and not about the buyer — and that one column is where a shop
-- writes the engraving, the personalisation or the gift message the customer
-- typed. A dossier that left it out would leave out the one part of the order
-- the person wrote herself.
--
-- The statement takes an ARRAY of order ids rather than one: a person with two
-- hundred orders is two hundred round trips otherwise, which is the same reason
-- LineItemsByIDs and OrderAddressesByOrderIDs exist.
-- name: ListOrderLineItemsForDisclosure :many
SELECT * FROM order_line_items
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, created_at, id;

-- ListOrderReturnsForDisclosure reads the return records of the given orders.
--
-- reason and note are free text and metadata is the caller's own document; all
-- three are declared and all three are disclosed with their Kind, which is what
-- tells the controller that gobit never read them.
-- name: ListOrderReturnsForDisclosure :many
SELECT * FROM order_returns
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, created_at, id;

-- ListOrderExchangesForDisclosure reads the exchange records of the given
-- orders.
-- name: ListOrderExchangesForDisclosure :many
SELECT * FROM order_exchanges
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, created_at, id;

-- ListOrderClaimsForDisclosure reads the damage and shortage records of the
-- given orders.
-- name: ListOrderClaimsForDisclosure :many
SELECT * FROM order_claims
WHERE order_id = ANY (sqlc.arg('order_ids')::text[])
ORDER BY order_id, created_at, id;

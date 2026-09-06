-- Erasure queries: finding a PERSON's orders and rewriting what identifies them.
--
-- # Why these three statements are one file and not three
--
-- Every other file here is named after a table, and these statements touch
-- three (orders, order_addresses, and — through the settlement facts — the
-- summary and the after-sales records). They are kept together because they are
-- ONE ANSWER: what this module holds about a person, and what it rewrites when
-- asked to forget them. Somebody answering a data-subject request has to read
-- the whole of that answer, and splitting it across the table files would hide
-- the fact that erasing an order means writing to two tables in one
-- transaction. The service that runs them is service/erasure.go and the
-- declaration of every column involved is order.Module.PersonalData.
--
-- # What is NOT here
--
-- No statement touches a free-form column: orders.metadata,
-- orders.cancel_reason, orders.idempotency_key, order_line_items.metadata,
-- order_addresses.metadata and the reason/note/metadata columns of the three
-- after-sales records are left exactly as the caller wrote them. ADR 0029 puts
-- the judgement of whether such a field holds personal data
-- in a given deployment with the CONTROLLER, not with the framework, and a
-- framework that rewrote a shop's own notes would have taken that judgement
-- back. What the module owes instead is to SAY so, which the Erase result does
-- through erasure.Result.Kept.

-- ListOrdersForErasure returns the person's orders together with the facts that
-- decide whether each one can be forgotten yet.
--
-- # Why the facts are computed here and not read one by one
--
-- Four questions decide whether an order is settled — its status, whether money
-- is still outstanding on it, and whether a return, an exchange or a claim is
-- still in its requested state — and every one of them lives in a different
-- table. Asking them per order would be four extra round trips per order for a
-- person who may have hundreds; asking them here makes the whole decision ONE
-- query whose cost grows with the person's orders and not with the product of
-- the two.
--
-- The money is carried up as the THREE RAW NUMBERS — the order total and the
-- summary's two lifetime amounts — and not as one subtracted figure. The
-- subtraction models.OrderSummary.Outstanding performs, total - (paid -
-- refunded), answers "how much of this sale is still unpaid", which is the
-- right question for a payment screen and the wrong one here: paid_total never
-- shrinks (order_summaries.sql merges it with GREATEST so that unordered,
-- at-least-once payment events converge), so a FULLY REFUNDED order keeps that
-- figure at its whole total for ever and would be refused erasure for ever.
-- What decides settlement is models.OrderErasureCandidate.Owed, and it needs
-- the numbers unsubtracted.
--
-- COALESCE and the LEFT JOIN are what keep an order without a summary readable
-- — a summary is born with the order, so today the row is always there, and a
-- LEFT JOIN costs nothing while an INNER JOIN would SILENTLY DROP an order
-- whose summary was lost and report the person as fully erased.
--
-- # Why the rows are locked
--
-- Every flow in this module that changes an order starts by locking it (see
-- service.Store), and this one changes orders. Without the lock a cancellation
-- or a completion could land between the moment this query reads the status and
-- the moment the anonymizing statement writes: the erasure would decide on a
-- status that no longer holds. FOR UPDATE OF o locks the order rows only —
-- order_summaries is on the nullable side of the join and cannot be locked
-- there, and it does not need to be, because the order lock already serializes
-- every writer of that order.
--
-- ORDER BY o.id is not for the reader, who does not care in which order the
-- person's orders come back; it is the lock order. Two sweeps running at once
-- for two people who share an order — which cannot happen today, since an order
-- has one buyer — would still take their locks in the same sequence, so no
-- cycle can form.
--
-- # Why soft-deleted orders are NOT filtered out
--
-- This is the one read in the module that deliberately omits `deleted_at IS
-- NULL`. Every other query filters it because a soft-deleted order is not part
-- of the business any more; here the question is not what the business can see
-- but what the DATABASE STILL HOLDS about a person, and a hidden row holds an
-- e-mail just as a visible one does. Reporting "anonymized" while a
-- soft-deleted row kept the address is exactly the false report the erasure
-- contract warns about.
--
-- # Why either identifier finds a row
--
-- The two handles are OR-ed rather than AND-ed. A guest order carries an e-mail
-- and a NULL customer_id, and an order opened by administration may carry a
-- customer id and no e-mail; requiring both would find neither. A subject with
-- no identifier at all matches nothing here, and the service refuses it before
-- the query is reached — erasing "everyone" is not an erasure request.
-- name: ListOrdersForErasure :many
SELECT o.id,
       o.display_id,
       o.status,
       o.currency_code,
       o.deleted_at,
       o.personal_data_erased_at,
       o.total,
       COALESCE(s.paid_total, 0)::bigint     AS paid_total,
       COALESCE(s.refunded_total, 0)::bigint AS refunded_total,
       EXISTS (
           SELECT 1 FROM order_returns r
           WHERE r.order_id = o.id AND r.deleted_at IS NULL AND r.status = 'requested'
       ) AS return_requested,
       EXISTS (
           SELECT 1 FROM order_exchanges e
           WHERE e.order_id = o.id AND e.deleted_at IS NULL AND e.status = 'requested'
       ) AS exchange_requested,
       EXISTS (
           SELECT 1 FROM order_claims c
           WHERE c.order_id = o.id AND c.deleted_at IS NULL AND c.status = 'requested'
       ) AS claim_requested
FROM orders o
LEFT JOIN order_summaries s ON s.order_id = o.id
WHERE (sqlc.narg('customer_id')::text IS NOT NULL AND o.customer_id = sqlc.narg('customer_id')::text)
   OR (sqlc.narg('email')::text IS NOT NULL AND o.email = sqlc.narg('email')::text)
ORDER BY o.id
FOR UPDATE OF o;

-- AnonymizeOrderContacts drops the person out of the order headers.
--
-- Only the e-mail goes. customer_id STAYS, and that is a decision rather than
-- an oversight: it is the only INDEXED handle this module has
-- (orders_customer_idx), so nulling it would make the second sweep — the one
-- the idempotence rule requires to answer the same thing — unable to find the
-- rows it already erased. The id identifies the person only through the
-- customer module's record, and that module answers the same request on its own
-- account; the order result names the column in erasure.Result.Kept so the
-- controller is told, rather than left to assume.
--
-- The stamp is written with COALESCE and not with now(): the FIRST erasure
-- keeps its moment, so re-running a sweep cannot make the record claim the
-- person was forgotten on the day of the re-run. The whole argument, and why
-- the column has no DEFAULT, is in migration 000009.
--
-- The statement takes an ARRAY rather than one id: a person with two hundred
-- orders is two hundred round trips otherwise, inside a transaction that is
-- holding a lock on every one of those rows.
--
-- There is no `WHERE personal_data_erased_at IS NULL` guard. Skipping the
-- already-erased rows would make the affected-row count shrink on the second
-- call, and that count is what the report tells the controller; the write is
-- idempotent in its VALUES, so doing it again costs one row rewrite and keeps
-- the answer stable.
-- name: AnonymizeOrderContacts :execrows
UPDATE orders
SET email                   = NULL,
    personal_data_erased_at = COALESCE(personal_data_erased_at, now()),
    updated_at              = now()
WHERE id = ANY (sqlc.arg('order_ids')::text[]);

-- AnonymizeOrderAddresses empties the address of the order without deleting it.
--
-- # Why the row survives
--
-- An ABSENT address already means something in this module: a shop selling a
-- download records none, and order_addresses.metadata's own migration says an
-- order may legitimately have neither. Deleting the row would therefore make
-- "this order's address was erased" unreadable as anything but "this order
-- never had one", and the two are different facts about a sale.
--
-- address_type and country_code stay for the same reason: they say a parcel
-- went somewhere and roughly where, which is what makes the surviving row
-- readable, and neither identifies a person on its own.
--
-- # Why source_address_id goes
--
-- It is a pointer INTO the person's address book in the customer module — the
-- one column here that reaches back at the person's own record rather than
-- describing this sale. Nothing in this repository READS it, and that was
-- measured across the tree rather than assumed (2026-09-06): it is written by
-- CreateOrderAddress out of the checkout snapshot and converted back into
-- models.OrderAddress, and from there no API DTO, no query provider and no
-- outbound interop surface carries it — the one interop struct that names the
-- field is interopAddress, which is the INBOUND snapshot PlaceOrderJSON parses.
-- Nulling it therefore costs no reader and removes a handle.
--
-- metadata is NOT touched, for the reason stated at the top of this file.
-- name: AnonymizeOrderAddresses :execrows
UPDATE order_addresses
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
WHERE order_id = ANY (sqlc.arg('order_ids')::text[]);

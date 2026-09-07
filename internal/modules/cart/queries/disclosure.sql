-- Disclosure queries: showing a PERSON everything this module holds about them.
--
-- # Why this is a second file next to erasure.sql
--
-- The two halves ask the SAME question of the SAME rows and answer it with
-- opposite verbs: erasure.sql finds the person's carts in order to rewrite
-- them, this file finds them in order to READ them. They are kept apart because
-- one of them writes and the other must be provably unable to: not a single
-- statement here is an UPDATE, none takes FOR UPDATE, and a reviewer asking
-- "can a disclosure change anything" reads one short file rather than picking
-- the reads out of a file whose top half rewrites two tables. What the two
-- files must NOT diverge on is the WHERE clause that resolves the person, and
-- the first statement below repeats erasure.sql's word for word for that
-- reason.
--
-- The service that runs these is service/disclosure.go, which carries the
-- argument for the bound and for what the answer says when it is hit; the
-- declaration every column here comes from is cart.Module.PersonalData.
--
-- # Why each SELECT list is written out instead of SELECT *
--
-- Every other read in this module selects the whole row and lets the
-- conversion layer decide what to carry up. Here the column list IS the
-- contract: a personaldata.Record may only hold the columns the module
-- DECLARED, because a disclosure that reached past the declaration would hand
-- the person data the declaration told the controller was not there. Selecting
-- exactly the declared columns puts that rule in the statement rather than in a
-- filter somewhere above it — the undeclared columns never leave the database —
-- and service/disclosure_internal_test.go reads these lists and requires them
-- to be, table by table, precisely the declared set.
--
-- The columns that are in a list WITHOUT being personal are id, cart_id and
-- carts.created_at. The first two are the row's identity and the tie back to
-- the cart, which is what lets a dossier be grouped by session; the third is
-- the cut-off the truncation notice quotes, and is read for that sentence
-- rather than reported as a holding.
--
-- # Why no statement filters deleted_at
--
-- For the reason erasure.sql gives for the same omission: the question is what
-- the database still HOLDS about a person, and a soft-deleted or completed cart
-- holds an address exactly as an open one does. A disclosure that hid the rows
-- every other read hides would tell a person "we do not have that" about data
-- the installation is still storing, which is the one answer this mechanism
-- exists to prevent.

-- ListCartsForDisclosure returns the person's carts, newest first, bounded.
--
-- # Why it is bounded at all
--
-- A cart is opened per shopping session, not per purchase, so a shop
-- accumulates far more carts than orders and a shopper of several years can
-- hold hundreds. The dossier is a single value assembled in memory and read by
-- a person; unbounded, one holder's part of it is decided by how often somebody
-- shopped. The bound is the caller's ($3) and the service passes
-- service.MaxDisclosedCarts.
--
-- What must not happen is a SILENT truncation, which would be worse than a
-- large document: the person cannot tell an omission from an absence. That is
-- what count(*) OVER () is for — it reports how many carts matched BEFORE the
-- LIMIT, in the same statement and therefore in the same snapshot, so the
-- service can say in the disclosure's Why exactly how many carts it did not
-- list. Two statements (a COUNT and a SELECT) would have answered the same
-- question from two snapshots, and this read deliberately opens no transaction.
--
-- # Why newest first
--
-- If a bound has to cut somewhere, the carts a person is most likely to be
-- asking about are the recent ones. The order is a total one because id is
-- unique, so the same request twice cuts at the same place rather than
-- returning two overlapping halves.
--
-- Neither carts_customer_idx nor carts_alive_idx can serve this query and that
-- is not an oversight: both are partial on deleted_at IS NULL, and this read
-- deliberately includes the soft-deleted rows. It is an admin-triggered request
-- answered once per data-subject request, and a sequential scan is the accepted
-- price of not lying about what is still stored.
--
-- # Why either identifier finds a row
--
-- The WHERE clause is erasure.sql's, word for word, and for the same reason: a
-- guest cart carries an e-mail and a NULL customer_id, a cart opened for a
-- signed-in shopper may carry the customer id and no e-mail, and the same
-- person often has both, so requiring both would find neither. If the two
-- clauses ever drift, the module will show a person less than it would erase
-- from them — which is the asymmetry this whole mechanism was built to close.
-- name: ListCartsForDisclosure :many
SELECT id,
       customer_id,
       email,
       metadata,
       created_at,
       count(*) OVER () AS total_carts
FROM carts
WHERE (sqlc.narg('customer_id')::text IS NOT NULL AND customer_id = sqlc.narg('customer_id')::text)
   OR (sqlc.narg('email')::text IS NOT NULL AND email = sqlc.narg('email')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('max_carts')::bigint;

-- ListCartAddressesForDisclosure returns every address of the given carts.
--
-- It takes the cart identifiers the statement above returned rather than
-- resolving the person again, and the reason is not only the round trip: after
-- an erasure a guest cart's e-mail is NULL, so a query that re-derived the set
-- from the subject would find the header and miss its address. Working from the
-- ids keeps the two tables answering about the same carts.
--
-- Every column of the declaration is here, country_code and metadata included,
-- and that is the difference between this and the anonymizing statement next
-- door: what the erasure LEAVES is still held, so a disclosure that skipped it
-- would be hiding data the erasure itself reports as kept.
--
-- The rows are ordered by cart and then by age so that a dossier reads as one
-- session after another; without it the addresses of a hundred carts would come
-- back interleaved in whatever order the scan produced.
-- name: ListCartAddressesForDisclosure :many
SELECT id,
       cart_id,
       source_address_id,
       first_name,
       last_name,
       company,
       address_1,
       address_2,
       city,
       province,
       postal_code,
       country_code,
       phone,
       metadata
FROM cart_addresses
WHERE cart_id = ANY (sqlc.arg('cart_ids')::text[])
ORDER BY cart_id, created_at, id;

-- ListCartLineItemNotesForDisclosure returns the lines that carry a note.
--
-- The one declared column of this table is the free-form metadata: what
-- somebody put in a basket is a fact about the basket, but the note typed
-- beside it — an engraving, a gift message, a name for a personalisation — is
-- the embedder's field and gobit does not look inside it. It is declared Open
-- for that reason and it is disclosed for the same one: a person asking what is
-- held about them is entitled to see the text somebody typed about them, and
-- the framework cannot decide on their behalf that it holds nothing.
--
-- The empty ones are filtered OUT in SQL rather than skipped in Go. The column
-- is NOT NULL DEFAULT '{}' and the overwhelming majority of lines never get a
-- note, so this is the difference between carrying every line of every cart
-- across the wire and carrying the few that hold anything; and a record whose
-- only field is an empty object tells the reader of a dossier nothing at all.
-- name: ListCartLineItemNotesForDisclosure :many
SELECT id,
       cart_id,
       metadata
FROM cart_line_items
WHERE cart_id = ANY (sqlc.arg('cart_ids')::text[])
  AND metadata <> '{}'::jsonb
ORDER BY cart_id, created_at, id;

-- ListCartShippingNotesForDisclosure returns the shipping methods that carry
-- provider data.
--
-- The same argument as the lines above, about the other free-form column this
-- module declares. What is typed here is the delivery provider's own data — a
-- pickup branch, a locker, a note for the courier — and a locker chosen near
-- somebody's home is exactly the kind of field that is personal in one
-- deployment and not in another, which is why the judgement is the
-- controller's (ADR 0029) and why the value is shown rather than assessed.
--
-- The method's own name ("standard", "next day") is not here: it describes the
-- service the shop offers, not the shopper, and it is not declared.
-- name: ListCartShippingNotesForDisclosure :many
SELECT id,
       cart_id,
       data
FROM cart_shipping_methods
WHERE cart_id = ANY (sqlc.arg('cart_ids')::text[])
  AND data <> '{}'::jsonb
ORDER BY cart_id, created_at, id;

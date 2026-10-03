-- customer queries. Every read filters on deleted_at IS NULL.
--
-- The ONE exception is the ERASURE queries at the end of the file, and the
-- exception is deliberate: a soft-deleted row goes on carrying its personal
-- data AS IT IS. The reason is written in the LockCustomerForErasure header.
-- (NOT a bracketed godoc link: sqlc copies this header as it is into the
-- package it generates, and there that name is a method, not a package-level
-- declaration — the link would not resolve.)

-- name: InsertCustomer :one
INSERT INTO customer (id, email, first_name, last_name, phone, has_account, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customer
WHERE id = $1 AND deleted_at IS NULL;

-- GetCustomerForUpdate reads the customer and locks its row UNTIL THE END OF
-- THE TRANSACTION.
--
-- The state-changing flows on the same customer (guest-to-account conversion,
-- default address assignment) ALWAYS take this lock FIRST. Because the order
-- is fixed, two flows cannot wait on each other in opposite orders; a deadlock
-- is structurally impossible.
--
-- FOR UPDATE RE-EVALUATES the WHERE condition once the lock is taken, so a
-- delete that slips in between shows up as "no record".
-- name: GetCustomerForUpdate :one
SELECT * FROM customer
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- GetAccountByEmail looks ONLY for a registered account.
--
-- Because guest records can share an e-mail, "the one customer with this
-- e-mail" is meaningful only for accounts; this query's has_account filter is
-- exactly the scope of the partial unique index.
-- name: GetAccountByEmail :one
SELECT * FROM customer
WHERE email = $1 AND has_account AND deleted_at IS NULL;

-- AccountEmailTakenByOther reports whether ANOTHER account uses the given
-- e-mail; it is the pre-check of the guest-to-account conversion.
-- name: AccountEmailTakenByOther :one
SELECT EXISTS (
    SELECT 1 FROM customer
    WHERE email = $1 AND id <> $2 AND has_account AND deleted_at IS NULL
);

-- ListCustomers returns the filtered, paged list of customers.
--
-- The group_id filter looks at the live group, NOT at the membership row:
-- membership rows stay in place when the group is soft-deleted, and a filter
-- that looked only at the membership would go on listing the members of a
-- deleted group. Every query that reads a group filters on deleted_at IS NULL;
-- this one follows the same rule.
-- name: ListCustomers :many
SELECT c.* FROM customer c
WHERE c.deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
  AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
  AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM customer_group_customer m
        JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
        WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text))
  AND (c.created_at, c.id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCustomers :one
SELECT count(*) FROM customer c
WHERE c.deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
  AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
  AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM customer_group_customer m
        JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
        WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text));

-- name: ListCustomersByIDs :many
SELECT * FROM customer
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateCustomer leaves the fields that are not given AS THEY ARE.
--
-- Written with COALESCE, this partial update keeps "the field was not sent"
-- apart from "the field was emptied": a NULL parameter keeps the old value,
-- and an empty string is a real clearing.
-- name: UpdateCustomer :one
UPDATE customer SET
    email      = COALESCE(sqlc.narg('email')::text, email),
    first_name = COALESCE(sqlc.narg('first_name')::text, first_name),
    last_name  = COALESCE(sqlc.narg('last_name')::text, last_name),
    phone      = COALESCE(sqlc.narg('phone')::text, phone),
    metadata   = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- PromoteCustomerToAccount turns a guest into an account.
--
-- The has_account = FALSE condition is required: promoting a record that is
-- already an account again would be a silent no-op, and the caller would
-- believe the operation had happened. When the condition does not hold, no row
-- comes back and the service tells the cases apart.
-- name: PromoteCustomerToAccount :one
UPDATE customer
SET has_account = TRUE, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL AND has_account = FALSE
RETURNING *;

-- name: SoftDeleteCustomer :one
UPDATE customer
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- SoftDeleteAddressesOfCustomer deletes the customer's addresses as well when
-- the customer is deleted.
--
-- The foreign key's ON DELETE CASCADE runs only on a REAL delete; since a soft
-- delete is an UPDATE, it does not take the addresses with it by itself. Had a
-- deleted customer's live addresses stayed behind, the address lists would
-- show records without an owner.
-- name: SoftDeleteAddressesOfCustomer :exec
UPDATE customer_address
SET deleted_at = $2, updated_at = $2
WHERE customer_id = $1 AND deleted_at IS NULL;

-- LockCustomerForErasure reads, by id, the row an erasure request REACHES and
-- locks it until the end of the transaction.
--
-- There is NO deleted_at FILTER, and that is the one point where it departs
-- from the rest of the file. The reason is measurable: SoftDeleteCustomer
-- writes only deleted_at and updated_at and touches not a single personal
-- column. A soft-deleted customer's e-mail, name and phone therefore stay in
-- the table UNCHANGED. Had the filter been there, a person whose record was
-- deleted would be told "your data has been anonymized" while the data stayed
-- where it was — and the report would say so nowhere.
--
-- Deletion (bookkeeping) and erasure (a legal answer) are two SEPARATE jobs;
-- this query seeing the deleted row too is that distinction's counterpart on
-- the database side.
--
-- FOR UPDATE is required: when a row read without a lock is overwritten, an
-- update that slips in between (e.g. the customer changing their own name on
-- the storefront) is lost or settles in AFTER the anonymization; the lock
-- guarantees that the one reading the row and the one writing it are the same
-- transaction.
--
-- The query returns ONLY the id. It used to return the e-mail too, because
-- whether the row was already anonymous was decided from it; that decision is
-- now made in AnonymizeCustomer's WHERE, by looking at the columns' REAL
-- values (the reasoning is written there).
-- name: LockCustomerForErasure :one
SELECT id FROM customer
WHERE id = $1
FOR UPDATE;

-- LockCustomersByEmailForErasure locks ALL the rows reached by e-mail.
--
-- It has to be plural: any number of GUEST records can be opened with the same
-- e-mail (see customer_account_email_uniq), and they are all the same person.
-- A query that looked only for the account (GetAccountByEmail) would leave
-- that person's guest records as they were.
--
-- ORDER BY id is not only for determinism: it FIXES the lock order. If two
-- concurrent erasure requests arrive for the same e-mail, both lock the rows in
-- the same order, and a mutual wait (deadlock) becomes structurally
-- impossible.
-- name: LockCustomersByEmailForErasure :many
SELECT id FROM customer
WHERE email = $1
ORDER BY id
FOR UPDATE;

-- LockCustomersByIDOrEmailForErasure resolves, in one query, a subject that
-- carries an id AND an e-mail TOGETHER.
--
-- Such a subject is not an exception but the NORM for the ADMIN ENDPOINT (see
-- internal/app/erasure.go: both fields of the request body pass straight into
-- erasure.Subject). A customer with a record may also have shopped as a guest
-- with the same e-mail address; the id points at that record, the e-mail at
-- the others, and the person is all of them. A rule of "when there is an id, do
-- not look at the e-mail" would leave the guest rows as they were for exactly
-- this person.
--
-- The reason for ONE query with an OR instead of two separate queries is the
-- lock order. An order that locks the id first and the e-mail second could run
-- against a second request, arriving by e-mail, that proceeds in id order: the
-- request arriving with (cust_C, e-mail) holds C and waits for A, while the
-- request arriving with the e-mail alone holds A and waits for C — a mutual
-- wait. The single query locks ALL the rows in a single id order, that order is
-- the same as the e-mail query's, and a deadlock stays structurally
-- impossible. Even when the same row satisfies both conditions it appears ONCE
-- in the result, so no deduplication (dedup) is needed.
-- name: LockCustomersByIDOrEmailForErasure :many
SELECT id FROM customer
WHERE id = sqlc.arg('id') OR email = sqlc.arg('email')
ORDER BY id
FOR UPDATE;

-- AnonymizeCustomer overwrites the customer's NAMED personal columns.
--
-- The three columns it does not write were each considered on their own:
--
--   - metadata is FREE-FORM and gobit never rewrites it (ADR 0029); deciding
--     whether it holds personal data is up to the embedding application. The
--     result report therefore HAS TO declare it in the Kept field.
--   - deleted_at is not touched: an erasure is not a deletion, and neither
--     reviving a deleted record nor deleting an undeleted one is part of this
--     job.
--   - has_account is kept; whether there is an account does not identify the
--     person, but it sets the scope of the partial uniqueness index.
--
-- The e-mail ARRIVES as a parameter and is not derived inside the SQL: the
-- rule's single source is models.AnonymousEmail, and were it repeated here,
-- two languages would carry the same rule separately, and a change to one
-- would silently make the other wrong.
--
-- The second half of the WHERE is for the count's CORRECTNESS and applies the
-- SAME rule as AnonymizeAddressesOfCustomer: a row is written only if it REALLY
-- carries something. The condition looks at all four columns, NOT only at the
-- e-mail. The difference is measurable: an anonymized record is NOT DELETED,
-- it stays live, and UpdateCustomer is a patch that COALESCEs column by
-- column — an admin or the storefront can write a fresh first_name and phone
-- without touching the e-mail at all. A condition that looked only at the
-- e-mail would count that row as "already anonymous" and skip the UPDATE
-- entirely, and the report would say "anonymized" while the person's name sat
-- in the row.
-- name: AnonymizeCustomer :execrows
UPDATE customer SET
    email      = sqlc.arg('email'),
    first_name = '',
    last_name  = '',
    phone      = '',
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id')
  AND (email      <> sqlc.arg('email')
    OR first_name <> ''
    OR last_name  <> ''
    OR phone      <> '');

-- AnonymizeAddressesOfCustomer anonymizes ALL of the customer's address rows.
--
-- Addresses fall within scope even when they are soft-deleted: a deleted
-- address row carries the person's street and phone too.
--
-- country_code is KEPT. A country code names not the person but the
-- JURISDICTION (tax and the retention period are read from it), and on its own
-- it points at millions of people. It could not have been emptied anyway:
-- customer_address_country_check requires the code to be exactly two UPPER
-- case letters. Because it is kept, it is declared in the result report's Kept
-- field.
--
-- address_1 and city CANNOT BE EMPTIED (NOT NULL + CHECK <> ''), so a
-- placeholder is written in their place; every other named column is set to
-- the empty string.
--
-- The second half of the WHERE is for the count's CORRECTNESS: a row that is
-- already anonymous is not updated. Without the condition, a second erasure
-- request would count the same rows as "written" again although it changed
-- nothing, and would advance updated_at — a receipt for work that was not
-- done.
-- name: AnonymizeAddressesOfCustomer :execrows
UPDATE customer_address SET
    first_name  = '',
    last_name   = '',
    company     = '',
    address_1   = sqlc.arg('placeholder'),
    address_2   = '',
    city        = sqlc.arg('placeholder'),
    province    = '',
    postal_code = '',
    phone       = '',
    updated_at  = sqlc.arg('updated_at')
WHERE customer_id = sqlc.arg('customer_id')
  AND (first_name  <> ''
    OR last_name   <> ''
    OR company     <> ''
    OR address_1   <> sqlc.arg('placeholder')
    OR address_2   <> ''
    OR city        <> sqlc.arg('placeholder')
    OR province    <> ''
    OR postal_code <> ''
    OR phone       <> '');

-- The DISCLOSURE queries are from here down, and they are the SECOND block
-- that carries no deleted_at filter.
--
-- The reason is the SAME fact as erasure's, read the other way round: a soft
-- delete touches not a single personal column, so a deleted row goes on
-- carrying the person's e-mail, name and phone UNCHANGED. Because the question
-- is not "does the record show up in lists" but "what does the database still
-- HOLD", there is no filter here either. Had the filter been there, the person
-- would be told "this is everything we hold about you" while the name on the
-- deleted row went unshown.
--
-- The queries are the lock-free TWINS of the erasure queries and branch into
-- the three paths at the same place (see repository/erasure.go,
-- lockErasureTargets). There is NO FOR UPDATE, and there must not be: this is a
-- READ, it opens no transaction, it writes nothing, and reading a report cannot
-- make a customer on the storefront wait.

-- GetCustomerForDisclosure reads ONE row by id.
--
-- A separate query for the subject that carries only an id is deliberate: this
-- path is a PRIMARY KEY lookup and does not scan. Had it been folded into a
-- single (id = $1 OR email = $2) query, a request arriving with the id alone —
-- the one the admin endpoint uses most — would fall into a sequential scan
-- too.
-- name: GetCustomerForDisclosure :one
SELECT * FROM customer
WHERE id = $1;

-- ListCustomersByEmailForDisclosure reads ALL the rows reached by e-mail.
--
-- It has to be plural, for the same reason as LockCustomersByEmailForErasure:
-- any number of GUEST records can be opened with the same e-mail, and they are
-- all the same person. A query that looked only for the account
-- (GetAccountByEmail) would leave that person's guest records out of the file.
--
-- The ordering is for determinism: a file produced twice for the same subject
-- has to show the records in the same order, or a person comparing the two
-- documents sees a difference that does not exist.
-- name: ListCustomersByEmailForDisclosure :many
SELECT * FROM customer
WHERE email = $1
ORDER BY id;

-- ListCustomersByIDOrEmailForDisclosure resolves, in one query, a subject that
-- carries an id AND an e-mail TOGETHER.
--
-- Such a subject is the norm for the admin endpoint (see
-- internal/app/erasure.go: both fields of the request body pass straight into
-- personaldata.Subject). A customer with a record may also have shopped as a
-- guest with the same e-mail address; the id points at that record, the e-mail
-- at the others, and the person is all of them.
--
-- Its twin in erasure is ONE query because of the lock order; here there is no
-- lock, and the reason for one query is a different one: merging the results
-- of two separate queries would push onto the caller the deduplication (dedup)
-- of a row that satisfies both conditions, and an undeduplicated file would
-- show the same person twice. In one query that row comes back ONCE anyway.
-- name: ListCustomersByIDOrEmailForDisclosure :many
SELECT * FROM customer
WHERE id = sqlc.arg('id') OR email = sqlc.arg('email')
ORDER BY id;

-- ListSegmentFacts pages the live customers in id order with what a segment
-- rule reads of their record (ADR 0217): whether they hold an account, when
-- they were created, and the country of their default shipping address.
-- name: ListSegmentFacts :many
SELECT c.id, c.has_account, c.created_at, a.country_code
FROM customer c
LEFT JOIN customer_address a
    ON a.customer_id = c.id AND a.is_default_shipping AND a.deleted_at IS NULL
WHERE c.deleted_at IS NULL AND c.id > sqlc.arg('after_id')::text
ORDER BY c.id
LIMIT sqlc.arg('row_limit');

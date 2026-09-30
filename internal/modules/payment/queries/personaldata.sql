-- The reads behind the payment module's answer to "what do you hold about this
-- person" (ADR 0277).
--
-- A person is found by the customer id alone: the ledgers, their sessions and
-- a collection name the customer, and a collection's sessions, payments,
-- refunds and provider sessions are reached through it. None of these columns
-- is indexed for the customer except the two ledgers': a request a person
-- makes rarely does not earn an index every checkout would write.

-- name: ListPaymentCollectionsOfCustomer :many
SELECT * FROM payment_collections
WHERE customer_id = sqlc.arg('customer_id')::text
ORDER BY created_at, id;

-- name: ListPaymentSessionsOfCollections :many
SELECT * FROM payment_sessions
WHERE payment_collection_id = ANY (sqlc.arg('collection_ids')::text[])
ORDER BY created_at, id;

-- name: ListPaymentsOfCollections :many
SELECT * FROM payments
WHERE payment_collection_id = ANY (sqlc.arg('collection_ids')::text[])
ORDER BY created_at, id;

-- name: ListRefundsOfPayments :many
SELECT * FROM refunds
WHERE payment_id = ANY (sqlc.arg('payment_ids')::text[])
ORDER BY created_at, id;

-- The provider sessions name the collection as their reference.

-- name: ListManualSessionsOfCollections :many
SELECT * FROM payment_manual_sessions
WHERE reference = ANY (sqlc.arg('collection_ids')::text[])
ORDER BY created_at, id;

-- name: ListGiftCardSessionsOfCollections :many
SELECT * FROM payment_gift_card_sessions
WHERE reference = ANY (sqlc.arg('collection_ids')::text[])
ORDER BY created_at, id;

-- name: ListStoreCreditEntriesOfCustomer :many
SELECT * FROM payment_store_credit_entries
WHERE customer_id = sqlc.arg('customer_id')::text
ORDER BY created_at, id;

-- name: ListStoreCreditSessionsOfCustomer :many
SELECT * FROM payment_store_credit_sessions
WHERE customer_id = sqlc.arg('customer_id')::text
ORDER BY created_at, id;

-- name: ListLoyaltyEntriesOfCustomer :many
SELECT * FROM payment_loyalty_entries
WHERE customer_id = sqlc.arg('customer_id')::text
ORDER BY created_at, id;

-- name: ListLoyaltySessionsOfCustomer :many
SELECT * FROM payment_loyalty_sessions
WHERE customer_id = sqlc.arg('customer_id')::text
ORDER BY created_at, id;

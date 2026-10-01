-- ListAddressesOfCustomers returns the living addresses of the given
-- customers in one read, each customer's in the order they were written (ADR
-- 0304, ADR 0308). The read layer derives a customer's address list and their
-- default shipping address from it.
-- name: ListAddressesOfCustomers :many
SELECT * FROM customer_address
WHERE customer_id = ANY (sqlc.arg('customer_ids')::text[])
  AND deleted_at IS NULL
ORDER BY customer_id, created_at, id;

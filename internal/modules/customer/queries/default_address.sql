-- ListDefaultShippingAddresses returns the default shipping address of each of
-- the given customers that has one, in one read (ADR 0304). The partial unique
-- index on is_default_shipping holds a customer to one.
-- name: ListDefaultShippingAddresses :many
SELECT * FROM customer_address
WHERE customer_id = ANY (sqlc.arg('customer_ids')::text[])
  AND is_default_shipping AND deleted_at IS NULL;

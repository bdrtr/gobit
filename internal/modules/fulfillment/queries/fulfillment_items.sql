-- fulfillment_items sorguları.
--
-- Kalem, sipariş satırının kimliğini taşır; o kimlik BAŞKA bir modüle aittir
-- ve burada doğrulanmaz (Prensip 2.2).

-- name: CreateFulfillmentItem :one
INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListFulfillmentItems :many
SELECT * FROM fulfillment_items
WHERE fulfillment_id = $1
ORDER BY id;

-- ListFulfillmentItemsByFulfillments kalemleri BİRDEN ÇOK gönderi için tek
-- turda döner; liste uçları gönderi başına sorgu atmaz (N+1 yok).
-- name: ListFulfillmentItemsByFulfillments :many
SELECT * FROM fulfillment_items
WHERE fulfillment_id = ANY (sqlc.arg('fulfillment_ids')::text[])
ORDER BY fulfillment_id, id;

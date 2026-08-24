-- order_line_items sorguları.
--
-- Sipariş satırları YAZILDIKTAN SONRA DEĞİŞMEZ: sipariş, "o an ne satıldı"
-- sorusunun kalıcı yanıtıdır ve satırın adedini ya da tutarını sonradan
-- düzeltmek o yanıtı bozmak olurdu. Bu yüzden burada UPDATE sorgusu YOKTUR;
-- düzeltme yolu iade/değişim kayıtlarıdır.

-- name: CreateOrderLineItem :one
INSERT INTO order_line_items (
    id, order_id, variant_id, title, quantity,
    unit_price, subtotal, discount_total, tax_total, total, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListOrderLineItems :many
SELECT * FROM order_line_items
WHERE order_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

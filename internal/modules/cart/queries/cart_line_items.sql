-- cart_line_items sorguları.
--
-- Satırlar her zaman SEPET KİLİDİ altında değiştirilir (bkz. LockCart); bu
-- yüzden satırın kendisi ayrıca kilitlenmez. Kilit sırası tektir: önce sepet,
-- sonra satır. Sıranın akışa göre değişmesi kilitlenme (deadlock) demektir.

-- name: CreateLineItem :one
INSERT INTO cart_line_items (
    id, cart_id, variant_id, title, quantity, unit_price, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetLineItem :one
SELECT * FROM cart_line_items
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL;

-- GetLineItemByVariant sepetteki bir varyantın YAŞAYAN satırını döner.
--
-- AddLineItem bunu kullanır: aynı varyant ikinci kez eklendiğinde yeni satır
-- açmak yerine var olanın adedini artırır (bkz. service.AddLineItem).
-- name: GetLineItemByVariant :one
SELECT * FROM cart_line_items
WHERE cart_id = $1 AND variant_id = $2 AND deleted_at IS NULL;

-- name: ListLineItems :many
SELECT * FROM cart_line_items
WHERE cart_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- ListLineItemsByCartIDs birden çok sepetin satırlarını TEK sorguda döner;
-- sepet başına sorgu (N+1) yapılmaz.
-- name: ListLineItemsByCartIDs :many
SELECT * FROM cart_line_items
WHERE cart_id = ANY (sqlc.arg('cart_ids')::text[]) AND deleted_at IS NULL
ORDER BY cart_id, created_at, id;

-- name: SetLineItemQuantity :one
UPDATE cart_line_items
SET quantity = $3, updated_at = now()
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL
RETURNING *;

-- SetLineItemTotals satırın PARA alanlarını workflow'un hesabıyla yazar.
--
-- Adet BURADA değişmez: adet sepet servisinin, tutarlar workflow'un verisidir.
-- İkisinin ayrı sorgularda olması, bir hesaplama turunun adedi sessizce
-- değiştirmesini yapısal olarak imkânsız kılar.
-- name: SetLineItemTotals :one
UPDATE cart_line_items
SET unit_price     = $3,
    subtotal       = $4,
    discount_total = $5,
    tax_total      = $6,
    total          = $7,
    updated_at     = now()
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteLineItem :execrows
UPDATE cart_line_items
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL;

-- name: SoftDeleteLineItemsByCart :exec
UPDATE cart_line_items
SET deleted_at = now(), updated_at = now()
WHERE cart_id = $1 AND deleted_at IS NULL;

-- customer_group ve üyelik sorguları.

-- name: InsertCustomerGroup :one
INSERT INTO customer_group (id, name, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
RETURNING *;

-- name: GetCustomerGroup :one
SELECT * FROM customer_group
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListCustomerGroups :many
SELECT * FROM customer_group
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCustomerGroups :one
SELECT count(*) FROM customer_group
WHERE deleted_at IS NULL;

-- UpdateCustomerGroup verilmeyen alanları OLDUĞU GİBİ bırakır.
--
-- Adın düzeltilebilmesi şarttır: ad canlı gruplar arasında benzersizdir ve
-- yanlış girilmiş bir ad, düzeltme yolu olmadan o adı sonsuza dek işgal
-- ederdi.
-- name: UpdateCustomerGroup :one
UPDATE customer_group SET
    name       = COALESCE(sqlc.narg('name')::text, name),
    metadata   = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- SoftDeleteCustomerGroup grubu yumuşak siler.
--
-- Üyelik satırları BIRAKILIR: silinmiş grup zaten hiçbir okumada görünmez
-- (grup okuyan her sorgu deleted_at IS NULL süzer) ve satırlar kayıt bir gün
-- gerçekten silindiğinde cascade ile gider. Ad, kısmi benzersiz indeksin
-- kapsamından çıktığı için yeniden kullanılabilir hâle gelir.
-- name: SoftDeleteCustomerGroup :one
UPDATE customer_group
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- AddCustomerToGroup üyeliği yazar; zaten varsa hiçbir şey yapmaz.
--
-- Üyelik bir KÜMEDİR: aynı çağrının iki kez gelmesi (yeniden deneme, çift
-- tıklama) hata değil, aynı sonuçtur. ON CONFLICT DO NOTHING bu idempotansı
-- tek satırda ifade eder.
-- name: AddCustomerToGroup :exec
INSERT INTO customer_group_customer (customer_id, customer_group_id, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (customer_id, customer_group_id) DO NOTHING;

-- RemoveCustomerFromGroup üyeliği siler ve SİLİNEN SATIR SAYISINI döner.
--
-- Sayı, "üyelik yoktu" ile "üyelik kaldırıldı" ayrımını yapan tek bilgidir;
-- servis olmayan bir üyeliğin kaldırılması isteğine errors.NotFound döner.
-- name: RemoveCustomerFromGroup :execrows
DELETE FROM customer_group_customer
WHERE customer_id = $1 AND customer_group_id = $2;

-- name: ListGroupsOfCustomer :many
SELECT g.* FROM customer_group g
JOIN customer_group_customer m ON m.customer_group_id = g.id
WHERE m.customer_id = $1 AND g.deleted_at IS NULL
ORDER BY g.created_at DESC, g.id DESC;

-- ListGroupIDsOfCustomers birden çok müşterinin grup kimliklerini TEK sorguda
-- döner.
--
-- Query sağlayıcısı müşterileri grup kimlikleriyle birlikte sunar; müşteri
-- başına ayrı sorgu, ADR 0004'ün yapısal olarak yasakladığı N+1 olurdu.
-- name: ListGroupIDsOfCustomers :many
SELECT m.customer_id, m.customer_group_id
FROM customer_group_customer m
JOIN customer_group g ON g.id = m.customer_group_id
WHERE m.customer_id = ANY(@customer_ids::text[]) AND g.deleted_at IS NULL
ORDER BY m.customer_id, m.customer_group_id;

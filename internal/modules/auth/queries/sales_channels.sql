-- sales_channel sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: InsertSalesChannel :one
INSERT INTO sales_channel (id, name, description, is_disabled, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $6)
RETURNING *;

-- name: GetSalesChannel :one
SELECT * FROM sales_channel
WHERE id = $1 AND deleted_at IS NULL;

-- LockLiveSalesChannel kanalın CANLI olduğunu doğrular ve satırı işlem sonuna
-- kadar kilitler.
--
-- Neden yalnızca foreign key yetmiyor: FK satırın FİZİKSEL varlığına bakar,
-- yumuşak silinmiş (deleted_at dolu) bir kanal onu geçer. Oysa okuma sorguları
-- o kanalı süzer; bağ kurulsa bile anahtar ÖLÜ DOĞAR — hiçbir kanala
-- bağlanamamış publishable anahtar mağaza kimliği kuramaz ve hata ancak ilk
-- istekte, "kanal bağlıydı ama çalışmıyor" biçiminde ortaya çıkardı.
--
-- FOR SHARE bilinçlidir: koşul sorgulandıktan sonra bağ yazılana kadar
-- geçen sürede kanalın yumuşak silinmesi mümkündü. Paylaşımlı kilit, silmeyi
-- yapan UPDATE'i işlem bitene kadar bekletir; bağ yazımını engellemez, çünkü
-- iki bağ yazımı birbirinin kilidiyle çakışmaz.
-- name: LockLiveSalesChannel :one
SELECT id FROM sales_channel
WHERE id = $1 AND deleted_at IS NULL
FOR SHARE;

-- name: ListSalesChannels :many
SELECT * FROM sales_channel
WHERE deleted_at IS NULL
  AND (sqlc.narg('name')::text IS NULL OR name = sqlc.narg('name')::text)
  AND (sqlc.narg('is_disabled')::boolean IS NULL OR is_disabled = sqlc.narg('is_disabled')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountSalesChannels :one
SELECT count(*) FROM sales_channel
WHERE deleted_at IS NULL
  AND (sqlc.narg('name')::text IS NULL OR name = sqlc.narg('name')::text)
  AND (sqlc.narg('is_disabled')::boolean IS NULL OR is_disabled = sqlc.narg('is_disabled')::boolean);

-- name: ListSalesChannelsByIDs :many
SELECT * FROM sales_channel
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- name: UpdateSalesChannel :one
UPDATE sales_channel SET
    name        = COALESCE(sqlc.narg('name')::text, name),
    description = COALESCE(sqlc.narg('description')::text, description),
    is_disabled = COALESCE(sqlc.narg('is_disabled')::boolean, is_disabled),
    metadata    = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at  = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteSalesChannel :one
UPDATE sales_channel
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- DeleteLinksOfSalesChannel kanal silinirken anahtar bağlarını da kaldırır.
--
-- Foreign key ON DELETE CASCADE yalnızca GERÇEK silmede çalışır; yumuşak silme
-- bir UPDATE olduğu için bağ satırları yerinde kalırdı. Bağ silinmezse,
-- silinmiş bir kanal aynı adla yeniden açıldığında eski anahtarlar sessizce
-- yeni kanala bağlı sanılabilirdi — bağ satırı kimliğe bakar ve kimlik yeni
-- olsa da bu, bağın neden kaldırıldığını unutulmaz kılar.
-- name: DeleteLinksOfSalesChannel :exec
DELETE FROM api_key_sales_channel
WHERE sales_channel_id = $1;

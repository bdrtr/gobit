-- api_key sorguları. Anahtarın DÜZ METNİ hiçbir sorguda geçmez; yalnızca
-- token_hash saklanır ve aranır (gerekçe: migrations/000001_auth_init.up.sql).

-- name: InsertAPIKey :one
INSERT INTO api_key (
    id, type, title, token_hash, redacted, scopes, created_by, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetAPIKey :one
SELECT * FROM api_key
WHERE id = $1 AND deleted_at IS NULL;

-- GetAPIKeyByHash gelen anahtarın özetine karşılık gelen kaydı döner.
--
-- İPTAL EDİLMİŞ anahtarlar burada SÜZÜLMEZ. Kararın nedeni: kaydı okuyup
-- "iptal edilmiş" diye reddetmek ile hiç bulamamak arasında ölçülebilir bir
-- süre farkı olmasın diye değil — iptalin servis tarafında ayrı ve AÇIK bir
-- dal olması, o dalın testle kanıtlanabilmesi içindir. Sorgu iptali süzseydi,
-- "iptal edilmiş anahtar reddedilir" iddiası "bulunamadı" ile karışır ve bir
-- gün süzgeç düştüğünde hiçbir test bunu yakalamazdı.
-- name: GetAPIKeyByHash :one
SELECT * FROM api_key
WHERE token_hash = $1 AND deleted_at IS NULL;

-- name: ListAPIKeys :many
SELECT * FROM api_key
WHERE deleted_at IS NULL
  AND (sqlc.narg('key_type')::text IS NULL OR type = sqlc.narg('key_type')::text)
  AND (sqlc.narg('revoked')::boolean IS NULL
       OR (sqlc.narg('revoked')::boolean AND revoked_at IS NOT NULL)
       OR (NOT sqlc.narg('revoked')::boolean AND revoked_at IS NULL))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountAPIKeys :one
SELECT count(*) FROM api_key
WHERE deleted_at IS NULL
  AND (sqlc.narg('key_type')::text IS NULL OR type = sqlc.narg('key_type')::text)
  AND (sqlc.narg('revoked')::boolean IS NULL
       OR (sqlc.narg('revoked')::boolean AND revoked_at IS NOT NULL)
       OR (NOT sqlc.narg('revoked')::boolean AND revoked_at IS NULL));

-- RevokeAPIKey anahtarı iptal eder.
--
-- revoked_at IS NULL koşulu şarttır: zaten iptal edilmiş bir anahtarı yeniden
-- iptal etmek sessiz bir no-op olurdu ve iptal zamanı ikinci çağrıyla
-- KAYARDI — denetim kaydı, anahtarın gerçekte ne zaman kapatıldığını
-- gösteremezdi. Koşul tutmazsa satır dönmez ve servis durumu ayırt eder.
-- name: RevokeAPIKey :one
UPDATE api_key SET
    revoked_at = $2,
    revoked_by = $3,
    updated_at = $2
WHERE id = $1 AND deleted_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- MarkAPIKeyUsed son kullanım anını günceller.
--
-- WHERE koşulundaki eşik bilinçlidir: bu sütun her istekte yazılsaydı, sıcak
-- bir publishable anahtar üzerinde her mağaza isteği aynı satıra yazmaya
-- çalışır ve kimlik doğrulama bir yazma darboğazına dönüşürdü. Eşik sayesinde
-- yazma anahtar başına en fazla pencere başına bir kez olur; sütunun değeri
-- YAKLAŞIKTIR ve öyle belgelenmiştir.
-- name: MarkAPIKeyUsed :exec
UPDATE api_key
SET last_used_at = sqlc.arg('used_at')::timestamptz
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND (last_used_at IS NULL OR last_used_at < sqlc.arg('stale_before')::timestamptz);

-- name: SoftDeleteAPIKey :one
UPDATE api_key
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- LinkAPIKeySalesChannel publishable anahtarı bir satış kanalına bağlar.
--
-- ON CONFLICT DO NOTHING bağın KÜME olmasının karşılığıdır: aynı bağın iki kez
-- kurulması bir hata değil, tekrardır.
-- name: LinkAPIKeySalesChannel :exec
INSERT INTO api_key_sales_channel (api_key_id, sales_channel_id, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (api_key_id, sales_channel_id) DO NOTHING;

-- name: UnlinkAPIKeySalesChannel :execrows
DELETE FROM api_key_sales_channel
WHERE api_key_id = $1 AND sales_channel_id = $2;

-- ListChannelIDsForKey anahtarın bağlı olduğu ETKİN kanalların kimliklerini
-- döner.
--
-- Devre dışı ve silinmiş kanallar burada süzülür: mağaza kimliği bu listeden
-- kurulur ve devre dışı bir kanalın kataloğu görünmemelidir.
-- name: ListChannelIDsForKey :many
SELECT l.sales_channel_id FROM api_key_sales_channel l
JOIN sales_channel c ON c.id = l.sales_channel_id
WHERE l.api_key_id = $1 AND c.deleted_at IS NULL AND NOT c.is_disabled
ORDER BY l.sales_channel_id;

-- ListChannelsForKey anahtarın bağlı olduğu kanalların TAMAMINI döner.
--
-- Devre dışı kanallar da dâhildir: yönetim yüzeyi bağı olduğu gibi göstermeli,
-- bir kanalın devre dışı olduğunu gizlememelidir.
-- name: ListChannelsForKey :many
SELECT c.* FROM api_key_sales_channel l
JOIN sales_channel c ON c.id = l.sales_channel_id
WHERE l.api_key_id = $1 AND c.deleted_at IS NULL
ORDER BY c.name, c.id;

-- name: DeleteLinksOfAPIKey :exec
DELETE FROM api_key_sales_channel
WHERE api_key_id = $1;

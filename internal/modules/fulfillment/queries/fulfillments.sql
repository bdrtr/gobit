-- fulfillments sorguları.
--
-- Gönderi satırı, sağlayıcıya GİTMEDEN ÖNCE yazılır: sağlayıcı sözleşmesinin
-- Reference alanı "çağıranın kendi kaydına verdiği kimlik"tir ve mutabakatta
-- iki sistemi eşleştiren şey odur. Önce sağlayıcıya gidilseydi, yanıt
-- kaybolduğunda hangi kaydın karşılığı olduğu bilinemeyen bir kargo etiketi
-- kalırdı.

-- InsertFulfillmentIfAbsent gönderiyi yalnızca o idempotency anahtarı HENÜZ
-- KULLANILMAMIŞSA yazar.
--
-- Çakışma hâlinde satır DÖNMEZ (pgx.ErrNoRows); çağıran o zaman anahtarla var
-- olan gönderiyi okur. "Önce oku, yoksa yaz" iki adımı arasında araya giren
-- eşzamanlı bir çağrı benzersiz indekse çarpar ve İŞLEMİ İPTAL EDERDİ;
-- ON CONFLICT DO NOTHING bu yarışı tek deyime indirir ve kaybeden taraf
-- kazananın işlemi bitene kadar BEKLER — böylece okuduğu satır sağlayıcı
-- yanıtıyla tamamlanmış olur.
-- name: InsertFulfillmentIfAbsent :one
INSERT INTO fulfillments (
    id, reference, shipping_option_id, provider_id, status, idempotency_key,
    data, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (idempotency_key) WHERE deleted_at IS NULL DO NOTHING
RETURNING *;

-- name: GetFulfillment :one
SELECT * FROM fulfillments
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetFulfillmentByIdempotencyKey :one
SELECT * FROM fulfillments
WHERE idempotency_key = $1 AND deleted_at IS NULL;

-- LockFulfillment gönderiyi işlem boyunca kilitler ve güncel hâlini döner.
--
-- Durum geçişleri (iptal, kargoya verme, teslim) yalnızca bu kilit altında
-- yapılır: kilitsiz okunan bir durum yazma anında bayat olabilir ve aynı
-- gönderiyi aynı anda iptal eden iki çağrı sağlayıcıya İKİ KEZ giderdi.
-- name: LockFulfillment :one
SELECT * FROM fulfillments
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListFulfillments :many
SELECT * FROM fulfillments
WHERE deleted_at IS NULL
  AND (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountFulfillments ListFulfillments ile AYNI filtreleri uygular; gerekçe için
-- bkz. CountShippingProfiles.
-- name: CountFulfillments :one
SELECT COUNT(*) FROM fulfillments
WHERE deleted_at IS NULL
  AND (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- UpdateFulfillmentProviderResult sağlayıcının yanıtını satıra yazar.
--
-- Sağlayıcı kimliği, takip bilgisi ve ham veri MUTLAK değerlerle yazılır:
-- artımlı bir güncelleme, kararı veren kodun gördüğü değer ile yazılan değeri
-- ayrıştırırdı.
-- name: UpdateFulfillmentProviderResult :one
UPDATE fulfillments
SET external_id      = $2,
    status           = $3,
    tracking_number  = $4,
    tracking_url     = $5,
    data             = $6,
    shipped_at       = $7,
    delivered_at     = $8,
    canceled_at      = $9,
    updated_at       = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- UpdateFulfillmentStatus durumu ve ona eşlik eden zaman damgasını yazar.
--
-- Damgalar MUTLAK verilir; şemadaki kısıtlar (fulfillments_*_stamp) durumu
-- damgasız bırakan bir yazmayı reddeder.
-- name: UpdateFulfillmentStatus :one
UPDATE fulfillments
SET status          = $2,
    tracking_number = $3,
    tracking_url    = $4,
    shipped_at      = $5,
    delivered_at    = $6,
    canceled_at     = $7,
    updated_at      = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

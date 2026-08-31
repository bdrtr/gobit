-- auth_user sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: InsertUser :one
INSERT INTO auth_user (id, email, first_name, last_name, avatar_url, scopes, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetUser :one
SELECT * FROM auth_user
WHERE id = $1 AND deleted_at IS NULL;

-- GetUserByEmail e-postaya göre CANLI kullanıcıyı döner.
--
-- Giriş akışının ilk adımıdır. Benzersizlik kısmi indeksle garanti altındadır,
-- bu yüzden sonuç en fazla bir satırdır.
-- name: GetUserByEmail :one
SELECT * FROM auth_user
WHERE email = $1 AND deleted_at IS NULL;

-- name: ListUsers :many
SELECT * FROM auth_user
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text)
  AND (sqlc.narg('scope')::text IS NULL OR sqlc.narg('scope')::text = ANY(scopes))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountUsers :one
SELECT count(*) FROM auth_user
WHERE deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR email = sqlc.narg('email')::text)
  AND (sqlc.narg('scope')::text IS NULL OR sqlc.narg('scope')::text = ANY(scopes));

-- name: ListUsersByIDs :many
SELECT * FROM auth_user
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateUser verilmeyen alanları OLDUĞU GİBİ bırakır.
--
-- COALESCE ile yazılan bu kısmi güncelleme, "alan gönderilmedi" ile "alan boşa
-- çekildi" ayrımını korur: NULL parametre eski değeri saklar, boş dize gerçek
-- bir temizlemedir.
-- name: UpdateUser :one
UPDATE auth_user SET
    email      = COALESCE(sqlc.narg('email')::text, email),
    first_name = COALESCE(sqlc.narg('first_name')::text, first_name),
    last_name  = COALESCE(sqlc.narg('last_name')::text, last_name),
    avatar_url = COALESCE(sqlc.narg('avatar_url')::text, avatar_url),
    scopes     = COALESCE(sqlc.narg('scopes')::text[], scopes),
    metadata   = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteUser :one
UPDATE auth_user
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- SoftDeleteIdentitiesOfUser kullanıcı silinirken kimliklerini de siler.
--
-- Foreign key ON DELETE CASCADE yalnızca GERÇEK silmede çalışır; yumuşak silme
-- bir UPDATE olduğu için kimlikleri kendiliğinden götürmez. Silinmiş bir
-- kullanıcının canlı kimliği geride kalsaydı, o kullanıcı SİLİNDİKTEN SONRA DA
-- giriş yapabilirdi — bu, modülün en pahalı sessiz hatası olurdu.
-- name: SoftDeleteIdentitiesOfUser :exec
UPDATE auth_identity
SET deleted_at = $2, updated_at = $2
WHERE user_id = $1 AND deleted_at IS NULL;

-- SyncIdentityProviderIdentity kullanıcının e-postası değiştiğinde giriş
-- kimliğini de günceller.
--
-- İkisi ayrı sütunlarda durur ama AYNI şeyi ifade eder: kullanıcının giriş
-- adresi. Senkron tutulmasalardı, e-postasını değiştiren bir kullanıcı eski
-- adresiyle giriş yapmaya devam eder ve (provider, provider_identity)
-- benzersizlik indeksi artık kimsenin kullanmadığı bir adresi işgal ederdi.
-- Çağrı, kullanıcı güncellemesiyle AYNI işlemdedir.
-- name: SyncIdentityProviderIdentity :exec
UPDATE auth_identity
SET provider_identity = $3, updated_at = $4
WHERE user_id = $1 AND provider = $2 AND deleted_at IS NULL;

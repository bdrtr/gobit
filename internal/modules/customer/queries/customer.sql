-- customer sorguları. Tüm okumalar deleted_at IS NULL filtresi uygular.

-- name: InsertCustomer :one
INSERT INTO customer (id, email, first_name, last_name, phone, has_account, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customer
WHERE id = $1 AND deleted_at IS NULL;

-- GetCustomerForUpdate müşteriyi okur ve satırını İŞLEM SONUNA KADAR kilitler.
--
-- Aynı müşteriye yapılan durum değiştiren akışlar (misafirden hesaba geçiş,
-- varsayılan adres atama) bu kilidi HER ZAMAN İLK sırada alır. Sıra sabit
-- olduğu için iki akış birbirini ters sırada bekleyemez; kilitlenme (deadlock)
-- yapısal olarak imkânsızdır.
--
-- FOR UPDATE kilit alındıktan sonra WHERE koşulunu YENİDEN değerlendirir; araya
-- giren bir silme bu yüzden "kayıt yok" olarak görünür.
-- name: GetCustomerForUpdate :one
SELECT * FROM customer
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- GetAccountByEmail YALNIZCA kayıtlı hesabı arar.
--
-- Misafir kayıtları aynı e-postayı paylaşabildiği için "e-postaya göre tek
-- müşteri" ancak hesaplar için anlamlıdır; bu sorgunun has_account süzgeci
-- kısmi benzersiz indeksin kapsamıyla birebir aynıdır.
-- name: GetAccountByEmail :one
SELECT * FROM customer
WHERE email = $1 AND has_account AND deleted_at IS NULL;

-- AccountEmailTakenByOther verilen e-postayı BAŞKA bir hesabın kullanıp
-- kullanmadığını bildirir; misafirden hesaba geçişin ön denetimidir.
-- name: AccountEmailTakenByOther :one
SELECT EXISTS (
    SELECT 1 FROM customer
    WHERE email = $1 AND id <> $2 AND has_account AND deleted_at IS NULL
);

-- ListCustomers süzgeçlenmiş ve sayfalanmış müşteri listesini döner.
--
-- group_id süzgeci üyelik satırına DEĞİL, canlı gruba bakar: üyelik satırları
-- grup yumuşak silindiğinde de yerinde kalır ve yalnızca üyeliğe bakan bir
-- süzgeç, silinmiş bir grubun üyelerini listelemeye devam ederdi. Grubu okuyan
-- her sorgu deleted_at IS NULL süzer; burası da aynı kurala uyar.
-- name: ListCustomers :many
SELECT c.* FROM customer c
WHERE c.deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
  AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
  AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM customer_group_customer m
        JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
        WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text))
  AND (c.created_at, c.id) < (
    COALESCE(sqlc.narg('after_at')::timestamptz, 'infinity'::timestamptz),
    COALESCE(sqlc.narg('after_id')::text, '')
  )
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountCustomers :one
SELECT count(*) FROM customer c
WHERE c.deleted_at IS NULL
  AND (sqlc.narg('email')::text IS NULL OR c.email = sqlc.narg('email')::text)
  AND (sqlc.narg('has_account')::boolean IS NULL OR c.has_account = sqlc.narg('has_account')::boolean)
  AND (sqlc.narg('group_id')::text IS NULL OR EXISTS (
        SELECT 1 FROM customer_group_customer m
        JOIN customer_group g ON g.id = m.customer_group_id AND g.deleted_at IS NULL
        WHERE m.customer_id = c.id AND m.customer_group_id = sqlc.narg('group_id')::text));

-- name: ListCustomersByIDs :many
SELECT * FROM customer
WHERE id = ANY(@ids::text[]) AND deleted_at IS NULL
ORDER BY id;

-- UpdateCustomer verilmeyen alanları OLDUĞU GİBİ bırakır.
--
-- COALESCE ile yazılan bu kısmi güncelleme, "alan gönderilmedi" ile "alan boşa
-- çekildi" ayrımını korur: NULL parametre eski değeri saklar, boş dize gerçek
-- bir temizlemedir.
-- name: UpdateCustomer :one
UPDATE customer SET
    email      = COALESCE(sqlc.narg('email')::text, email),
    first_name = COALESCE(sqlc.narg('first_name')::text, first_name),
    last_name  = COALESCE(sqlc.narg('last_name')::text, last_name),
    phone      = COALESCE(sqlc.narg('phone')::text, phone),
    metadata   = COALESCE(sqlc.narg('metadata')::jsonb, metadata),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- PromoteCustomerToAccount misafiri hesaba çevirir.
--
-- has_account = FALSE koşulu şarttır: zaten hesap olan bir kaydı yeniden
-- yükseltmek sessiz bir no-op olurdu ve çağıran işlemin gerçekleştiğini
-- sanırdı. Koşul tutmazsa satır dönmez ve servis durumu ayırt eder.
-- name: PromoteCustomerToAccount :one
UPDATE customer
SET has_account = TRUE, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL AND has_account = FALSE
RETURNING *;

-- name: SoftDeleteCustomer :one
UPDATE customer
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- SoftDeleteAddressesOfCustomer müşteri silinirken adreslerini de siler.
--
-- Foreign key ON DELETE CASCADE yalnızca GERÇEK silmede çalışır; yumuşak silme
-- bir UPDATE olduğu için adresleri kendiliğinden götürmez. Silinmiş bir
-- müşterinin canlı adresleri geride kalsaydı, adres listeleri sahipsiz kayıt
-- gösterirdi.
-- name: SoftDeleteAddressesOfCustomer :exec
UPDATE customer_address
SET deleted_at = $2, updated_at = $2
WHERE customer_id = $1 AND deleted_at IS NULL;

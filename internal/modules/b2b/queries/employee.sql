-- b2b_company_employee sorguları. Tüm okumalar deleted_at IS NULL filtresi
-- uygular.
--
-- Bu dosyada customer_id GEÇMEZ: çalışan ile müşteri arasındaki bağ şemada
-- değil core/link'tedir (bkz. migrations/000001_b2b_init.up.sql).

-- name: InsertEmployee :one
INSERT INTO b2b_company_employee (
    id, company_id, spending_limit, is_company_admin, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING *;

-- name: GetEmployee :one
SELECT * FROM b2b_company_employee
WHERE id = $1 AND deleted_at IS NULL;

-- ListEmployees süzgeçlenmiş ve sayfalanmış çalışan listesini döner.
-- name: ListEmployees :many
SELECT * FROM b2b_company_employee
WHERE deleted_at IS NULL
  AND (sqlc.narg('company_id')::text IS NULL OR company_id = sqlc.narg('company_id')::text)
  AND (sqlc.narg('is_company_admin')::boolean IS NULL
       OR is_company_admin = sqlc.narg('is_company_admin')::boolean)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountEmployees :one
SELECT count(*) FROM b2b_company_employee
WHERE deleted_at IS NULL
  AND (sqlc.narg('company_id')::text IS NULL OR company_id = sqlc.narg('company_id')::text)
  AND (sqlc.narg('is_company_admin')::boolean IS NULL
       OR is_company_admin = sqlc.narg('is_company_admin')::boolean);

-- UpdateEmployee verilmeyen alanları OLDUĞU GİBİ bırakır.
--
-- spending_limit için COALESCE KULLANILAMAZ: alanın kendisi NULL olabilir
-- ("sınırsız") ve COALESCE, "sınırsız yap" isteğini "dokunma"dan ayıramazdı.
-- Ayrım ayrı bir bayrakla taşınır: clear_limit doğruysa sütun NULL'a çekilir,
-- değilse verilen değer yazılır ya da eski değer korunur.
-- name: UpdateEmployee :one
UPDATE b2b_company_employee SET
    spending_limit   = CASE
        WHEN sqlc.arg('clear_limit')::boolean THEN NULL
        ELSE COALESCE(sqlc.narg('spending_limit')::bigint, spending_limit)
    END,
    is_company_admin = COALESCE(sqlc.narg('is_company_admin')::boolean, is_company_admin),
    updated_at       = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteEmployee :one
UPDATE b2b_company_employee
SET deleted_at = $2, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING id;

-- SoftDeleteEmployeesOfCompany şirketin tüm çalışanlarını yumuşak siler ve
-- SİLİNENLERİN KİMLİKLERİNİ döner.
--
-- Kimlikler dönmek zorundadır: çalışanın müşteriyle bağı link tablosundadır ve
-- o bağ silinmezse müşteri, kardinalite kısıtı yüzünden bir daha HİÇBİR
-- şirkete çalışan olarak eklenemez (bkz. service.Definitions, OneToOne).
-- name: SoftDeleteEmployeesOfCompany :many
UPDATE b2b_company_employee
SET deleted_at = $2, updated_at = $2
WHERE company_id = $1 AND deleted_at IS NULL
RETURNING id;

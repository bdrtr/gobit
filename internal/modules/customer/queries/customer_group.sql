-- customer_group ve üyelik sorguları.

-- name: InsertCustomerGroup :one
INSERT INTO customer_group (id, name, rank, metadata, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
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
    -- rank is nullable in the ARGUMENT and not in the column: a nil means "do
    -- not touch", which is what lets a merchant rename a group without silently
    -- resetting the order they set (ADR 0049).
    rank       = COALESCE(sqlc.narg('rank')::int, rank),
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

-- ListGroupsOfCustomer bir musterinin gruplarini SIRALI dondurur.
--
-- Siralama RANK, sonra id: bastaki grup, saticinin sectigi kazanandir. Bu
-- siralama ADR 0049 ile SOZLESME haline geldi — Service.CustomerGroupIDs'in
-- basi, sepetin kural baglamina yazdigi tek gruptur. Onceki siralama
-- (created_at DESC, id DESC) keyfi degildi ama bir SOZ de degildi; simdi soz.
--
-- name: ListGroupsOfCustomer :many
SELECT g.* FROM customer_group g
JOIN customer_group_customer m ON m.customer_group_id = g.id
WHERE m.customer_id = $1 AND g.deleted_at IS NULL
ORDER BY g.rank, g.id;

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

-- SetGroupSegment makes a group a segment with the given rule, or gives it a
-- new rule (ADR 0217). The rule's moment is new, so a pass that read the old
-- rule writes nothing more, and the group waits for its next evaluation.
-- name: SetGroupSegment :one
UPDATE customer_group
SET segment = sqlc.arg('segment'), segment_set_at = sqlc.arg('set_at'),
    segment_evaluated_at = NULL, updated_at = sqlc.arg('set_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- ClearGroupSegment hands a segment back to the operator; its members stay.
-- name: ClearGroupSegment :one
UPDATE customer_group
SET segment = NULL, segment_set_at = NULL, segment_evaluated_at = NULL, updated_at = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- CountSegments counts the live segments other than the given group.
-- name: CountSegments :one
SELECT count(*) FROM customer_group
WHERE segment IS NOT NULL AND deleted_at IS NULL AND id <> $1;

-- ListSegments reads the live segments in id order.
-- name: ListSegments :many
SELECT * FROM customer_group
WHERE segment IS NOT NULL AND deleted_at IS NULL
ORDER BY id
LIMIT $1;

-- LockSegment locks a live segment's row and returns the moment of its rule;
-- no row is a group that is gone or is no segment any more.
-- name: LockSegment :one
SELECT segment_set_at FROM customer_group
WHERE id = $1 AND deleted_at IS NULL AND segment IS NOT NULL
FOR UPDATE;

-- RemoveSegmentStrays takes out of a segment the members whose id falls in
-- (after_id, last_id] and who are not among the given ones; an empty last_id
-- reaches the end of the ids.
-- name: RemoveSegmentStrays :execrows
DELETE FROM customer_group_customer
WHERE customer_group_id = sqlc.arg('group_id')
  AND customer_id > sqlc.arg('after_id')::text
  AND (sqlc.arg('last_id')::text = '' OR customer_id <= sqlc.arg('last_id')::text)
  AND NOT (customer_id = ANY(sqlc.arg('members')::text[]));

-- AddSegmentMembers puts the given live customers in a segment.
-- name: AddSegmentMembers :execrows
INSERT INTO customer_group_customer (customer_id, customer_group_id, created_at)
SELECT c.id, sqlc.arg('group_id'), sqlc.arg('created_at')
FROM customer c
WHERE c.id = ANY(sqlc.arg('members')::text[]) AND c.deleted_at IS NULL
ON CONFLICT (customer_id, customer_group_id) DO NOTHING;

-- FinishSegment records that a pass wrote a segment's members, only while the
-- rule it evaluated is still the segment's.
-- name: FinishSegment :execrows
UPDATE customer_group
SET segment_evaluated_at = sqlc.arg('evaluated_at')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL AND segment_set_at = sqlc.arg('set_at');

-- LockSegmentCount serializes the writers that turn a group into a segment, so
-- the count under MaxSegments is the count they all see. The key is the
-- repository's SegmentCountLockKey.
-- name: LockSegmentCount :exec
SELECT pg_advisory_xact_lock(sqlc.arg('lock_key')::bigint);

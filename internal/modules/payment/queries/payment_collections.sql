-- payment_collections sorguları.
--
-- Koleksiyon satırı, bir ödemenin TÜM alt kayıtları için kilit sırasının İLK
-- adımıdır (bkz. service.Store "Kilit sırası"). Oturum, tahsilat ve iade
-- yazan her akış işlemine LockPaymentCollection ile başlar; bu sayede aynı
-- koleksiyona dokunan iki akış birbirini seri hâle getirir ve türetilen durum
-- alanı asla iki farklı hesaptan yazılmaz.

-- name: CreatePaymentCollection :one
INSERT INTO payment_collections (
    id, reference, amount, currency_code, status, metadata
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPaymentCollection :one
SELECT * FROM payment_collections
WHERE id = $1 AND deleted_at IS NULL;

-- LockPaymentCollection koleksiyonu işlem boyunca kilitler ve güncel hâlini
-- döner. Tutarları değiştiren her akış okumasını BU metotla yapar: kilitsiz
-- okunan bir tutar yazma anında bayat olabilir ve iki eşzamanlı tahsilat aynı
-- yetkilendirmeyi iki kez harcayabilirdi.
-- name: LockPaymentCollection :one
SELECT * FROM payment_collections
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- name: ListPaymentCollections :many
SELECT * FROM payment_collections
WHERE deleted_at IS NULL
  AND (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountPaymentCollections sayfalama zarfının toplam sayısını verir ve
-- ListPaymentCollections ile AYNI filtreleri uygular; ikisi birlikte
-- değiştirilmelidir.
--
-- Toplam, satırlarla birlikte dönen bir pencere fonksiyonundan okunamaz:
-- aralık dışı bir sayfada hiç satır dönmez, pencere değerlendirilmez ve toplam
-- 0 görünürdü. Toplam sayfanın değil FİLTRENİN sayısıdır.
-- name: CountPaymentCollections :one
SELECT COUNT(*) FROM payment_collections
WHERE deleted_at IS NULL
  AND (sqlc.narg('reference')::text IS NULL OR reference = sqlc.narg('reference')::text)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- GetPaymentCollectionsByIDs Query katmanının FetchByIDs çağrısını TEK turda
-- karşılar; kimlik başına sorgu (N+1) yapılmaz.
-- name: GetPaymentCollectionsByIDs :many
SELECT * FROM payment_collections
WHERE id = ANY (sqlc.arg('ids')::text[]) AND deleted_at IS NULL
ORDER BY id;

-- PaymentMomentsByCollectionIDs, koleksiyonların para ANLARINI tek sorguda
-- döner.
--
-- Tutarlar koleksiyon satırında zaten var; eksik olan ZAMANlardı ve ikisi de
-- başka tablolarda: para ilk ne zaman hareket etti (payments.captured_at) ve
-- son iade ne zaman çıktı (refunds.created_at — refunds tablosunda ayrı bir
-- refunded_at kolonu yok).
--
-- İlki MIN, ikincisi MAX, ve asimetri kasıtlıdır: bir destek masası "ne zaman
-- ödendi" diye sorduğunda paranın hareket ETMEYE BAŞLADIĞI anı kastediyor,
-- "ne zaman iade edildi" diye sorduğunda ise en SON iadeyi. Kısmi tahsilat ve
-- kısmi iade ikisini de çoğullaştırır; tek bir an isteyen okuma bu ikisini
-- ister, her anı isteyen okuma ayrı bir çağrıdır.
--
-- İki alt sorgu da kısmi indeksleri kullanır (payments ve refunds koleksiyon/
-- ödeme başına indekslidir), ve silinmiş satırlar iki tarafta da elenir.
-- name: PaymentMomentsByCollectionIDs :many
SELECT c.id AS payment_collection_id,
       (SELECT min(p.captured_at)
          FROM payments p
         WHERE p.payment_collection_id = c.id
           AND p.deleted_at IS NULL)::timestamptz AS first_captured_at,
       (SELECT max(r.created_at)
          FROM refunds r
          JOIN payments p2 ON p2.id = r.payment_id
         WHERE p2.payment_collection_id = c.id
           AND r.deleted_at IS NULL
           AND p2.deleted_at IS NULL)::timestamptz AS last_refunded_at
FROM payment_collections c
WHERE c.id = ANY (sqlc.arg('ids')::text[]) AND c.deleted_at IS NULL
ORDER BY c.id;

-- UpdatePaymentCollectionTotals tutarları ve türetilen durumu MUTLAK
-- değerlerle yazar.
--
-- Artımlı (amount = amount + n) güncelleme kasten kullanılmaz: yeni değer,
-- kilit altında okunan değerden hesaplanır ve kararı veren kodun gördüğü sayı
-- ile yazılan sayı aynı olur.
-- name: UpdatePaymentCollectionTotals :one
UPDATE payment_collections
SET status            = $2,
    authorized_amount = $3,
    captured_amount   = $4,
    refunded_amount   = $5,
    updated_at        = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

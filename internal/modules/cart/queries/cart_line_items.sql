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

-- SetLineItemTotals bir hesap turunun TÜM satır tutarlarını TEK deyimle yazar.
--
-- Adet BURADA değişmez: adet sepet servisinin, tutarlar workflow'un verisidir.
-- İkisinin ayrı sorgularda olması, bir hesaplama turunun adedi sessizce
-- değiştirmesini yapısal olarak imkânsız kılar.
--
-- Deyim TEKTİR çünkü satır başına bir UPDATE, sepetin KİLİDİ altında koşuyordu.
-- Ölçüldü (yerel konteyner, TCP gidiş-dönüş ~30 µs, 100 satırlık sepet, kilit
-- alındıktan SON YAZMA dönene kadar geçen süre, p50): satır başına UPDATE
-- 8,0 ms, aynı UPDATE'ler tek boru hattında 3,0 ms, buradaki tek deyim
-- 0,55 ms. Kazancın yalnızca bir bölümü gidiş-dönüşten gelir; geri kalanı
-- deyim başına ayrıştırma/planlama maliyetidir ve onu ancak TEK deyim siler —
-- boru hattı (pgx batch, sqlc :batchexec) kazancın üçte ikisinde kalırdı.
--
-- ÖLÇÜM NEYİ İÇERMİYOR: harness'ın konteyneri fsync=off ile koşar
-- (testcontainers-go postgres modülü bunu sabit yazar), yani yukarıdaki süreler
-- kilidin altındaki YAZMA EVRESİDİR, ardından gelen commit'in WAL flush'ı
-- DEĞİLDİR. Flush da aynı kilidin altındadır — kilit commit'te bırakılır — ve
-- bu değişiklik ona DOKUNMAZ: kalıcı bir kümede (fsync=on, aynı makine)
-- ölçüldü, 1 satır güncelleyen commit de 100 satır güncelleyen commit de
-- 6,2 ms. Yani gerçek kilit süresi ~14,2 ms'den ~6,8 ms'ye iner: kazanç 14 kat
-- değil ~2 kattır. 14 kat, yazma evresinin kendi içindeki orandır ve deyim
-- sayısı hakkındaki iddia odur.
--
-- Eşleştirme dizilerin SIRASIYLA yapılır: v.id ile aynı indeksteki tutarlar
-- aynı satıra gider. Diziler tek bir döngüde kurulur (bkz. repository), bu
-- yüzden uzunlukları yapısal olarak eşittir; eşit olmasalardı ROWS FROM kısa
-- diziyi NULL ile doldurur ve NOT NULL kısıtı deyimi düşürürdü — sessiz bir
-- yanlış tutar değil, gürültülü bir hata.
--
-- ROWS FROM (unnest(a), unnest(b), ...) çok argümanlı unnest(a, b, ...) ile
-- aynı şeydir; sqlc çok argümanlı biçimi ayrıştıramadığı için bu biçim yazıldı.
-- Ölçüldü: ikisi arasında fark yok (100 satır, p50 545 µs / 555 µs).
--
-- cart_id JOIN'in değil WHERE'in parçasıdır: başka sepetin satırı hiçbir
-- dizide eşleşemez. Eşleşmeyen kimlik RETURNING'de görünmez ve çağıran yazılan
-- kimlikleri istenenlerle karşılaştırıp turu düşürür.
--
-- Tekrarlanan kimlik verilmez (servis reddeder): UPDATE ... FROM aynı hedef
-- satır birden çok kaynak satırla eşleştiğinde HANGİ kaynağın kazandığını
-- tanımlamaz.
-- name: SetLineItemTotals :many
UPDATE cart_line_items AS li
SET unit_price     = v.unit_price,
    subtotal       = v.subtotal,
    discount_total = v.discount_total,
    tax_total      = v.tax_total,
    total          = v.total,
    updated_at     = now()
FROM ROWS FROM (
    unnest(sqlc.arg('line_ids')::text[]),
    unnest(sqlc.arg('unit_prices')::bigint[]),
    unnest(sqlc.arg('subtotals')::bigint[]),
    unnest(sqlc.arg('discount_totals')::bigint[]),
    unnest(sqlc.arg('tax_totals')::bigint[]),
    unnest(sqlc.arg('totals')::bigint[])
) AS v (id, unit_price, subtotal, discount_total, tax_total, total)
WHERE li.id = v.id
  AND li.cart_id = sqlc.arg('cart_id')
  AND li.deleted_at IS NULL
RETURNING li.id;

-- name: SoftDeleteLineItem :execrows
UPDATE cart_line_items
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND cart_id = $2 AND deleted_at IS NULL;

-- name: SoftDeleteLineItemsByCart :exec
UPDATE cart_line_items
SET deleted_at = now(), updated_at = now()
WHERE cart_id = $1 AND deleted_at IS NULL;

-- payment_sessions sorguları.
--
-- Oturum kaydı SİLİNMEZ, durumu değişir. Telafinin (CancelPayment) idempotent
-- olması buna dayanır: ikinci çağrı kaydı bulur, "canceled" görür ve
-- sağlayıcıya ikinci kez gitmeden başarıyla döner. Silinmiş bir oturum ile hiç
-- var olmamış bir oturum birbirinden ayırt edilemezdi.

-- name: CreatePaymentSession :one
INSERT INTO payment_sessions (
    id, payment_collection_id, provider_id, external_id, status,
    amount, authorized_amount, currency_code, data, idempotency_key
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetPaymentSession :one
SELECT * FROM payment_sessions
WHERE id = $1 AND deleted_at IS NULL;

-- LockPaymentSession oturumu işlem boyunca kilitler; durum geçişleri
-- (authorize/capture/cancel) yalnızca bu kilit altında yapılır. Aynı oturumu
-- aynı anda yetkilendirmeye çalışan iki çağrıdan ikincisi, birincinin yazdığı
-- durumu görür ve sağlayıcıya İKİNCİ KEZ gitmez.
-- name: LockPaymentSession :one
SELECT * FROM payment_sessions
WHERE id = $1 AND deleted_at IS NULL
FOR UPDATE;

-- GetPaymentSessionByIdempotencyKey aynı anahtarla açılmış oturumu bulur.
-- CreateSession sağlayıcıya GİTMEDEN ÖNCE bunu sorar; ikinci çağrı yeni oturum
-- açmaz (plan Bölüm 2.6, internal/core/provider idempotency şartı).
-- name: GetPaymentSessionByIdempotencyKey :one
SELECT * FROM payment_sessions
WHERE provider_id = $1 AND idempotency_key = $2 AND deleted_at IS NULL;

-- name: ListPaymentSessionsByCollection :many
SELECT * FROM payment_sessions
WHERE payment_collection_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- CountPaymentSessionStates koleksiyonun oturumlarını duruma göre TEK sorguda
-- sayar. Koleksiyonun türetilen durumu bu sayımlara bakar: hiç oturumu olmayan
-- koleksiyon "not_paid", canlı oturumu olan "awaiting", yalnızca iptal edilmiş
-- oturumu olan "canceled" olur (bkz. service.CollectionStatusFor).
-- name: CountPaymentSessionStates :one
SELECT
    COUNT(*) FILTER (WHERE status IN ('pending', 'authorized')) AS live_count,
    COUNT(*) FILTER (WHERE status = 'canceled')                 AS canceled_count,
    COUNT(*) FILTER (WHERE status = 'failed')                   AS failed_count,
    COUNT(*)                                                    AS total_count
FROM payment_sessions
WHERE payment_collection_id = $1 AND deleted_at IS NULL;

-- SumLiveSessionAmounts koleksiyonun CANLI oturumlarının rezerve ettiği toplam
-- tutarı verir. Yeni bir oturumun kapabileceği kalan tutar bundan hesaplanır.
--
-- Bekleyen oturum kendi TUTARINI rezerve eder: henüz yetkilendirilmemiştir ama
-- yetkilendirildiğinde tutarının tamamını bloke edebilir. Yetkilendirilmiş
-- oturum ise yalnızca BLOKE EDİLENİ tutar; ikinci kez yetkilendirilemeyeceği
-- için (bkz. models.SessionStatus.AuthorizeAction) fazlası bir daha kullanılmaz.
-- Yalnızca yetkilendirilmiş tutara bakan bir hesap, hiçbiri yetkilendirilmemiş
-- iki TAM tutarlı oturumun aynı koleksiyonda açılmasına ve ikisi de
-- yetkilendirilince ÇİFT TAHSİLATA izin verirdi.
-- name: SumLiveSessionAmounts :one
SELECT COALESCE(SUM(
    CASE WHEN status = 'pending' THEN amount ELSE authorized_amount END
), 0)::bigint AS reserved_amount
FROM payment_sessions
WHERE payment_collection_id = $1
  AND status IN ('pending', 'authorized')
  AND deleted_at IS NULL;

-- UpdatePaymentSessionState oturumun durumunu, yetkilendirilen tutarını, ham
-- sağlayıcı verisini ve ret sebebini MUTLAK değerlerle yazar.
-- name: UpdatePaymentSessionState :one
UPDATE payment_sessions
SET status            = $2,
    authorized_amount = $3,
    data              = $4,
    decline_reason    = $5,
    updated_at        = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- ListSessionsForReconciliation, sağlayıcıya SORULMASI gereken oturumları
-- döner: yetkilendirilmiş ama tahsil edilmemiş görünen, ve bir süredir öyle
-- duranlar.
--
-- # Neden tam olarak bu küme
--
-- Modül sağlayıcı çağrısını KENDİ işleminin içinde yapar. Para alındıktan sonra
-- işlem geri alınırsa oturum yerelde 'authorized' kalır, sağlayıcıda ise
-- 'captured'tır — ve bu fark başka hiçbir yerden görülemez (bkz.
-- internal/workflows/checkout/doc.go, "kalan risk").
--
-- 'pending' DIŞARIDA BIRAKILIR ve bu bilinçlidir: bir oturum yetkilendirilmeden
-- önce para hareket etmemiştir, dolayısıyla ayrışacak bir tutar da yoktur.
-- Kapsamı oraya genişletmek, her açılıp terk edilmiş sepeti sağlayıcıya
-- sordurmak olurdu — yani gürültüyü, bakılması gereken satırın önüne koymak.
--
-- $2 bir BEKLEME SÜRESİDİR, isteğe bağlı bir eşik değil: uçuştaki bir tahsilat
-- saniyeler boyunca tam olarak bu durumda durur ve onu ayrışma saymak, her
-- normal ödemeyi rapora düşürürdü.
-- name: ListSessionsForReconciliation :many
SELECT * FROM payment_sessions
WHERE status = 'authorized'
  AND updated_at < $1
  AND deleted_at IS NULL
ORDER BY updated_at
LIMIT $2;

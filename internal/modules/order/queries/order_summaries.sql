-- order_summaries sorguları.
--
-- Özet siparişle BİRLİKTE ve sıfırlanmış olarak doğar; "özeti olmayan sipariş"
-- diye bir durum yoktur.

-- name: CreateOrderSummary :one
INSERT INTO order_summaries (id, order_id)
VALUES ($1, $2)
RETURNING *;

-- name: GetOrderSummary :one
SELECT * FROM order_summaries
WHERE order_id = $1;

-- SetOrderSummaryTotals ödenen ve iade edilen KÜMÜLATİF tutarları birleştirir.
--
-- Artımlı (paid_total = paid_total + $2) bir yazma seçilmedi: ödeme olayları
-- en az bir kez teslim edilir (bkz. core/eventbus) ve tekrarlanan bir olay
-- artımlı yazmada tutarı İKİ KEZ eklerdi.
--
-- Ama düz mutlak yazma da (paid_total = $2) yetmez: en-az-bir-kez teslim SIRA
-- garantisi VERMEZ. Gecikmiş bir tahsilat olayı yeniden işlendiğinde, daha
-- sonra kaydedilmiş bir iadeyi sıfıra yazar ve kimse hata görmezdi. Bu yüzden
-- yazma GREATEST ile birleştirilir: iki tutar da siparişin ÖMÜR BOYU
-- toplamlarıdır ve yalnızca BÜYÜYEBİLİR. Sonuç hem idempotent (aynı değer
-- ikinci kez zararsız) hem de SIRADAN BAĞIMSIZDIR — hangi sırayla gelirse
-- gelsin aynı yere yakınsar ve kaydedilmiş hiçbir tutar kaybolmaz.
--
-- Birleştirme order_summaries_refund_within_paid kısıtını KIRAMAZ: her iki
-- girdi de refunded <= paid sağlıyorsa max(r1,r2) <= max(p1,p2) da sağlanır.
-- name: SetOrderSummaryTotals :one
UPDATE order_summaries
SET paid_total     = GREATEST(paid_total, $2),
    refunded_total = GREATEST(refunded_total, $3),
    updated_at     = now()
WHERE order_id = $1
RETURNING *;

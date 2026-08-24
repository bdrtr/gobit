package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// SummaryTotalsInput siparişin ödeme özetine bildirilen KÜMÜLATİF tutarlardır.
//
// Artımlı değil kümülatif olması bilinçlidir: ödeme olayları en az bir kez
// teslim edilir (bkz. core/eventbus) ve artımlı bir yazma, tekrarlanan bir
// olayda tutarı İKİ KEZ eklerdi. Burada verilen değerler "şu ana kadar toplam
// şu kadar tahsil edildi / iade edildi" demektir; aynı değerin ikinci kez
// bildirilmesi zararsızdır.
//
// Değerler kaydın ÜZERİNE YAZILMAZ, onunla BİRLEŞTİRİLİR; gerekçe için bkz.
// [Service.SetOrderSummaryTotals].
type SummaryTotalsInput struct {
	// PaidTotal siparişe karşılık TAHSİL EDİLEN toplam tutardır (minor unit).
	PaidTotal int64
	// RefundedTotal müşteriye GERİ ÖDENEN toplam tutardır (minor unit).
	RefundedTotal int64
}

// GetOrderSummary siparişin ödeme/iade özetini döner.
//
// Özet siparişle birlikte doğduğu için var olan her sipariş için bulunur;
// NotFound yalnızca sipariş yoksa (ya da doğrudan SQL ile özet silinmişse)
// döner.
func (s *Service) GetOrderSummary(ctx context.Context, orderID string) (models.OrderSummary, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderSummary{}, err
	}
	return s.store.GetSummary(ctx, orderID)
}

// SetOrderSummaryTotals siparişin ödenen ve iade edilen tutarlarını bildirir.
//
// # Bu yüzeyi kim çağırır
//
// payment modülü DEĞİL: iki modül birbirini tanımaz (Prensip 2.1/2.4). Tahsilat
// sonucunu bilen taraf complete_cart workflow'u ya da ödeme olaylarını dinleyen
// bir abonedir ve buraya container'dan çözdüğü dar bir arayüzle gelir.
//
// # Yazma neden BİRLEŞTİRME, üzerine yazma değil
//
// Bildirilen değerler kaydın üzerine yazılmaz; her alan için kayıtlı değerle
// bildirilen değerin BÜYÜĞÜ saklanır. Sebep, çağıranın olay veri yolundan
// beslenmesidir: teslim EN AZ BİR KEZDİR ve sıra GARANTİ EDİLMEZ. Üzerine
// yazan bir uçta gecikmiş bir tahsilat olayının yeniden işlenmesi, ondan sonra
// kaydedilmiş bir iadeyi sessizce sıfırlardı — çağrı hatasız döner, kayıtlı
// para kaybolurdu.
//
// İki tutar da siparişin ÖMÜR BOYU toplamıdır ve doğaları gereği yalnızca
// büyür; bu yüzden birleştirme veri kaybetmez ve çağrı hem idempotent hem de
// SIRADAN BAĞIMSIZ olur. Küçülten bir bildirim hata DÖNMEZ, yok sayılır ve
// DEBUG seviyesinde loglanır: gecikmiş teslim bir olgudur, çağıranın hatası
// değildir ve hata dönmek aboneyi sonsuz yeniden denemeye sokardı. Yanlış
// yazılmış bir tutarın düzeltilmesi bu yüzeyin işi DEĞİLDİR.
//
// # Neden siparişin kilidi altında
//
// Modülün her yazan akışı siparişi kilitleyerek başlar (bkz. [Store]); para
// yazan bu yol da istisna değildir. Kilit ayrıca siparişin GERÇEKTEN var
// olduğunu doğrular: kilitsiz hâlde yalnızca özet satırı aranırdı ve eksik
// siparişin hatası "özet bulunamadı" gibi görünürdü.
//
// # Neden sipariş toplamıyla karşılaştırılmaz
//
// PaidTotal'ın sipariş toplamını AŞMASI reddedilmez. Fazla tahsilat gerçek bir
// olgudur (kur farkı, sağlayıcı tarafında yapılan düzeltme) ve onu reddetmek,
// gerçekte olmuş bir tahsilatı kayda geçirememek demek olurdu. Fark
// [models.OrderSummary.Outstanding] üzerinden NEGATİF olarak görünür; sıfıra
// kırpılmaz, çünkü kırpmak fazla tahsilatı görünmez kılardı.
//
// Tek yapısal kural, iade edilenin tahsil edileni aşamamasıdır: tahsil
// edilmemiş bir tutar iade edilemez. Kural veritabanında da durur
// (order_summaries_refund_within_paid) ve birleştirme onu kıramaz.
func (s *Service) SetOrderSummaryTotals(ctx context.Context, orderID string, in SummaryTotalsInput) (models.OrderSummary, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderSummary{}, err
	}
	if err := checkAmount("paid_total", in.PaidTotal, models.MaxTotal); err != nil {
		return models.OrderSummary{}, err
	}
	if err := checkAmount("refunded_total", in.RefundedTotal, models.MaxTotal); err != nil {
		return models.OrderSummary{}, err
	}
	if in.RefundedTotal > in.PaidTotal {
		return models.OrderSummary{}, errors.Invalid(CodeSummaryInvalid,
			"iade edilen tutar tahsil edileni aşamaz: refunded_total=%d, paid_total=%d",
			in.RefundedTotal, in.PaidTotal)
	}

	var merged models.OrderSummary
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		merged, err = s.store.SetSummaryTotals(ctx, orderID, in.PaidTotal, in.RefundedTotal)
		if err != nil {
			return err
		}
		if merged.PaidTotal != in.PaidTotal || merged.RefundedTotal != in.RefundedTotal {
			s.log.DebugContext(ctx, "gecikmiş özet bildirimi yok sayıldı, kayıtlı tutarlar korundu",
				"order_id", orderID, "display_id", order.DisplayID,
				"bildirilen_paid", in.PaidTotal, "bildirilen_refunded", in.RefundedTotal,
				"kayitli_paid", merged.PaidTotal, "kayitli_refunded", merged.RefundedTotal)
		}
		return nil
	})
	if err != nil {
		return models.OrderSummary{}, err
	}
	return merged, nil
}

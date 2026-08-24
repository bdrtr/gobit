package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// Bu dosya satış sonrası kayıtlarının (iade, değişim, hasar) İSKELETİDİR
// (plan Bölüm 6, Faz 6 kapsamı).
//
// Üçü de aynı örüntüyü paylaşır: kayıt "talep edildi" durumunda doğar,
// listelenir ve tekil okunur. Durum geçişleri, satır bazlı iade, stoğun geri
// alınması ve ödemenin iadesi SONRAKİ FAZLARIN işidir; bu yüzden burada geçiş
// metodu yoktur. İskeletin şimdi kurulmasının sebebi, siparişin şemasının ve
// API zarfının o fazda değişmek zorunda kalmamasıdır.
//
// # Neden iptal edilmiş siparişe kayıt açılamaz
//
// Üç oluşturma da siparişin kilidini alır ve iptal edilmiş siparişi reddeder.
// İptal edilmiş bir siparişte teslim edilmiş mal yoktur: iade edilecek, değişecek
// ya da hasar görecek bir şey de yoktur. Kilit, kontrolü YARIŞSIZ kılar —
// kilitsiz okuma ile yazma arasında sipariş iptal edilebilir ve kayıt iptal
// edilmiş bir siparişe bağlanabilirdi.
//
// Siparişin VARLIĞI ayrıca kontrol edilmez; kilit zaten NotFound döner.
//
// # İade tutarının tavanı
//
// İade/hasar kaydının tutarı siparişin TOPLAMINI aşamaz: satılmamış bir malın
// parası geri verilemez. Kontrol kilit altında, siparişin okunmuş hâline karşı
// yapılır (bkz. [Service.requireLiveOrder]).
//
// Tavanın özetteki paid_total olmaması bilinçlidir: kayıt bir TALEPTİR ve
// tahsilat henüz yazılmamışken de açılabilmelidir; ödemeyle ilişkilendirme
// iade akışının (sonraki faz) işidir. Kural veritabanı kısıtına da
// çevrilemez — CHECK tek satır içinde kalır, order_returns.refund_amount ile
// orders.total FARKLI tablolardadır ve bunu zorlamanın tek yolu bir trigger
// olurdu.

// CreateReturnInput yeni bir iade kaydının girdisidir.
type CreateReturnInput struct {
	// OrderID iadenin ait olduğu siparişdir; ZORUNLUDUR.
	OrderID string
	// RefundAmount iade edilmesi planlanan tutardır (minor unit).
	RefundAmount int64
	// Reason iade gerekçesidir; opsiyoneldir.
	Reason string
	// Note serbest nottur; opsiyoneldir.
	Note string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateReturn siparişe bir iade kaydı açar.
//
// Kayıt daima [models.ReturnRequested] durumunda doğar: iade bir TALEPTİR ve
// alındı damgası malın gerçekten teslim alınmasıyla, sonraki fazın iş akışında
// vurulur.
func (s *Service) CreateReturn(ctx context.Context, in CreateReturnInput) (models.Return, error) {
	if err := requireID("order_id", in.OrderID); err != nil {
		return models.Return{}, err
	}
	if err := checkAmount("refund_amount", in.RefundAmount, models.MaxTotal); err != nil {
		return models.Return{}, err
	}
	if err := checkTextLen("reason", in.Reason); err != nil {
		return models.Return{}, err
	}
	if err := checkTextLen("note", in.Note); err != nil {
		return models.Return{}, err
	}

	var created models.Return
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.requireLiveOrder(ctx, in.OrderID, "iade kaydı")
		if err != nil {
			return err
		}
		if err := checkRefundWithinOrder(order, in.RefundAmount); err != nil {
			return err
		}
		created, err = s.store.CreateReturn(ctx, models.Return{
			ID:           models.NewReturnID(),
			OrderID:      in.OrderID,
			Status:       models.ReturnRequested,
			RefundAmount: in.RefundAmount,
			Reason:       in.Reason,
			Note:         in.Note,
			Metadata:     in.Metadata,
		})
		return err
	})
	if err != nil {
		return models.Return{}, err
	}
	return created, nil
}

// GetReturn iade kaydını kimliğiyle döner.
func (s *Service) GetReturn(ctx context.Context, returnID string) (models.Return, error) {
	if err := requireID("return_id", returnID); err != nil {
		return models.Return{}, err
	}
	return s.store.GetReturn(ctx, returnID)
}

// ListReturns siparişin iade kayıtlarını sayfalayarak döner; ikinci değer
// toplam sayıdır.
func (s *Service) ListReturns(ctx context.Context, orderID string, page Page) ([]models.Return, int64, error) {
	filter, err := childFilter(orderID, page)
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListReturns(ctx, filter)
}

// CreateExchangeInput yeni bir değişim kaydının girdisidir.
type CreateExchangeInput struct {
	// OrderID değişimin ait olduğu siparişdir; ZORUNLUDUR.
	OrderID string
	// DifferenceDue değişim farkıdır (minor unit) ve NEGATİF OLABİLİR:
	// pozitifse fark müşteriden tahsil edilir, negatifse müşteriye ödenir.
	DifferenceDue int64
	// Note serbest nottur; opsiyoneldir.
	Note string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateExchange siparişe bir değişim kaydı açar.
func (s *Service) CreateExchange(ctx context.Context, in CreateExchangeInput) (models.Exchange, error) {
	if err := requireID("order_id", in.OrderID); err != nil {
		return models.Exchange{}, err
	}
	if err := checkSignedAmount("difference_due", in.DifferenceDue, models.MaxTotal); err != nil {
		return models.Exchange{}, err
	}
	if err := checkTextLen("note", in.Note); err != nil {
		return models.Exchange{}, err
	}

	var created models.Exchange
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.requireLiveOrder(ctx, in.OrderID, "değişim kaydı"); err != nil {
			return err
		}
		var err error
		created, err = s.store.CreateExchange(ctx, models.Exchange{
			ID:            models.NewExchangeID(),
			OrderID:       in.OrderID,
			Status:        models.ExchangeRequested,
			DifferenceDue: in.DifferenceDue,
			Note:          in.Note,
			Metadata:      in.Metadata,
		})
		return err
	})
	if err != nil {
		return models.Exchange{}, err
	}
	return created, nil
}

// GetExchange değişim kaydını kimliğiyle döner.
func (s *Service) GetExchange(ctx context.Context, exchangeID string) (models.Exchange, error) {
	if err := requireID("exchange_id", exchangeID); err != nil {
		return models.Exchange{}, err
	}
	return s.store.GetExchange(ctx, exchangeID)
}

// ListExchanges siparişin değişim kayıtlarını sayfalayarak döner; ikinci değer
// toplam sayıdır.
func (s *Service) ListExchanges(ctx context.Context, orderID string, page Page) ([]models.Exchange, int64, error) {
	filter, err := childFilter(orderID, page)
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListExchanges(ctx, filter)
}

// CreateClaimInput yeni bir hasar kaydının girdisidir.
type CreateClaimInput struct {
	// OrderID talebin ait olduğu siparişdir; ZORUNLUDUR.
	OrderID string
	// Type talebin nasıl karşılanacağıdır; ZORUNLUDUR.
	Type models.ClaimType
	// RefundAmount Type [models.ClaimRefund] iken iade edilecek tutardır.
	RefundAmount int64
	// Reason talebin gerekçesidir; opsiyoneldir.
	Reason string
	// Note serbest nottur; opsiyoneldir.
	Note string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateClaim siparişe bir hasar/eksik kaydı açar.
//
// [models.ClaimReplace] türünde bir talep için tutar SIFIR olmalıdır: yerine
// yenisi gönderilen bir talepte iade edilecek para yoktur ve dolu bir tutar,
// müşterinin hem malı hem parayı aldığı sessiz bir çift ödeme anlamına gelirdi.
func (s *Service) CreateClaim(ctx context.Context, in CreateClaimInput) (models.Claim, error) {
	if err := requireID("order_id", in.OrderID); err != nil {
		return models.Claim{}, err
	}
	if !in.Type.Valid() {
		return models.Claim{}, errors.Invalid(CodeInvalidInput,
			"tanımsız hasar kaydı türü: %q (geçerli: %q, %q)",
			in.Type, models.ClaimRefund, models.ClaimReplace)
	}
	if err := checkAmount("refund_amount", in.RefundAmount, models.MaxTotal); err != nil {
		return models.Claim{}, err
	}
	if in.Type == models.ClaimReplace && in.RefundAmount != 0 {
		return models.Claim{}, errors.Invalid(CodeInvalidInput,
			"%q türünde talepte refund_amount sıfır olmalı: %d", models.ClaimReplace, in.RefundAmount)
	}
	if err := checkTextLen("reason", in.Reason); err != nil {
		return models.Claim{}, err
	}
	if err := checkTextLen("note", in.Note); err != nil {
		return models.Claim{}, err
	}

	var created models.Claim
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.requireLiveOrder(ctx, in.OrderID, "hasar kaydı")
		if err != nil {
			return err
		}
		if err := checkRefundWithinOrder(order, in.RefundAmount); err != nil {
			return err
		}
		created, err = s.store.CreateClaim(ctx, models.Claim{
			ID:           models.NewClaimID(),
			OrderID:      in.OrderID,
			Type:         in.Type,
			Status:       models.ClaimRequested,
			RefundAmount: in.RefundAmount,
			Reason:       in.Reason,
			Note:         in.Note,
			Metadata:     in.Metadata,
		})
		return err
	})
	if err != nil {
		return models.Claim{}, err
	}
	return created, nil
}

// GetClaim hasar kaydını kimliğiyle döner.
func (s *Service) GetClaim(ctx context.Context, claimID string) (models.Claim, error) {
	if err := requireID("claim_id", claimID); err != nil {
		return models.Claim{}, err
	}
	return s.store.GetClaim(ctx, claimID)
}

// ListClaims siparişin hasar kayıtlarını sayfalayarak döner; ikinci değer
// toplam sayıdır.
func (s *Service) ListClaims(ctx context.Context, orderID string, page Page) ([]models.Claim, int64, error) {
	filter, err := childFilter(orderID, page)
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListClaims(ctx, filter)
}

// requireLiveOrder siparişi KİLİTLER, iptal edilmiş olmadığını doğrular ve
// kilit altındaki hâlini döner.
//
// Kilit, kontrol ile kaydın yazılması arasına araya giren bir iptalin
// girmesini engeller; kilitsiz bir kontrol yalnızca "o an" doğru olurdu.
//
// Siparişin DÖNÜLMESİ, tutar kontrollerinin aynı kilit altında ve aynı okunmuş
// hâl üzerinde yapılabilmesi içindir; ikinci bir okuma bayat olabilirdi.
func (s *Service) requireLiveOrder(ctx context.Context, orderID, what string) (models.Order, error) {
	order, err := s.store.LockOrder(ctx, orderID)
	if err != nil {
		return models.Order{}, err
	}
	if order.Canceled() {
		return models.Order{}, errors.Conflict(CodeNotPending,
			"iptal edilmiş siparişe %s açılamaz: %s", what, orderID)
	}
	return order, nil
}

// checkRefundWithinOrder iade tutarının siparişin toplamını aşmadığını
// doğrular.
//
// Sıfır tutar her zaman geçerlidir: yerine yenisi gönderilen bir hasar
// kaydında ([models.ClaimReplace]) iade edilecek para yoktur.
func checkRefundWithinOrder(order models.Order, refundAmount int64) error {
	if refundAmount > order.Total {
		return errors.Invalid(CodeRefundExceedsOrder,
			"iade tutarı siparişin toplamını aşamaz: refund_amount=%d, sipariş toplamı=%d (%s)",
			refundAmount, order.Total, order.ID)
	}
	return nil
}

// childFilter iade/değişim/hasar listelemesinin ölçütünü doğrular ve kurar.
func childFilter(orderID string, page Page) (models.ChildFilter, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.ChildFilter{}, err
	}
	normalized, err := page.normalize()
	if err != nil {
		return models.ChildFilter{}, err
	}
	return models.ChildFilter{
		OrderID: orderID,
		Limit:   normalized.Limit,
		Offset:  normalized.Offset,
	}, nil
}

// checkSignedAmount İŞARETLİ bir tutarın büyüklüğünü doğrular.
//
// [checkAmount]'tan ayrıdır çünkü negatif değeri REDDETMEZ: değişim farkı iki
// yönde de doğabilir (bkz. [models.Exchange.DifferenceDue]). Doğrulanan şey
// büyüklüğün sınır içinde kalmasıdır; en küçük int64 için mutlak değer
// alınamayacağı için karşılaştırma iki uçtan ayrı yapılır.
func checkSignedAmount(label string, value, upper int64) error {
	if value > upper || value < -upper {
		return errors.Invalid(CodeInvalidInput,
			"%s -%d ile %d arasında olmalı: %d", label, upper, upper, value)
	}
	return nil
}

package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// RedeemInput bir kupon kullanımının girdisidir.
//
// Promosyon ya kimlikle ya kodla adlandırılır; ikisi birden verilirse KİMLİK
// kazanır (daha kesindir) ve kod yalnızca doğrulanır.
type RedeemInput struct {
	// PromotionID kullanılacak promosyonun kimliğidir; boşsa [Code] kullanılır.
	PromotionID string
	// Code kullanılacak promosyonun kupon kodudur; [PromotionID] boşsa
	// ZORUNLUDUR.
	Code string
	// Reference kullanımın hangi iş kaydına ait olduğudur (örn. sipariş
	// kimliği). İdempotency ANAHTARIDIR: aynı referansla ikinci çağrı sayacı
	// artırmaz.
	Reference string
	// Amount fiilen uygulanan indirim tutarıdır (minor unit); para ölçülü
	// kampanya bütçesi bu kadar tüketilir.
	Amount int64
	// CurrencyCode indirimin para birimidir (ISO 4217).
	CurrencyCode string
}

// RedeemPromotion promosyonu bir referans için kullanır ve sayaçları artırır.
//
// # İDEMPOTENTTİR
//
// Aynı [RedeemInput.Reference] ile ikinci çağrı YENİ kullanım yazmaz ve hiçbir
// sayacı artırmaz; var olan kaydı döner. Sipariş tamamlama saga'sı bir adımı
// yeniden çalıştırabilir ve tekrar, kuponun ikinci kez harcanması anlamına
// GELMEMELİDİR.
//
// # Reddedilme sebepleri
//
// Şu durumlarda errors.Conflict döner ve HİÇBİR ŞEY yazılmaz:
//
//   - Promosyon YAYINDA DEĞİLDİR (taslak ya da pasif). Yayına hiç alınmamış bir
//     promosyonun kuponu tüketilemez ve kampanya bütçesi yenemez.
//   - Kampanyası varsa: kampanya SİLİNMİŞTİR, tarih penceresi kullanım anını
//     KAPSAMAZ ya da bütçesinin para birimi kullanımınkiyle uyuşmaz.
//   - Kullanım hakkı dolmuştur ya da kampanya bütçesi yetmez.
//
// Denetimlerin HEPSİ veritabanında, satır kilidi altında yapılır (bkz.
// repository.Redeem); sayaç sınırları ayrıca koşullu UPDATE ile zorlanır.
// Uygulamada "önce oku sonra yaz" biçiminde yapılsaydı, iki eşzamanlı kullanım
// aynı son hakkı alabilirdi.
//
// Denetimler idempotency'den SONRA gelir: aynı referansla yapılan ikinci çağrı,
// promosyon bu arada durdurulmuş olsa bile var olan kaydı döner. Gerekçe
// repository.Redeem godoc'undadır.
//
// Eleme burada [Service.ComputeDiscounts]'takiyle aynı kapı DEĞİLDİR: hesap yan
// etkisizdir ve elemesi müşteriye ne gösterileceğine karar verir; buradaki
// denetim ise sayaca yazma anının hakemidir ve hesapla kullanım arasında
// değişen bir durumu yakalayan TEK yerdir.
//
// # Neden hesap yeniden yapılmaz
//
// Tutar ÇAĞIRANDAN gelir; servis onu [Service.ComputeDiscounts] ile yeniden
// hesaplamaz. Sebep, hesabın sepetin o anki şekline dayanmasıdır: sepet
// kullanım anında değişmiş olabilir ve burada yapılacak ikinci bir hesap,
// müşteriye gösterilenden farklı bir tutarı bütçeye yazardı. Tutarın doğruluğu
// çağıranın (sipariş tamamlama akışının) sorumluluğundadır; bu modül yalnızca
// yazılanı defterler.
func (s *Service) RedeemPromotion(ctx context.Context, in RedeemInput) (models.Redemption, error) {
	if err := s.ready(); err != nil {
		return models.Redemption{}, err
	}

	promo, err := s.resolvePromotion(ctx, in.PromotionID, in.Code)
	if err != nil {
		return models.Redemption{}, err
	}
	if err := validateText("kullanım referansı", in.Reference, 1, MaxReferenceLen); err != nil {
		return models.Redemption{}, err
	}
	if err := validateAmount("indirim tutarı", in.Amount); err != nil {
		return models.Redemption{}, err
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.Redemption{}, err
	}

	now := s.clock()
	redemption, created, err := s.repo.Redeem(ctx, models.Redemption{
		ID:           models.NewRedemptionID(now),
		PromotionID:  promo.ID,
		Reference:    in.Reference,
		Amount:       in.Amount,
		CurrencyCode: currency,
	}, now)
	if err != nil {
		return models.Redemption{}, err
	}

	// Tutar loglanır çünkü bütçe muhasebesinin izini sürmenin tek yolu budur;
	// kupon KODU loglanmaz (kullanılabilir bir sırdır, plan Bölüm 8).
	s.log.DebugContext(ctx, "promosyon kullanıldı",
		slog.String("promotion_id", promo.ID),
		slog.String("reference", in.Reference),
		slog.Bool("yeni_kayit", created),
		slog.Int64("tutar", redemption.Amount),
	)
	return redemption, nil
}

// ReleaseInput bir kupon kullanımının geri alınması girdisidir.
type ReleaseInput struct {
	// PromotionID promosyonun kimliğidir; boşsa [Code] kullanılır.
	PromotionID string
	// Code promosyonun kupon kodudur; [PromotionID] boşsa ZORUNLUDUR.
	Code string
	// Reference geri alınacak kullanımın referansıdır.
	Reference string
}

// ReleasePromotion bir kullanımı serbest bırakır ve sayaçları geri alır.
//
// # SAGA TELAFİSİDİR ve İDEMPOTENTTİR
//
// İki kez çağrılırsa ikinci çağrı hata VERMEZ ve sayaçlar ikinci kez düşmez.
// Hiç kullanım yazılmamışsa da hata dönmez: telafi, yazmadan patlamış bir
// adımın ardından da çalışabilmelidir (plan Bölüm 5.5).
//
// Promosyonun KENDİSİ yoksa errors.NotFound döner. Bu, sessizce yutulmaması
// gereken bir kurulum hatasıdır: var olmayan bir promosyonun telafisi, yanlış
// kimlikle çağrılmış bir adım demektir.
//
// İkinci dönüş değeri BU ÇAĞRIDA bir şeyin geri alınıp alınmadığını bildirir;
// telafinin gerçekten iş yaptığını sınayan testler buna bakar.
func (s *Service) ReleasePromotion(ctx context.Context, in ReleaseInput) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}

	promo, err := s.resolvePromotion(ctx, in.PromotionID, in.Code)
	if err != nil {
		return false, err
	}
	if err := validateText("kullanım referansı", in.Reference, 1, MaxReferenceLen); err != nil {
		return false, err
	}

	_, released, err := s.repo.Release(ctx, promo.ID, in.Reference, s.clock())
	if err != nil {
		return false, err
	}

	s.log.DebugContext(ctx, "promosyon kullanımı serbest bırakıldı",
		slog.String("promotion_id", promo.ID),
		slog.String("reference", in.Reference),
		slog.Bool("geri_alindi", released),
	)
	return released, nil
}

// GetRedemption bir referansın GEÇERLİ kullanımını döner; yoksa errors.NotFound.
func (s *Service) GetRedemption(ctx context.Context, promotionID, reference string) (models.Redemption, error) {
	if err := s.ready(); err != nil {
		return models.Redemption{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.Redemption{}, err
	}
	if err := validateText("kullanım referansı", reference, 1, MaxReferenceLen); err != nil {
		return models.Redemption{}, err
	}
	return s.repo.GetRedemption(ctx, promotionID, reference)
}

// ListRedemptions bir promosyonun kullanım defterini sayfalanmış döner.
//
// Serbest bırakılmış kayıtlar da döner: defter bir geçmiştir ve geri alınmış
// bir kullanımın izi silinmemelidir.
func (s *Service) ListRedemptions(
	ctx context.Context,
	promotionID string,
	limit, offset int32,
) (Page[models.Redemption], error) {
	if err := s.ready(); err != nil {
		return Page[models.Redemption]{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return Page[models.Redemption]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.Redemption]{}, err
	}
	if _, err := s.repo.GetPromotion(ctx, promotionID); err != nil {
		return Page[models.Redemption]{}, err
	}

	items, total, err := s.repo.ListRedemptions(ctx, promotionID, limit, offset)
	if err != nil {
		return Page[models.Redemption]{}, err
	}
	return Page[models.Redemption]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// resolvePromotion promosyonu kimlikten ya da koddan çözer.
//
// Kimlik verildiyse o kullanılır; kod da verilmişse promosyonun kodu ile
// EŞLEŞMESİ beklenir. Uyumsuzluk sessizce yok sayılmaz: iki farklı promosyonu
// adlandıran bir istek, çağıranın hangisini kastettiğini bilmediği anlamına
// gelir ve sayaç yanlış promosyona yazılırdı.
func (s *Service) resolvePromotion(ctx context.Context, id, code string) (models.Promotion, error) {
	switch {
	case id != "":
		if err := requireID(id, models.PromotionIDPrefix, "promotion id"); err != nil {
			return models.Promotion{}, err
		}
		promo, err := s.repo.GetPromotion(ctx, id)
		if err != nil {
			return models.Promotion{}, err
		}
		if code != "" {
			normalized, codeErr := normalizeCode(code)
			if codeErr != nil {
				return models.Promotion{}, codeErr
			}
			if normalized != promo.Code {
				return models.Promotion{}, errors.Invalid(CodeInvalidInput,
					"promotion id ile kupon kodu farklı promosyonları gösteriyor: %s / %s",
					id, normalized)
			}
		}
		return promo, nil
	case code != "":
		normalized, err := normalizeCode(code)
		if err != nil {
			return models.Promotion{}, err
		}
		return s.repo.GetPromotionByCode(ctx, normalized)
	default:
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"promotion id ya da kupon kodu verilmeli")
	}
}

package service

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// RuleInput tek bir promosyon kuralının yazma girdisidir.
type RuleInput struct {
	// RuleType kuralın neye baktığıdır (context | target).
	RuleType models.RuleType
	// Attribute bağlamda ya da kalemde bakılacak alan adıdır.
	Attribute string
	// Operator karşılaştırma işlecidir.
	Operator models.RuleOperator
	// Values karşılaştırmanın sağ tarafıdır; en az bir eleman içermelidir.
	Values []string
}

// ApplicationMethodInput bir uygulama yönteminin yazma girdisidir.
type ApplicationMethodInput struct {
	// Type indirimin ölçüsüdür (fixed | percentage).
	Type models.ApplicationMethodType
	// TargetType indirimin hedefidir (items | shipping_methods | order).
	TargetType models.ApplicationTargetType
	// Allocation dağıtım biçimidir; boş verilirse "each" kabul edilir,
	// hedef "order" ise "across"a zorlanır.
	Allocation models.Allocation
	// Value sabit tutar (minor unit) ya da baz puandır ([Type]'a göre).
	Value int64
	// MaxQuantity sabit tutarın uygulanacağı azami adettir; nil ise sınırsız.
	MaxQuantity *int64
	// CurrencyCode "fixed" indirimin para birimidir; "percentage"ta verilmemelidir.
	CurrencyCode string
}

// AddPromotionRule bir promosyona kural ekler; promosyon yoksa ya da silinmişse
// errors.NotFound döner.
//
// Promosyonun CANLI olduğu denetimi burada DEĞİL, yazmayla aynı işlemde ve
// satır kilidi altında yapılır (bkz. repository.CreatePromotionRule).
// Reddedilen alternatif — ve bu metodun bir süre yaptığı şey — denetimi burada,
// ayrı bir okumayla yapmaktı: o biçimde okuma ile yazma iki AYRI autocommit
// deyimidir ve araya giren bir yumuşak silme, kuralın silinmiş bir promosyonun
// altına inmesine izin verir. Foreign key bunu durdurmaz; yumuşak silme satırı
// yerinde bırakır (ölçüldü, 2026-09-06).
func (s *Service) AddPromotionRule(
	ctx context.Context,
	promotionID string,
	in RuleInput,
) (models.PromotionRule, error) {
	if err := s.ready(); err != nil {
		return models.PromotionRule{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.PromotionRule{}, err
	}
	if err := validateRuleInput(in); err != nil {
		return models.PromotionRule{}, err
	}

	now := s.clock()
	return s.repo.CreatePromotionRule(ctx, models.PromotionRule{
		ID:          models.NewPromotionRuleID(now),
		PromotionID: promotionID,
		RuleType:    in.RuleType,
		Attribute:   in.Attribute,
		Operator:    in.Operator,
		Values:      slices.Clone(in.Values),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, now)
}

// GetPromotionRule kimliğe göre kuralı döner; yoksa errors.NotFound.
func (s *Service) GetPromotionRule(ctx context.Context, id string) (models.PromotionRule, error) {
	if err := s.ready(); err != nil {
		return models.PromotionRule{}, err
	}
	if err := requireID(id, models.PromotionRuleIDPrefix, "promosyon kuralı kimliği"); err != nil {
		return models.PromotionRule{}, err
	}
	return s.repo.GetPromotionRule(ctx, id)
}

// ListPromotionRules bir promosyonun kurallarını döner.
//
// YÖNETİM yüzeyi içindir. Kurallar müşteriye SIZDIRILMAZ: bir kuralın sağ
// tarafı (örn. bir müşteri grubunun kimliği ya da bir segment listesi) iş
// bilgisidir ve store yüzeyinde hiçbir uç nokta onu dönmez.
func (s *Service) ListPromotionRules(ctx context.Context, promotionID string) ([]models.PromotionRule, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return nil, err
	}
	// Promosyonun varlığı doğrulanır: olmayan bir promosyonun kuralları boş
	// dilim olarak dönerse istemci 404 yerine "kuralı yok" sanırdı.
	if _, err := s.repo.GetPromotion(ctx, promotionID); err != nil {
		return nil, err
	}
	return s.repo.ListPromotionRules(ctx, promotionID)
}

// DeletePromotionRule kuralı soft delete ile siler.
func (s *Service) DeletePromotionRule(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PromotionRuleIDPrefix, "promosyon kuralı kimliği"); err != nil {
		return err
	}
	return s.repo.DeletePromotionRule(ctx, id, s.clock())
}

// SetApplicationMethod promosyonun uygulama yöntemini yazar; varsa üzerine
// yazar. Promosyon yoksa ya da silinmişse errors.NotFound döner.
//
// Promosyonun canlı olduğu denetimi burada DEĞİL, yazmayla aynı işlemde ve
// satır kilidi altında yapılır; gerekçe [Service.AddPromotionRule] ile aynıdır.
func (s *Service) SetApplicationMethod(
	ctx context.Context,
	promotionID string,
	in ApplicationMethodInput,
) (models.ApplicationMethod, error) {
	if err := s.ready(); err != nil {
		return models.ApplicationMethod{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.ApplicationMethod{}, err
	}

	now := s.clock()
	method, err := buildApplicationMethod(models.NewApplicationMethodID(now), promotionID, in, now)
	if err != nil {
		return models.ApplicationMethod{}, err
	}
	return s.repo.SetApplicationMethod(ctx, method, now)
}

// GetApplicationMethod promosyonun uygulama yöntemini döner; yoksa
// errors.NotFound.
func (s *Service) GetApplicationMethod(ctx context.Context, promotionID string) (models.ApplicationMethod, error) {
	if err := s.ready(); err != nil {
		return models.ApplicationMethod{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.ApplicationMethod{}, err
	}
	return s.repo.GetApplicationMethod(ctx, promotionID)
}

// DeleteApplicationMethod yöntemi soft delete ile siler.
//
// Yöntemsiz kalan promosyon indirim üretmez ve hesapta atlanır; bu, promosyonu
// silmeden geçici olarak etkisizleştirmenin yoludur.
func (s *Service) DeleteApplicationMethod(ctx context.Context, promotionID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return err
	}
	return s.repo.DeleteApplicationMethod(ctx, promotionID, s.clock())
}

// buildApplicationMethod girdiyi doğrular ve yazılacak domain modeline çevirir.
//
// Türe bağlı üç kural zorlanır ve üçü de migration'daki CHECK kısıtlarıyla
// eşleşir:
//
//   - "fixed" para birimi İSTER ve değeri [models.MaxAmount]'u aşamaz.
//   - "percentage" para birimi TAŞIMAZ ve değeri
//     [models.BasisPointDenominator]'ı (yani %100'ü) aşamaz.
//   - Hedef "order" ise tahsis "across"tır: sipariş tek bir toplamdır ve
//     "her birine ayrı ayrı" orada anlamsızdır.
func buildApplicationMethod(
	id, promotionID string,
	in ApplicationMethodInput,
	now time.Time,
) (models.ApplicationMethod, error) {
	if !in.Type.Valid() {
		return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
			"uygulama yöntemi türü tanımsız: %q", string(in.Type))
	}
	if !in.TargetType.Valid() {
		return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
			"uygulama hedefi tanımsız: %q", string(in.TargetType))
	}

	allocation := in.Allocation
	if allocation == "" {
		allocation = models.AllocationEach
	}
	if !allocation.Valid() {
		return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
			"tahsis biçimi tanımsız: %q", string(in.Allocation))
	}
	if in.TargetType == models.TargetOrder {
		// Sessizce düzeltmek yerine REDDETMEK seçilmiştir: "each" isteyen bir
		// operatör, siparişin tamamına kalem başına indirim uygulanacağını
		// sanıyor olabilir ve sessiz düzeltme o yanılgıyı sürdürürdü.
		if in.Allocation != "" && in.Allocation != models.AllocationAcross {
			return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
				"sipariş hedefli indirim yalnızca %q tahsisiyle uygulanır, %q verildi",
				string(models.AllocationAcross), string(in.Allocation))
		}
		allocation = models.AllocationAcross
	}

	if in.MaxQuantity != nil {
		if err := validateQuantity("azami adet", *in.MaxQuantity); err != nil {
			return models.ApplicationMethod{}, err
		}
	}

	currency := ""
	switch in.Type {
	case models.MethodFixed:
		if err := validateAmount("indirim tutarı", in.Value); err != nil {
			return models.ApplicationMethod{}, err
		}
		code, err := normalizeCurrency(in.CurrencyCode)
		if err != nil {
			return models.ApplicationMethod{}, err
		}
		currency = code
	case models.MethodPercentage:
		if in.Value < 0 || in.Value > models.BasisPointDenominator {
			return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
				"yüzde indirim [0, %d] baz puan aralığında olmalı, %d verildi",
				models.BasisPointDenominator, in.Value)
		}
		if in.CurrencyCode != "" {
			return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
				"yüzde indirimde para birimi verilemez, %q verildi", in.CurrencyCode)
		}
	}

	return models.ApplicationMethod{
		ID:           id,
		PromotionID:  promotionID,
		Type:         in.Type,
		TargetType:   in.TargetType,
		Allocation:   allocation,
		Value:        in.Value,
		MaxQuantity:  copyInt64(in.MaxQuantity),
		CurrencyCode: currency,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// matchRules verilen kuralların HEPSİNİN bağlamla eşleştiğini bildirir.
// Kuralsız promosyon koşulsuzdur ve daima eşleşir.
func matchRules(rules []models.PromotionRule, attributes map[string]string) bool {
	for i := range rules {
		if !matchRule(rules[i], attributes) {
			return false
		}
	}
	return true
}

// matchRule tek bir kuralın bağlamla eşleştiğini bildirir.
//
// Kuralın baktığı alan bağlamda YOKSA kural eşleşmez — "ne" (eşit değil) gibi
// olumsuz işleçlerde bile. Aksi hâlde bağlamı boş bir istek, tüm olumsuz
// kuralları sağlayarak segment indirimlerini herkese açardı.
//
// DEĞERSİZ kural da eşleşmez ve PANİK ÜRETMEZ. Böyle bir kaydı servis
// doğrulaması üretmez, ama hesaplama veritabanından okuduğu her satıra
// dayanıklı olmalıdır: doğrudan SQL çalıştıran bir bakım betiği ya da kısmi bir
// geri yükleme değerleri boş bırakabilir. Gerekçe tanınmayan işleçtekiyle
// aynıdır — okunamayan bir koşul, kuralı sessizce devre dışı bırakıp indirimi
// herkese AÇMAMALIDIR.
func matchRule(rule models.PromotionRule, attributes map[string]string) bool {
	if len(rule.Values) == 0 {
		return false
	}

	value, ok := attributes[rule.Attribute]
	if !ok {
		return false
	}

	switch rule.Operator {
	case models.OpEq:
		return value == rule.Values[0]
	case models.OpNe:
		return value != rule.Values[0]
	case models.OpIn:
		return slices.Contains(rule.Values, value)
	case models.OpNin:
		return !slices.Contains(rule.Values, value)
	case models.OpGt, models.OpGte, models.OpLt, models.OpLte:
		return matchNumeric(rule, value)
	default:
		// Tanınmayan işleç EŞLEŞMEZ: veritabanına sonradan sızmış bir değer,
		// kuralı sessizce devre dışı bırakıp indirimi herkese açık hâle
		// getirmemelidir.
		return false
	}
}

// matchNumeric sayısal işleçleri değerlendirir.
//
// İki taraf da tam sayıya çevrilebilmelidir; çevrilemeyen bir bağlam değeri
// kuralı eşleşmez yapar (hata üretmez): bağlam dışarıdan gelir ve tek bir bozuk
// alan tüm indirim hesabını düşürmemelidir.
//
// YALNIZCA matchRule'dan çağrılır ve kuralın en az bir değeri olduğu orada
// güvence altına alınmıştır; ilk değer bu yüzden doğrudan okunur.
func matchNumeric(rule models.PromotionRule, value string) bool {
	left, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false
	}
	right, err := strconv.ParseInt(rule.Values[0], 10, 64)
	if err != nil {
		return false
	}

	switch rule.Operator {
	case models.OpGt:
		return left > right
	case models.OpGte:
		return left >= right
	case models.OpLt:
		return left < right
	case models.OpLte:
		return left <= right
	case models.OpEq, models.OpNe, models.OpIn, models.OpNin:
		return false
	default:
		return false
	}
}

// Package service promotion modülünün iş mantığını barındırır.
//
// # Modüller arası yüzey (ADR 0001)
//
// promotion hiçbir modülü import ETMEZ ve hiçbir modülden veri OKUMAZ; bu
// yüzden bu pakette tüketici tarafı bir arayüz yoktur. Ters yön vardır: sepet
// akışı (internal/workflows/cart) ve sipariş tamamlama saga'sı promotion'a
// ihtiyaç duyar. O tarafın kendi paketinde dar bir arayüz tanımlayabilmesi için
// promotion'ın yüzeyi İKİYE ayrılmıştır:
//
//   - Modül içi zengin yüzey — [models] tiplerini kullanır
//     ([Service.CreatePromotion], [Service.ComputeDiscounts] …). Bu metotları
//     yalnızca promotion'ın kendi API katmanı, query sağlayıcısı ve interop
//     yüzeyi çağırır.
//   - Modüller arası yüzey — YALNIZCA ilkel ve stdlib tipleri kullanır;
//     interop.go dosyasındadır ve container'a "promotion.interop" adıyla
//     kaydedilir.
//
// Ayrım zorunludur: Go'da yapısal uyum imza EŞİTLİĞİ ister. Tüketici modül
// promotion'ı import edemediği için [models.Promotion] gibi bir tipi imzasında
// adlandıramaz; adlandırdığı an kendi paketindeki farklı bir tip olur ve somut
// servis arayüzü karşılamaz.
//
// # Para ve oran
//
// Tutarlar TAM SAYI minor unit'tir ve para birimi ayrı alandır (plan Bölüm 8).
// Oranlar BAZ PUANDIR (2000 = %20). Servis hiçbir yerde float kullanmaz;
// yuvarlama yönü [models.BasisPointDenominator] yanında belgelidir.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "promotion_invalid_input"
	// CodeBuyGetNotActivatable buyget promosyonunun bu fazda etkinleştirilemediğini
	// bildirir (bkz. [models.PromotionBuyGet]).
	CodeBuyGetNotActivatable = "promotion_buyget_not_activatable"
	// CodePromotionNotUsable promosyonun MÜŞTERİYE sunulabilir durumda olmadığını
	// bildirir; store yüzeyi bunu "yok" olarak gösterir.
	CodePromotionNotUsable = "promotion_not_usable"
	// CodeUnconfigured servisin kurulmadığını bildirir.
	CodeUnconfigured = "promotion_service_unconfigured"
)

// Sayfalama sınırları. Limit verilmezse varsayılan, aşırı büyük verilirse
// azami değer uygulanır; istemci tek istekle veritabanını tarayamaz.
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyudur.
	DefaultLimit int32 = 50
	// MaxLimit tek istekte dönebilecek azami kayıt sayısıdır.
	MaxLimit int32 = 100
)

// Page sayfalanmış bir liste sonucudur.
//
// Limit ve Offset, isteğin ham değerleri değil UYGULANAN değerlerdir; API zarfı
// bu alanları olduğu gibi yazar, böylece istemci kırpılan bir limitten haberdar
// olur.
type Page[T any] struct {
	// Items geçerli sayfadaki kayıtlardır.
	Items []T
	// Count filtreye uyan TOPLAM kayıt sayısıdır (sayfa boyu değil).
	Count int64
	// Limit uygulanan sayfa boyudur.
	Limit int32
	// Offset uygulanan atlama sayısıdır.
	Offset int32
}

// Repository servisin ihtiyaç duyduğu veri erişim yüzeyidir.
//
// Arayüz TÜKETEN tarafta (burada) tanımlıdır; somut uygulama
// internal/modules/promotion/repository paketindedir. Bu, ADR 0001'in
// örüntüsünün modül İÇİNDEKİ karşılığıdır ve servisin veritabanı olmadan test
// edilmesini sağlar.
type Repository interface {
	CreateCampaign(ctx context.Context, c models.Campaign, now time.Time) (models.Campaign, error)
	GetCampaign(ctx context.Context, id string) (models.Campaign, error)
	GetCampaignByIdentifier(ctx context.Context, identifier string) (models.Campaign, error)
	ListCampaigns(ctx context.Context, limit, offset int32) ([]models.Campaign, int64, error)
	GetCampaignsByIDs(ctx context.Context, ids []string) ([]models.Campaign, error)
	UpdateCampaign(ctx context.Context, c models.Campaign, now time.Time) (models.Campaign, error)
	DeleteCampaign(ctx context.Context, id string, now time.Time) error

	CreatePromotion(ctx context.Context, p models.Promotion, now time.Time) (models.Promotion, error)
	GetPromotion(ctx context.Context, id string) (models.Promotion, error)
	GetPromotionByCode(ctx context.Context, code string) (models.Promotion, error)
	ListPromotions(ctx context.Context, status, campaignID *string, limit, offset int32) ([]models.Promotion, int64, error)
	GetPromotionsByIDs(ctx context.Context, ids []string) ([]models.Promotion, error)
	UpdatePromotion(ctx context.Context, p models.Promotion, now time.Time) (models.Promotion, error)
	DeletePromotion(ctx context.Context, id string, now time.Time) error
	ListCandidates(ctx context.Context, codes []string) ([]models.PromotionCandidate, error)

	SetApplicationMethod(ctx context.Context, m models.ApplicationMethod, now time.Time) (models.ApplicationMethod, error)
	GetApplicationMethod(ctx context.Context, promotionID string) (models.ApplicationMethod, error)
	DeleteApplicationMethod(ctx context.Context, promotionID string, now time.Time) error

	CreatePromotionRule(ctx context.Context, rule models.PromotionRule, now time.Time) (models.PromotionRule, error)
	GetPromotionRule(ctx context.Context, id string) (models.PromotionRule, error)
	ListPromotionRules(ctx context.Context, promotionID string) ([]models.PromotionRule, error)
	DeletePromotionRule(ctx context.Context, id string, now time.Time) error

	Redeem(ctx context.Context, req models.Redemption, now time.Time) (models.Redemption, bool, error)
	Release(ctx context.Context, promotionID, reference string, now time.Time) (models.Redemption, bool, error)
	GetRedemption(ctx context.Context, promotionID, reference string) (models.Redemption, error)
	ListRedemptions(ctx context.Context, promotionID string, limit, offset int32) ([]models.Redemption, int64, error)
}

// Options servisin kurulum ayarlarıdır.
type Options struct {
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
	// Now zaman kaynağıdır; nil ise time.Now kullanılır. Testler burayı sabit
	// bir saatle doldurarak zamana bağlı dalları (kampanya penceresi)
	// belirlenimci hâle getirir.
	Now func() time.Time
}

// Service promotion modülünün public servisidir. Eşzamanlı kullanıma güvenlidir.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time
}

// New verilen depo üzerinde çalışan bir servis üretir.
//
// repo nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, log: log, now: now}
}

// ready deponun kurulu olduğunu doğrular.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable(CodeUnconfigured, "promotion servisi kurulmamış")
	}
	return nil
}

// clock geçerli anı UTC olarak döner.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// PromotionInput bir promosyonun yazma girdisidir.
type PromotionInput struct {
	// Code kupon kodudur; büyük/küçük harf serbesttir, BÜYÜK harfe
	// normalleştirilerek saklanır.
	Code string
	// IsAutomatic promosyonun kod girilmeden uygulanıp uygulanmayacağıdır.
	IsAutomatic bool
	// Type promosyonun mekaniğidir; boş verilirse "standard" kabul edilir.
	Type models.PromotionType
	// CampaignID promosyonu bir kampanyaya bağlar; nil ise kampanyasızdır.
	CampaignID *string
	// Status yayın durumudur; boş verilirse "draft" kabul edilir.
	//
	// Varsayılanın taslak olması bilinçlidir: eksik doldurulmuş bir istek
	// kazara yayına giren bir indirim üretmemelidir.
	Status models.PromotionStatus
	// UsageLimit kullanım sınırıdır; nil ise sınırsız.
	UsageLimit *int64
	// Metadata operatörün serbest notudur; iş kuralına girmez.
	Metadata map[string]string
}

// CreatePromotion yeni bir promosyon oluşturur.
//
// Kod BENZERSİZDİR; aynı kod ikinci kez alınamaz ve deneme errors.Conflict
// döner (benzersizliği veritabanı kısmi indeksi zorlar, servis değil — iki
// eşzamanlı istek arasında yalnızca veritabanı hakem olabilir).
func (s *Service) CreatePromotion(ctx context.Context, in PromotionInput) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}

	now := s.clock()
	promo, err := buildPromotion(models.NewPromotionID(now), in, now)
	if err != nil {
		return models.Promotion{}, err
	}
	return s.repo.CreatePromotion(ctx, promo, now)
}

// GetPromotion kimliğe göre promosyonu döner; yoksa errors.NotFound.
func (s *Service) GetPromotion(ctx context.Context, id string) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promosyon kimliği"); err != nil {
		return models.Promotion{}, err
	}
	return s.repo.GetPromotion(ctx, id)
}

// GetPromotionByCode kupon koduna göre promosyonu döner; yoksa errors.NotFound.
//
// YÖNETİM yüzeyi içindir ve HİÇBİR süzgeç uygulamaz: taslak ve pasif
// promosyonlar da döner. Müşteriye giden yüzey için [Service.LookupStoreCoupon]
// kullanılmalıdır.
func (s *Service) GetPromotionByCode(ctx context.Context, code string) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	normalized, err := normalizeCode(code)
	if err != nil {
		return models.Promotion{}, err
	}
	return s.repo.GetPromotionByCode(ctx, normalized)
}

// ListPromotionsInput promosyon listelemesinin isteğe bağlı süzgeçleridir.
type ListPromotionsInput struct {
	// Status yalnızca bu durumdaki promosyonları döndürür; nil ise süzülmez.
	Status *models.PromotionStatus
	// CampaignID yalnızca bu kampanyaya bağlı promosyonları döndürür; nil ise
	// süzülmez.
	CampaignID *string
	// Limit sayfa boyudur; 0 ise [DefaultLimit] uygulanır.
	Limit int32
	// Offset atlanacak kayıt sayısıdır.
	Offset int32
}

// ListPromotions sayfalanmış promosyon listesini döner.
func (s *Service) ListPromotions(ctx context.Context, in ListPromotionsInput) (Page[models.Promotion], error) {
	if err := s.ready(); err != nil {
		return Page[models.Promotion]{}, err
	}
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Promotion]{}, err
	}

	var status *string
	if in.Status != nil {
		if !in.Status.Valid() {
			return Page[models.Promotion]{}, errors.Invalid(CodeInvalidInput,
				"promosyon durumu tanımsız: %q", string(*in.Status))
		}
		value := string(*in.Status)
		status = &value
	}
	if in.CampaignID != nil {
		if err := requireID(*in.CampaignID, models.CampaignIDPrefix, "kampanya kimliği"); err != nil {
			return Page[models.Promotion]{}, err
		}
	}

	items, total, err := s.repo.ListPromotions(ctx, status, in.CampaignID, limit, offset)
	if err != nil {
		return Page[models.Promotion]{}, err
	}
	return Page[models.Promotion]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdatePromotion promosyonun tanımını YERİNE KOYAR.
//
// Kısmi güncelleme değildir: verilmeyen alanlar sıfırlanır. Sebep, kısmi
// güncellemenin "alan gönderilmedi" ile "alan boşaltılmak isteniyor" ayrımını
// istemciye bırakmasıdır; bir promosyonun kampanyasını kaldırmak da bir istek
// olabilir ve sessizce yok sayılmamalıdır.
//
// Kullanım sayacı bu yoldan DEĞİŞMEZ.
func (s *Service) UpdatePromotion(ctx context.Context, id string, in PromotionInput) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promosyon kimliği"); err != nil {
		return models.Promotion{}, err
	}

	now := s.clock()
	promo, err := buildPromotion(id, in, now)
	if err != nil {
		return models.Promotion{}, err
	}
	return s.repo.UpdatePromotion(ctx, promo, now)
}

// DeletePromotion promosyonu soft delete ile siler.
func (s *Service) DeletePromotion(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promosyon kimliği"); err != nil {
		return err
	}
	return s.repo.DeletePromotion(ctx, id, s.clock())
}

// buildPromotion girdiyi doğrular ve yazılacak domain modeline çevirir.
func buildPromotion(id string, in PromotionInput, now time.Time) (models.Promotion, error) {
	code, err := normalizeCode(in.Code)
	if err != nil {
		return models.Promotion{}, err
	}

	promoType := in.Type
	if promoType == "" {
		promoType = models.PromotionStandard
	}
	if !promoType.Valid() {
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"promosyon türü tanımsız: %q", string(in.Type))
	}

	status := in.Status
	if status == "" {
		status = models.PromotionDraft
	}
	if !status.Valid() {
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"promosyon durumu tanımsız: %q", string(in.Status))
	}

	// buyget mekaniği bu fazda YOKTUR ve eksiği sessiz bırakmamak için tür
	// yapısal olarak kapatılmıştır (bkz. [models.PromotionBuyGet]). Taslak ya da
	// pasif olarak hazırlanabilir; yayına ancak mekanik geldiğinde alınır.
	if promoType == models.PromotionBuyGet && status == models.PromotionActive {
		return models.Promotion{}, errors.Invalid(CodeBuyGetNotActivatable,
			"buyget promosyonu bu sürümde etkinleştirilemez; mekanik henüz uygulanmadı (kod: %s)", code)
	}

	if in.CampaignID != nil {
		if err := requireID(*in.CampaignID, models.CampaignIDPrefix, "kampanya kimliği"); err != nil {
			return models.Promotion{}, err
		}
	}
	if err := validateUsageLimit(in.UsageLimit); err != nil {
		return models.Promotion{}, err
	}
	metadata, err := normalizeMetadata(in.Metadata)
	if err != nil {
		return models.Promotion{}, err
	}

	return models.Promotion{
		ID:          id,
		Code:        code,
		IsAutomatic: in.IsAutomatic,
		Type:        promoType,
		CampaignID:  copyString(in.CampaignID),
		Status:      status,
		UsageLimit:  copyInt64(in.UsageLimit),
		Metadata:    metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// copyString bir dize işaretçisini KOPYALAYARAK döner.
//
// Kopya şarttır: çağıranın işaretçisi doğrudan modele konsaydı, istek nesnesini
// sonradan değiştiren bir çağıran yazılmış kaydı da değiştirmiş olurdu.
func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// copyInt64 bir tam sayı işaretçisini KOPYALAYARAK döner; gerekçe copyString'teki
// ile aynıdır.
func copyInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

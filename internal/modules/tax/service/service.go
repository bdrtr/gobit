// Package service tax modülünün iş mantığını barındırır.
//
// # Modülün devraldığı iş
//
// Faz 5'te vergi GEÇİCİ olarak region modülünde duruyordu: bölge satırında tek
// bir tax_rate (baz puan) ve automatic_taxes bayrağı vardı ve sepet akışı
// onları okuyordu. region'ın godoc'u bunu açıkça "Faz 7'de tax modülü
// devralacak" diye işaretlemişti. Bu modül o devralmayı sağlar: vergi bölgesi,
// oran ve kural verisinin TEK yazma yetkilisi burasıdır (Prensip 2.3).
//
// tax, region'ı İMPORT ETMEZ ve onun tablosunu görmez (ADR 0001). Ortak nokta
// yalnızca ISO 3166-1 ülke kodudur: sepet akışı elindeki ülke koduyla buraya
// gelir, bu modül kendi tablosundan bölgeyi çözer.
//
// # Modül içi ve modüller arası yüzey
//
// Yüzey İKİYE ayrılmıştır (region'daki örüntünün aynısı):
//
//   - Modül içi zengin yüzey — [models] tiplerini kullanır
//     ([Service.CreateTaxRegion], [Service.CalculateTax] …). Bunları yalnızca
//     tax'ın kendi API katmanı ve query sağlayıcısı çağırır.
//   - Modüller arası yüzey — YALNIZCA ilkel ve stdlib tipleri kullanır
//     (bkz. interop.go: [Interop.CalculateTaxJSON], [Interop.RateForCountry]).
//
// Ayrım zorunludur: Go'da yapısal uyum imza EŞİTLİĞİ ister. Tüketici modül
// tax'ı import edemediği için [models.TaxRegion] gibi bir tipi imzasında
// adlandıramaz; adlandırdığı an kendi paketindeki farklı bir tip olur ve somut
// servis arayüzü karşılamaz.
//
// # Sağlayıcı soyutlaması ÇEKİRDEKTE DEĞİL, BURADA
//
// Plan Bölüm 6 "TaxProvider" der, ama internal/core/provider'da bir vergi
// sağlayıcısı YOKTUR (yalnızca Payment ve Fulfillment vardır) ve bu modül
// çekirdeğe dokunamaz. Bu yüzden sözleşme bu pakette tanımlanmıştır
// ([TaxProvider], bkz. taxprovider.go) ve kutudan çıkan uygulama yerel
// hesaplamadır ([LocalProvider]).
//
// KARAR AÇIKÇA GEÇİCİDİR: sözleşme olgunlaştığında (ikinci bir gerçek
// sağlayıcı yazıldığında) internal/core/provider/tax.go'ya taşınmalıdır.
// Taşıma sırasında bu paketteki tipler çekirdektekilerin takma adı hâline
// getirilebilir; imzalar bu düşünülerek çekirdekteki PaymentProvider ve
// FulfillmentProvider ile aynı biçimde yazılmıştır (ID() string + tek bir iş
// metodu, girdi/çıktı struct'ları).
//
// # Para ve oran
//
// Vergi oranı BAZ PUANDIR ve TAM SAYIDIR (2000 = %20). Tutarlar minor unit tam
// sayıdır. Bu pakette hiçbir yerde kayan nokta kullanılmaz (plan Bölüm 8).
// Yuvarlama yönü ve nerede yapıldığı [Service.CalculateTax] godoc'unda
// belgelidir.
package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "tax_invalid_input"
	// CodeUnconfigured servisin kurulmadığını bildirir.
	CodeUnconfigured = "tax_service_unconfigured"
	// CodeRegionNotFound istenen vergi bölgesinin bulunamadığını bildirir.
	CodeRegionNotFound = "tax_region_not_found"
	// CodeParentInvalid eyalet bölgesinin kökünün geçersiz olduğunu bildirir.
	CodeParentInvalid = "tax_parent_invalid"
	// CodeRootExists ülkenin zaten bir kök vergi bölgesi olduğunu bildirir.
	CodeRootExists = "tax_region_root_exists"
	// CodeDefaultExists bölgede zaten bir varsayılan oran olduğunu bildirir.
	CodeDefaultExists = "tax_default_rate_exists"
	// CodeRateOutOfRange bir oranın sözleşme dışı olduğunu bildirir.
	CodeRateOutOfRange = "tax_rate_out_of_range"
	// CodeAmountOverflow bir tutarın izin verilen aralığı aştığını bildirir.
	CodeAmountOverflow = "tax_amount_overflow"
	// CodeProviderExists aynı kimlikli bir sağlayıcının zaten kayıtlı
	// olduğunu bildirir.
	CodeProviderExists = "tax_provider_exists"
	// CodeProviderNotFound istenen sağlayıcının kayıtlı olmadığını bildirir.
	CodeProviderNotFound = "tax_provider_not_found"
	// CodeProviderMisconfigured bölgenin kayıtlı OLMAYAN bir sağlayıcıya
	// işaret ettiğini bildirir; kurulum hatasıdır.
	CodeProviderMisconfigured = "tax_provider_misconfigured"
	// CodeProviderInvalidResult sağlayıcının sözleşme dışı bir sonuç
	// döndürdüğünü bildirir.
	CodeProviderInvalidResult = "tax_provider_invalid_result"
)

// Sayfalama sınırları. Limit verilmezse varsayılan, aşırı büyük verilirse
// azami değer uygulanır; istemci tek istekle veritabanını tarayamaz.
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int32 = 50
	// MaxLimit tek istekte dönebilecek azami kayıt sayısıdır.
	MaxLimit int32 = 200
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
// internal/modules/tax/repository paketindedir. Bu, ADR 0001'in örüntüsünün
// modül İÇİNDEKİ karşılığıdır ve servisin veritabanı olmadan test edilmesini
// sağlar.
type Repository interface {
	CreateTaxRegion(ctx context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error)
	GetTaxRegion(ctx context.Context, id string) (models.TaxRegion, error)
	GetTaxRegionsByIDs(ctx context.Context, ids []string) ([]models.TaxRegion, error)
	ListTaxRegions(ctx context.Context, countryCode string, limit, offset int32) ([]models.TaxRegion, int64, error)
	ResolveTaxRegions(ctx context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error)
	DeleteTaxRegion(ctx context.Context, id string, now time.Time) error

	CreateTaxRate(ctx context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error)
	GetTaxRate(ctx context.Context, id string) (models.TaxRate, error)
	ListTaxRates(ctx context.Context, regionID string) ([]models.TaxRate, error)
	ListTaxRatesByRegions(ctx context.Context, regionIDs []string) ([]models.TaxRate, error)
	UpdateTaxRate(ctx context.Context, id string, patch models.TaxRatePatch, now time.Time) (models.TaxRate, error)
	DeleteTaxRate(ctx context.Context, id string, now time.Time) error

	CreateTaxRateRule(ctx context.Context, rule models.TaxRateRule, now time.Time) (models.TaxRateRule, error)
	GetTaxRateRule(ctx context.Context, id string) (models.TaxRateRule, error)
	ListTaxRateRules(ctx context.Context, rateID string) ([]models.TaxRateRule, error)
	ListTaxRateRulesByRates(ctx context.Context, rateIDs []string) ([]models.TaxRateRule, error)
	DeleteTaxRateRule(ctx context.Context, id string, now time.Time) error
}

// Options servisin kurulum ayarlarıdır.
type Options struct {
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
	// Now zaman kaynağıdır; nil ise time.Now kullanılır. Testler burayı sabit
	// bir saatle doldurarak zamana bağlı alanları belirlenimci hâle getirir.
	Now func() time.Time
	// Providers vergi sağlayıcılarının kaydıdır; nil ise yalnızca yerel
	// hesaplamayı içeren bir kayıt kurulur.
	Providers *ProviderRegistry
}

// Service tax modülünün public servisidir. Eşzamanlı kullanıma güvenlidir.
type Service struct {
	repo      Repository
	providers *ProviderRegistry
	log       *slog.Logger
	now       func() time.Time
}

// New verilen depo üzerinde çalışan bir servis üretir.
//
// repo nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
//
// Sağlayıcı kaydı verilmezse yerel hesaplama sağlayıcısı ([LocalProvider])
// tek başına kurulur ve depo onun oran kaynağı olur. Kaydın hiç olmaması,
// vergisi yapılandırılmış her bölgenin ilk hesapta patlaması demek olurdu.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	providers := opts.Providers
	if providers == nil {
		providers = NewProviderRegistry()
		if repo != nil {
			// Hata yok sayılabilir: yeni kurulan boş bir kayıtta aynı kimlikle
			// ikinci bir sağlayıcı bulunamaz, dolayısıyla çakışma imkânsızdır.
			_ = providers.Register(NewLocalProvider(repo))
		}
	}

	return &Service{repo: repo, providers: providers, log: log, now: now}
}

// ready deponun kurulu olduğunu doğrular.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable(CodeUnconfigured, "tax servisi kurulmamış")
	}
	return nil
}

// clock geçerli anı UTC olarak döner.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// Providers servisin sağlayıcı kaydını döner.
//
// Modül bunu container'a ayrıca kaydeder; Faz 9'daki plugin sistemi kendi vergi
// sağlayıcısını çekirdeğe ve bu modüle dokunmadan ekleyebilsin diye.
func (s *Service) Providers() *ProviderRegistry {
	if s == nil {
		return nil
	}
	return s.providers
}

// CreateTaxRegionInput yeni bir vergi bölgesinin yazma girdisidir.
type CreateTaxRegionInput struct {
	// CountryCode ISO 3166-1 alpha-2 kodudur; büyük/küçük harf serbesttir,
	// BÜYÜK harfe normalleştirilerek saklanır. Zorunludur.
	CountryCode string
	// ProvinceCode eyalet/il kodudur. Boş bırakılırsa ÜLKE KÖKÜ oluşturulur;
	// dolu verilirse [CreateTaxRegionInput.ParentID] de zorunludur.
	ProvinceCode string
	// ParentID eyalet bölgesinin bağlanacağı ülke köküdür. Ülke kökü
	// oluştururken boş bırakılmalıdır.
	ParentID string
	// ProviderID vergi sağlayıcısının kimliğidir ve KAYITLI olmalıdır.
	//
	// Boş bırakılabilir: eyalet bölgesinde "ülkenin sağlayıcısını devral",
	// kök bölgede "yerel hesaplama" demektir (bkz. [Service.providerFor]).
	// Baş/son boşlukları kırpılarak saklanır.
	ProviderID string
	// Metadata serbest üstveridir.
	Metadata map[string]any
}

// CreateTaxRegion yeni bir vergi bölgesi oluşturur.
//
// # İki biçim
//
// Girdi ya ÜLKE KÖKÜ (eyalet kodu ve ebeveyn boş) ya da EYALET (ikisi de dolu)
// tanımlar. Yarım biçimler reddedilir: ebeveynsiz bir eyalet hiç bulunamaz —
// çözüm yolu daima ülkeden başlar — eyalet kodu taşıyan bir kök ise ülkenin
// tamamı yerine tek bir ile oran uygular.
//
// # Ülkeye ikinci kök
//
// Reddedilir (errors.Conflict, kod [CodeRootExists]). Servis bunu önce OKUYARAK
// denetler, ama son savunma veritabanındaki kısmi benzersiz indekstir: iki
// eşzamanlı istek "önce oku, sonra yaz" denetimini birlikte geçebilir ve o
// andan sonra hangi oranın uygulanacağı satır sırasına kalırdı.
//
// # Ebeveyn denetimi
//
// Ebeveyn var olmalı, KÖK olmalı ve AYNI ülkeye ait olmalıdır. Üçü de burada
// okunabilir bir hatayla denetlenir; veritabanındaki bileşik foreign key
// (parent_id, country_code) aynı garantiyi ikinci kez verir.
//
// # Sağlayıcı YAZMADAN ÖNCE doğrulanır
//
// [CreateTaxRegionInput.ProviderID] dolu verilirse kayıtlı olduğu denetlenir ve
// değilse errors.Invalid döner ([CodeProviderNotFound]); gerekçe
// [Service.normalizeProviderID] godoc'undadır. Boş değer serbesttir ve
// devralma anlamına gelir (bkz. [Service.providerFor]).
func (s *Service) CreateTaxRegion(ctx context.Context, in CreateTaxRegionInput) (models.TaxRegion, error) {
	if err := s.ready(); err != nil {
		return models.TaxRegion{}, err
	}

	country, err := NormalizeCountryCode(in.CountryCode)
	if err != nil {
		return models.TaxRegion{}, err
	}
	province, err := NormalizeProvinceCode(in.ProvinceCode)
	if err != nil {
		return models.TaxRegion{}, err
	}
	providerID, err := s.normalizeProviderID(in.ProviderID)
	if err != nil {
		return models.TaxRegion{}, err
	}

	region := models.TaxRegion{
		CountryCode: country,
		ProviderID:  providerID,
		Metadata:    in.Metadata,
	}

	switch {
	case province == "" && in.ParentID == "":
		if err := s.assertNoRoot(ctx, country); err != nil {
			return models.TaxRegion{}, err
		}
	case province != "" && in.ParentID != "":
		parent, err := s.parentForProvince(ctx, in.ParentID, country)
		if err != nil {
			return models.TaxRegion{}, err
		}
		region.ParentID = &parent.ID
		region.ProvinceCode = &province
	case province == "":
		return models.TaxRegion{}, errors.Invalid(CodeInvalidInput,
			"ebeveyn verildiğinde eyalet kodu da zorunludur; ülke kökü ebeveynsiz oluşturulur")
	default:
		return models.TaxRegion{}, errors.Invalid(CodeInvalidInput,
			"eyalet bölgesi için ebeveyn (ülke kökü) kimliği zorunludur")
	}

	// Saat BİR KEZ okunur: kimliğin zaman damgası ile created_at'in ayrışması,
	// kimliğe göre sıralanan bir listenin oluşturma sırasıyla uyuşmaması
	// demektir.
	now := s.clock()
	region.ID = models.NewTaxRegionID(now)
	return s.repo.CreateTaxRegion(ctx, region, now)
}

// assertNoRoot ülkenin henüz kök bölgesi olmadığını doğrular.
func (s *Service) assertNoRoot(ctx context.Context, country string) error {
	existing, err := s.repo.ResolveTaxRegions(ctx, country, "")
	if err != nil {
		return err
	}
	for i := range existing {
		if existing[i].IsRoot() {
			return errors.Conflict(CodeRootExists,
				"%s ülkesinin kök vergi bölgesi zaten var: %s", country, existing[i].ID)
		}
	}
	return nil
}

// parentForProvince eyalet bölgesinin bağlanacağı kökü okur ve doğrular.
func (s *Service) parentForProvince(ctx context.Context, parentID, country string) (models.TaxRegion, error) {
	if err := requireID(parentID, models.TaxRegionIDPrefix, "vergi bölgesi kimliği"); err != nil {
		return models.TaxRegion{}, err
	}

	parent, err := s.repo.GetTaxRegion(ctx, parentID)
	if err != nil {
		return models.TaxRegion{}, err
	}
	if !parent.IsRoot() {
		return models.TaxRegion{}, errors.Invalid(CodeParentInvalid,
			"%s bir eyalet bölgesidir; vergi hiyerarşisi iki seviyedir ve eyaletin altına bölge açılamaz",
			parent.ID)
	}
	if parent.CountryCode != country {
		return models.TaxRegion{}, errors.Invalid(CodeParentInvalid,
			"eyalet bölgesinin ülkesi (%s) kökün ülkesinden (%s) farklı olamaz",
			country, parent.CountryCode)
	}
	return parent, nil
}

// normalizeProviderID sağlayıcı kimliğini kırpar ve KAYITLI olduğunu doğrular.
//
// # Neden yazmadan önce
//
// Doğrulanmayan bir kimliğin bedeli GECİKMELİ ve büyüktür: yazım hatası yazma
// anında değil, o ülkedeki İLK vergi hesabında ortaya çıkar ve orada
// [CodeProviderMisconfigured] + KindInternal (500) olur. Sepet toplamı bu
// hesabı her turda çağırdığı için tek bir yönetici yazım hatası o ülkedeki tüm
// sepetleri kapatırdı ve hata ancak müşteri sepete ürün eklerken görülürdü.
// Kardeş modüldeki örüntü de budur: payment/service/session.go oturumu
// yazmadan ÖNCE sağlayıcıyı kayıttan çözer.
//
// Kayıt hatası errors.Invalid'e çevrilir: kayıtlı olmayan bir kimlik yazanın
// GİRDİSİNDEKİ hatadır, sunucunun değil.
//
// # Neden kırpılıyor ve sınırlanıyor
//
// Kayıt araması kimliği kırparak yapar ([ProviderRegistry.Get]); kırpılmamış
// bir değer saklansaydı "saklanan" ile "uygulanan" ayrışırdı. Uzunluk sınırı
// modülün diğer kimlikleriyle aynıdır (maxIDLen): sınırsız bir metin alanı,
// tek istekle tabloya megabaytlarca veri yazmanın en ucuz yoludur.
func (s *Service) normalizeProviderID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxIDLen {
		return "", errors.Invalid(CodeInvalidInput,
			"vergi sağlayıcısı kimliği en fazla %d bayt olabilir, %d bayt verildi",
			maxIDLen, len(trimmed))
	}
	if s.providers == nil {
		return "", errors.Internal(CodeProviderMisconfigured, "tax sağlayıcı kaydı kurulmamış")
	}
	if _, err := s.providers.Get(trimmed); err != nil {
		return "", errors.Wrap(err, errors.KindInvalid, CodeProviderNotFound,
			"%q vergi sağlayıcısı bu kurulumda kayıtlı değil", trimmed)
	}
	return trimmed, nil
}

// GetTaxRegion kimliğe göre bölge döner; yoksa errors.NotFound.
func (s *Service) GetTaxRegion(ctx context.Context, id string) (models.TaxRegion, error) {
	if err := s.ready(); err != nil {
		return models.TaxRegion{}, err
	}
	if err := requireID(id, models.TaxRegionIDPrefix, "vergi bölgesi kimliği"); err != nil {
		return models.TaxRegion{}, err
	}
	return s.repo.GetTaxRegion(ctx, id)
}

// ListTaxRegions sayfalanmış bölge listesini döner.
//
// countryCode boş verilirse süzgeç uygulanmaz; dolu verilirse biçimi doğrulanır
// ve BÜYÜK harfe normalleştirilir. Biçimsiz bir kodu sessizce "süzgeç yok"a
// çevirmek, istemcinin istediğinden çok daha geniş bir liste döndürürdü.
func (s *Service) ListTaxRegions(ctx context.Context, countryCode string, limit, offset int32) (Page[models.TaxRegion], error) {
	if err := s.ready(); err != nil {
		return Page[models.TaxRegion]{}, err
	}

	filter := ""
	if countryCode != "" {
		normalized, err := NormalizeCountryCode(countryCode)
		if err != nil {
			return Page[models.TaxRegion]{}, err
		}
		filter = normalized
	}

	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.TaxRegion]{}, err
	}

	regions, total, err := s.repo.ListTaxRegions(ctx, filter, limit, offset)
	if err != nil {
		return Page[models.TaxRegion]{}, err
	}
	return Page[models.TaxRegion]{Items: regions, Count: total, Limit: limit, Offset: offset}, nil
}

// DeleteTaxRegion bölgeyi, alt bölgelerini ve oranlarını yumuşak siler.
//
// Silme AĞACI kapsar; gerekçe repository.DeleteTaxRegion godoc'undadır. Silinen
// bir bölge o ülkedeki her sepetin vergisini sıfıra düşürür, bu yüzden izi
// loglanır.
func (s *Service) DeleteTaxRegion(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.TaxRegionIDPrefix, "vergi bölgesi kimliği"); err != nil {
		return err
	}

	if err := s.repo.DeleteTaxRegion(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "vergi bölgesi silindi", slog.String("tax_region_id", id))
	return nil
}

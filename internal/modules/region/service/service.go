// Package service region modülünün iş mantığını barındırır.
//
// # Modüller arası yüzey (ADR 0001)
//
// region hiçbir modülü import ETMEZ ve hiçbir modülden veri OKUMAZ; bu yüzden
// bu pakette tüketici tarafı bir arayüz yoktur. Ters yön vardır: cart (Faz 5),
// order (Faz 6) ve tax (Faz 7) region'a ihtiyaç duyar. O tarafın kendi
// paketinde dar bir arayüz tanımlayabilmesi için region'ın yüzeyi İKİYE
// ayrılmıştır:
//
//   - Modül içi zengin yüzey — [models] tiplerini kullanır ([Service.CreateRegion],
//     [Service.ResolveRegionForCountry] …). Bu metotları yalnızca region'ın kendi
//     API katmanı ve query sağlayıcısı çağırır.
//   - Modüller arası yüzey — YALNIZCA ilkel ve stdlib tipleri kullanır
//     (bkz. interop.go: [Service.RegionCurrency], [Service.RegionTax],
//     [Service.RegionIDForCountry], [Service.CurrencyDecimalDigits]).
//
// Ayrım zorunludur: Go'da yapısal uyum imza EŞİTLİĞİ ister. Tüketici modül
// region'ı import edemediği için [models.Region] gibi bir tipi imzasında
// adlandıramaz; adlandırdığı an kendi paketindeki farklı bir tip olur ve somut
// servis arayüzü karşılamaz.
//
// # Para
//
// Bu modül TUTAR taşımaz; para birimi TANIMINI taşır. Sepet tutarları minor
// unit tam sayıdır (plan Bölüm 8) ve o tam sayının sunum çarpanı
// [models.Currency.DecimalDigits]'ten gelir. Vergi oranı da tam sayıdır
// (baz puan); servis hiçbir yerde float kullanmaz.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "region_invalid_input"
	// CodeRegionNotFound istenen bölgenin bulunamadığını bildirir.
	CodeRegionNotFound = "region_not_found"
	// CodeCountryUnassigned ülkenin hiçbir bölgeye bağlı olmadığını bildirir.
	CodeCountryUnassigned = "country_has_no_region"
	// CodeCountryRegionMissing ülkenin bağlı olduğu bölgenin bulunamadığını
	// bildirir (bölge silinmiş ve ülke serbest bırakılmamışsa oluşur).
	CodeCountryRegionMissing = "country_region_missing"
	// CodeDecorateFailed vitrin görünümünün kurulamadığını bildirir; iç
	// tutarsızlık göstergesidir ve normal akışta oluşmaz.
	CodeDecorateFailed = "region_decorate_failed"
)

// Sayfalama sınırları. Limit verilmezse varsayılan, aşırı büyük verilirse
// azami değer uygulanır; istemci tek istekle veritabanını tarayamaz.
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int32 = 50
	// MaxLimit tek istekte dönebilecek azami kayıt sayısıdır.
	//
	// Ülke listesi için bilinçli olarak cömerttir: ISO 3166'da 249 ülke vardır
	// ve bir yönetim ekranının tamamını iki üç sayfada alabilmesi beklenir.
	MaxLimit int32 = 250
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
// internal/modules/region/repository paketindedir. Bu, ADR 0001'in örüntüsünün
// modül İÇİNDEKİ karşılığıdır ve servisin veritabanı olmadan test edilmesini
// sağlar.
type Repository interface {
	CreateRegion(ctx context.Context, region models.Region, now time.Time) (models.Region, error)
	GetRegion(ctx context.Context, id string) (models.Region, error)
	ListRegions(ctx context.Context, limit, offset int32) ([]models.Region, int64, error)
	GetRegionsByIDs(ctx context.Context, ids []string) ([]models.Region, error)
	UpdateRegion(ctx context.Context, id string, patch models.RegionPatch, now time.Time) (models.Region, error)
	DeleteRegion(ctx context.Context, id string, now time.Time) error
	GetRegionByCountry(ctx context.Context, countryCode string) (models.Region, error)

	AssignCountry(ctx context.Context, regionID, countryCode string, now time.Time) (models.Country, error)
	UnassignCountry(ctx context.Context, regionID, countryCode string, now time.Time) error
	GetCountry(ctx context.Context, code string) (models.Country, error)
	ListCountries(ctx context.Context, regionID *string, limit, offset int32) ([]models.Country, int64, error)
	ListCountriesByRegions(ctx context.Context, regionIDs []string) (map[string][]models.Country, error)

	GetCurrency(ctx context.Context, code string) (models.Currency, error)
	ListCurrencies(ctx context.Context, limit, offset int32) ([]models.Currency, int64, error)
	GetCurrenciesByCodes(ctx context.Context, codes []string) ([]models.Currency, error)
}

// Options servisin kurulum ayarlarıdır.
type Options struct {
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
	// Now zaman kaynağıdır; nil ise time.Now kullanılır. Testler burayı sabit
	// bir saatle doldurarak zamana bağlı alanları belirlenimci hâle getirir.
	Now func() time.Time
}

// Service region modülünün public servisidir. Eşzamanlı kullanıma güvenlidir.
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
		return errors.Unavailable("region_service_unconfigured", "region servisi kurulmamış")
	}
	return nil
}

// clock geçerli anı UTC olarak döner.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// CreateRegionInput yeni bir bölgenin yazma girdisidir.
type CreateRegionInput struct {
	// Name bölgenin görünen adıdır; zorunludur.
	Name string
	// CurrencyCode ISO 4217 kodudur; büyük/küçük harf serbesttir, BÜYÜK harfe
	// normalleştirilerek saklanır. Zorunludur.
	CurrencyCode string
	// AutomaticTaxes verginin otomatik uygulanıp uygulanmayacağıdır.
	AutomaticTaxes bool
	// TaxRate GEÇİCİ vergi oranıdır (baz puan; 2000 = %20).
	TaxRate int32
}

// CreateRegion yeni bir bölge oluşturur.
//
// Para birimi kodu biçimsel olarak geçersizse errors.Invalid döner ve
// veritabanına hiç gidilmez. Biçimsel olarak geçerli ama TANIMSIZ bir kod da
// errors.Invalid ile reddedilir; o denetim veritabanındaki foreign key'dedir
// (bkz. repository.CreateRegion).
func (s *Service) CreateRegion(ctx context.Context, in CreateRegionInput) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}

	name, err := normalizeName(in.Name)
	if err != nil {
		return models.Region{}, err
	}
	currency, err := NormalizeCurrencyCode(in.CurrencyCode)
	if err != nil {
		return models.Region{}, err
	}
	if err := validateTaxRate(in.TaxRate); err != nil {
		return models.Region{}, err
	}

	now := s.clock()
	return s.repo.CreateRegion(ctx, models.Region{
		ID:             models.NewRegionID(now),
		Name:           name,
		CurrencyCode:   currency,
		AutomaticTaxes: in.AutomaticTaxes,
		TaxRate:        in.TaxRate,
	}, now)
}

// GetRegion kimliğe göre bölge döner; yoksa errors.NotFound.
func (s *Service) GetRegion(ctx context.Context, id string) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}
	if err := requireRegionID(id); err != nil {
		return models.Region{}, err
	}
	return s.repo.GetRegion(ctx, id)
}

// ListRegions sayfalanmış bölge listesini döner.
func (s *Service) ListRegions(ctx context.Context, limit, offset int32) (Page[models.Region], error) {
	if err := s.ready(); err != nil {
		return Page[models.Region]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.Region]{}, err
	}

	regions, total, err := s.repo.ListRegions(ctx, limit, offset)
	if err != nil {
		return Page[models.Region]{}, err
	}
	return Page[models.Region]{Items: regions, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateRegionInput bir bölgenin KISMİ güncelleme girdisidir.
//
// nil alan "dokunma" demektir. Tam gövde istenseydi, gövdesinde tax_rate
// göndermeyi unutan bir istemci oranı sessizce sıfırlardı.
type UpdateRegionInput struct {
	// Name yeni addır; nil ise ad değişmez.
	Name *string
	// CurrencyCode yeni para birimi kodudur; nil ise para birimi değişmez.
	CurrencyCode *string
	// AutomaticTaxes verginin otomatik uygulanıp uygulanmayacağıdır; nil ise değişmez.
	AutomaticTaxes *bool
	// TaxRate yeni vergi oranıdır (baz puan); nil ise oran değişmez.
	TaxRate *int32
}

// UpdateRegion bölgenin verilen alanlarını günceller.
//
// Hiçbir alan verilmezse errors.Invalid döner: boş bir yama, istemcinin
// gönderdiğini sandığı alanın adını yanlış yazdığının en olası göstergesidir
// ve sessizce başarılı dönmek o hatayı gizlerdi.
func (s *Service) UpdateRegion(ctx context.Context, id string, in UpdateRegionInput) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}
	if err := requireRegionID(id); err != nil {
		return models.Region{}, err
	}

	patch, err := buildRegionPatch(in)
	if err != nil {
		return models.Region{}, err
	}
	if patch.Empty() {
		return models.Region{}, errors.Invalid(CodeInvalidInput,
			"güncellenecek alan verilmedi")
	}

	return s.repo.UpdateRegion(ctx, id, patch, s.clock())
}

// buildRegionPatch güncelleme girdisini doğrular ve yamaya çevirir.
//
// Doğrulama yalnızca DOLU alanlara uygulanır: dokunulmayan bir alanın mevcut
// değeri, bugün geçerli olmayan bir kuralı ihlal etse bile güncellemeyi
// düşürmemelidir.
func buildRegionPatch(in UpdateRegionInput) (models.RegionPatch, error) {
	var patch models.RegionPatch

	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return models.RegionPatch{}, err
		}
		patch.Name = &name
	}
	if in.CurrencyCode != nil {
		currency, err := NormalizeCurrencyCode(*in.CurrencyCode)
		if err != nil {
			return models.RegionPatch{}, err
		}
		patch.CurrencyCode = &currency
	}
	if in.AutomaticTaxes != nil {
		automatic := *in.AutomaticTaxes
		patch.AutomaticTaxes = &automatic
	}
	if in.TaxRate != nil {
		if err := validateTaxRate(*in.TaxRate); err != nil {
			return models.RegionPatch{}, err
		}
		rate := *in.TaxRate
		patch.TaxRate = &rate
	}
	return patch, nil
}

// DeleteRegion bölgeyi soft delete ile siler ve ülkelerini serbest bırakır.
func (s *Service) DeleteRegion(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireRegionID(id); err != nil {
		return err
	}

	if err := s.repo.DeleteRegion(ctx, id, s.clock()); err != nil {
		return err
	}

	// Silme, o bölgeye düşen her sepetin para birimini çözümsüz bırakır ve
	// ülkelerini serbest bırakır; izini sürülebilir olması gerekir.
	s.log.InfoContext(ctx, "bölge silindi", slog.String("region_id", id))
	return nil
}

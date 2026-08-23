// Package service pricing modülünün iş mantığını barındırır.
//
// # Modüller arası yüzey (ADR 0001)
//
// pricing hiçbir modülü import ETMEZ ve hiçbir modülden veri OKUMAZ; bu yüzden
// bu pakette tüketici tarafı bir arayüz yoktur. Ters yön vardır: product ve
// (Faz 5'te) cart pricing'e ihtiyaç duyar. O tarafın kendi paketinde dar bir
// arayüz tanımlayabilmesi için pricing'in yüzeyi İKİYE ayrılmıştır:
//
//   - Modül içi zengin yüzey — [models] tiplerini kullanır ([Service.CreatePriceSet],
//     [Service.SetPrices], [Service.CalculatePrice] …). Bu metotları yalnızca
//     pricing'in kendi API katmanı ve query sağlayıcısı çağırır.
//   - Modüller arası yüzey — YALNIZCA ilkel ve stdlib tipleri kullanır
//     ([Service.CreateEmptyPriceSet], [Service.SetBasePrices],
//     [Service.CalculateAmount]).
//
// Ayrım zorunludur: Go'da yapısal uyum imza EŞİTLİĞİ ister. Tüketici modül
// pricing'i import edemediği için [models.PriceSet] gibi bir tipi imzasında
// adlandıramaz; adlandırdığı an kendi paketindeki farklı bir tip olur ve somut
// servis arayüzü karşılamaz. İlkel tiplerle yazılmış imzalar ise tüketicinin
// kendi paketinde birebir tekrarlanabilir:
//
//	// product modülünde, pricing import EDİLMEDEN:
//	type PriceSetCreator interface {
//	    CreateEmptyPriceSet(ctx context.Context) (string, error)
//	}
//	creator, err := container.Resolve[PriceSetCreator](c, "pricing.service")
//
// # Para
//
// Tutarlar TAM SAYI minor unit'tir ve para birimi ayrı alandır (plan Bölüm 8).
// Servis hiçbir yerde float kullanmaz, yuvarlama yapmaz.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "pricing_invalid_input"
	// CodeNotCalculable verilen bağlamda geçerli fiyat bulunmadığını bildirir.
	CodeNotCalculable = "price_not_calculable"
	// CodePriceSetNotFound istenen price set'in bulunamadığını bildirir.
	CodePriceSetNotFound = "price_set_not_found"
)

// Sayfalama sınırları. Limit verilmezse varsayılan, aşırı büyük verilirse
// azami değer uygulanır; istemci tek istekle veritabanını tarayamaz.
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
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
// internal/modules/pricing/repository paketindedir. Bu, ADR 0001'in örüntüsünün
// modül İÇİNDEKİ karşılığıdır ve servisin veritabanı olmadan test edilmesini
// sağlar.
type Repository interface {
	CreatePriceSet(ctx context.Context, id string, prices []models.Price, now time.Time) (models.PriceSet, error)
	GetPriceSet(ctx context.Context, id string) (models.PriceSet, error)
	ListPriceSets(ctx context.Context, limit, offset int32) ([]models.PriceSet, int64, error)
	GetPriceSetsByIDs(ctx context.Context, ids []string) ([]models.PriceSet, error)
	DeletePriceSet(ctx context.Context, id string, now time.Time) error

	ListPrices(ctx context.Context, priceSetID string) ([]models.Price, error)
	ListPriceCandidatesBySets(ctx context.Context, priceSetIDs []string) (map[string][]models.PriceCandidate, error)
	ListPriceCandidates(ctx context.Context, priceSetID string) ([]models.PriceCandidate, error)
	ReplacePrices(ctx context.Context, priceSetID string, prices []models.Price, now time.Time) ([]models.Price, error)
	GetPrice(ctx context.Context, id string) (models.Price, error)

	CreatePriceRule(ctx context.Context, rule models.PriceRule, now time.Time) (models.PriceRule, error)
	GetPriceRule(ctx context.Context, id string) (models.PriceRule, error)
	ListPriceRules(ctx context.Context, priceID string) ([]models.PriceRule, error)
	DeletePriceRule(ctx context.Context, id string, now time.Time) error

	CreatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error)
	GetPriceList(ctx context.Context, id string) (models.PriceList, error)
	ListPriceLists(ctx context.Context, limit, offset int32) ([]models.PriceList, int64, error)
	UpdatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error)
	DeletePriceList(ctx context.Context, id string, now time.Time) error
}

// Options servisin kurulum ayarlarıdır.
type Options struct {
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
	// Now zaman kaynağıdır; nil ise time.Now kullanılır. Testler burayı sabit
	// bir saatle doldurarak zamana bağlı dalları (fiyat listesi penceresi)
	// belirlenimci hâle getirir.
	Now func() time.Time
}

// Service pricing modülünün public servisidir. Eşzamanlı kullanıma güvenlidir.
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
		return errors.Unavailable("pricing_service_unconfigured", "pricing servisi kurulmamış")
	}
	return nil
}

// clock geçerli anı UTC olarak döner.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// PriceInput tek bir fiyatın yazma girdisidir.
type PriceInput struct {
	// CurrencyCode ISO 4217 kodudur; büyük/küçük harf serbesttir, BÜYÜK
	// harfe normalleştirilerek saklanır.
	CurrencyCode string
	// Amount minor unit cinsinden tutardır.
	Amount int64
	// MinQuantity alt adet sınırıdır; 0 verilirse 1 kabul edilir.
	MinQuantity int32
	// MaxQuantity üst adet sınırıdır; nil ise sınırsız.
	MaxQuantity *int32
	// PriceListID fiyatı bir kampanya/segment listesine bağlar; nil ise taban fiyat.
	PriceListID *string
	// Rules fiyatın geçerlilik koşullarıdır.
	Rules []RuleInput
}

// RuleInput tek bir fiyat kuralının yazma girdisidir.
type RuleInput struct {
	// Attribute hesaplama bağlamında bakılacak alan adıdır.
	Attribute string
	// Operator karşılaştırma işlecidir.
	Operator models.RuleOperator
	// Values karşılaştırmanın sağ tarafıdır; en az bir eleman içermelidir.
	Values []string
}

// CreatePriceSet yeni bir price set oluşturur ve verilen fiyatları yazar.
//
// prices boş bırakılabilir: bir varyant önce fiyatsız yaratılıp fiyatları
// sonra yazılabilir. Fiyatlardan biri geçersizse HİÇBİRİ yazılmaz ve price set
// de oluşturulmaz — bu, girdiyi servis doğrulaması eleyince de, veritabanı
// reddedince de (örn. var olmayan bir fiyat listesine bağlı fiyat) geçerlidir:
// kap ve fiyatları TEK işlemde yazılır.
func (s *Service) CreatePriceSet(ctx context.Context, prices []PriceInput) (models.PriceSet, error) {
	if err := s.ready(); err != nil {
		return models.PriceSet{}, err
	}

	now := s.clock()
	// Doğrulama YAZMADAN ÖNCE yapılır; geçersiz bir fiyat için veritabanına hiç
	// gidilmez.
	toWrite, err := s.buildPrices("", prices, now)
	if err != nil {
		return models.PriceSet{}, err
	}

	return s.repo.CreatePriceSet(ctx, models.NewPriceSetID(now), toWrite, now)
}

// GetPriceSet kimliğe göre price set döner; yoksa errors.NotFound.
func (s *Service) GetPriceSet(ctx context.Context, id string) (models.PriceSet, error) {
	if err := s.ready(); err != nil {
		return models.PriceSet{}, err
	}
	if err := requireID(id, models.PriceSetIDPrefix, "price set kimliği"); err != nil {
		return models.PriceSet{}, err
	}
	return s.repo.GetPriceSet(ctx, id)
}

// ListPriceSets sayfalanmış price set listesini döner.
func (s *Service) ListPriceSets(ctx context.Context, limit, offset int32) (Page[models.PriceSet], error) {
	if err := s.ready(); err != nil {
		return Page[models.PriceSet]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.PriceSet]{}, err
	}

	sets, total, err := s.repo.ListPriceSets(ctx, limit, offset)
	if err != nil {
		return Page[models.PriceSet]{}, err
	}
	return Page[models.PriceSet]{Items: sets, Count: total, Limit: limit, Offset: offset}, nil
}

// DeletePriceSet price set'i ve fiyatlarını soft delete ile siler.
func (s *Service) DeletePriceSet(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PriceSetIDPrefix, "price set kimliği"); err != nil {
		return err
	}
	return s.repo.DeletePriceSet(ctx, id, s.clock())
}

// SetPrices bir price set'in fiyatlarını TOPLUCA değiştirir.
//
// İşlem yerine koymadır (replace), ekleme değil: verilmeyen fiyatlar silinir.
// Yazma atomiktir — girdilerden biri veritabanınca reddedilirse hiçbiri
// yazılmaz ve kap eski fiyatlarıyla kalır.
//
// Boş dilim geçerli bir istektir ve kabın tüm fiyatlarını kaldırır.
func (s *Service) SetPrices(ctx context.Context, priceSetID string, prices []PriceInput) ([]models.Price, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set kimliği"); err != nil {
		return nil, err
	}

	now := s.clock()
	toWrite, err := s.buildPrices(priceSetID, prices, now)
	if err != nil {
		return nil, err
	}

	written, err := s.repo.ReplacePrices(ctx, priceSetID, toWrite, now)
	if err != nil {
		return nil, err
	}

	// Yerine koyma yıkıcı bir işlemdir: kaç fiyatın silinip kaç fiyatın
	// yazıldığı, yanlış bir toplu çağrının izini sürmenin tek yoludur.
	// Tutarlar loglanmaz; kimlik ve sayı yeter (plan Bölüm 8).
	s.log.DebugContext(ctx, "price set fiyatları yenilendi",
		slog.String("price_set_id", priceSetID),
		slog.Int("fiyat_sayisi", len(written)),
	)
	return written, nil
}

// ListStorePrices kabın MÜŞTERİYE gösterilebilir fiyatlarını döner.
//
// [Service.ListPrices]'tan farkı süzgeçtir: yalnızca yayınlanmış ve süresi
// geçmemiş listelere ait ya da taban fiyatlar döner, kurala bağlı fiyatlar
// hiç dönmez. Kurala bağlı bir fiyatın müşteriye gösterilmesi iki yönden
// yanlıştır — fiyat o müşteri için geçerli olmayabilir, ve kuralın kendisi
// (ör. bir müşteri grubunun kimliği) iş bilgisidir.
//
// Dönen fiyatların Rules alanı BOŞALTILIR: seçim zaten burada yapıldığı için
// koşulların dışarı çıkmasına gerek yoktur.
//
// Yönetim yüzeyi ListPrices kullanmaya devam eder; operatör taslak kampanyaları
// ve kural koşullarını GÖRMELİDİR.
func (s *Service) ListStorePrices(ctx context.Context, priceSetID string) ([]models.Price, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set kimliği"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetPriceSet(ctx, priceSetID); err != nil {
		return nil, err
	}

	candidates, err := s.repo.ListPriceCandidates(ctx, priceSetID)
	if err != nil {
		return nil, err
	}

	// Query sağlayıcısıyla AYNI süzgeç kullanılır; iki müşteri yüzeyinin
	// ayrışması, birinde sızan bir fiyatın diğerinde görünmemesi demek olurdu.
	prices := listablePrices(candidates, s.clock())
	for i := range prices {
		prices[i].Rules = nil
	}
	return prices, nil
}

// ListPrices bir price set'in fiyatlarını kurallarıyla döner.
//
// YÖNETİM yüzeyi içindir ve HİÇBİR süzgeç uygulamaz: taslak kampanya fiyatları
// ve kurala bağlı fiyatlar da döner. Müşteriye giden yüzey için
// [Service.ListStorePrices] kullanılmalıdır.
func (s *Service) ListPrices(ctx context.Context, priceSetID string) ([]models.Price, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set kimliği"); err != nil {
		return nil, err
	}
	// Kabın varlığı doğrulanır: olmayan bir kabın fiyatları boş dilim olarak
	// dönerse istemci 404 yerine "fiyatı yok" sanırdı.
	if _, err := s.repo.GetPriceSet(ctx, priceSetID); err != nil {
		return nil, err
	}
	return s.repo.ListPrices(ctx, priceSetID)
}

// buildPrices girdileri doğrular ve yazılacak domain modellerine çevirir.
//
// priceSetID boş verilebilir (kap henüz oluşturulmamışsa); kimlik yazma anında
// depo tarafından atanır.
//
// Hata, kaçıncı fiyatın reddedildiğini [detailIndex] anahtarıyla taşır; kural
// düzeyindeki hatalarda [detailRuleIndex] de doludur ve iki seviye birbirini
// EZMEZ.
func (s *Service) buildPrices(priceSetID string, inputs []PriceInput, now time.Time) ([]models.Price, error) {
	out := make([]models.Price, 0, len(inputs))
	for i, in := range inputs {
		price, err := buildPrice(priceSetID, in, now)
		if err != nil {
			return nil, withIndex(err, detailIndex, i)
		}
		out = append(out, price)
	}
	return out, nil
}

// buildPrice tek bir girdiyi doğrular ve modele çevirir.
func buildPrice(priceSetID string, in PriceInput, now time.Time) (models.Price, error) {
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.Price{}, err
	}
	if err := validateAmount(in.Amount); err != nil {
		return models.Price{}, err
	}
	minQty, maxQty, err := normalizeQuantityRange(in.MinQuantity, in.MaxQuantity)
	if err != nil {
		return models.Price{}, err
	}
	if err := validatePriceListRef(in.PriceListID); err != nil {
		return models.Price{}, err
	}

	priceID := models.NewPriceID(now)
	rules := make([]models.PriceRule, 0, len(in.Rules))
	for i, ruleIn := range in.Rules {
		rule, err := buildRule(priceID, ruleIn, now)
		if err != nil {
			return models.Price{}, withIndex(err, detailRuleIndex, i)
		}
		rules = append(rules, rule)
	}

	return models.Price{
		ID:           priceID,
		PriceSetID:   priceSetID,
		PriceListID:  in.PriceListID,
		CurrencyCode: currency,
		Amount:       in.Amount,
		MinQuantity:  minQty,
		MaxQuantity:  maxQty,
		Rules:        rules,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// buildRule tek bir kural girdisini doğrular ve modele çevirir.
func buildRule(priceID string, in RuleInput, now time.Time) (models.PriceRule, error) {
	if err := validateRule(in); err != nil {
		return models.PriceRule{}, err
	}
	return models.PriceRule{
		ID:        models.NewPriceRuleID(now),
		PriceID:   priceID,
		Attribute: in.Attribute,
		Operator:  in.Operator,
		Values:    append([]string(nil), in.Values...),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

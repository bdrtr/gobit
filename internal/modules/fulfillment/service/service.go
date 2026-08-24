// Package service fulfillment modülünün iş mantığıdır.
//
// Modülün sorumluluğu tek cümleyle: bir siparişin FİZİKSEL olarak nereye
// kadar geldiğini bilmek — hangi kargo seçeneği kaç para, gönderi açıldı mı,
// yola çıktı mı, teslim edildi mi.
//
// # Durum makinesi
//
// Gönderinin geçiş tablosu [models.FulfillmentStatus] üzerindeki CancelAction,
// ShipAction ve DeliverAction metotlarında, saf fonksiyonlar olarak durur; bu
// paket yalnızca sonucu tipli hataya çevirir. Geçersiz her geçiş
// errors.Conflict döner (örn. teslim edilmiş bir gönderiyi iptal etmek). Zaten
// hedef durumdaki bir geçiş ise hata DEĞİL, sessiz bir no-op'tur;
// idempotentlik oradan gelir.
//
// # Fiyat iki kaynaktan gelmez
//
// Bir kargo seçeneğinin ücreti ya satırdaki sabit tutardır ([models.PriceFlat])
// ya da sağlayıcının Quote'udur ([models.PriceCalculated]) — ikisi birden asla.
// Hesaplanan seçeneğin Amount alanı sıfır olmak zorundadır ve bu şema
// düzeyinde de zorlanır; iki kaynaklı bir fiyat, hangisinin geçerli olduğunu
// okuyana bırakırdı.
//
// # Sağlayıcı çağrısı işlemin İÇİNDEDİR
//
// Gönderi oluşturma ve iptal, sağlayıcıyı satır kilidi (ya da benzersiz indeks
// kilidi) ALTINDA çağırır. Bedeli açıktır: yavaş bir sağlayıcı satırın kilidini
// o süre boyunca tutar. Karşılığında kazanılan şey "tam olarak bir gönderi"
// garantisidir — kilit sağlayıcı çağrısından önce bırakılsaydı, aynı
// idempotency anahtarıyla gelen iki eşzamanlı çağrının ikincisi, birincisinin
// henüz sağlayıcı kimliği yazılmamış satırını okur ve YARIM bir gönderi
// dönerdi. payment modülünde aynı karar aynı gerekçeyle alınmıştır.
//
// Manuel sağlayıcı aynı depoyu paylaştığı için çağrısı bu işleme KATILIR
// (bkz. repository.Repository.WithTx: iç içe çağrı yeni işlem açmaz). Bu,
// taklit sağlayıcının defterini modülün kayıtlarıyla atomik tutar; gerçek bir
// ağ sağlayıcısında böyle bir garanti olmaz ve saga telafisi tam da onun
// içindir.
//
// # Modül izolasyonu
//
// Bu modül başka hiçbir modülü tanımaz (Prensip 2.1/2.4, ADR 0001).
// [models.Fulfillment.Reference] bir sipariş kimliğidir,
// [models.ShippingOption.RegionID] bir bölge kimliğidir ve
// [models.FulfillmentItem.LineItemID] bir sipariş satırı kimliğidir; üçü de
// serbest metin olarak saklanır, foreign key verilmez (Prensip 2.2) ve varlığı
// burada doğrulanmaz — doğrulama, o modülleri tanıyan workflow'un işidir.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// EntityName modülün Query katmanına sunduğu entity adıdır. Sağlayıcı
// container'a "<EntityName>.query" adıyla kaydedilir (ADR 0004).
const EntityName = "shipping_option"

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "fulfillment_invalid_input"
	// CodeInvalidTransition durum makinesinde geçersiz bir geçiş denendiğini
	// bildirir (örn. teslim edilmiş bir gönderiyi iptal etmek).
	CodeInvalidTransition = "fulfillment_invalid_transition"
	// CodeProviderNotFound istenen sağlayıcının kayıtlı olmadığını bildirir.
	CodeProviderNotFound = "fulfillment_provider_not_found"
	// CodeProviderExists aynı kimlikle ikinci bir sağlayıcı kaydedilmek
	// istendiğini bildirir.
	CodeProviderExists = "fulfillment_provider_already_registered"
	// CodeIdempotencyMismatch aynı anahtarın BAŞKA bir gönderi için
	// kullanıldığını bildirir.
	CodeIdempotencyMismatch = "fulfillment_idempotency_key_mismatch"
	// CodeProfileInUse seçeneği duran bir profilin silinmek istendiğini
	// bildirir.
	CodeProfileInUse = "fulfillment_shipping_profile_in_use"
	// CodeProviderContract sağlayıcının sözleşme dışı bir yanıt döndüğünü
	// bildirir; normal işleyişte oluşmaz.
	CodeProviderContract = "fulfillment_provider_contract_violation"
	// CodeNotReady servisin eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "fulfillment_service_not_ready"
)

// Sayfalama sınırları (plan Bölüm 8: limit/offset).
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int64 = 50
	// MaxLimit tek istekte istenebilecek en büyük sayfa boyutudur.
	MaxLimit int64 = 100
)

// maxTextLen serbest metin alanları için üst sınırdır. Sınır, tek bir isteğin
// veritabanına sınırsız büyüklükte metin yazmasını engeller.
const maxTextLen = 512

// maxItemsPerFulfillment tek bir gönderiye konabilecek kalem sayısıdır.
// Sınır, tek bir isteğin sınırsız satır yazmasını engeller.
const maxItemsPerFulfillment = 500

// Store servisin ihtiyaç duyduğu kalıcılık yüzeyidir.
//
// Arayüz TÜKETEN tarafta, yani burada tanımlıdır (ADR 0001'in örüntüsü).
// Servis repository paketini import ETMEZ; somut depo bu imzaları yapısal
// olarak karşılar ve bağlantı module.go'da kurulur. Böylece birim testleri
// gerçek bir veritabanı olmadan, birkaç satırlık bir sahte depo ile
// yazılabilir.
//
// # Manuel sağlayıcının defteri BURADA YOKTUR
//
// Somut depo fulfillment_manual_shipments tablosuna da erişir, ama o metotlar
// bu arayüze BİLİNÇLİ OLARAK alınmamıştır: sağlayıcının iç durumu modülün
// verisi değildir ve servise ona dokunma imkânı verilmemelidir. Sınır bir
// yorum değil, tip sistemidir.
//
// # İşlem sınırı
//
// [Store.WithTx] verilen işlevi tek bir veritabanı işleminde çalıştırır ve
// işlemi işlevin aldığı context ile taşır. Bu yüzden işlem içindeki her çağrı
// İŞLEVE VERİLEN ctx ile yapılmalıdır; dıştaki ctx kullanılırsa o çağrı işlemin
// dışında kalır ve atomiklik sessizce kaybolur.
//
// [Store.LockFulfillment], [Store.LockShippingProfile] ve
// [Store.LockShippingProfileShared] satırı işlem sonuna kadar kilitler ve
// yalnızca [Store.WithTx] içinde çağrılabilir.
type Store interface {
	// WithTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem geri alınır.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateShippingProfile yeni bir kargo profili kaydeder.
	CreateShippingProfile(ctx context.Context, profile models.ShippingProfile) (models.ShippingProfile, error)
	// GetShippingProfile profili kimliğiyle döner; yoksa NotFound.
	GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error)
	// LockShippingProfile profili işlem sonuna kadar YAZMA kilidiyle okur.
	LockShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error)
	// LockShippingProfileShared profili işlem sonuna kadar PAYLAŞIMLI kilitle
	// okur.
	LockShippingProfileShared(ctx context.Context, id string) (models.ShippingProfile, error)
	// ListShippingProfiles profilleri süzer ve sayfalar; ikinci değer süzgece
	// uyan TÜM satırların sayısıdır.
	ListShippingProfiles(ctx context.Context, filter models.ProfileFilter) ([]models.ShippingProfile, int64, error)
	// UpdateShippingProfile profilin alanlarını MUTLAK değerlerle yazar.
	UpdateShippingProfile(ctx context.Context, profile models.ShippingProfile) (models.ShippingProfile, error)
	// SoftDeleteShippingProfile profili yumuşak siler.
	SoftDeleteShippingProfile(ctx context.Context, id string) error
	// CountAliveOptionsByProfile profile bağlı yaşayan seçenekleri sayar.
	CountAliveOptionsByProfile(ctx context.Context, profileID string) (int64, error)

	// CreateShippingOption yeni bir kargo seçeneği kaydeder.
	CreateShippingOption(ctx context.Context, option models.ShippingOption) (models.ShippingOption, error)
	// GetShippingOption seçeneği kimliğiyle döner; yoksa NotFound.
	GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error)
	// ListShippingOptions seçenekleri süzer ve sayfalar.
	ListShippingOptions(ctx context.Context, filter models.OptionFilter) ([]models.ShippingOption, int64, error)
	// ShippingOptionsByIDs kimlik kümesini TEK sorguda getirir (N+1 yok).
	ShippingOptionsByIDs(ctx context.Context, ids []string) ([]models.ShippingOption, error)
	// ListEligibleShippingOptions bir sepet bağlamının ADAY seçeneklerini
	// KURALLARIYLA birlikte döner.
	ListEligibleShippingOptions(ctx context.Context, filter models.EligibilityFilter) ([]models.ShippingOption, error)
	// UpdateShippingOption seçeneğin alanlarını MUTLAK değerlerle yazar.
	UpdateShippingOption(ctx context.Context, option models.ShippingOption) (models.ShippingOption, error)
	// SoftDeleteShippingOption seçeneği yumuşak siler.
	SoftDeleteShippingOption(ctx context.Context, id string) error

	// CreateShippingOptionRule yeni bir kural kaydeder.
	CreateShippingOptionRule(ctx context.Context, rule models.ShippingOptionRule) (models.ShippingOptionRule, error)
	// GetShippingOptionRule kuralı kimliğiyle döner; yoksa NotFound.
	GetShippingOptionRule(ctx context.Context, id string) (models.ShippingOptionRule, error)
	// ListShippingOptionRules bir seçeneğin kurallarını döner.
	ListShippingOptionRules(ctx context.Context, optionID string) ([]models.ShippingOptionRule, error)
	// SoftDeleteShippingOptionRule kuralı yumuşak siler.
	SoftDeleteShippingOptionRule(ctx context.Context, id string) error

	// InsertFulfillmentIfAbsent gönderiyi yalnızca idempotency anahtarı henüz
	// kullanılmamışsa yazar. İkinci dönüş değeri satırın yazılıp
	// yazılmadığıdır; çakışma HATA DEĞİLDİR.
	InsertFulfillmentIfAbsent(ctx context.Context, ful models.Fulfillment) (models.Fulfillment, bool, error)
	// GetFulfillment gönderiyi kimliğiyle döner; yoksa NotFound.
	GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error)
	// FulfillmentByIdempotencyKey aynı anahtarla oluşturulmuş gönderiyi döner;
	// yoksa NotFound.
	FulfillmentByIdempotencyKey(ctx context.Context, key string) (models.Fulfillment, error)
	// LockFulfillment gönderiyi kilitler ve güncel hâlini döner.
	LockFulfillment(ctx context.Context, id string) (models.Fulfillment, error)
	// ListFulfillments gönderileri süzer ve sayfalar.
	ListFulfillments(ctx context.Context, filter models.FulfillmentFilter) ([]models.Fulfillment, int64, error)
	// UpdateFulfillmentProviderResult sağlayıcının yanıtını satıra yazar.
	UpdateFulfillmentProviderResult(
		ctx context.Context,
		id, externalID string,
		status models.FulfillmentStatus,
		trackingNumber, trackingURL string,
		data []byte,
		shippedAt, deliveredAt, canceledAt *time.Time,
	) (models.Fulfillment, error)
	// UpdateFulfillmentStatus durumu, takip bilgisini ve damgaları MUTLAK
	// değerlerle yazar.
	UpdateFulfillmentStatus(
		ctx context.Context,
		id string,
		status models.FulfillmentStatus,
		trackingNumber, trackingURL string,
		shippedAt, deliveredAt, canceledAt *time.Time,
	) (models.Fulfillment, error)

	// CreateFulfillmentItem gönderiye bir kalem ekler.
	CreateFulfillmentItem(ctx context.Context, item models.FulfillmentItem) (models.FulfillmentItem, error)
	// ListFulfillmentItems bir gönderinin kalemlerini döner.
	ListFulfillmentItems(ctx context.Context, fulfillmentID string) ([]models.FulfillmentItem, error)
	// FulfillmentItemsByFulfillments kalemleri BİRDEN ÇOK gönderi için TEK
	// sorguda döner (N+1 yok).
	FulfillmentItemsByFulfillments(ctx context.Context, fulfillmentIDs []string) ([]models.FulfillmentItem, error)
}

// Options servisin kurulum bağımlılıklarıdır.
type Options struct {
	// Store kalıcılık yüzeyidir; zorunludur.
	Store Store
	// Providers kayıtlı kargo sağlayıcılarıdır; zorunludur.
	Providers *ProviderRegistry
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
	// Clock "şimdi"yi üretir; nil verilirse time.Now kullanılır.
	//
	// Enjekte edilebilir olması testler içindir: bir gönderinin sevk anının
	// gerçekten yazıldığı, sabit bir saatle kesin olarak sınanabilir.
	Clock func() time.Time
}

// Service fulfillment modülünün dışa açık servisidir.
// Eşzamanlı kullanıma güvenlidir.
type Service struct {
	store     Store
	providers *ProviderRegistry
	log       *slog.Logger
	clock     func() time.Time
}

// New verilen bağımlılıklarla bir servis üretir.
//
// Eksik bir bağımlılık kurulum hatasıdır ve AÇIKÇA döner: nil bir depoyla
// kurulmuş servis ilk istekte panik üretirdi ve hata, kurulumdan çok sonra
// ortaya çıkardı.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.Internal(CodeNotReady, "fulfillment servisi depo olmadan kurulamaz")
	}
	if opts.Providers == nil {
		return nil, errors.Internal(CodeNotReady, "fulfillment servisi sağlayıcı kaydı olmadan kurulamaz")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Service{store: opts.Store, providers: opts.Providers, log: log, clock: clock}, nil
}

// ProviderIDs kayıtlı kargo sağlayıcılarının kimliklerini sıralı döner.
//
// Yönetim yüzeyi bir seçenek açarken hangi sağlayıcıların kurulu olduğunu
// buradan öğrenir; sağlayıcı nesnesinin kendisi dışarı SIZMAZ — dışarıya
// açılan tek şey kimliktir.
//
// ctx bugün kullanılmaz; imzada durmasının sebebi, sağlayıcı kaydının ileride
// (Faz 9 plugin sistemi) süreç dışından beslenebilecek olmasıdır. Projede tüm
// servis metotları context alır ve o gün imza değişmek zorunda kalmamalıdır.
func (s *Service) ProviderIDs(_ context.Context) []string { return s.providers.IDs() }

// now servisin saatinden UTC bir an döner.
func (s *Service) now() time.Time { return s.clock().UTC() }

// Page liste isteklerinin sayfalama parametreleridir.
type Page struct {
	// Limit döndürülecek azami satır sayısıdır; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// normalize sayfalama parametrelerini doğrular ve varsayılanları uygular.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "limit negatif olamaz: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "offset negatif olamaz: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"limit en fazla %d olabilir: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

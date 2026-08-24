// Package service payment modülünün iş mantığıdır.
//
// Modülün sorumluluğu tek cümleyle: bir sepet ya da sipariş için PARANIN
// hangi aşamada olduğunu bilmek — bloke mi, çekildi mi, iade mi edildi.
//
// # Durum makinesi
//
// Oturumun geçiş tablosu [models.SessionStatus] üzerindeki AuthorizeAction,
// CaptureAction ve CancelAction metotlarında, saf fonksiyonlar olarak durur;
// bu paket yalnızca sonucu tipli hataya çevirir. Geçersiz her geçiş
// errors.Conflict döner (örn. tahsil edilmiş bir oturumu yetkilendirmek).
// Zaten hedef durumdaki bir geçiş ise hata DEĞİL, sessiz bir no-op'tur;
// idempotentlik oradan gelir.
//
// Koleksiyonun durumu saklanır ama TÜRETİLİR: her mutasyondan sonra
// tutarlardan ve oturum sayımlarından yeniden hesaplanıp yazılır
// (bkz. [models.CollectionStatusFor]).
//
// # Eşzamanlılık ve kilit sırası
//
// Para yazan HER akış tek bir veritabanı işleminde koşar ve kilitleri DAİMA
// aynı sırada alır: önce KOLEKSİYON, sonra OTURUM, sonra TAHSİLAT. Sıra akışa
// göre değişseydi iki akış aynı iki satırı ters sırada isteyip birbirini
// kilitler ve veritabanı işlemlerden birini öldürürdü.
//
// Koleksiyon kilidi yalnızca bir varlık kontrolü değildir; sıranın kendisidir
// ve koleksiyonun türetilmiş durumunun tek bir hesaptan yazılmasını sağlar.
// Aynı oturumu aynı anda yetkilendiren iki çağrıdan TAM OLARAK BİRİ sağlayıcıya
// gider; ikincisi birincinin yazdığı durumu görür ve no-op'a düşer.
//
// # Sağlayıcı çağrısı işlemin İÇİNDEDİR
//
// Yetkilendirme, tahsilat ve iptal, sağlayıcıyı satır kilidi ALTINDA çağırır.
// Bedeli açıktır: yavaş bir sağlayıcı oturumun satır kilidini o süre boyunca
// tutar. Karşılığında kazanılan şey "tam olarak bir yetkilendirme" garantisidir
// — kilit sağlayıcı çağrısından önce bırakılsaydı iki eşzamanlı çağrı da
// sağlayıcıya gider ve tekillik yalnızca sağlayıcının kendi idempotency'sine
// kalırdı; her sağlayıcı bunu sunmaz. Alternatif, ara bir "authorizing"
// durumuyla iki fazlı yazmaktır ve sertleştirme fazına (Faz 9) aittir.
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
// [models.PaymentCollection.Reference] bir sepet ya da sipariş kimliğidir;
// serbest metin olarak saklanır, foreign key verilmez (Prensip 2.2) ve varlığı
// burada doğrulanmaz — doğrulama, o modülleri tanıyan workflow'un işidir.
package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// EntityName modülün Query katmanına sunduğu entity adıdır. Sağlayıcı
// container'a "<EntityName>.query" adıyla kaydedilir (ADR 0004).
const EntityName = "payment_collection"

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "payment_invalid_input"
	// CodeInvalidTransition durum makinesinde geçersiz bir geçiş denendiğini
	// bildirir (örn. tahsil edilmiş bir oturumu yetkilendirmek).
	CodeInvalidTransition = "payment_invalid_transition"
	// CodeAuthorizationDeclined sağlayıcının yetkilendirmeyi reddettiğini
	// bildirir. Sunucu hatası DEĞİLDİR; beklenen bir iş sonucudur.
	CodeAuthorizationDeclined = "payment_authorization_declined"
	// CodeProviderNotFound istenen sağlayıcının kayıtlı olmadığını bildirir.
	CodeProviderNotFound = "payment_provider_not_found"
	// CodeProviderExists aynı kimlikle ikinci bir sağlayıcı kaydedilmek
	// istendiğini bildirir.
	CodeProviderExists = "payment_provider_already_registered"
	// CodeIdempotencyMismatch aynı anahtarın BAŞKA bir koleksiyon için
	// kullanıldığını bildirir.
	CodeIdempotencyMismatch = "payment_idempotency_key_mismatch"
	// CodeCollectionClosed kapanmış bir koleksiyona yeni oturum açılmak
	// istendiğini bildirir.
	CodeCollectionClosed = "payment_collection_closed"
	// CodeSessionTerminal idempotency anahtarının SONLANMIŞ (iptal edilmiş ya
	// da reddedilmiş) bir oturuma ait olduğunu bildirir; çağıranın YENİ bir
	// anahtarla devam etmesi gerekir.
	CodeSessionTerminal = "payment_session_terminal"
	// CodeNothingToRefund iade edilecek tutar kalmadığını bildirir.
	CodeNothingToRefund = "payment_nothing_to_refund"
	// CodeProviderContract sağlayıcının sözleşme dışı bir yanıt döndüğünü
	// bildirir; normal işleyişte oluşmaz.
	CodeProviderContract = "payment_provider_contract_violation"
	// CodeInconsistentState koleksiyon tutarları ile alt kayıtların birbirini
	// tutmadığını bildirir; normal işleyişte oluşmaz.
	CodeInconsistentState = "payment_inconsistent_state"
	// CodeNotReady servisin eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "payment_service_not_ready"
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

// Store servisin ihtiyaç duyduğu kalıcılık yüzeyidir.
//
// Arayüz TÜKETEN tarafta, yani burada tanımlıdır (ADR 0001'in örüntüsü). Servis
// repository paketini import ETMEZ; somut depo bu imzaları yapısal olarak
// karşılar ve bağlantı module.go'da kurulur. Böylece birim testleri gerçek bir
// veritabanı olmadan, birkaç satırlık bir sahte depo ile yazılabilir.
//
// # Manuel sağlayıcının defteri BURADA YOKTUR
//
// Somut depo payment_manual_sessions tablosuna da erişir, ama o metotlar bu
// arayüze BİLİNÇLİ OLARAK alınmamıştır: sağlayıcının iç durumu modülün verisi
// değildir ve servise ona dokunma imkânı verilmemelidir. Sınır bir yorum değil,
// tip sistemidir.
//
// # İşlem sınırı
//
// [Store.WithTx] verilen işlevi tek bir veritabanı işleminde çalıştırır ve
// işlemi işlevin aldığı context ile taşır. Bu yüzden işlem içindeki her çağrı
// İŞLEVE VERİLEN ctx ile yapılmalıdır; dıştaki ctx kullanılırsa o çağrı işlemin
// dışında kalır ve atomiklik sessizce kaybolur.
//
// Lock ile başlayan metotlar satırı işlem sonuna kadar kilitler ve yalnızca
// [Store.WithTx] içinde çağrılabilir. Kilitler DAİMA koleksiyon -> oturum ->
// tahsilat sırasında alınır.
type Store interface {
	// WithTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem geri alınır.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreatePaymentCollection yeni bir ödeme koleksiyonu kaydeder.
	CreatePaymentCollection(ctx context.Context, col models.PaymentCollection) (models.PaymentCollection, error)
	// GetPaymentCollection koleksiyonu kimliğiyle döner; yoksa NotFound.
	GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error)
	// LockPaymentCollection koleksiyonu kilitler ve güncel hâlini döner.
	LockPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error)
	// ListPaymentCollections koleksiyonları süzer ve sayfalar; ikinci değer
	// süzgece uyan TÜM satırların sayısıdır.
	ListPaymentCollections(ctx context.Context, filter models.CollectionFilter) ([]models.PaymentCollection, int64, error)
	// PaymentCollectionsByIDs kimlik kümesini TEK sorguda getirir (N+1 yok).
	PaymentCollectionsByIDs(ctx context.Context, ids []string) ([]models.PaymentCollection, error)
	// UpdatePaymentCollectionTotals tutarları ve türetilmiş durumu MUTLAK
	// değerlerle yazar.
	UpdatePaymentCollectionTotals(
		ctx context.Context,
		id string,
		status models.CollectionStatus,
		authorized, captured, refunded int64,
	) (models.PaymentCollection, error)

	// CreatePaymentSession yeni bir ödeme oturumu kaydeder.
	CreatePaymentSession(ctx context.Context, ses models.PaymentSession) (models.PaymentSession, error)
	// GetPaymentSession oturumu kimliğiyle döner; yoksa NotFound.
	GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error)
	// LockPaymentSession oturumu kilitler ve güncel hâlini döner.
	LockPaymentSession(ctx context.Context, id string) (models.PaymentSession, error)
	// PaymentSessionByIdempotencyKey aynı anahtarla açılmış oturumu döner;
	// yoksa NotFound.
	PaymentSessionByIdempotencyKey(ctx context.Context, providerID, key string) (models.PaymentSession, error)
	// ListPaymentSessionsByCollection koleksiyonun oturumlarını döner.
	ListPaymentSessionsByCollection(ctx context.Context, collectionID string) ([]models.PaymentSession, error)
	// SessionCounts koleksiyonun oturumlarını duruma göre TEK sorguda sayar.
	SessionCounts(ctx context.Context, collectionID string) (models.SessionCounts, error)
	// LiveSessionAmount koleksiyonun canlı (bekleyen ya da yetkilendirilmiş)
	// oturumlarının rezerve ettiği toplam tutarı döner; yoksa 0.
	LiveSessionAmount(ctx context.Context, collectionID string) (int64, error)
	// UpdatePaymentSessionState oturumun durumunu, bloke tutarını, ham
	// sağlayıcı verisini ve ret sebebini MUTLAK değerlerle yazar.
	UpdatePaymentSessionState(
		ctx context.Context,
		id string,
		status models.SessionStatus,
		authorizedAmount int64,
		data []byte,
		declineReason string,
	) (models.PaymentSession, error)

	// CreatePayment yeni bir tahsilat kaydeder.
	CreatePayment(ctx context.Context, pay models.Payment) (models.Payment, error)
	// GetPayment tahsilatı kimliğiyle döner; yoksa NotFound.
	GetPayment(ctx context.Context, id string) (models.Payment, error)
	// LockPayment tahsilatı kilitler ve güncel hâlini döner.
	LockPayment(ctx context.Context, id string) (models.Payment, error)
	// PaymentBySession oturumdan doğan tahsilatı döner; yoksa NotFound.
	PaymentBySession(ctx context.Context, sessionID string) (models.Payment, error)
	// ListPaymentsByCollection koleksiyonun tahsilatlarını döner.
	ListPaymentsByCollection(ctx context.Context, collectionID string) ([]models.Payment, error)
	// UpdatePaymentRefundedAmount iade edilen tutarı MUTLAK değerle yazar.
	UpdatePaymentRefundedAmount(ctx context.Context, id string, refunded int64) (models.Payment, error)

	// CreateRefund yeni bir iade kaydeder.
	CreateRefund(ctx context.Context, ref models.Refund) (models.Refund, error)
	// ListRefundsByPayment tahsilatın iadelerini döner.
	ListRefundsByPayment(ctx context.Context, paymentID string) ([]models.Refund, error)
}

// Options servisin kurulum bağımlılıklarıdır.
type Options struct {
	// Store kalıcılık yüzeyidir; zorunludur.
	Store Store
	// Providers kayıtlı ödeme sağlayıcılarıdır; zorunludur.
	Providers *ProviderRegistry
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// Service payment modülünün dışa açık servisidir.
// Eşzamanlı kullanıma güvenlidir.
type Service struct {
	store     Store
	providers *ProviderRegistry
	log       *slog.Logger
}

// New verilen bağımlılıklarla bir servis üretir.
//
// Eksik bir bağımlılık kurulum hatasıdır ve AÇIKÇA döner: nil bir depoyla
// kurulmuş servis ilk istekte panik üretirdi ve hata, kurulumdan çok sonra
// ortaya çıkardı.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.Internal(CodeNotReady, "payment servisi depo olmadan kurulamaz")
	}
	if opts.Providers == nil {
		return nil, errors.Internal(CodeNotReady, "payment servisi sağlayıcı kaydı olmadan kurulamaz")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: opts.Store, providers: opts.Providers, log: log}, nil
}

// ProviderIDs kayıtlı ödeme sağlayıcılarının kimliklerini sıralı döner.
//
// Vitrinin ödeme adımı hangi yolların açık olduğunu buradan öğrenir; sağlayıcı
// nesnesinin kendisi dışarı SIZMAZ — dışarıya açılan tek şey kimliktir ve ödeme
// akışları sağlayıcıyı o kimlikle ister.
//
// ctx bugün kullanılmaz; imzada durmasının sebebi, sağlayıcı kaydının ileride
// (Faz 9 plugin sistemi) süreç dışından beslenebilecek olmasıdır. Projede tüm
// servis metotları context alır ve o gün imza değişmek zorunda kalmamalıdır.
func (s *Service) ProviderIDs(_ context.Context) []string { return s.providers.IDs() }

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

// writeCollectionTotals koleksiyonun tutarlarını yazar ve durumunu YENİDEN
// TÜRETİR.
//
// Durum hiçbir akışta elle atanmaz; hep buradan geçer. Alternatif — her akışın
// kendi durumunu yazması — aynı kuralın beş yere dağılması ve bir dalın
// unutulması demekti. Oturum sayımı bu yüzden BURADA, oturum satırı
// güncellendikten SONRA okunur.
//
// İşlem İÇİNDE çağrılmalıdır; koleksiyonun kilidi çağıran tarafından alınmıştır.
func (s *Service) writeCollectionTotals(
	ctx context.Context,
	col models.PaymentCollection,
	authorized, captured, refunded int64,
) (models.PaymentCollection, error) {
	if authorized < 0 || captured < 0 || refunded < 0 {
		return models.PaymentCollection{}, errors.Internal(CodeInconsistentState,
			"koleksiyon tutarı negatife düşerdi: bloke %d, tahsil %d, iade %d (%s)",
			authorized, captured, refunded, col.ID)
	}

	counts, err := s.store.SessionCounts(ctx, col.ID)
	if err != nil {
		return models.PaymentCollection{}, err
	}

	next := col
	next.AuthorizedAmount = authorized
	next.CapturedAmount = captured
	next.RefundedAmount = refunded

	return s.store.UpdatePaymentCollectionTotals(ctx, col.ID,
		models.CollectionStatusFor(next, counts), authorized, captured, refunded)
}

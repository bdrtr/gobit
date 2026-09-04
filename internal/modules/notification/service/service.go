// Package service notification modülünün iş mantığıdır.
//
// Modülün sorumluluğu tek cümleyle: bir olayın müşteriye bildirilmesi gerekiyorsa
// bunu SEÇİLİ sağlayıcıya yaptırmak ve denemeyi bir günlüğe yazmak. Metnin
// kendisini bu modül üretmez; şablonu sağlayıcı çözer (bkz. çekirdekteki
// [coreprovider.Notification]).
//
// # Neden bir teslim günlüğü var
//
// Gönderilmiş bir e-posta geri alınamaz, dolayısıyla bu modülde telafi
// (compensation) yolu da yoktur. Elde kalan tek koruma TEKRARI ÖNLEMEKTİR ve o
// da ancak kalıcı bir kayıtla mümkündür: (şablon, referans) çifti günlükte
// BENZERSİZDİR ve kayıt sağlayıcıya gidilmeden ÖNCE açılır. Kaydın ikinci işi
// teşhistir — "müşteriye onay gitti mi" sorusunun cevabı başka hiçbir yerde
// yoktur.
//
// # Modül izolasyonu
//
// Bu modül başka hiçbir modülü tanımaz (Prensip 2.1/2.4, ADR 0001). Siparişin
// iletişim bilgisi, bu pakette tanımlı DAR arayüzle ([OrderContactReader]) ve
// container'dan ADLA çözülen "order.interop" yüzeyinden okunur; taşınan veri
// JSON'dur (ADR 0006). [models.Delivery.Reference] bir sipariş kimliğidir;
// serbest metin olarak saklanır ve varlığı burada doğrulanmaz.
package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "notification_invalid_input"
	// CodeProviderNotFound seçili sağlayıcının kayıtlı olmadığını bildirir.
	CodeProviderNotFound = "notification_provider_not_found"
	// CodeProviderExists aynı kimlikle ikinci bir sağlayıcı kaydedilmek
	// istendiğini bildirir.
	CodeProviderExists = "notification_provider_already_registered"
	// CodeSendFailed sağlayıcının gönderimi reddettiğini bildirir.
	CodeSendFailed = "notification_send_failed"
	// CodeEventInvalid olay yükünün sözleşmeye uymadığını bildirir.
	CodeEventInvalid = "notification_event_payload_invalid"
	// CodeContactUnavailable sipariş okuma yüzeyine ulaşılamadığını bildirir.
	CodeContactUnavailable = "notification_order_contact_unavailable"
	// CodeContactInvalid sipariş yüzeyinin yanıtının çözülemediğini bildirir.
	CodeContactInvalid = "notification_order_contact_invalid"
	// CodeNotReady servisin eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "notification_service_not_ready"
)

// Sayfalama sınırları (plan Bölüm 8: limit/offset).
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int64 = 50
	// MaxLimit tek istekte istenebilecek en büyük sayfa boyutudur.
	MaxLimit int64 = 100
)

// Store servisin ihtiyaç duyduğu kalıcılık yüzeyidir.
//
// Arayüz TÜKETEN tarafta, yani burada tanımlıdır (ADR 0001'in örüntüsü). Servis
// repository paketini import ETMEZ; somut depo bu imzaları yapısal olarak
// karşılar ve bağlantı module.go'da kurulur. Böylece birim testleri gerçek bir
// veritabanı olmadan, birkaç satırlık bir sahte depo ile yazılabilir.
type Store interface {
	// ClaimDelivery kaydı yalnızca (şablon, referans) çifti henüz
	// kullanılmamışsa yazar. İkinci dönüş değeri satırın yazılıp
	// yazılmadığıdır; çakışma HATA DEĞİLDİR.
	ClaimDelivery(ctx context.Context, d models.Delivery) (models.Delivery, bool, error)
	// FinishDelivery gönderim denemesinin sonucunu yazar.
	FinishDelivery(
		ctx context.Context,
		id string,
		status models.DeliveryStatus,
		failure string,
	) (models.Delivery, error)
	// GetDelivery kaydı kimliğiyle döner; yoksa NotFound.
	GetDelivery(ctx context.Context, id string) (models.Delivery, error)
	// ListDeliveries kayıtları süzer ve sayfalar; ikinci değer süzgece uyan
	// TÜM satırların sayısıdır.
	ListDeliveries(ctx context.Context, filter models.DeliveryFilter) ([]models.Delivery, int64, error)
}

// Options servisin kurulum bağımlılıklarıdır.
type Options struct {
	// Store kalıcılık yüzeyidir; zorunludur.
	Store Store
	// Providers kayıtlı bildirim sağlayıcılarıdır; zorunludur.
	Providers *ProviderRegistry
	// ProviderID gönderimde KULLANILACAK sağlayıcının kimliğidir
	// (NOTIFICATION_PROVIDER); zorunludur.
	//
	// Sağlayıcının kayıtlı olup olmadığı BURADA doğrulanmaz ve doğrulanamaz:
	// eklentilerin getirdiği sağlayıcılar modüller ayağa kalktıktan SONRA
	// kaydedilir (bkz. coreplugin.Registry'nin iki fazı). Kurulumun tamamı
	// bittikten sonraki denetim kompozisyon kökündedir (cmd/server).
	ProviderID string
	// Contacts sipariş iletişim bilgisinin okunduğu yüzeydir; zorunludur.
	Contacts OrderContactReader
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// Service notification modülünün dışa açık servisidir.
// Eşzamanlı kullanıma güvenlidir.
type Service struct {
	store      Store
	providers  *ProviderRegistry
	providerID string
	contacts   OrderContactReader
	log        *slog.Logger
}

// New verilen bağımlılıklarla bir servis üretir.
//
// Eksik bir bağımlılık kurulum hatasıdır ve AÇIKÇA döner: nil bir depoyla
// kurulmuş servis ilk olayda panik üretirdi ve hata, kurulumdan çok sonra —
// ilk siparişin verildiği anda — ortaya çıkardı.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.Internal(CodeNotReady, "notification servisi depo olmadan kurulamaz")
	}
	if opts.Providers == nil {
		return nil, errors.Internal(CodeNotReady,
			"notification servisi sağlayıcı kaydı olmadan kurulamaz")
	}
	if opts.Contacts == nil {
		return nil, errors.Internal(CodeNotReady,
			"notification servisi sipariş okuma yüzeyi olmadan kurulamaz")
	}
	if opts.ProviderID == "" {
		return nil, errors.Internal(CodeNotReady,
			"notification servisi sağlayıcı kimliği olmadan kurulamaz")
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		store:      opts.Store,
		providers:  opts.Providers,
		providerID: opts.ProviderID,
		contacts:   opts.Contacts,
		log:        log,
	}, nil
}

// ProviderID gönderimde kullanılan sağlayıcının kimliğini döner.
func (s *Service) ProviderID() string { return s.providerID }

// ListDeliveriesInput teslim günlüğü listelemesinin girdisidir.
type ListDeliveriesInput struct {
	// Reference verilirse yalnızca o siparişin kayıtları döner.
	Reference *string
	// Status verilirse yalnızca o durumdaki kayıtlar döner.
	Status *string
	// Page sayfalama parametreleridir.
	Page Page
}

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
			"the limit can be at most %d: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// ListDeliveries teslim günlüğünü süzerek ve sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM kayıtların sayısıdır.
//
// Tanınmayan bir durum süzgeci errors.Invalid ile REDDEDİLİR; sessizce boş
// liste dönmek, "hiç başarısız bildirim yok" ile "durum adını yanlış yazdım"ı
// ayırt edilemez hâle getirirdi.
func (s *Service) ListDeliveries(
	ctx context.Context,
	in ListDeliveriesInput,
) ([]models.Delivery, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.Status != nil && !models.DeliveryStatus(*in.Status).Valid() {
		return nil, 0, errors.Invalid(CodeInvalidInput,
			"%q tanınmayan bir teslim durumu; %s, %s, %s ya da %s olmalı",
			*in.Status, models.DeliveryPending, models.DeliverySent,
			models.DeliveryFailed, models.DeliverySkipped)
	}

	return s.store.ListDeliveries(ctx, models.DeliveryFilter{
		Reference: in.Reference,
		Status:    in.Status,
		Limit:     page.Limit,
		Offset:    page.Offset,
	})
}

// GetDelivery tek bir teslim kaydını döner; yoksa errors.NotFound.
func (s *Service) GetDelivery(ctx context.Context, id string) (models.Delivery, error) {
	if id == "" {
		return models.Delivery{}, errors.Invalid(CodeInvalidInput, "kayıt kimliği boş olamaz")
	}
	return s.store.GetDelivery(ctx, id)
}

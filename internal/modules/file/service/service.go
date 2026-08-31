// Package service file modülünün iş mantığıdır.
//
// Modülün sorumluluğu tek cümleyle: istemciden gelen RASTGELE BAYTLARI
// denetleyip bir depoya yazdırmak ve yazılanın ne olduğunu kalıcı bir deftere
// geçirmek. Baytları saklayan taraf bu modül değil, çekirdekteki FileProvider
// sözleşmesini karşılayan bir SAĞLAYICIDIR.
//
// # Denetim NEREDE yapılır
//
// İçerik tipi İÇERİKTEN tespit edilir ve bu tespit HTTP katmanında yapılır —
// ilk baytları okuyabilen tek yer orasıdır. İzin listesi ise BURADA uygulanır
// ve sağlayıcıya gitmeden ÖNCE: depoya tek bayt yazılmadan reddedilen bir
// dosyanın temizlenmesi gerekmez. Denetimi sağlayıcıya bırakmak, her
// sağlayıcının aynı kuralı yeniden yazması ve birinin unutması demekti.
//
// # Modül izolasyonu
//
// Bu modül başka hiçbir modülü tanımaz (Prensip 2.1/2.4, ADR 0001).
// [models.Upload.UploadedBy] bir kullanıcı ya da API anahtarı kimliğidir;
// serbest metin olarak saklanır ve varlığı burada doğrulanmaz.
package service

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "file_invalid_input"
	// CodeProviderNotFound seçili sağlayıcının kayıtlı olmadığını bildirir.
	CodeProviderNotFound = "file_provider_not_found"
	// CodeProviderExists aynı kimlikle ikinci bir sağlayıcı kaydedilmek
	// istendiğini bildirir.
	CodeProviderExists = "file_provider_already_registered"
	// CodeTypeNotAllowed tespit edilen içerik tipinin izin listesinde
	// olmadığını bildirir.
	CodeTypeNotAllowed = "file_content_type_not_allowed"
	// CodeTooLarge gövdenin azami boyutu aştığını bildirir.
	CodeTooLarge = "file_upload_too_large"
	// CodeUploadFailed sağlayıcının yazma işlemini tamamlayamadığını bildirir.
	CodeUploadFailed = "file_upload_failed"
	// CodeNotServable kaydın sağlayıcısının dosya okumayı desteklemediğini
	// bildirir.
	CodeNotServable = "file_not_servable"
	// CodeNotReady servisin eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "file_service_not_ready"
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
	// CreateUpload yükleme kaydını yazar.
	CreateUpload(ctx context.Context, u models.Upload) (models.Upload, error)
	// GetUpload kaydı kimliğiyle döner; yoksa NotFound.
	GetUpload(ctx context.Context, id string) (models.Upload, error)
	// GetUploadByKey kaydı depo anahtarıyla döner; yoksa NotFound.
	GetUploadByKey(ctx context.Context, key string) (models.Upload, error)
	// ListUploads kayıtları sayfalar; ikinci değer TÜM satırların sayısıdır.
	ListUploads(ctx context.Context, filter models.UploadFilter) ([]models.Upload, int64, error)
	// DeleteUpload kaydı siler; ikinci değer satırın gerçekten silinip
	// silinmediğidir ve olmayan kimlik HATA DEĞİLDİR.
	DeleteUpload(ctx context.Context, id string) (bool, error)
}

// Options servisin kurulum bağımlılıklarıdır.
type Options struct {
	// Store kalıcılık yüzeyidir; zorunludur.
	Store Store
	// Providers kayıtlı dosya sağlayıcılarıdır; zorunludur.
	Providers *ProviderRegistry
	// ProviderID YÜKLEMEDE kullanılacak sağlayıcının kimliğidir
	// (FILE_PROVIDER); zorunludur.
	//
	// Sağlayıcının kayıtlı olup olmadığı BURADA doğrulanmaz ve doğrulanamaz:
	// eklentilerin getirdiği sağlayıcılar modüller ayağa kalktıktan SONRA
	// kaydedilir (bkz. coreplugin.Registry'nin iki fazı). Kurulumun tamamı
	// bittikten sonraki denetim kompozisyon kökündedir (cmd/server).
	ProviderID string
	// MaxUploadBytes tek bir yüklemenin azami boyutudur; zorunludur.
	MaxUploadBytes int64
	// AllowedTypes kabul edilen İÇERİK tipleridir; en az bir tip zorunludur.
	AllowedTypes []string
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// Service file modülünün dışa açık servisidir.
// Eşzamanlı kullanıma güvenlidir.
type Service struct {
	store        Store
	providers    *ProviderRegistry
	providerID   string
	maxBytes     int64
	allowedTypes []string
	log          *slog.Logger
}

// New verilen bağımlılıklarla bir servis üretir.
//
// Eksik bir bağımlılık kurulum hatasıdır ve AÇIKÇA döner: nil bir depoyla
// kurulmuş servis ilk yüklemede panik üretirdi ve hata, kurulumdan çok sonra
// ortaya çıkardı.
//
// BOŞ izin listesi de reddedilir. "Liste boşsa her şeyi kabul et" en tehlikeli
// varsayılan olurdu: yapılandırmadaki tek bir yazım hatası, denetimi sessizce
// kaldırırdı.
func New(opts Options) (*Service, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.Internal(CodeNotReady, "file servisi depo olmadan kurulamaz")
	case opts.Providers == nil:
		return nil, errors.Internal(CodeNotReady, "file servisi sağlayıcı kaydı olmadan kurulamaz")
	case opts.ProviderID == "":
		return nil, errors.Internal(CodeNotReady, "file servisi sağlayıcı kimliği olmadan kurulamaz")
	case opts.MaxUploadBytes <= 0:
		return nil, errors.Internal(CodeNotReady,
			"file servisi pozitif bir boyut sınırı olmadan kurulamaz, %d verildi", opts.MaxUploadBytes)
	case len(opts.AllowedTypes) == 0:
		return nil, errors.Internal(CodeNotReady,
			"file servisi boş izin listesiyle kurulamaz; kabul edilen tipler açıkça sayılmalıdır")
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	// Liste KOPYALANIR ve sıralanır: çağıranın dilimini olduğu gibi tutmak,
	// onu sonradan değiştiren bir kodun izin listesini çalışırken
	// genişletebilmesi demekti. Sıra ise hata mesajını kararlı kılar.
	tipler := slices.Clone(opts.AllowedTypes)
	slices.Sort(tipler)

	return &Service{
		store:        opts.Store,
		providers:    opts.Providers,
		providerID:   opts.ProviderID,
		maxBytes:     opts.MaxUploadBytes,
		allowedTypes: tipler,
		log:          log,
	}, nil
}

// ProviderID yüklemede kullanılan sağlayıcının kimliğini döner.
func (s *Service) ProviderID() string { return s.providerID }

// MaxUploadBytes tek bir yüklemenin azami boyutunu döner.
//
// HTTP katmanı bunu okur: gövdeyi saran [net/http.MaxBytesReader] sınırı
// isteğin TAMAMINA uygular ve sınırı iki yerde ayrı ayrı yazmak, ikisinin
// sessizce ayrışması demek olurdu.
func (s *Service) MaxUploadBytes() int64 { return s.maxBytes }

// AllowedTypes kabul edilen içerik tiplerini sıralı olarak döner.
func (s *Service) AllowedTypes() []string { return slices.Clone(s.allowedTypes) }

// Page liste isteklerinin sayfalama parametreleridir.
type Page struct {
	// Limit döndürülecek azami satır sayısıdır; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// normalize sayfalama parametrelerini doğrular ve varsayılanları uygular.
func (p Page) normalize() (Page, error) {
	switch {
	case p.Limit < 0:
		return Page{}, errors.Invalid(CodeInvalidInput, "limit negatif olamaz: %d", p.Limit)
	case p.Offset < 0:
		return Page{}, errors.Invalid(CodeInvalidInput, "offset negatif olamaz: %d", p.Offset)
	case p.Limit > MaxLimit:
		return Page{}, errors.Invalid(CodeInvalidInput,
			"limit en fazla %d olabilir: %d", MaxLimit, p.Limit)
	case p.Limit == 0:
		p.Limit = DefaultLimit
	}

	return p, nil
}

// ListUploads yükleme defterini sayfalayarak döner.
// İkinci dönüş değeri TÜM kayıtların sayısıdır.
func (s *Service) ListUploads(ctx context.Context, page Page) ([]models.Upload, int64, error) {
	normal, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}

	return s.store.ListUploads(ctx, models.UploadFilter{Limit: normal.Limit, Offset: normal.Offset})
}

// GetUpload tek bir yükleme kaydını döner; yoksa errors.NotFound.
func (s *Service) GetUpload(ctx context.Context, id string) (models.Upload, error) {
	if id == "" {
		return models.Upload{}, errors.Invalid(CodeInvalidInput, "yükleme kimliği boş olamaz")
	}

	return s.store.GetUpload(ctx, id)
}

// DeleteUpload dosyayı depodan ve kaydı defterden siler. İDEMPOTENTTİR:
// olmayan bir kimlik hata DEĞİLDİR.
//
// # Neden idempotent
//
// Silme bir SON DURUM iddiasıdır ("bu yükleme artık yok") ve çağıran onu
// yeniden deneyebilir: bir ürün görselini kaldıran akış, ikinci turunda ikinci
// kez bu ucu çağırır. İkinci çağrının 404 dönmesi, istenen son durum
// SAĞLANMIŞKEN akışı hata sayardı — yani tam olarak temizlemesi gereken şeyi
// temizlenemez kılardı.
//
// # Neden ÖNCE dosya, SONRA kayıt
//
// İki taraf ayrı sistemlerdedir ve tek bir işleme alınamaz; geriye yalnızca
// SIRA kalır. Bu sırada oluşabilecek tek tutarsızlık "kaydı var, dosyası yok"
// hâlidir ve yeniden deneme onu KAPATIR: sağlayıcının silmesi idempotenttir,
// ikinci turda hata vermez ve kayıt da silinir. Ters sıra yakınsamazdı —
// kayıt gittikten sonra dosyanın anahtarını bilen kimse kalmaz, silme
// başarısız olmuşsa o dosya sonsuza kadar erişilemez çöp olurdu.
func (s *Service) DeleteUpload(ctx context.Context, id string) error {
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "yükleme kimliği boş olamaz")
	}

	kayit, err := s.store.GetUpload(ctx, id)
	if err != nil {
		// Kayıt yoksa yapacak bir şey de yoktur; son durum zaten sağlanmıştır.
		if errors.IsNotFound(err) {
			return nil
		}

		return err
	}

	// Sağlayıcı kaydın KENDİ sağlayıcısıdır, o an yapılandırılmış olan değil:
	// kurulum bir gün nesne deposuna geçtiğinde eski kayıtlar hâlâ yerel
	// diskte durur ve onları silebilecek tek şey onları yazan sağlayıcıdır.
	prov, err := s.providers.Get(kayit.ProviderID)
	if err != nil {
		return err
	}

	if err := prov.Delete(ctx, kayit.StorageKey); err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeUploadFailed,
			"dosya depodan silinemedi: %s", kayit.StorageKey)
	}

	if _, err := s.store.DeleteUpload(ctx, id); err != nil {
		return err
	}

	s.log.DebugContext(ctx, "yükleme silindi",
		"upload_id", id, "saglayici", kayit.ProviderID)

	return nil
}

// OpenedFile sunulmaya hazır bir dosyadır.
type OpenedFile struct {
	// Upload dosyanın defterdeki kaydıdır. Sunulan Content-Type BURADAN
	// yazılır — istemcinin yükleme sırasında bildirdiği tipten değil.
	Upload models.Upload
	// Content dosyanın içeriğidir; çağıran KAPATMAKLA yükümlüdür.
	//
	// io.ReadSeeker olması gerekir: [net/http.ServeContent] aralık (Range)
	// isteklerini ancak konumlanabilir bir kaynakta karşılayabilir.
	Content io.ReadSeekCloser
	// ModTime dosyanın son değişme zamanıdır; koşullu istekler (If-Modified-Since)
	// bunun üzerinden yanıtlanır.
	ModTime time.Time
}

// fileOpener bir sağlayıcının dosya OKUYABİLDİĞİNİ bildiren OPSİYONEL
// yüzeydir.
//
// Çekirdek sözleşmesinde ([coreprovider.FileProvider]) yoktur ve olmamalıdır:
// bir nesne deposunda dosyayı CDN sunar, uygulama hiç okumaz. Yüzeyi zorunlu
// kılmak, sunmayacak sağlayıcılara asla çağrılmayacak bir metot yazdırmak
// olurdu.
//
// Arayüz TÜKETEN tarafta tanımlıdır (ADR 0001); local.Provider onu yapısal
// olarak karşılar.
type fileOpener interface {
	Open(ctx context.Context, key string) (io.ReadSeekCloser, time.Time, error)
}

// OpenByKey depo anahtarıyla bir dosyayı sunulmak üzere açar.
//
// # Sıra ÖNEMLİDİR: önce DEFTER, sonra DEPO
//
// Adres çubuğundan gelen anahtar önce veritabanına sorulur. Satır yoksa depoya
// hiç dokunulmaz; yani depoya ulaşabilen tek anahtar, bu modülün kendi üretip
// deftere yazdığı anahtardır. "Sunulan şey yalnızca yüklenmiş dosyalardır"
// iddiasını taşıyan yapı budur — bir dize denetimi değil.
//
// Sağlayıcı okumayı desteklemiyorsa errors.NotFound döner: nesne deposuna
// yazan bir kurulumda dosyanın gerçek adresi CDN'dedir ve bu yol boştur.
// "Uygulanmadı" demek yerine "burada yok" demek doğrudur — istemci için
// gerçekten yoktur.
func (s *Service) OpenByKey(ctx context.Context, key string) (OpenedFile, error) {
	if strings.TrimSpace(key) == "" {
		return OpenedFile{}, errors.Invalid(CodeInvalidInput, "depo anahtarı boş olamaz")
	}

	kayit, err := s.store.GetUploadByKey(ctx, key)
	if err != nil {
		return OpenedFile{}, err
	}

	prov, err := s.providers.Get(kayit.ProviderID)
	if err != nil {
		return OpenedFile{}, err
	}

	acici, destekliyor := prov.(fileOpener)
	if !destekliyor {
		return OpenedFile{}, errors.NotFound(CodeNotServable,
			"%q sağlayıcısı dosyaları uygulamadan sunmuyor", kayit.ProviderID)
	}

	icerik, modTime, err := acici.Open(ctx, kayit.StorageKey)
	if err != nil {
		return OpenedFile{}, err
	}

	return OpenedFile{Upload: kayit, Content: icerik, ModTime: modTime}, nil
}

// Package plugin çekirdeğe dokunmadan yetenek ekleyen eklentileri tanımlar.
//
// Bir eklenti modül, route, event subscriber ve sağlayıcı (payment,
// fulfillment, notification) kaydedebilir. Bunu yaparken hiçbir commerce
// modülünü import ETMEZ: kayıt noktalarına container'dan ADLA ulaşır ve
// sözleşmeleri çekirdekteki [github.com/bdrtr/gobit/internal/core/provider]
// paketinden alır (ADR 0001).
//
// # Neden derleme zamanı eklentisi
//
// Go'nun standart [plugin] paketi (.so yükleme) burada BİLİNÇLİ olarak
// kullanılmıyor. Nedenleri: yalnızca Linux/macOS'ta çalışır, çapraz derlemeyi
// desteklemez, eklenti ile ana ikilinin TÜM bağımlılıklarının bit düzeyinde
// aynı sürümde derlenmiş olmasını şart koşar ve yüklenen kod hiç boşaltılamaz.
// Bu kısıtlar, "eklentiyi çalışırken tak" vaadini pratikte "her eklenti için
// tüm uygulamayı yeniden derle"ye çevirir — yani derleme zamanı kaydının
// zaten sağladığı şeye, üstüne kırılganlık ekleyerek.
//
// Bunun yerine eklenti sıradan bir Go paketidir; uygulama onu import eder ve
// [Registry]'ye ekler. "Çekirdeğe dokunmadan" ölçütü şöyle karşılanır:
// eklenti eklemek yalnızca kurulum dosyasına bir satır ekler, çekirdeğin ya
// da herhangi bir modülün kodu DEĞİŞMEZ.
//
// # İki faz
//
// Eklentiler [Registry.Install] ile kurulur, [Registry.Start] ile başlatılır.
// Arada modüller ayağa kalkar. Bu ayrım zorunludur: eklenti bir sağlayıcıyı
// "payment.providers" kaydına eklemek ister ama o kayıt, payment modülü
// Register edilene kadar container'da YOKTUR. Install sırasında yapılan
// sağlayıcı ve subscriber kayıtları bu yüzden hemen uygulanmaz, KUYRUĞA
// alınır ve Start'ta işlenir.
package plugin

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/module"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// PaymentProvidersName ödeme sağlayıcı kaydının container'daki adıdır.
//
// payment modülünün ProvidersName sabitiyle aynı değeri taşır ama o paketi
// import ETMEZ: çekirdek modülleri import edemez (Prensip 2.4). Değer bir
// sözleşmedir; ikisinin uyumu [TestSaglayiciKayitAdlariUyusuyor] ile korunur.
const PaymentProvidersName = "payment.providers"

// FulfillmentProvidersName kargo sağlayıcı kaydının container'daki adıdır.
const FulfillmentProvidersName = "fulfillment.providers"

// NotificationProvidersName bildirim sağlayıcı kaydının container'daki adıdır.
const NotificationProvidersName = "notification.providers"

// Hata kodları.
const (
	codeNameEmpty       = "plugin_name_empty"
	codeNameDuplicate   = "plugin_name_duplicate"
	codeSetupFailed     = "plugin_setup_failed"
	codeStartFailed     = "plugin_start_failed"
	codeSinkMissing     = "plugin_provider_sink_missing"
	codeSinkUnusable    = "plugin_provider_sink_unusable"
	codeSubscribeFailed = "plugin_subscribe_failed"
	codeRouteConflict   = "plugin_route_conflict"
	codeRouteInvalid    = "plugin_route_invalid"
)

// Plugin çekirdeğe yetenek ekleyen bir eklentidir.
type Plugin interface {
	// Name eklentinin benzersiz adıdır (örn. "payment-stripe").
	// Loglarda ve hata mesajlarında kullanılır.
	Name() string

	// Setup eklentinin kayıtlarını [Host] üzerinden bildirir.
	//
	// DİKKAT: Bu aşamada modüller HENÜZ ayağa kalkmamıştır. Container'dan
	// modül servisi çözmeye çalışmayın; [Host]'un kayıt metodları çağrıyı
	// zaten kuyruğa alır ve doğru anda uygular.
	Setup(ctx context.Context, h *Host) error
}

// paymentSink ödeme sağlayıcı kaydının bu paketin ihtiyaç duyduğu dar
// yüzeyidir (tüketici tarafı arayüz, ADR 0001).
type paymentSink interface {
	Register(p coreprovider.PaymentProvider) error
}

// fulfillmentSink kargo sağlayıcı kaydının dar yüzeyidir.
type fulfillmentSink interface {
	Register(p coreprovider.FulfillmentProvider) error
}

// notificationSink bildirim sağlayıcı kaydının dar yüzeyidir.
type notificationSink interface {
	Register(p coreprovider.NotificationProvider) error
}

// routeKaydi bir eklentinin bağlamak istediği route işlevidir.
//
// İşlevin yanında eklentinin adı da taşınır: çakışma hatası "hangi yol"un yanı
// sıra "hangi eklenti" sorusunu da yanıtlayamazsa, kurulumu düzeltecek kişi
// tüm eklentileri tek tek denemek zorunda kalır.
type routeKaydi struct {
	// eklenti kaydı yapan eklentinin adıdır.
	eklenti string
	// baglama route'ları verilen router'a kaydeder.
	baglama func(r chi.Router)
}

// kuyrukIsi Start aşamasında uygulanacak bir kaydı temsil eder.
type kuyrukIsi struct {
	// aciklama hata mesajında hangi işin başarısız olduğunu söyler.
	aciklama string
	// uygula işi gerçekleştirir.
	uygula func(ctx context.Context, h *Host) error
}

// Host eklentinin çekirdeğe kayıt yaptığı yüzeydir.
//
// Eşzamanlı kullanıma güvenli DEĞİLDİR: eklentiler sırayla kurulur.
type Host struct {
	// c container'dır; eklentiler kendi servislerini buraya koyabilir.
	c *container.Container
	// modules modül kaydıdır; eklenti modülü buraya eklenir.
	modules *module.Registry
	// bus event otobüsüdür; nil olabilir.
	bus eventbus.EventBus
	// log eklentinin adıyla etiketlenmiş logger'dır.
	log *slog.Logger
	// settings eklenti yapılandırmasıdır (ortam değişkenlerinden gelir).
	settings map[string]string

	// aktif o an Setup'ı çalışan eklentinin adıdır; hata mesajları için.
	aktif string
	// routes eklentilerin bağlamak istediği route kayıtlarıdır.
	routes []routeKaydi
	// kuyruk Start'ta uygulanacak işlerdir.
	kuyruk []kuyrukIsi
}

// NewHost eklentilerin kullanacağı host'u kurar.
//
// settings nil olabilir; o durumda [Host.Setting] her anahtar için false döner.
func NewHost(
	c *container.Container,
	modules *module.Registry,
	bus eventbus.EventBus,
	log *slog.Logger,
	settings map[string]string,
) *Host {
	if log == nil {
		log = slog.Default()
	}

	return &Host{c: c, modules: modules, bus: bus, log: log, settings: settings}
}

// Container çekirdek container'ını döner.
//
// Setup sırasında buradan modül servisi ÇÖZMEYİN: modüller henüz kayıtlı
// değildir. Kendi servisinizi kaydetmek (Provide) güvenlidir.
func (h *Host) Container() *container.Container { return h.c }

// Logger eklentinin adıyla etiketlenmiş logger'ı döner.
func (h *Host) Logger() *slog.Logger { return h.log.With("plugin", h.aktif) }

// Setting eklenti yapılandırmasından bir değer okur.
//
// Anahtar yoksa ya da değer boşsa ikinci dönüş false olur. Boş dizeyi
// "verilmemiş" saymak bilinçlidir: ortam değişkeni tabanlı yapılandırmada
// tanımlı ama boş bir değişken neredeyse her zaman bir yapılandırma hatasıdır
// ve sessizce boş bir API anahtarıyla çalışmaya başlamaktan iyidir.
func (h *Host) Setting(key string) (string, bool) {
	v, ok := h.settings[key]
	if !ok {
		return "", false
	}

	v = strings.TrimSpace(v)

	return v, v != ""
}

// AddModule eklentinin getirdiği modülü kayda ekler.
//
// Modül, çekirdek modüllerle aynı yaşam döngüsünden geçer: Register,
// migration ve route bağlama.
func (h *Host) AddModule(m module.Module) {
	if m == nil || h.modules == nil {
		return
	}

	h.modules.Add(m)
}

// AddRoutes eklentinin route'larını bağlayacak işlevi kaydeder.
//
// İşlev, modüllerin route'ları bağlandıktan SONRA çalıştırılır.
//
// DİKKAT: İşlev İKİ KEZ çağrılır. [Registry.MountRoutes] önce onu boş bir
// sonda router'ında çalıştırıp hangi desenleri istediğini öğrenir, çakışma
// yoksa gerçek router'da yeniden çalıştırır. Bu yüzden işlev yalnızca route
// kaydı yapmalı, başka yan etkisi (sayaç artırma, bağlantı açma) olmamalıdır.
func (h *Host) AddRoutes(fn func(r chi.Router)) {
	if fn == nil {
		return
	}

	h.routes = append(h.routes, routeKaydi{eklenti: h.aktif, baglama: fn})
}

// RegisterPaymentProvider bir ödeme sağlayıcısını payment modülüne ekler.
//
// Kayıt hemen değil, [Registry.Start] sırasında yapılır: payment modülü
// Setup anında henüz ayağa kalkmamış olabilir.
//
// payment modülü hiç kayıtlı değilse Start bir HATA döner. Sessizce yok
// saymak, "stripe eklentisi kurulu" sanılan bir kurulumun aslında hiç ödeme
// alamaması demek olurdu.
func (h *Host) RegisterPaymentProvider(p coreprovider.PaymentProvider) {
	if p == nil {
		return
	}

	ad := h.aktif
	h.kuyruk = append(h.kuyruk, kuyrukIsi{
		aciklama: ad + " eklentisinin ödeme sağlayıcısı (" + p.ID() + ")",
		uygula: func(_ context.Context, host *Host) error {
			sink, err := cozSink[paymentSink](host, PaymentProvidersName, "payment")
			if err != nil {
				return err
			}

			return sink.Register(p)
		},
	})
}

// RegisterFulfillmentProvider bir kargo sağlayıcısını fulfillment modülüne ekler.
func (h *Host) RegisterFulfillmentProvider(p coreprovider.FulfillmentProvider) {
	if p == nil {
		return
	}

	ad := h.aktif
	h.kuyruk = append(h.kuyruk, kuyrukIsi{
		aciklama: ad + " eklentisinin kargo sağlayıcısı (" + p.ID() + ")",
		uygula: func(_ context.Context, host *Host) error {
			sink, err := cozSink[fulfillmentSink](host, FulfillmentProvidersName, "fulfillment")
			if err != nil {
				return err
			}

			return sink.Register(p)
		},
	})
}

// RegisterNotificationProvider bir bildirim sağlayıcısını notification
// modülüne ekler.
//
// Sıralama kuralı [Host.RegisterPaymentProvider] ile aynıdır ve burada daha da
// bağlayıcıdır: bildirim modülü, sağlayıcısını "order.placed" abonesinden
// çağırır ve o abonelik de aynı Start turunda kurulur. Kayıt Setup'ta
// denenseydi eklenti sağlayıcısı kaydın açılmasından önce gelir ve kurulum
// patlardı.
//
// notification modülü hiç kayıtlı değilse Start bir HATA döner; sessizce yok
// saymak, sipariş e-postası gönderdiğini sanan bir kurulumun hiçbir müşteriye
// ulaşmaması demek olurdu.
func (h *Host) RegisterNotificationProvider(p coreprovider.NotificationProvider) {
	if p == nil {
		return
	}

	ad := h.aktif
	h.kuyruk = append(h.kuyruk, kuyrukIsi{
		aciklama: ad + " eklentisinin bildirim sağlayıcısı (" + p.ID() + ")",
		uygula: func(_ context.Context, host *Host) error {
			sink, err := cozSink[notificationSink](host, NotificationProvidersName, "notification")
			if err != nil {
				return err
			}

			return sink.Register(p)
		},
	})
}

// Subscribe eklentiyi bir event'e abone eder.
//
// Abonelik [Registry.Start] sırasında kurulur.
func (h *Host) Subscribe(eventName string, fn eventbus.Handler) {
	if fn == nil {
		return
	}

	ad := h.aktif
	h.kuyruk = append(h.kuyruk, kuyrukIsi{
		aciklama: ad + " eklentisinin " + eventName + " aboneliği",
		uygula: func(_ context.Context, host *Host) error {
			if host.bus == nil {
				return coreerrors.Invalid(codeSubscribeFailed,
					"event otobüsü yok, %s aboneliği kurulamaz", eventName)
			}

			return host.bus.Subscribe(eventName, fn)
		},
	})
}

// cozSink container'dan sağlayıcı kaydını istenen dar yüzeyle çözer.
//
// Ayrı bir jenerik fonksiyondur çünkü metodlar tip parametresi alamaz.
func cozSink[T any](h *Host, ad, modulAdi string) (T, error) {
	var sifir T

	if h.c == nil || !h.c.Has(ad) {
		return sifir, coreerrors.Invalid(codeSinkMissing,
			"%s modülü kayıtlı değil; %q container'da bulunamadı", modulAdi, ad)
	}

	v, err := container.Resolve[T](h.c, ad)
	if err != nil {
		return sifir, coreerrors.Wrap(err, coreerrors.KindInternal, codeSinkUnusable,
			"%q sağlayıcı kaydı beklenen yüzeyi karşılamıyor", ad)
	}

	return v, nil
}

// Registry kurulu eklentileri tutar ve iki fazda çalıştırır.
type Registry struct {
	// log kayıt olaylarını yazar.
	log *slog.Logger
	// plugins kurulu eklentilerdir; sıra korunur.
	plugins []Plugin
}

// NewRegistry boş bir eklenti kaydı kurar.
func NewRegistry(log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}

	return &Registry{log: log}
}

// Add bir eklentiyi kayda ekler. Kurulum sırası ekleme sırasıdır.
func (r *Registry) Add(p Plugin) {
	if p == nil {
		return
	}

	r.plugins = append(r.plugins, p)
}

// Plugins kurulu eklentilerin adlarını döner.
func (r *Registry) Plugins() []string {
	adlar := make([]string, 0, len(r.plugins))
	for _, p := range r.plugins {
		adlar = append(adlar, p.Name())
	}

	return adlar
}

// Install her eklentinin Setup'ını çalıştırır.
//
// MODÜLLER AYAĞA KALKMADAN ÖNCE çağrılmalıdır: eklentinin eklediği modülün de
// Register/migration/route döngüsünden geçebilmesi buna bağlıdır.
func (r *Registry) Install(ctx context.Context, h *Host) error {
	if err := r.validateNames(); err != nil {
		return err
	}

	for _, p := range r.plugins {
		h.aktif = p.Name()
		if err := p.Setup(ctx, h); err != nil {
			h.aktif = ""

			return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
				"%s eklentisi kurulamadı", p.Name())
		}

		r.log.DebugContext(ctx, "eklenti kuruldu", "plugin", p.Name())
	}

	h.aktif = ""

	r.log.InfoContext(ctx, "eklentiler kuruldu", "sayi", len(r.plugins))

	return nil
}

// Start kuyruğa alınmış sağlayıcı ve subscriber kayıtlarını uygular.
//
// MODÜLLER AYAĞA KALKTIKTAN SONRA çağrılmalıdır.
func (r *Registry) Start(ctx context.Context, h *Host) error {
	for _, is := range h.kuyruk {
		if err := is.uygula(ctx, h); err != nil {
			return coreerrors.Wrap(err, coreerrors.KindOf(err), codeStartFailed,
				"%s kaydedilemedi", is.aciklama)
		}

		r.log.DebugContext(ctx, "eklenti kaydı uygulandı", "is", is.aciklama)
	}

	h.kuyruk = nil

	return nil
}

// MountRoutes eklentilerin route'larını router'a bağlar.
//
// Modül route'larından SONRA çağrılmalıdır: eklentinin bir modülün yolunu
// gölgelemesi ancak burada, mevcut ağaç okunabildiği için yakalanabilir.
//
// # Neden çakışma denetimi
//
// Yalnızca "sonra çağır" demek koruma DEĞİLDİR. chi'de aynı desen ikinci kez
// kaydedilirse handler SESSİZCE ezilir (mevcut bir yola Mount denenirse de
// panic edilir): eklenti "/store/v1/products"u kaydettiğinde mağaza ürün
// listesi eklentinin handler'ına düşer ve bu ancak müşteri boş liste
// gördüğünde fark edilir. Bu yüzden her eklenti route'u önce boş bir sonda
// router'ına kaydedilip istediği desenler [chi.Walk] ile okunur; desen zaten
// varsa GERÇEK router'a hiç dokunulmadan tipli bir çakışma hatası dönülür.
//
// İlk çakışmada durulur ve o eklentiden sonrakiler de bağlanmaz: kısmen
// bağlanmış bir yönetim yüzeyi, hiç açılmamış bir sunucudan daha zor teşhis
// edilir. Çağıranın hatayı yutması hâlinde bile modül route'u korunur, çünkü
// çakışan kayıt hiç uygulanmamıştır.
func (r *Registry) MountRoutes(router chi.Router, h *Host) error {
	if router == nil {
		return nil
	}

	mevcut, err := desenleriTopla(router)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeRouteInvalid,
			"mevcut route ağacı okunamadı")
	}

	for _, kayit := range h.routes {
		istenen, err := kayit.istenenDesenler()
		if err != nil {
			return err
		}

		for _, desen := range istenen {
			if _, cakisiyor := mevcut[desen]; !cakisiyor {
				continue
			}

			// Çağıran hatayı yutsa bile arıza görünür kalmalı: eklenti
			// route'ları bağlanmadan sunucu ayağa kalkarsa tek ipucu budur.
			r.log.Error("eklenti route çakışması", "plugin", kayit.eklenti, "route", desen)

			return coreerrors.Conflict(codeRouteConflict,
				"%s eklentisi zaten kayıtlı bir yolu bağlamaya çalıştı: %s",
				kayit.eklenti, desen)
		}

		if err := kayit.calistir(router); err != nil {
			return err
		}

		for _, desen := range istenen {
			mevcut[desen] = struct{}{}
		}
	}

	return nil
}

// istenenDesenler route işlevinin bağlamak istediği desenleri toplar.
//
// İşlev boş bir sonda router'ında çalıştırılır; gerçek router'a bu aşamada
// DOKUNULMAZ, çünkü amaç tam da kaydın yapılıp yapılmayacağına karar
// vermektir. Sonda router'ı chi'nin kendi ağacıdır: desenleri elle
// ayrıştırmak, Route/Mount/Group iç içe geçtiğinde chi'nin gerçekte ürettiği
// yollardan sapardı.
func (k routeKaydi) istenenDesenler() ([]string, error) {
	sonda := chi.NewRouter()
	if err := k.calistir(sonda); err != nil {
		return nil, err
	}

	kume, err := desenleriTopla(sonda)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeRouteInvalid,
			"%s eklentisinin route'ları okunamadı", k.eklenti)
	}

	desenler := make([]string, 0, len(kume))
	for desen := range kume {
		desenler = append(desenler, desen)
	}

	// Harita sırası rastgeledir; sıralamadan aynı çakışma her çalıştırmada
	// başka bir yolu suçlayabilirdi.
	sort.Strings(desenler)

	return desenler, nil
}

// calistir route işlevini verilen router üzerinde çalıştırır.
//
// chi geçersiz bir desende ("/" ile başlamayan yol), route'lardan sonra
// eklenen middleware'de ya da var olan bir yola Mount denemesinde PANİK eder.
// Panik olduğu gibi bırakılsaydı açılışta yalnızca chi'nin iç yığın izi
// görünür, hangi eklentinin suçlu olduğu yazmazdı; burada tipli bir hataya
// çevrilir.
func (k routeKaydi) calistir(r chi.Router) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = coreerrors.Invalid(codeRouteInvalid,
				"%s eklentisinin route kaydı chi tarafından reddedildi: %v", k.eklenti, p)
		}
	}()

	k.baglama(r)

	return nil
}

// desenleriTopla router ağacındaki "METOD desen" anahtarlarını toplar.
func desenleriTopla(r chi.Routes) (map[string]struct{}, error) {
	kume := map[string]struct{}{}

	err := chi.Walk(r, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		kume[strings.ToUpper(method)+" "+normalizeDesen(route)] = struct{}{}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return kume, nil
}

// normalizeDesen chi'nin döndürdüğü route dizesini karşılaştırılabilir hâle
// getirir.
//
// Mount edilmiş alt router'lar "/store/v1/products/*/" gibi kalıntılar bırakır
// ve chi mount edilen yolu hem "/x" hem "/x/" olarak servis eder. Kalıntılar
// temizlenmezse eklentinin "/store/v1/products" kaydı modülün aynı yoluyla
// EŞLEŞMEZ ve çakışma gözden kaçardı.
func normalizeDesen(route string) string {
	desen := strings.ReplaceAll(route, "/*/", "/")
	desen = strings.TrimSuffix(desen, "/*")

	if len(desen) > 1 {
		desen = strings.TrimSuffix(desen, "/")
	}

	if desen == "" {
		return "/"
	}

	return desen
}

// validateNames eklenti adlarının boş olmadığını ve tekrarlanmadığını doğrular.
//
// Tekrarlanan ad, hangi eklentinin hangi sağlayıcıyı kaydettiğini logdan
// izlenemez kılar; ayrıca aynı eklentinin iki kez kurulduğu bir yapılandırma
// hatasının en olası belirtisidir.
func (r *Registry) validateNames() error {
	gorulen := make(map[string]struct{}, len(r.plugins))

	for _, p := range r.plugins {
		ad := p.Name()
		if strings.TrimSpace(ad) == "" {
			return coreerrors.Invalid(codeNameEmpty, "eklenti adı boş olamaz")
		}

		if _, dup := gorulen[ad]; dup {
			return coreerrors.Conflict(codeNameDuplicate, "eklenti adı tekrarlandı: %s", ad)
		}

		gorulen[ad] = struct{}{}
	}

	return nil
}

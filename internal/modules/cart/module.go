// Package cart sepet modülüdür (plan Bölüm 6, Faz 5).
//
// Sorumluluğu tek cümleyle: bir sepetin NEYE sahip olduğunu bilmek — hangi
// bölgede, kimin adına, hangi satırlarla, hangi adresi ve kargo yöntemiyle.
// Modül Cart, LineItem, CartAddress ve ShippingMethod verisinin TEK yazma
// yetkilisidir (Prensip 2.3).
//
// # Neyi bilmez
//
// Sepetin NE KADAR TUTTUĞUNU hesaplamaz. Fiyat pricing'in, vergi region/tax'ın
// verisidir; ikisini bir araya getiren akış calculate_totals WORKFLOW'udur
// (plan Bölüm 2.5, ADR 0006). Bu modül hiçbir fiyat/vergi kaynağını çağırmaz;
// toplamları yalnızca [service.Service.SetTotals] ile alır, DOĞRULAR ve saklar.
//
// Modül başka HİÇBİR modülü import etmez (Prensip 2.1/2.4, ADR 0001; kural
// .golangci.yml içindeki depguard ve internal/arch testleriyle zorlanır).
// region_id, customer_id ve variant_id başka modüllerin kimlikleridir; serbest
// metin olarak saklanır ve foreign key verilmez (Prensip 2.2).
//
// # Dışarıya açtığı yüzeyler
//
//   - "cart.service" — modüller arası çağrılar ve workflow'lar için servis.
//   - "cart.interop" — akışların kullandığı İLKEL yüzey (ADR 0006).
//     complete_cart saga'sı sepeti buradan kapatır.
//   - "cart.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//   - /store/v1/carts … — müşteri API'si (sepeti kuran, değiştiren ve
//     SİPARİŞE ÇEVİREN yüzey).
//   - /admin/v1/carts — yönetim API'si (YALNIZCA okuma).
//
// # Kullandığı akışlar
//
// Vitrinin üç ucu — satır ekleme, satır adedi güncelleme ve sepeti tamamlama —
// modüller arası AKIŞLARA devredilmiştir; modül onları container'dan
// [LinePricingName] ve [CartCompletionName] adlarıyla, KENDİ paketinde
// tanımladığı dar arayüzlerle çözer (ADR 0001/0006). Gerekçe: satırın fiyatı
// pricing'in, başlığı kataloğun, sipariş ise order + payment + inventory'nin
// verisidir ve bu modül hiçbirini bilmez.
//
// Fiyat yolu KAPALI arızalanır: akış çözülemezse satır eklenmez
// (bkz. [linePricing]).
//
// # Kullandığı modül yüzeyi
//
// Bir tane vardır: BÖLGE ([RegionServiceName]). Sepetin para birimi bölgenin
// verisidir — region şemasında bölge başına tek bir sütundur — ve vitrin ucu
// onu istemciden almaz, bölgeden TÜRETİR. Çözüm yine adladır ve arayüz yine bu
// modülün kendi paketinde tanımlıdır (api.RegionCurrencyReader); modül region'ı
// import etmez.
//
// Bu yol da KAPALI arızalanır: bölge yüzeyi çözülemezse sepet açılmaz
// (bkz. [regionCurrency]). Para birimini bir varsayılana düşürmek, hangi fiyat
// listesinin uygulanacağını sunucunun bilmediği bir sepet üretirdi.
//
// # Bildirdiği linkler
//
// Yoktur. Sepetin bölgesi ve müşterisi KENDİ SÜTUNLARINDA durur ve her okuma o
// sütunlardan yapılır; aynı ilişkiyi bir de link tablosunda tutmak satır
// yazardı, bakım maliyeti doğururdu ve hiçbir okumaya hizmet etmezdi
// (bkz. CHANGELOG, "cart_customer/cart_region kaldırıldı").
package cart

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/repository"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// ModuleName modülün adıdır; container adlarının ve migration sürüm defterinin
// önekidir.
const ModuleName = "cart"

// ServiceName modül servisinin container'daki adıdır.
//
// Başka modüller ve workflow'lar (ADR 0001/0006 gereği bu paketi import
// ETMEDEN) sepet servisine bu adla ulaşır ve KENDİ paketlerinde tanımladıkları
// dar bir arayüzle kullanır.
const ServiceName = ModuleName + ".service"

// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
//
// Servisin kendisinden AYRI kaydedilir: servis cart'ın zengin tipleriyle
// konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. Sepet akışları onu
// kendi tanımladıkları dar arayüzle çözer.
const InteropName = ModuleName + ".interop"

// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// LinePricingName satır fiyatlandırma akışının container'daki adıdır
// (ADR 0001/0006).
//
// Ad internal/workflows/cart paketinindir ve burada DİZE olarak tekrarlanır;
// modüller workflow paketlerini import edemez (ADR 0006 her iki yönde de) ve
// tekrarın bedeli izolasyonun kabul edilen bedelidir. Aynı örüntü order
// modülünün SpendingPolicyName sabitinde de kullanılır.
//
// Yazım hatası SESSİZ KALMAZ ve b2b'dekinin tersine bir degradasyona da yol
// açmaz: ad çözülemezse satır ekleme ucu kapalı arızalanır
// (bkz. [linePricing]). Adın tek doğruluk kaynağı sepet akışlarının
// InteropName sabitidir.
const LinePricingName = "workflows.cart.interop"

// CartCompletionName sepet tamamlama akışının container'daki adıdır
// (ADR 0001/0006).
//
// Ad internal/workflows/checkout paketinindir ve aynı gerekçeyle burada
// tekrarlanır; adın tek doğruluk kaynağı o paketin InteropName sabitidir.
const CartCompletionName = "workflows.checkout.interop"

// RegionServiceName bölge modülünün container'daki adıdır (ADR 0001/0006).
//
// Ad region modülünündür ve burada DİZE olarak tekrarlanır: modüller birbirini
// import edemez ve tekrarın bedeli izolasyonun kabul edilen bedelidir. Aynı
// örüntü [LinePricingName] sabitinde de kullanılır; adın tek doğruluk kaynağı
// region modülünün ServiceName sabitidir.
//
// Yazım hatası SESSİZ KALMAZ: ad çözülemezse sepet açma ucu kapalı arızalanır
// (bkz. [regionCurrency]), çünkü para birimi türetilemeden sepet açmak, tam
// olarak kapatılan yetki kapısını geri açardı.
const RegionServiceName = "region.service"

// svcDB veritabanı havuzunun container'daki adıdır.
const svcDB = "core.db"

// codeSetupFailed modülün kablolanamadığını bildiren hata kodudur.
const codeSetupFailed = "cart_module_setup_failed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module cart modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
	svc     *service.Service
	handler *api.Handler
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca sepetin uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kaydedilmeye hazır bir cart modülü üretir.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
func New() *Module {
	return &Module{}
}

// Name modülün benzersiz adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi ve Query sağlayıcısını container'a kaydeder.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi). core.db modüller
// ayağa kalkmadan önce main.go'da hazır değer olarak kaydedildiği için burada
// çözülmesi güvenlidir ve eksikliği modülün hiç çalışamayacağı bir kurulum
// hatasıdır — sessizce ertelenmez.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, svcDB)
	}

	// Uygulama açılışta slog.SetDefault ile yapılandırılmış logger'ı kurar;
	// modül ayrı bir logger kaydı aramaz.
	log := slog.Default().With("modul", ModuleName)

	svc, err := service.New(service.Options{
		Repo:   repository.New(pool.Pool()),
		Logger: log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// Sağlayıcı adı "<entity>.query" biçimindedir; Query onu bu adla arar ve
	// Entity() ile adın örtüştüğünü doğrular (ADR 0004).
	// Modüller arası yüzey AYRI bir adla kaydedilir: servisin kendisi cart'ın
	// zengin tipleriyle konuşur, bu yüzey ise yalnızca ilkel tiplerle (ADR 0006).
	// Sepet akışları onu kendi dar arayüzleriyle çözer.
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	// Akışlar bu aşamada HENÜZ KAYITLI DEĞİLDİR ve olamazlar: akış, tüm
	// modüllerin servislerini container'dan çözerek kurulur, yani Register
	// döngüsünün TAMAMI bittikten sonra doğar. Handler ise akışa ihtiyaç duyar.
	// Bağımlılık dairesi, çözümü İSTEK ANINA erteleyerek kırılır
	// (bkz. [linePricing] ve [cartCompletion]); aynı kalıbı order modülü
	// harcama limiti kuralı için uygular.
	//
	// Bölge yüzeyi de aynı sarmalayıcıdan geçer ama sebebi daha basittir:
	// region modülü bu modülden SONRA Register olabilir ve kayıt sırasına
	// bağımlı bir modül, kompozisyon kökündeki bir satır yer değiştirdiğinde
	// sessizce bozulurdu.
	m.handler = api.New(svc, &regionCurrency{c: c, log: log}, api.Flows{
		Pricing:  &linePricing{c: c, log: log},
		Checkout: &cartCompletion{c: c, log: log},
	})
	slog.Default().DebugContext(ctx, "cart modülü kaydedildi",
		"servis", ServiceName, "saglayici", ProviderName)
	return nil
}

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("cart modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.handler.Routes(r)
}

// Describe modülün vitrin ve yönetim uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir: gövde şemaları o paketin dışa kapalı
// DTO'larından türetilir ve tipleri yalnızca belge uğruna dışa açmak modülün
// yüzeyini genişletirdi.
//
// [Module.Routes]'un tersine Register kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil. Kontrol koymak, kurulmamış bir modülün
// belgesini de sessizce boşaltırdı.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service modülün servisini döner; Register çağrılmadıysa nil'dir.
//
// Testler ve gömülü kullanım içindir; normal akışta servis container'dan
// [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }

// mustSub alt dizini açar; açılamazsa panikler.
//
// Panik burada güvenlidir: dizin adı derleme zamanında sabittir ve go:embed
// dosyaların varlığını zaten derleme zamanında doğrulamıştır. Yine de sessizce
// nil dönmek, modülün migration'sız (yani tablosuz) ayağa kalkması demek
// olurdu; kurulum hatası açıkça patlamalıdır.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("cart: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}

// linePricing satır fiyatlandırma akışını İLK KULLANIMDA çözen sarmalayıcıdır.
//
// # Neden tembel
//
// [module.Module] sözleşmesi Register sırasında başka modüllerin servislerinin
// henüz kayıtlı olmayabileceğini söyler. Buradaki bağımlılık daha da geç
// doğar: akış, TÜM modüllerin yüzeylerini container'dan çözerek kurulur, yani
// Register döngüsü bittikten sonra — oysa handler Register sırasında kurulur.
// Daire, çözümü ilk isteğe erteleyerek kırılır. Kayıt sırası böylece
// önemsizleşir ve bileşim kökü akışları modüllerden SONRA kaydedebilir.
//
// # Neden KAPALI arızalanıyor
//
// Bu, order modülünün harcama limiti sarmalayıcısının (spendingPolicy)
// TERSİDİR ve fark bilinçlidir. Orada ad kayıtlı değilse doğru cevap "limit
// yok"tur: b2b kurulu olmayan bir mağazada harcama limiti diye bir kavram
// yoktur. Burada ad kayıtlı değilse doğru cevap "fiyat yok" DEĞİLDİR —
// fiyatı olmayan bir satırı sepete yazmak (ne istemcinin gönderdiği tutarla,
// ne sıfırla) sessizce bedava mal satmak olurdu. Tek doğru sonuç, satırın HİÇ
// EKLENMEMESİDİR; bu yüzden çözülemeyen ad da, yüzeyi karşılamayan kayıtlı bir
// tip de HATA döndürür ve uç kapanır.
//
// Karar BİR KEZ verilir ve saklanır. Akışlar açılışta, ilk isteğten önce
// kaydedilir; ilk çözümde bulunmayan bir ad sonraki isteklerde de
// bulunmayacaktır ve her istekte yeniden denemek aynı hatayı sonsuza kadar
// tekrar üretmekten başka bir şey yapmazdı.
type linePricing struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.LinePricing
	err  error
}

// Sarmalayıcının handler'ın beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir.
var _ api.LinePricing = (*linePricing)(nil)

// AddPricedLineItem sepete satır ekler ve satırın kimliğini döner.
func (p *linePricing) AddPricedLineItem(
	ctx context.Context,
	cartID, variantID string,
	quantity int64,
	metadata json.RawMessage,
) (string, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return "", p.err
	}
	return p.svc.AddPricedLineItem(ctx, cartID, variantID, quantity, metadata)
}

// SetLineItemQuantity satırın adedini yazar ve satırın kaldırılıp
// kaldırılmadığını bildirir.
func (p *linePricing) SetLineItemQuantity(
	ctx context.Context,
	cartID, lineItemID string,
	quantity int64,
) (bool, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return false, p.err
	}
	return p.svc.SetLineItemQuantity(ctx, cartID, lineItemID, quantity)
}

// resolve akışı container'dan çözer; sonucu bir kez saklar.
//
// # Hata sınıfı container'dan DEVRALINMAZ
//
// Sarmalama sınıfı sabit [errors.KindInternal]'dır; container'ın kendi sınıfı
// (kayıtsız ad → KindNotFound, yanlış tipte kayıt → KindInvalid) olduğu gibi
// geçirilmez. Sebep, o sınıfların hatanın MUHATABINI yanlış göstermesidir:
// ikisi de istemcinin düzeltebileceği bir şey değil, SUNUCU YAPILANDIRMASI
// arızasıdır.
//
// Devralınsaydı satır ekleme ucu 404 dönerdi. Üç ayrı bedeli vardır: istemciye
// "böyle bir uç yok" der ve vitrini var olmayan bir yolu aramaya iter, 5xx
// üzerinden kurulmuş uyarı zinciri hiç çalmaz, ara katmanlar da 404'ü
// önbelleğe alıp arızayı kurulum düzeldikten SONRA da sürdürebilir. Yanlış
// tipte kayıt ise 422 ile "gövden geçersiz" derdi; gövde kusursuz olsa bile.
//
// Operatörün ihtiyacı olan metin (hangi ad çözülemedi, neyin imkânsız olduğu)
// hatada KORUNUR ve istemciye sızmaz: taşıma katmanı KindInternal gövdelerini
// maskeler, gerçek zinciri yalnızca loga yazar (bkz. corehttp.WriteError).
// İstemcinin gördüğü tek makine okunur alan [codeSetupFailed] kalır.
func (p *linePricing) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.LinePricing](p.c, LinePricingName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"%s modülü satır fiyatlandırma akışını çözemedi (%q); fiyatı sunucu "+
				"belirlemeden satır eklenemez", ModuleName, LinePricingName)
		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "satır fiyatlandırma akışı bağlandı", "akis", LinePricingName)
}

// cartCompletion sepet tamamlama akışını İLK KULLANIMDA çözen sarmalayıcıdır.
//
// Tembelliğin ve kapalı arızalanmanın gerekçesi [linePricing] ile aynıdır;
// burada daha da açıktır: akış yoksa sipariş, ödeme ve stok rezervasyonu da
// yoktur ve "sepeti tamamlandı say" diye bir kestirme yol olamaz.
type cartCompletion struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.CartCompletion
	err  error
}

// Sarmalayıcının handler'ın beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir.
var _ api.CartCompletion = (*cartCompletion)(nil)

// CompleteCartJSON sepeti siparişe çevirir.
func (p *cartCompletion) CompleteCartJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return nil, p.err
	}
	return p.svc.CompleteCartJSON(ctx, request)
}

// resolve akışı container'dan çözer; sonucu bir kez saklar.
//
// Hata sınıfının container'dan devralınmama gerekçesi [linePricing.resolve]
// godoc'undadır ve burada da aynen geçerlidir.
func (p *cartCompletion) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.CartCompletion](p.c, CartCompletionName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"%s modülü sepet tamamlama akışını çözemedi (%q)", ModuleName, CartCompletionName)
		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "sepet tamamlama akışı bağlandı", "akis", CartCompletionName)
}

// regionCurrency bölge yüzeyini İLK KULLANIMDA çözen sarmalayıcıdır.
//
// # Neden burada bir modül yüzeyi çözülüyor
//
// Sepetin para birimi bölgenin verisidir ve bölgeden TÜRETİLİR
// (bkz. api.RegionCurrencyReader). Türetmeyi yapabilmek için bölgeyi bilen
// tarafa sormak gerekir; bu modül region'ı import edemez (ADR 0001), o yüzden
// yüzey container'dan ADLA çözülür ve modül yalnızca kendi tanımladığı dar
// arayüzü tanır.
//
// # Neden tembel ve neden KAPALI arızalanıyor
//
// Tembellik kayıt sırası içindir: region bu modülden sonra Register olabilir.
// Kapalı arızalanma ise [linePricing] ile aynı gerekçededir — bölge yüzeyi
// yoksa doğru cevap "para birimi yok" ya da "istemcininkini kullan" DEĞİLDİR.
// Sepetin para birimi hangi fiyat listesinden fiyatlanacağını seçer; bir
// varsayılana düşmek, kapatılan yetki kapısını geri açardı. Sepet HİÇ AÇILMAZ.
type regionCurrency struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.RegionCurrencyReader
	err  error
}

// Sarmalayıcının handler'ın beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir.
var _ api.RegionCurrencyReader = (*regionCurrency)(nil)

// RegionCurrency bölgenin para birimini ve ondalık basamak sayısını döner.
func (p *regionCurrency) RegionCurrency(
	ctx context.Context,
	regionID string,
) (code string, decimalDigits int32, err error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return "", 0, p.err
	}
	return p.svc.RegionCurrency(ctx, regionID)
}

// resolve bölge yüzeyini container'dan çözer; sonucu bir kez saklar.
//
// Hata sınıfının container'dan devralınmama gerekçesi [linePricing.resolve]
// godoc'undadır ve burada da aynen geçerlidir: eksik ya da yanlış tipte bir
// kayıt istemcinin değil KURULUMUN sorunudur.
func (p *regionCurrency) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.RegionCurrencyReader](p.c, RegionServiceName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"%s modülü bölge yüzeyini çözemedi (%q); sepetin para birimi bölgeden "+
				"türetilemeden sepet açılamaz", ModuleName, RegionServiceName)
		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "bölge yüzeyi bağlandı", "yuzey", RegionServiceName)
}

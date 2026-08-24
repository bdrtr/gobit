// Package tax vergi modülüdür (plan Bölüm 6, Faz 7).
//
// Sorumluluğu tek cümleyle: bir satışın hangi coğrafyada, hangi oranla
// vergilendirileceğini bilmek ve bir kalem listesinin vergisini hesaplamak.
// Modül TaxRegion, TaxRate ve TaxRateRule verisinin TEK yazma yetkilisidir
// (Prensip 2.3).
//
// # region'dan devralınan iş
//
// Faz 5'te vergi GEÇİCİ olarak region modülünde duruyordu: region tablosunda
// tek bir tax_rate (baz puan) ve automatic_taxes bayrağı vardı ve sepet akışı
// onları okuyordu. region'ın godoc'u bunu açıkça "Faz 7'de tax modülü
// devralacak" diye işaretlemişti. Devralmayı bu modül sağlar.
//
// Devralma bu turda region'a DOKUNMADAN yapılır (ADR 0001: modüller birbirini
// import etmez ve birbirinin tablosunu görmez). tax kendi şemasını ve yüzeyini
// kurar; sepet akışının "region.service" yerine "tax.interop" çözmesi ayrı bir
// kablolama adımıdır. İki yüzeyin karşılığı birebirdir:
//
//	region: RegionTax(ctx, regionID)   -> (rateBps int32, automatic bool, err error)
//	tax:    RateForCountry(ctx, code)  -> (rateBps int32, found bool, err error)
//
// İkinci dönüş değerinin anlamı DEĞİŞMİŞTİR ve bu bilinçlidir: region'daki
// bayrak "vergiyi uygula/uygulama" tercihiydi, buradaki ise "yapılandırma var
// mı" bilgisidir. Tercih artık verinin kendisinde ifade edilir — vergi
// istenmiyorsa o ülke için bölge açılmaz ya da varsayılan oran sıfır yazılır.
//
// # Sağlayıcı soyutlaması ÇEKİRDEKTE DEĞİL
//
// Plan "TaxProvider" der, ama internal/core/provider yalnızca PaymentProvider
// ve FulfillmentProvider tanımlar ve bu modül çekirdeğe dokunamaz. Sözleşme bu
// yüzden modülün kendi paketindedir ([service.TaxProvider]) ve kutudan çıkan
// uygulama yerel hesaplamadır ([service.LocalProvider]). Karar AÇIKÇA
// geçicidir; taşıma koşulu ve yolu service paketinin godoc'unda yazılıdır.
//
// # Neyi bilmez
//
// tax hiçbir modülü import etmez. Ülke kodları region'ın verisidir ama bu
// modül onu okumaz: ISO 3166-1 kodunun BİÇİMİNİ doğrular, TANIMLI olup
// olmadığını sormaz. Kural kayıtlarındaki ürün/ürün tipi/kargo seçeneği
// kimlikleri de serbest metindir ve foreign key DEĞİLDİR (Prensip 2.2).
//
// Bu yüzden modül HİÇBİR link tanımı bildirmez: bağın sahibi vergi değil,
// vergiye ihtiyaç duyan taraftır.
//
// # Dışarıya açtığı yüzeyler
//
//   - "tax.service" — modül içi zengin yüzey (domain tipleriyle).
//   - "tax.interop" — modüller arası İLKEL yüzey (ADR 0001/0006); sepet akışı
//     vergiyi buradan hesaplatır.
//   - "tax.providers" — sağlayıcı kaydı; eklentiler buraya sağlayıcı ekler.
//   - "tax_region.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//   - /admin/v1/tax-regions, /admin/v1/tax-rates (+ kurallar) — yönetim API'si.
//
// Store API'si YOKTUR; gerekçesi internal/modules/tax/api paket yorumundadır.
package tax

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/tax/api"
	"github.com/bdrtr/gobit/internal/modules/tax/repository"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// Container'daki adlar.
const (
	// ModuleName modülün benzersiz adıdır; migration versiyon tablosunun
	// öneki de budur.
	ModuleName = "tax"
	// ServiceName servisin container'daki adıdır. Tüketici modüller onu bu adla
	// ve KENDİ tanımladıkları dar arayüzle çözer (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
	//
	// Servisin kendisinden AYRI kaydedilir: servis tax'ın zengin tipleriyle
	// konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. Sepet akışı onu
	// kendi dar arayüzüyle çözer.
	InteropName = ModuleName + ".interop"
	// ProvidersName sağlayıcı kaydının container'daki adıdır.
	//
	// Faz 9'daki plugin sistemi kendi vergi sağlayıcısını bu kaydı çözüp
	// ekler; modülün kodunu değiştirmesi gerekmez.
	ProvidersName = ModuleName + ".providers"
	// ProviderName query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

// Hata kodları.
const (
	codeSetupFailed      = "tax_module_setup_failed"
	codeProviderRegister = "tax_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot migration dosyalarının kök dizinidir.
//
// golang-migrate kaynağı köke bakar (iofs.New(src, ".")), embed.FS ise
// dosyaları "migrations/" altında tutar; alt ağaç bu yüzden bir kez burada
// açılır.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module tax modülünün [module.Module] uygulamasıdır.
type Module struct {
	svc *service.Service
	api *api.API
	log *slog.Logger
}

var _ module.Module = (*Module)(nil)

// New kurulmamış bir tax modülü üretir; servis [Module.Register] içinde
// kurulur. log nil ise loglar atılır.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name modülün adını döner.
func (m *Module) Name() string { return ModuleName }

// Register servisi, modüller arası yüzeyi, sağlayıcı kaydını ve query
// sağlayıcısını container'a kaydeder.
//
// tax hiçbir MODÜLÜN servisine ihtiyaç duymaz; yalnızca çekirdek havuzunu
// çözer. Havuz Bootstrap'tan ÖNCE kaydedildiği için burada doğrudan çözmek
// güvenlidir — modül sırasına bağımlılık yaratan tek şey başka bir MODÜLÜN
// servisini çözmek olurdu ve bu yapılmaz.
//
// Varsayılan sağlayıcı ([service.LocalProvider]) burada kaydedilir ve aynı
// depo örneğini oran kaynağı olarak kullanır. Kayıt AÇIKÇA yapılır (servisin
// örtük varsayılanına bırakılmaz) ki container'daki "tax.providers" değeri ile
// servisin kullandığı kayıt AYNI nesne olsun; iki ayrı kayıt olsaydı bir
// eklentinin eklediği sağlayıcı hesapta hiç görünmezdi.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindUnavailable, codeSetupFailed,
			"tax modülü %q servisini çözemedi", dbServiceName)
	}

	repo := repository.New(pool.Pool())

	providers := service.NewProviderRegistry()
	if err := providers.Register(service.NewLocalProvider(repo)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"tax modülü varsayılan sağlayıcıyı kaydedemedi")
	}

	m.svc = service.New(repo, service.Options{Logger: m.log, Providers: providers})
	m.api = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(m.svc)); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "tax modülü kaydedildi",
		slog.String("servis", ServiceName),
		slog.String("interop", InteropName),
		slog.Any("saglayicilar", providers.IDs()),
		slog.String("query", ProviderName),
	)
	return nil
}

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Routes modülün admin route'larını router'a bağlar.
//
// Register'dan SONRA çağrılır (bkz. module.Registry.Bootstrap); api bu yüzden
// kurulmuş olur. Yine de nil kontrolü vardır: Register hata verip Bootstrap
// yarıda kesilirse Routes hiç çağrılmaz, ama modül elle kullanılırsa panik
// yerine sessiz bir no-op daha güvenlidir.
func (m *Module) Routes(r chi.Router) {
	if m.api == nil {
		m.log.Warn("tax modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.api.Routes(r)
}

// Service kurulmuş servisi döner; Register çağrılmadıysa nil.
//
// Modülü doğrudan kullanan testler ve gömen uygulamalar içindir; normal akışta
// servis container'dan [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }

// mustSub alt dosya sistemini açar; açılamazsa panikler.
//
// //go:embed dizinin varlığını derleme zamanında garanti ettiği için hata yolu
// erişilemezdir. Yine de sessizce nil dönmek, modülün migration'sız (yani
// tablosuz) ayağa kalkması demek olurdu; kurulum hatası açıkça patlamalıdır.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("tax: migration dizini açılamadı: " + err.Error())
	}
	return sub
}

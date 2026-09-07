// Package inventory stok modülüdür: stok kalemleri, lokasyonlar, seviyeler ve
// rezervasyonlar (plan Bölüm 6, Faz 4).
//
// Modül kendi tablolarına sahiptir ve başka HİÇBİR modülü import etmez
// (Prensip 2.1/2.4, ADR 0001). Dışarıya üç yüzey açar:
//
//   - Servis: container'da [ServiceName] adıyla. Faz 6'daki complete_cart
//     saga'sı stok adımını ve telafisini buradan çağırır.
//   - Query sağlayıcısı: container'da "inventory_item.query" adıyla (ADR 0004).
//     Kayıtlar toplam satılabilir adetle birlikte döner; product'ın mağaza
//     listelemesi ürünü ve stoğunu tek çağrıda görür.
//   - Admin API: /admin/v1/stock-locations ve /admin/v1/inventory-items.
//
// Link tanımı BİLDİRMEZ: varyant ile stok kalemi arasındaki
// "product_variant_inventory" bağını, ilişkinin sahibi olan product modülü
// bildirir.
package inventory

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// Container adları.
const (
	// ModuleName modülün adıdır; migration versiyon tablosunun da önekidir.
	ModuleName = "inventory"
	// ServiceName modül servisinin container'daki adıdır. Başka modüller
	// servisi bu adla çözer ve KENDİ paketlerinde tanımladıkları dar bir
	// arayüzle kullanır (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
	//
	// Servisten AYRI kaydedilir: servis inventory'nin zengin tipleriyle konuşur,
	// bu yüzey yalnızca ilkel ve stdlib tipleriyle. Saga'lar onu kendi
	// tanımladıkları dar arayüzle çözer.
	InteropName = ModuleName + ".interop"
	// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.EntityName + query.ProviderSuffix
	// AdminName modülün YÖNETİM YAZMA yüzeyinin container'daki adıdır
	// (ADR 0013). Yazmanın yanında bir OKUMA da taşır: sorgu sağlayıcısı
	// lokasyon kırılımını sunmaz ve operatör toplamla stok düzenleyemez.
	AdminName = ModuleName + ".admin"
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

// Kişisel veri bildiriminde geçen tablo adları.
//
// Sabitler yazım hatasını tek yere indirir, doğruluğu kanıtlamaz: bildirimin
// gerçekten şemadaki sütunları adlandırdığını migration'ları okuyan denetim
// gösterir (erasure_test.go).
const (
	tableStockLocations = "stock_locations"
	tableInventoryItems = "inventory_items"
	tableReservations   = "inventory_reservations"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot migration dosyalarının kök dizinidir.
//
// golang-migrate kaynağı köke bakar (iofs.New(src, ".")), embed.FS ise dosyaları
// "migrations/" altında tutar; alt ağaç bu yüzden bir kez burada açılır.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module inventory modülünün çekirdek sözleşmesini uygular.
type Module struct {
	svc *service.Service
}

// Modülün çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır.
var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca stok uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// Kişisel veriyi bildirebildiği de aynı sebeple sabitlenir.
//
// [personaldata.Declarer] de opsiyoneldir ve süpürücü onu TİP İDDİASIYLA arar; pin
// bu yüzden [openapi.Describer]'ınkinin yanındadır. Modül [personaldata.Eraser]'ı
// UYGULAMAZ ve bu bir yarım iş değildir: silme öznesi müşteri kimliği ile
// e-posta taşır, bu modülün hiçbir sütunu ikisinden birini tutmaz, yani modül
// bir kişiyi bulamaz. Bildiren ama silemeyen bir tutucuyu koordinatör her
// raporda RETAINED olarak listeler — bildirim, modülün bir veri sahibine
// verilen cevapta görünür olmasının tek yoludur.
var _ personaldata.Declarer = (*Module)(nil)

// New kaydedilmeye hazır bir inventory modülü üretir.
func New() *Module {
	return &Module{}
}

// Name modülün adını döner.
func (m *Module) Name() string {
	return ModuleName
}

// Register servisi ve Query sağlayıcısını container'a kaydeder.
//
// Yalnızca ÇEKİRDEK servisi (core.db) çözülür; başka bir modülün servisi burada
// çözülmez, çünkü bu aşamada henüz kayıtlı olmayabilir (bkz. module.Module
// sözleşmesi). core.db, modüller ayağa kalkmadan önce main.go'da hazır değer
// olarak kaydedildiği için burada çözülmesi güvenlidir ve eksikliği modülün
// hiç çalışamayacağı bir kurulum hatasıdır — sessizce ertelenmez.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), "inventory_db_unavailable",
			"%s modülü %q servisini çözemedi", ModuleName, dbServiceName)
	}

	svc := service.New(repository.New(pool.Pool()), slog.Default())

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}
	// Yönetim yüzeyi AYRI bir adla kaydedilir; gerekçesi [AdminName]'de.
	if err := c.Provide(AdminName, service.NewAdminSurface(svc)); err != nil {
		return err
	}

	m.svc = svc
	slog.Default().DebugContext(ctx, "inventory modülü kaydedildi",
		"servis", ServiceName, "saglayici", ProviderName)
	return nil
}

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS {
	return migrationsRoot
}

// Routes modülün admin route'larını router'a bağlar.
//
// Register çağrılmadan route bağlanmaz: servissiz bir handler her isteği
// panikle karşılardı; sessiz kalmak (route yok -> 404) daha güvenlidir ve
// Bootstrap zaten Register'ı Routes'tan önce çalıştırır.
func (m *Module) Routes(r chi.Router) {
	if m.svc == nil {
		slog.Default().Warn("inventory modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	api.NewHandler(m.svc).Routes(r)
}

// Describe modülün yönetim uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir: gövde şemaları o paketin dışa kapalı
// tiplerinden türetilir ve tipleri yalnızca belge uğruna dışa açmak modülün
// yüzeyini genişletirdi.
//
// [Module.Routes]'un tersine servis kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil. Kontrol koymak, kurulmamış bir modülün
// belgesini de sessizce boşaltırdı.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// PersonalData modülün kişisel veriyi nerede tuttuğunu söyler (ADR 0029).
//
// Bağlam almaz ve veritabanına dokunmaz: bildirim KODUN özelliğidir, boş bir
// kurulumda da dolu bir kurulumda da aynı cümledir, ve bir denetim onu
// bağlantısız okur. Bu yüzden [Module.Routes]'un aksine servis kontrolü de
// yoktur; Register edilmemiş bir modül de ne tuttuğunu söyleyebilmelidir.
//
// Holder BOŞ bırakılır. Koordinatör onu kayıt defterindeki adla doldurur; adı
// bir de buraya yazmak, iki yerin sessizce ayrışabileceği bir kopya olurdu.
//
// # Bu adres kimin adresi
//
// Aşağıdaki bildirimin ağırlığı bir DEPO adresidir ve depo, alışveriş eden
// kişinin değil İŞLETMECİNİN kendi yeridir. Küçük bir kurulumda çoğu zaman
// işletmecinin EV adresidir: gerçek bir insana ait kişisel veri, ama o insan
// mağazadan alışveriş eden veri sahibi değil, tüccarın kendisi. Ayrım iki
// sonucu birden doğurur ve ikisi de bilerek verilmiştir.
//
// BİLDİRİLİR. Bildirim "bu kurulum kişisel veriyi nerede tutuyor" sorusunun
// cevabıdır, "şu kişinin verisi nerede" sorusunun değil. Bildirilmezse
// işletmecinin KENDİ erişim ya da silme talebi cevaplanamaz kalır, üstelik
// modül hiçbir raporda hiç görünmez. Fatura modülü seller_address için aynı
// sonuca vardı: şahıs işletmesinde satıcı bir gerçek kişidir.
//
// SİLİNMEZ. Bir müşterinin silme talebi mağazanın deposunu boşaltamaz.
// Modülün [personaldata.Eraser]'ı uygulamamasının ilk sebebi özneyi zaten
// çözemiyor olması; ikincisi budur — bir alışverişçinin süpürmesinde bu
// tabloda yapılacak iş YOKTUR.
//
// # Sözleşme bu ayrımı taşıyamıyor
//
// [personaldata.Holding] Table, Column, Kind ve Why taşır; VERİ SAHİBİNİN SINIFINI
// söyleyecek alanı yoktur. Ayrım bu yüzden her Why'ın ilk cümleciğine yazıldı
// ("the operator's own premises rather than a shopper's"). Bugün elde olan tek
// yer budur ve YETMEZ: süpürme raporu Why'ı taşımaz — bildirip silmeyen bir
// tutucuyu RETAINED'a çeviren yol yalnızca "tablo.sütun" listeler ve cümleyi
// kendisi yazar — yani ayrım yalnızca GET /admin/v1/erasure/personal-data
// belgesinde görünür. Bir alışverişçiye verilen raporda deponun yedi sütunu,
// kimin olduğunu söylemeden durur.
//
// # Named ile Open
//
// stock_locations'ın sütunlarını gobit TANIMLADI: address_1 bir posta adresi
// satırıdır ve çerçeve alanın NE OLDUĞUNU bilir. Değeri operatörün yazmış
// olması bunu değiştirmez; müşterinin adını da müşteri yazar. Serbest metin
// alanları (title, description) Open'dır: içine ne konduğuna yalnızca gömen
// uygulama karar verebilir ve gobit onları okumaz (ADR 0029).
//
// # Bildirilmeyenler
//
// inventory_levels HİÇ geçmez: iki kimlik ve iki sayıdan ibarettir, kişiye
// dair tek bir metin taşımaz. inventory_reservations.line_item_id de geçmez —
// sepet satırını gösteren çıplak bir yabancı kimliktir ve yanındaki sütunlar
// anonimleştikten sonra hiç kimseyi göstermez. sku bir MAL kodudur.
// Buna karşılık inventory_reservations.description modüldeki tek KİŞİ BAŞINA
// satırın serbest metnidir — rezervasyon bir alışverişçinin sepet satırından
// doğar — yani mağaza müşterisine ait kişisel verinin bu modülde durabileceği
// tek yerdir.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holdings: []personaldata.Holding{
			{
				Table: tableStockLocations, Column: "name", Kind: personaldata.Named,
				Why: "the operator's own premises rather than a shopper's — the warehouse or shop name, which in a one-person business is frequently the trader's own name",
			},
			{
				Table: tableStockLocations, Column: "address_1", Kind: personaldata.Named,
				Why: "the operator's own premises rather than a shopper's — the street line the goods sit at, which for a sole trader is frequently a home address",
			},
			{
				Table: tableStockLocations, Column: "address_2", Kind: personaldata.Named,
				Why: "the operator's own premises rather than a shopper's — flat, floor or door, which narrows the street line to a single household",
			},
			{
				Table: tableStockLocations, Column: "city", Kind: personaldata.Named,
				Why: "the operator's own premises rather than a shopper's — the town the warehouse is in",
			},
			{
				Table: tableStockLocations, Column: "province", Kind: personaldata.Named,
				Why: "the operator's own premises rather than a shopper's — the province or state of the warehouse address",
			},
			{
				Table: tableStockLocations, Column: "postal_code", Kind: personaldata.Named,
				Why: "the operator's own premises rather than a shopper's — the postal code of the warehouse, which in some countries reaches one building",
			},
			{
				Table: tableStockLocations, Column: "country_code", Kind: personaldata.Named,
				Why: "the operator's own premises rather than a shopper's — the country of the warehouse address, the least identifying part of it and declared because it is part of it",
			},
			{
				Table: tableInventoryItems, Column: "title", Kind: personaldata.Open,
				Why: "free text the shop types to name a stock item; it names goods rather than people, but a made-to-order item is routinely titled after the person it is being made for and gobit does not read it",
			},
			{
				Table: tableInventoryItems, Column: "description", Kind: personaldata.Open,
				Why: "free text the shop types about a stock item; gobit puts nothing in it and never reads it, so whether a person is described there is the controller's judgement",
			},
			{
				Table: tableReservations, Column: "description", Kind: personaldata.Open,
				Why: "free text on a reservation, and the only column in this module on a row born of one shopper's checkout, so a note naming that shopper lands here; gobit writes nothing into it",
			},
		},
	}
}

// mustSub alt dosya sistemini açar; açılamazsa panikler.
//
// //go:embed dizinin varlığını derleme zamanında garanti ettiği için hata yolu
// erişilemezdir. Yine de sessizce nil dönmek, modülün migration'sız (yani
// tablosuz) ayağa kalkması demek olurdu; kurulum hatası açıkça patlamalıdır.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("inventory: migration dizini açılamadı: " + err.Error())
	}
	return sub
}

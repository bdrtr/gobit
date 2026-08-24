package cart

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Container'daki servis adları (ADR 0006). Somut tipler bu adlarla çözülür;
// hiçbiri derleme zamanında tanınmaz.
const (
	// ServiceCart sepet modülünün servisidir.
	ServiceCart = "cart.interop"
	// ServicePricing fiyat modülünün servisidir.
	ServicePricing = "pricing.service"
	// ServiceRegion bölge modülünün servisidir.
	ServiceRegion = "region.service"
	// ServiceCustomer müşteri modülünün servisidir.
	ServiceCustomer = "customer.service"
	// ServiceLink çekirdeğin Module Links servisidir.
	ServiceLink = "core.link"
	// ServiceQuery çekirdeğin cross-module okuma katmanıdır.
	ServiceQuery = "core.query"
)

// Modüller arası SÖZLEŞME sabitleri.
//
// Değerler product modülünde de tanımlıdır ve burada TEKRARLANIR: bu paket o
// modülü import edemez (ADR 0006) ve tekrar, izolasyonun kabul edilen
// bedelidir (ADR 0001). Bir yazım hatası sessiz kalmaz — link adı yanlışsa
// core/link errors.NotFound, entity adı yanlışsa Query errors.NotFound döner.
const (
	// LinkVariantPriceSet varyantı pricing modülündeki fiyat kümesine bağlayan
	// linkin adıdır; tanımı product modülü bildirir.
	LinkVariantPriceSet = "product_variant_price_set"
	// EntityVariant varyantların Query katmanındaki entity adıdır.
	EntityVariant = "variant"
	// FieldTitle varyant kaydında başlığın bulunduğu alan adıdır.
	FieldTitle = "title"
)

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "cart_workflow_invalid_input"
	// CodeNotReady akışların eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "cart_workflow_not_ready"
	// CodeDependencyMissing container'da bir servisin çözülemediğini bildirir.
	CodeDependencyMissing = "cart_workflow_dependency_missing"
	// CodeVariantUnknown katalogda bulunmayan bir varyanta atıf yapıldığını
	// bildirir.
	CodeVariantUnknown = "cart_workflow_variant_unknown"
	// CodeVariantNotPriced varyantın hiçbir fiyat kümesine bağlı olmadığını
	// bildirir.
	CodeVariantNotPriced = "cart_workflow_variant_not_priced"
	// CodeLinkReadFailed bağ katmanının OKUNAMADIĞINI bildirir.
	//
	// İş kodlarından ayrıdır: "fiyatı yok" ile "fiyatı olup olmadığını
	// öğrenemedik" farklı durumlardır ve istemci ikisine farklı davranır
	// (biri kalıcı, diğeri yeniden denenebilir).
	CodeLinkReadFailed = "cart_workflow_link_read_failed"
	// CodeCatalogReadFailed katalog okumasının başarısız olduğunu bildirir;
	// varyantın var olmadığı anlamına GELMEZ.
	CodeCatalogReadFailed = "cart_workflow_catalog_read_failed"
	// CodeVariantPriceSetAmbiguous varyantın birden çok fiyat kümesine bağlı
	// göründüğünü bildirir.
	CodeVariantPriceSetAmbiguous = "cart_workflow_variant_price_set_ambiguous"
	// CodePriceUnavailable varyantın sepetin para biriminde fiyatı olmadığını
	// bildirir.
	CodePriceUnavailable = "cart_workflow_price_unavailable"
	// CodeCartCompleted tamamlanmış bir sepette hesap istendiğini bildirir.
	CodeCartCompleted = "cart_workflow_cart_completed"
	// CodeSnapshotInvalid sepet anlık görüntüsünün okunamadığını bildirir.
	CodeSnapshotInvalid = "cart_workflow_snapshot_invalid"
	// CodeTaxRateInvalid bölgenin sözleşme dışı bir vergi oranı bildirdiğini
	// bildirir.
	CodeTaxRateInvalid = "cart_workflow_tax_rate_invalid"
	// CodeAmountOverflow bir tutarın izin verilen aralığı aştığını bildirir.
	CodeAmountOverflow = "cart_workflow_amount_overflow"
	// CodeTotalsConflict sepetin, hesap yazılamayacak kadar sık değiştiğini
	// bildirir.
	CodeTotalsConflict = "cart_workflow_totals_conflict"
	// CodeTotalsAfterChange satırın YAZILDIĞINI ama toplamların
	// hesaplanamadığını bildirir.
	CodeTotalsAfterChange = "cart_workflow_totals_after_change_failed"
)

// Carts sepet modülünün ("cart.interop") bu paketçe kullanılan yüzeyidir.
//
// # Neden ilkel imzalar ve JSON
//
// Bu paket cart modülünü import EDEMEZ (ADR 0006), dolayısıyla imzalarında
// models.Cart gibi bir tipi ADLANDIRAMAZ; adlandırdığı an o, bu paketin kendi
// tipi olur ve somut servis arayüzü yapısal olarak karşılamaz. Bu yüzden
// imzalar yalnızca ilkel ve stdlib tipleri kullanır — region, pricing ve
// customer modüllerindeki interop yüzeyleriyle aynı örüntü.
//
// İki metot yapısal veri taşır (sepetin anlık şekli ve hesaplanan toplamlar) ve
// sınırı json.RawMessage olarak geçer. Alternatifler daha kötüydü: alan başına
// ayrı metot, sepeti tek bir tutarlı anda okumayı imkânsız kılar (satırlar bir
// çağrıda, revision başka bir çağrıda okunur ve arada sepet değişir); paralel çalışan
// dilimler (kimlikler, tutarlar, vergiler …) ise çağrı yerinde sessizce
// kayabilir. JSON, iki tarafın da adlandırabildiği TEK yapısal biçimdir ve
// modül ileride ayrı bir servise çıkarsa sözleşme aynen kalır. Şema
// [Snapshot] ve [Totals] tiplerinde, tek yerde tanımlıdır.
//
// # Ad çakışması uyarısı
//
// Metot adları cart servisindeki karşılıklarından bilinçli olarak FARKLIDIR
// (OpenCart, AddCartLineItem …): aynı adı taşısalardı tek bir tip iki farklı
// imzayı aynı anda taşıyamayacağı için modül bu yüzeyi hiç yayımlayamazdı.
// Tek istisna RemoveLineItem'dır; cart'ın var olan imzası zaten birebir uyar.
type Carts interface {
	// OpenCart yeni bir sepet açar ve KİMLİĞİNİ döner.
	//
	// customerID boş ise sepet MİSAFİRE aittir; email boş bırakılabilir.
	// Karşılığı cart servisindeki CreateCart'tır.
	OpenCart(ctx context.Context, regionID, currencyCode, customerID, email string) (cartID string, err error)

	// CartSnapshotJSON sepetin hesaba giren şeklini TEK okumada döner.
	//
	// Gövde [Snapshot] şemasındadır. Sepet yoksa errors.NotFound.
	// Karşılığı cart servisindeki GetCart'tır.
	CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error)

	// AddCartLineItem sepete satır ekler ve satırın KİMLİĞİNİ döner.
	//
	// Aynı varyant zaten sepetteyse yeni satır açılmaz, var olan satırın adedi
	// artar ve o satırın kimliği döner. Karşılığı cart servisindeki
	// AddLineItem'dır.
	AddCartLineItem(ctx context.Context, cartID, variantID, title string, quantity, unitPrice int64) (lineItemID string, err error)

	// SetCartLineItemQuantity satırın adedini MUTLAK değerle yazar; adet
	// pozitif olmalıdır. Karşılığı cart servisindeki
	// UpdateLineItemQuantity'dir.
	SetCartLineItemQuantity(ctx context.Context, cartID, lineItemID string, quantity int64) error

	// RemoveLineItem satırı sepetten kaldırır. Satır yoksa errors.NotFound.
	RemoveLineItem(ctx context.Context, cartID, lineItemID string) error

	// SetCartTotalsJSON hesaplanan toplamları sepete yazar.
	//
	// Gövde [Totals] şemasındadır ve sepetin TÜM satırlarını kapsamalıdır.
	// Beyan edilen revision sepetin güncel şekliyle uyuşmazsa errors.Conflict
	// döner. Karşılığı cart servisindeki SetTotals'tır.
	SetCartTotalsJSON(ctx context.Context, cartID string, totals json.RawMessage) error
}

// Prices fiyat modülünün ("pricing.service") bu paketçe kullanılan yüzeyidir.
type Prices interface {
	// CalculateAmount bir fiyat kümesinin verilen bağlamdaki BİRİM tutarını
	// minor unit olarak döner. Uygun fiyat yoksa errors.NotFound.
	CalculateAmount(
		ctx context.Context,
		priceSetID, currencyCode string,
		quantity int32,
		attributes map[string]string,
	) (int64, error)
}

// Regions bölge modülünün ("region.service") bu paketçe kullanılan yüzeyidir.
type Regions interface {
	// RegionIDForCountry ülke kodundan bölge kimliğini döner; bölge yoksa
	// errors.NotFound.
	RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
	// RegionCurrency bölgenin para birimi kodunu ve ondalık basamak sayısını
	// döner.
	RegionCurrency(ctx context.Context, regionID string) (code string, decimalDigits int32, err error)
	// RegionTax bölgenin baz puan vergi oranını ve verginin otomatik uygulanıp
	// uygulanmayacağını döner.
	RegionTax(ctx context.Context, regionID string) (rateBps int32, automatic bool, err error)
}

// Customers müşteri modülünün ("customer.service") bu paketçe kullanılan
// yüzeyidir.
type Customers interface {
	// CustomerEmail müşterinin e-posta adresini döner; müşteri yoksa
	// errors.NotFound.
	CustomerEmail(ctx context.Context, customerID string) (string, error)
}

// Links çekirdeğin Module Links servisinin ("core.link") bu paketçe kullanılan
// yüzeyidir.
//
// Yalnızca BATCH okuma vardır: tek satır için de aynı yol kullanılır ve
// böylece satır sayısı arttıkça sorgu sayısı değişmez (N+1 yoktur).
type Links interface {
	// ListMany verilen kaynak kimliklerin bağlarını TEK sorguda döner.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
}

// Catalog çekirdeğin Query katmanının ("core.query") bu paketçe kullanılan
// yüzeyidir (ADR 0004).
//
// Katalog verisi (varyantın başlığı) buradan okunur: product modülünün servisi
// zengin tiplerle konuştuğu için modüller arası çağrıya kapalıdır, Query ise
// tam bu boşluk için vardır.
type Catalog interface {
	// Graph spec'e göre kök kayıtları çeker ve genişletmeleri uygular.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Deps akışların bağımlılıklarıdır.
type Deps struct {
	// Carts sepet yüzeyidir; zorunludur.
	Carts Carts
	// Prices fiyat yüzeyidir; zorunludur.
	Prices Prices
	// Regions bölge yüzeyidir; zorunludur.
	Regions Regions
	// Customers müşteri yüzeyidir; zorunludur.
	//
	// Yalnızca KAYITLI müşteri sepetinde çağrılır; misafir akışı bu yüzeye hiç
	// dokunmaz. Yine de zorunludur: eksikliği, kayıtlı müşteri sepetinin ilk
	// isteğinde patlayan bir kurulum hatasıdır ve o hata açılışta görülmelidir.
	Customers Customers
	// Links Module Links yüzeyidir; zorunludur.
	Links Links
	// Catalog Query yüzeyidir; zorunludur.
	Catalog Catalog
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// Workflows sepet akışlarını yürüten tiptir. Eşzamanlı kullanıma güvenlidir.
type Workflows struct {
	carts     Carts
	prices    Prices
	regions   Regions
	customers Customers
	links     Links
	catalog   Catalog
	log       *slog.Logger
}

// New verilen bağımlılıklarla akışları kurar.
//
// Eksik bir bağımlılık KURULUM anında hata döner; çalışma zamanında nil
// kontrolü yapılmaz. Eksikliğin ilk çağrıya bırakılması, yanlış kablolanmış bir
// kurulumun ancak müşterinin sepetinde patlaması demek olurdu.
func New(deps Deps) (*Workflows, error) {
	for _, dep := range []struct {
		name    string
		missing bool
	}{
		{ServiceCart, deps.Carts == nil},
		{ServicePricing, deps.Prices == nil},
		{ServiceRegion, deps.Regions == nil},
		{ServiceCustomer, deps.Customers == nil},
		{ServiceLink, deps.Links == nil},
		{ServiceQuery, deps.Catalog == nil},
	} {
		if dep.missing {
			return nil, errors.Internal(CodeNotReady,
				"sepet akışları %q yüzeyi olmadan kurulamaz", dep.name)
		}
	}

	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Workflows{
		carts:     deps.Carts,
		prices:    deps.Prices,
		regions:   deps.Regions,
		customers: deps.Customers,
		links:     deps.Links,
		catalog:   deps.Catalog,
		log:       log,
	}, nil
}

// FromContainer bağımlılıkları container'dan ADLA çözer ve akışları kurar
// (ADR 0006).
//
// Çözüm sırası kayıt adına göre SABİTTİR: eksik ya da uyumsuz birden çok servis
// varsa her çalıştırmada aynı hata döner ve teşhis yeniden üretilebilir olur.
// Uyumsuzluk hatası hem kayıtlı somut tipi hem beklenen arayüzü, arayüzse
// eksik metotları yazar (bkz. container.Resolve).
func FromContainer(c *container.Container) (*Workflows, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady, "sepet akışları container olmadan kurulamaz")
	}

	carts, err := resolve[Carts](c, ServiceCart)
	if err != nil {
		return nil, err
	}
	prices, err := resolve[Prices](c, ServicePricing)
	if err != nil {
		return nil, err
	}
	regions, err := resolve[Regions](c, ServiceRegion)
	if err != nil {
		return nil, err
	}
	customers, err := resolve[Customers](c, ServiceCustomer)
	if err != nil {
		return nil, err
	}
	links, err := resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, err
	}
	catalog, err := resolve[Catalog](c, ServiceQuery)
	if err != nil {
		return nil, err
	}

	return New(Deps{
		Carts:     carts,
		Prices:    prices,
		Regions:   regions,
		Customers: customers,
		Links:     links,
		Catalog:   catalog,
		// Uygulama açılışta slog.SetDefault ile logger'ı kurar; akışlar ayrı
		// bir logger kaydı aramaz.
		Logger: slog.Default().With("workflow", "cart"),
	})
}

// resolve tek bir servisi çözer ve hatasını SINIFINI KORUYARAK sarar.
//
// Sınıfın korunması şart: kayıtsız ad NotFound (404), uyumsuz tip Invalid
// (422) olarak kalmalıdır. Hepsini Internal'a çevirmek, düzeltilebilir bir
// kablolama hatasını sunucu arızası gibi gösterirdi.
func resolve[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T
		return zero, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
			"sepet akışları %q servisini çözemedi", name)
	}
	return value, nil
}

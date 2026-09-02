package checkout

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Container'daki servis adları (ADR 0006). Somut tipler bu adlarla çözülür;
// hiçbiri derleme zamanında tanınmaz.
const (
	// ServiceCart sepet modülünün modüller arası ilkel yüzeyidir.
	ServiceCart = "cart.interop"
	// ServiceInventory stok modülünün modüller arası ilkel yüzeyidir.
	ServiceInventory = "inventory.interop"
	// ServiceOrder sipariş modülünün modüller arası ilkel yüzeyidir.
	ServiceOrder = "order.interop"
	// ServicePayment ödeme modülünün modüller arası ilkel yüzeyidir.
	ServicePayment = "payment.interop"
	// ServiceFulfillment kargo modülünün modüller arası ilkel yüzeyidir.
	//
	// Ad, fulfillment modülünün KENDİ kablolamasından okunmuştur (modülün
	// InteropName sabiti); tahmin edilmiş bir ad ancak çözüm anında, kurulum
	// hatası olarak görünürdü.
	ServiceFulfillment = "fulfillment.interop"
	// ServiceLink çekirdeğin Module Links servisidir.
	ServiceLink = "core.link"
	// ServiceQuery çekirdeğin cross-module okuma katmanıdır.
	ServiceQuery = "core.query"
	// ServiceWorkflow çekirdeğin saga motorudur; yürütme durumu pgstore'a yazılır.
	ServiceWorkflow = "core.workflow"
)

// Modüller arası SÖZLEŞME sabitleri.
//
// Değerler product modülünde de tanımlıdır ve burada TEKRARLANIR: bu paket o
// modülü import edemez (ADR 0006) ve tekrar, izolasyonun kabul edilen
// bedelidir (ADR 0001). Yazım hatası sessiz kalmaz — link adı yanlışsa
// core/link errors.NotFound, entity ya da alan adı yanlışsa Query
// errors.NotFound/errors.Invalid döner.
const (
	// LinkVariantInventory varyantı stok kalemine bağlayan linkin adıdır;
	// tanımı product modülü bildirir.
	LinkVariantInventory = "product_variant_inventory"
	// EntityVariant varyantların Query katmanındaki entity adıdır.
	EntityVariant = "variant"
	// FieldTitle varyant kaydında başlığın bulunduğu alan adıdır.
	FieldTitle = "title"
	// FilterIDs varyant sağlayıcısının TOPLU kimlik filtresidir; satır başına
	// ayrı sorgu (N+1) bu filtre sayesinde gerekmez.
	FilterIDs = "ids"
)

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "checkout_workflow_invalid_input"
	// CodeNotReady akışların eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "checkout_workflow_not_ready"
	// CodeDependencyMissing container'da bir servisin çözülemediğini bildirir.
	CodeDependencyMissing = "checkout_workflow_dependency_missing"
	// CodeCartCompleted tamamlanmış bir sepetin yeniden sipariş edilmek
	// istendiğini bildirir.
	CodeCartCompleted = "checkout_workflow_cart_completed"
	// CodeCartEmpty satırsız bir sepetin sipariş edilmek istendiğini bildirir.
	CodeCartEmpty = "checkout_workflow_cart_empty"
	// CodeCartChanged hesap ile anlık görüntü arasında sepetin değiştiğini
	// bildirir.
	CodeCartChanged = "checkout_workflow_cart_changed"
	// CodeTotalMismatch çağıranın onayladığı tutarın hesaplanan tutarla
	// uyuşmadığını bildirir.
	CodeTotalMismatch = "checkout_workflow_total_mismatch"
	// CodeSnapshotInvalid sepet anlık görüntüsünün okunamadığını bildirir.
	CodeSnapshotInvalid = "checkout_workflow_snapshot_invalid"
	// CodeTotalsInvalid hesabın sepetin satırlarını kapsamadığını bildirir.
	CodeTotalsInvalid = "checkout_workflow_totals_invalid"
	// CodeAmountInvalid tahsil edilecek tutarın geçersiz olduğunu bildirir.
	CodeAmountInvalid = "checkout_workflow_amount_invalid"
	// CodeLinkReadFailed bağ katmanının OKUNAMADIĞINI bildirir; "stok kalemi
	// yok" ile "olup olmadığını öğrenemedik" farklı durumlardır.
	CodeLinkReadFailed = "checkout_workflow_link_read_failed"
	// CodeCatalogReadFailed katalog okumasının başarısız olduğunu bildirir;
	// varyantın var olmadığı anlamına GELMEZ.
	CodeCatalogReadFailed = "checkout_workflow_catalog_read_failed"
	// CodeVariantUnknown katalogda bulunmayan bir varyanta atıf yapıldığını
	// bildirir.
	CodeVariantUnknown = "checkout_workflow_variant_unknown"
	// CodeVariantNotStocked varyantın hiçbir stok kalemine bağlı olmadığını
	// bildirir.
	CodeVariantNotStocked = "checkout_workflow_variant_not_stocked"
	// CodeVariantInventoryAmbiguous varyantın birden çok stok kalemine bağlı
	// göründüğünü bildirir.
	CodeVariantInventoryAmbiguous = "checkout_workflow_variant_inventory_ambiguous"
	// CodeReservationFailed stok ayırma adımının patladığını bildirir.
	//
	// YEDEK koddur: adımın patlamasına yol açan hata kendi kodunu taşıyorsa O
	// korunur ve bu kod görünmez (bkz. [reserveInventoryStep.unwind]). Geriye
	// yalnızca kodsuz bir hata için — ve bu paketin kendi ürettiği sözleşme
	// ihlali hataları için — kalır.
	CodeReservationFailed = "checkout_workflow_reservation_failed"
	// CodeReservationLeaked ayrılan stoğun geri BIRAKILAMADIĞINI bildirir;
	// elle müdahale gerekir.
	CodeReservationLeaked = "checkout_workflow_reservation_leaked"
	// CodePaymentUnderauthorized bloke edilen tutarın toplanması gereken tutarı
	// karşılamadığını bildirir (TAM ÖDEME KURALI).
	CodePaymentUnderauthorized = "checkout_workflow_payment_underauthorized"
	// CodePaymentUndercaptured tahsil edilen tutarın toplanması gereken tutarı
	// karşılamadığını bildirir.
	CodePaymentUndercaptured = "checkout_workflow_payment_undercaptured"
	// CodeCaptureIrreversible tahsil edilmiş bir tutarın bu akışta geri
	// alınamayacağını bildirir; iade AYRI bir akıştır.
	CodeCaptureIrreversible = "checkout_workflow_capture_irreversible"
	// CodeCaptureAmbiguous tahsilat çağrısının hata dönmesine RAĞMEN paranın
	// gitmiş olabileceğini bildirir: saga geri ALINMAMIŞTIR ve elle mutabakat
	// gerekir.
	CodeCaptureAmbiguous = "checkout_workflow_capture_ambiguous"
	// CodeEmptyIdentifier bir modülün hata dönmeden BOŞ kimlik döndürdüğünü
	// bildirir; yan etki dünyada, izi elimizde yoktur.
	CodeEmptyIdentifier = "checkout_workflow_empty_identifier"
	// CodeSharedStateInvalid adımlar arası taşınan verinin bozulduğunu bildirir.
	CodeSharedStateInvalid = "checkout_workflow_shared_state_invalid"
)

// Carts sepet modülünün ("cart.interop") bu paketçe kullanılan yüzeyidir.
//
// Yüzey iki metotla sınırlıdır: saga sepeti OKUR ve sonunda TAMAMLANMIŞ
// damgalar. Sepete satır ekleme/çıkarma bu akışın işi değildir ve o metotları
// buraya yazmak, checkout'a sepeti değiştirme yetkisi vermek olurdu.
//
// Sepetin hesabı bu yüzeyden okunmaz; hesabı üreten [CartTotals]'tır ve
// gerekçesi paket yorumundadır.
type Carts interface {
	// CartSnapshotJSON sepetin hesaba giren şeklini TEK okumada döner.
	//
	// Gövde [Snapshot] şemasındadır. Sepet yoksa errors.NotFound.
	CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error)

	// MarkCompleted sepeti tamamlanmış olarak damgalar.
	//
	// İDEMPOTENT DEĞİLDİR: tamamlanmış bir sepetin ikinci kez damgalanması
	// errors.Conflict döner ve bu bilinçlidir (bkz. cart modülünde
	// MarkCompleted). Tekrar koruması motorun idempotency anahtarındadır.
	// Sepet satırsızsa ya da toplamları BAYATSA çağrı errors.Conflict döner;
	// bu yüzden hesap saga'dan önce yenilenir.
	MarkCompleted(ctx context.Context, cartID string) error
}

// CartTotals sepet hesabının (internal/workflows/cart) bu paketçe kullanılan
// yüzeyidir.
//
// Container'dan ADLA çözülmez; [FromContainer] onu aynı container üzerinde
// kurar (bkz. cartwf.FromContainer). Arayüz olarak tanımlı olması testler
// içindir: gerçek hesap akışı pricing, region ve Query ister, oysa bu paketin
// birim testleri hesabın SONUCUNU verir ve hesabın kendisini yeniden sınamaz.
//
// İmzası bu paketin diğer yüzeylerinden farklı olarak ilkel DEĞİLDİR: dönüş
// tipi kardeş paketin tipidir ve o paket import EDİLEBİLİR (ADR 0006 yalnızca
// internal/modules'ü yasaklar). Aynı veriyi JSON'a çevirip yeniden çözmek,
// derleyicinin denetleyebildiği bir sınırı gereksiz yere gevşetirdi.
type CartTotals interface {
	// CalculateTotals sepetin toplamlarını YENİDEN hesaplar ve sepete yazar.
	//
	// Tamamlanmış sepette errors.Conflict döner. Sonuç satır başına birim
	// fiyat, ara toplam, indirim ve vergi taşır; siparişin satırları bundan
	// kurulur.
	CalculateTotals(ctx context.Context, cartID string) (cartwf.Totals, error)
}

// Inventory stok modülünün ("inventory.interop") bu paketçe kullanılan
// yüzeyidir.
//
// AvailableQuantity BİLİNÇLİ OLARAK yoktur: ayırmadan önce yapılan bir
// "yeterli mi" okuması, [Inventory.Reserve]'ün işlem içinde ve kilit altında
// yaptığı denetimin yarışa açık bir kopyasıdır. Okuma ile ayırma arasında
// başka bir sepet son adedi alabilir; o hâlde ön kontrol yalnızca hatanın
// yerini değiştirir, kendisini engellemez.
//
// [Inventory.LocationsWithStock] o yasağın istisnası DEĞİLDİR, çünkü başka bir
// soru sorar: "yeterli mi" sorusunun tek yetkilisi hâlâ Reserve'dür, listenin
// yanıtladığı soru "NEREDE" sorusudur. Cevabı olmadan bu akış tek bir depo adı
// bile üretemez ve lokasyonu çağıranın bildirmesi ZORUNLU kalırdı
// (bkz. [CompleteCartInput.LocationID]).
type Inventory interface {
	// LocationsWithStock kalemden en az quantity adet AYRILABİLEN lokasyonların
	// kimliklerini döner.
	//
	// Dönen şey bir ADAY listesidir, güvence değil: liste kilitsiz okunur ve
	// iki çağrı arasında son adedi başka bir sepet alabilir. O durumda hata
	// bugünkü yolla — Reserve'ün errors.Conflict'i ile — raporlanır, yani liste
	// hiçbir denetimi devralmaz.
	//
	// Hiçbir lokasyon yetmiyorsa boş dilim döner, HATA DEĞİL: "sipariş
	// verilemez" bir stok modülü kararı değildir ve sonucu bu paket çıkarır
	// (bkz. [reserveInventoryStep.locationFor]).
	//
	// Sıra deterministiktir ama TERCİH sırası değildir; adayları tercih
	// sırasına [Fulfillment.RankLocations] dizer. Ayrım bu yüzeyin varlık
	// sebebidir: sıraya politika saklamak, kararı hiç kimsenin bakmadığı bir
	// yerde — stok modülünün sıralamasında — verirdi.
	LocationsWithStock(ctx context.Context, inventoryItemID string, quantity int64) ([]string, error)

	// Reserve stoğu ayırır ve rezervasyon kimliğini döner.
	//
	// Yeterli stok yoksa errors.Conflict döner ve saga bunu "sipariş
	// verilemez" olarak yorumlar.
	Reserve(
		ctx context.Context,
		inventoryItemID, locationID string,
		quantity int64,
		lineItemID string,
	) (reservationID string, err error)

	// ReleaseReservation ayrılan stoğu geri bırakır; SAGA TELAFİSİDİR ve
	// İDEMPOTENTTİR. Zaten bırakılmış bir rezervasyon ikinci çağrıda hata
	// vermez; bilinmeyen bir kimlik errors.NotFound döner.
	ReleaseReservation(ctx context.Context, reservationID string) error

	// ConfirmReservation rezervasyonu düşülmüş stoğa çevirir. Bu noktadan
	// sonra stok geri bırakılamaz; iade ayrı bir akıştır.
	ConfirmReservation(ctx context.Context, reservationID string) error
}

// Fulfillment kargo modülünün ("fulfillment.interop") bu paketçe kullanılan
// yüzeyidir.
//
// Yüzey TEK metotludur ve bu bilinçlidir: bu akış gönderi AÇMAZ. Gönderi,
// ödemesi alınmış bir siparişin ardından başlayan ayrı bir iştir; onu saga'ya
// eklemek, telafisi kargo firmasına verilmiş bir çağrıyı pivot'un ötesine
// taşımak olurdu (bkz. paket yorumu, "Dönüşü olmayan nokta").
//
// Buraya sorulan tek soru şudur: satırın stoğu HANGİ depodan ayrılacak.
// Sorunun cevabı bir KARGO kararıdır — bugün deponun hedef bölgeye hizmet edip
// etmediğine ve işletmecinin tercih sırasına bakar — oysa "hangi depolarda
// yeterli stok var" bir STOK OLGUSUDUR ve [Inventory.LocationsWithStock]'tan
// gelir. İkisini tek yüzeyde toplamak, stok sorgusunu kargo politikasına ya da
// kargo politikasını stok şemasına bağımlı kılardı.
type Fulfillment interface {
	// RankLocations adayları TERCİH SIRASINA dizer: gönderi ilkinden çıkar.
	//
	// destinationRegionID gönderinin gideceği kargo bölgesidir ve ZORUNLUDUR;
	// bu paket onu plandan verir. Modül deponun o bölgeye hizmet edip
	// etmediğini kendi kaydından bilir, yani bu paket bir POLİTİKA taşımaz,
	// politikanın SORUSUNU taşır. Boş verilirse modül errors.Invalid döner.
	//
	// # Zorunlu değişmez: dönen dilim girdinin ALT KÜMESİDİR
	//
	// Elemanlar candidateLocationIDs'in elemanlarıyla BİREBİR aynı dizelerdir
	// ve hiçbiri iki kez geçmez. Normalleştirilmiş bir kopya ya da politika
	// tablosundan okunmuş bir eş DEĞİL: bu paket sonucu kendi aday defterinde
	// arar ve bulamazsa akışı errors.Internal ile düşürür
	// (bkz. [reserveInventoryStep.sirala]).
	//
	// # Sıra BİR KEZ sorulur
	//
	// Tükenen bir depodan sonra bu yüzey YENİDEN çağrılmaz; çağıran sıradaki
	// adayı kendi listesinden alır (bkz. [reserveInventoryStep.reserveLine]).
	// Tek lokasyon değil sıra dönmesinin sebebi budur: her tükenişte yeniden
	// sormak, aynı kayıtları aynı sıra için tekrar tekrar okumak olurdu — sıra
	// deterministik olduğu için o okumalar zaten aynı cevabı üretirdi — ve her
	// biri, adayların KİLİTSİZ okunmasıyla ayırmanın KİLİTLİ yapılması
	// arasındaki yarış penceresini uzatırdı.
	//
	// Sıra aynı adaylar VE aynı politika kayıtlarıyla DETERMİNİSTİKTİR ve
	// adayların geliş sırasından bağımsızdır. İşletmeci politikayı iki çağrı
	// arasında değiştirirse sıra da değişir; determinizm iddiası onu KAPSAMAZ
	// ve kapsaması gerekmez, çünkü bu bir ayarın beklenen sonucudur. Bir
	// yürütmenin İÇİNDE sıranın değişmesi de mümkün değildir: satır başına tek
	// çağrı yapılır.
	//
	// Aday listesi bu paket tarafından BOŞ verilmez
	// (bkz. [reserveInventoryStep.locationFor]); verilirse modül
	// errors.Conflict döner. Modülün adayları elemesi de mümkündür — bugün
	// hedef bölgeye hizmet etmeyen depoları eler — ve hiçbiri kalmazsa hata
	// yine errors.Conflict'tir: çağıran onu yetersiz stokla AYNI dalda
	// ("sipariş verilemez") karşılar. Sınıfın önemi bu paketin dallanmasında
	// DEĞİL, iki başka yerdedir: hatanın HTTP karşılığı sınıftan türer ve
	// motorun varsayılan yeniden deneme yüklemi KindConflict'i DENEMEZ. Elenmiş
	// bir aday kümesi tekrar denemekle değişmez.
	//
	// Modülün kendi kodu KORUNUR ve istemciye ulaşan kod odur: adım hatası
	// [reserveInventoryStep.unwind] tarafından sarılırken kod devralınır.
	// [CodeReservationFailed] yalnızca kodsuz bir hata için yedektir.
	RankLocations(
		ctx context.Context,
		destinationRegionID string,
		candidateLocationIDs []string,
	) (orderedLocationIDs []string, err error)
}

// Orders sipariş modülünün ("order.interop") bu paketçe kullanılan yüzeyidir.
//
// CompleteOrder BİLİNÇLİ OLARAK yoktur. İki sebebi vardır: (1) tamamlanmış bir
// sipariş İPTAL EDİLEMEZ, yani saga onu çağırdığı anda kendi telafisini
// imkânsız kılardı; (2) siparişin "tamamlandı" damgası ödemenin değil
// teslimatın sonucudur ve plan Faz 7'de fulfillment'a aittir. Sipariş bu
// akıştan "pending" olarak çıkar.
type Orders interface {
	// PlaceOrderJSON sepetin anlık görüntüsünden sipariş açar ve kimliğini
	// döner.
	//
	// Gövde [orderSnapshot] şemasındadır. Görüntüdeki "idempotency_key" doluysa
	// çağrı İDEMPOTENTTİR: aynı anahtarla ikinci çağrı yeni sipariş açmaz.
	PlaceOrderJSON(ctx context.Context, snapshot json.RawMessage) (orderID string, err error)

	// CancelOrder siparişi iptal eder; SAGA TELAFİSİDİR ve İDEMPOTENTTİR.
	// Tamamlanmış bir siparişin iptali errors.Conflict döner.
	CancelOrder(ctx context.Context, orderID, reason string) error
}

// Payments ödeme modülünün ("payment.interop") bu paketçe kullanılan yüzeyidir.
//
// Refund BİLİNÇLİ OLARAK yoktur: iade tahsilatın telafisi değil, ayrı bir
// akıştır (bkz. paket yorumu, "Dönüşü olmayan nokta"). SessionStatus da
// yoktur; saga oturumun durumunu SORMAZ, tutarlara bakar
// (bkz. [Payments.Collection]).
type Payments interface {
	// CreateCollection bir referans için ödeme koleksiyonu açar ve kimliğini
	// döner. Tutar POZİTİF olmalıdır.
	CreateCollection(ctx context.Context, reference, currencyCode string, amount int64) (collectionID string, err error)

	// OpenSessionWithData koleksiyon için bir sağlayıcıda ödeme oturumu açar
	// ve oturumun kimliğini döner.
	//
	// data sağlayıcıya olduğu gibi iletilen serbest JSON nesnesidir (kart
	// tokenı, dönüş adresi, test davranış anahtarları). Aynı idempotencyKey ile
	// ikinci çağrı YENİ oturum açmaz.
	OpenSessionWithData(
		ctx context.Context,
		collectionID, providerID, idempotencyKey string,
		data json.RawMessage,
	) (sessionID string, err error)

	// Authorize oturumu yetkilendirir; oturumun YENİ durumunu ve fiilen BLOKE
	// EDİLEN tutarı döner. Sağlayıcı reddederse hata döner.
	Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error)

	// Capture bloke edilmiş tutarı tahsil eder ve tahsilatın kimliğini döner.
	// amount sıfırsa bloke tutarın TAMAMI çekilir.
	Capture(ctx context.Context, sessionID string, amount int64) (paymentID string, err error)

	// Cancel oturumu iptal eder ve blokajı serbest bırakır; SAGA TELAFİSİDİR
	// ve İDEMPOTENTTİR. Tahsil edilmiş bir oturum iptal EDİLEMEZ
	// (errors.Conflict).
	Cancel(ctx context.Context, sessionID string) error

	// Collection koleksiyonun güncel durumunu ve TUTARLARINI döner.
	//
	// Dönen değerler sırasıyla durum, toplanması gereken tutar, bloke edilen,
	// tahsil edilen ve iade edilen tutardır (hepsi minor unit).
	//
	Collection(ctx context.Context, collectionID string) (
		status string,
		amount, authorized, captured, refunded int64,
		err error,
	)
}

// Links çekirdeğin Module Links servisinin ("core.link") bu paketçe kullanılan
// yüzeyidir.
//
// Yalnızca BATCH okuma vardır: tek satır için de aynı yol kullanılır ve böylece
// satır sayısı arttıkça sorgu sayısı değişmez (N+1 yoktur).
type Links interface {
	// ListMany verilen kaynak kimliklerin bağlarını TEK sorguda döner.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
}

// Catalog çekirdeğin Query katmanının ("core.query") bu paketçe kullanılan
// yüzeyidir (ADR 0004).
//
// Sipariş satırının BAŞLIĞI buradan okunur: product modülünün servisi zengin
// tiplerle konuştuğu için modüller arası çağrıya kapalıdır, Query ise tam bu
// boşluk için vardır.
type Catalog interface {
	// Graph spec'e göre kök kayıtları çeker ve genişletmeleri uygular.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Deps akışın bağımlılıklarıdır.
type Deps struct {
	// Carts sepet yüzeyidir; zorunludur.
	Carts Carts
	// Totals sepet hesabı yüzeyidir; zorunludur.
	Totals CartTotals
	// Inventory stok yüzeyidir; zorunludur.
	Inventory Inventory
	// Fulfillment kargo yüzeyidir; zorunludur.
	//
	// Lokasyonu çağıranın bildirdiği isteklerde hiç çağrılmaz ama yine de
	// ZORUNLUDUR: bağımlılık KURULUMDA denetlenmezse, yanlış kablolanmış bir
	// kurulum eksikliğini ancak lokasyonsuz ilk istekte — yani müşterinin ödeme
	// sayfasında — gösterirdi.
	Fulfillment Fulfillment
	// Orders sipariş yüzeyidir; zorunludur.
	Orders Orders
	// Payments ödeme yüzeyidir; zorunludur.
	Payments Payments
	// Links Module Links yüzeyidir; zorunludur.
	Links Links
	// Catalog Query yüzeyidir; zorunludur.
	Catalog Catalog
	// Executor saga motorudur; zorunludur.
	//
	// Bellek içi bir motor (workflow.NewInMemory) yalnızca test içindir:
	// idempotency koruması o hâlde süreç sınırında kalır ve iki replika aynı
	// sepeti aynı anda sipariş edebilir.
	Executor workflow.Executor
	// Logger nil verilirse loglar atılır.
	Logger *slog.Logger
}

// Workflows sipariş tamamlama akışını yürüten tiptir. Eşzamanlı kullanıma
// güvenlidir.
type Workflows struct {
	carts       Carts
	totals      CartTotals
	inventory   Inventory
	fulfillment Fulfillment
	orders      Orders
	payments    Payments
	links       Links
	catalog     Catalog
	executor    workflow.Executor
	log         *slog.Logger
}

// New verilen bağımlılıklarla akışı kurar.
//
// Eksik bir bağımlılık KURULUM anında hata döner; çalışma zamanında nil
// kontrolü yapılmaz. Eksikliğin ilk çağrıya bırakılması, yanlış kablolanmış bir
// kurulumun ancak müşterinin ödeme sayfasında patlaması demek olurdu.
func New(deps Deps) (*Workflows, error) {
	for _, dep := range []struct {
		name    string
		missing bool
	}{
		{ServiceCart, deps.Carts == nil},
		{serviceCartTotals, deps.Totals == nil},
		{ServiceInventory, deps.Inventory == nil},
		{ServiceFulfillment, deps.Fulfillment == nil},
		{ServiceOrder, deps.Orders == nil},
		{ServicePayment, deps.Payments == nil},
		{ServiceLink, deps.Links == nil},
		{ServiceQuery, deps.Catalog == nil},
		{ServiceWorkflow, deps.Executor == nil},
	} {
		if dep.missing {
			return nil, errors.Internal(CodeNotReady,
				"sipariş tamamlama akışı %q yüzeyi olmadan kurulamaz", dep.name)
		}
	}

	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Workflows{
		carts:       deps.Carts,
		totals:      deps.Totals,
		inventory:   deps.Inventory,
		fulfillment: deps.Fulfillment,
		orders:      deps.Orders,
		payments:    deps.Payments,
		links:       deps.Links,
		catalog:     deps.Catalog,
		executor:    deps.Executor,
		log:         log,
	}, nil
}

// serviceCartTotals hesap yüzeyinin hata mesajlarındaki adıdır.
//
// Container'da BÖYLE bir kayıt yoktur ve olmamalıdır: hesap akışı bir modül
// servisi değil, aynı container üzerine kurulan kardeş bir workflow paketidir
// (bkz. [CartTotals]). Ad yalnızca eksik bağımlılığı adlandırmak için vardır.
const serviceCartTotals = "workflows.cart"

// FromContainer bağımlılıkları container'dan ADLA çözer ve akışı kurar
// (ADR 0006).
//
// Çözüm sırası kayıt adına göre SABİTTİR: eksik ya da uyumsuz birden çok servis
// varsa her çalıştırmada aynı hata döner ve teşhis yeniden üretilebilir olur.
// Uyumsuzluk hatası hem kayıtlı somut tipi hem beklenen arayüzü yazar
// (bkz. container.Resolve).
//
// Sepet hesabı ADLA çözülmez, aynı container üzerinde KURULUR: hesap akışı
// container'a kaydedilmiş bir servis değil, kendi bağımlılıklarını yine
// adlarıyla çözen kardeş bir workflow paketidir.
func FromContainer(c *container.Container) (*Workflows, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady,
			"sipariş tamamlama akışı container olmadan kurulamaz")
	}

	carts, err := resolve[Carts](c, ServiceCart)
	if err != nil {
		return nil, err
	}
	inventory, err := resolve[Inventory](c, ServiceInventory)
	if err != nil {
		return nil, err
	}
	fulfillment, err := resolve[Fulfillment](c, ServiceFulfillment)
	if err != nil {
		return nil, err
	}
	orders, err := resolve[Orders](c, ServiceOrder)
	if err != nil {
		return nil, err
	}
	payments, err := resolve[Payments](c, ServicePayment)
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
	executor, err := resolve[workflow.Executor](c, ServiceWorkflow)
	if err != nil {
		return nil, err
	}

	totals, err := cartwf.FromContainer(c)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
			"sipariş tamamlama akışı sepet hesabını kuramadı")
	}

	return New(Deps{
		Carts:       carts,
		Totals:      totals,
		Inventory:   inventory,
		Fulfillment: fulfillment,
		Orders:      orders,
		Payments:    payments,
		Links:       links,
		Catalog:     catalog,
		Executor:    executor,
		// Uygulama açılışta slog.SetDefault ile logger'ı kurar; akış ayrı bir
		// logger kaydı aramaz.
		Logger: slog.Default().With("workflow", WorkflowName),
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
			"sipariş tamamlama akışı %q servisini çözemedi", name)
	}
	return value, nil
}

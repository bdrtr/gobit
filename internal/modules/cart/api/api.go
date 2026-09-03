// Package api cart modülünün HTTP yüzeyidir.
//
// İki yüzey vardır: müşteri tarafı (/store/v1/carts …) sepeti kurar ve
// değiştirir, yönetim tarafı (/admin/v1/carts) YALNIZCA OKUR. Sepet yönetim
// panelinden değiştirilmez; sepeti değiştiren tek taraf müşteridir ve sipariş
// düzeltmeleri Faz 6'daki order modülünün işidir.
//
// # HTTP'ye açılmayan yüzeyler
//
// [service.Service.SetTotals] ve [service.Service.MarkCompleted] BİLİNÇLİ
// OLARAK route almaz. İkisi de workflow yüzeyidir: toplamları hesaplayan
// calculate_totals ve sepeti kapatan complete_cart onları container'dan çözerek
// çağırır (ADR 0006). HTTP'ye açılsalardı bir istemci sepetin tutarını kendi
// yazabilir ya da ödeme yapmadan sepeti kapatabilirdi.
//
// # Akışa devredilen uçlar
//
// Vitrinin YAZAN uçlarının tamamı işini KENDİ SERVİSİYLE değil, container'dan
// adla çözülen bir AKIŞLA yapar: sepet açma ([CartOpening]), satır ekleme ve
// satır adedi güncelleme ([LinePricing]), sepeti tamamlama ([CartCompletion]).
// Sebep tek cümleyle şudur — bu uçların doğru çalışması cart modülünün
// BİLMEDİĞİ verilere (bölge, para birimi, katalog başlığı, fiyat, vergi, stok,
// ödeme) bağlıdır ve o veriler ancak modüller arası bir akışta bir araya gelir.
//
// Akışların HTTP sahibinin modül olması bilinçli bir karardır: URL'ler sepetin
// altında kalır, bileşim köküne handler kodu girmez ve modül somut akışı değil
// yalnızca kendi tanımladığı dar arayüzü tanır (ADR 0001'in kalıbı; aynısı
// order modülünün b2b harcama kuralında da kullanılır).
//
// # Bu paket artık hiçbir MODÜL yüzeyi çözmüyor
//
// Sepet açma ucu bir süre farklı çalıştı: bölgeyi gövdeden alıyor, para
// birimini de o bölgeden okumak için region modülünün yüzeyini adla çözüyordu.
// İki kusuru vardı. Birincisi, region_id müşterinin ifade ettiği şey DEĞİLDİR
// — müşteri bir ÜLKE seçer ve bölge onun sunucudaki karşılığıdır; ikincisi,
// türetmeyi zaten yapan bir akış (create_cart) varken uç onu ATLIYORDU. İkisi
// de akışa devredilerek kapandı ve bu paketin region'a olan tek bağı da
// böylece düştü: bölgeyi bilen taraf artık akıştır.
//
// # Yetki
//
// /admin/v1 altındaki uçlar kimlikten AYRI olarak yetki ister:
//
//   - [ScopeRead] ("cart:read") — GET uçlarını açar.
//   - [ScopeWrite] ("cart:write") — yazma uçlarını açardı; cart'ın yönetim
//     yüzeyi yalnızca okuma olduğu için bugün hiçbir route'a bağlı değildir.
//
// corehttp.ScopeAdmin ("admin") ÜST YETKİDİR ve ikisini de karşılar; tam
// yetkili bir kimliğe ayrıca verilmesi gerekmez.
//
// /store/v1 uçları yetki İSTEMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki taşımaz.
//
// # Vitrin sepetlerinde SAHİPLİK
//
// /store/v1/carts/{id} altındaki hiçbir uç, isteği yapanın o sepetin sahibi
// olduğunu doğrulamaz. Bu bir GÖZDEN KAÇMA DEĞİL, seçilmiş bir modeldir:
// sepetin kimliği YETENEĞİN kendisidir ("yetenek URL"). Kimlik 48 bit zaman
// damgası + 80 bit kriptografik rastgelelikten üretilir
// (bkz. models.NewCartID); tahmin edilemez, dolayısıyla onu BİLMEK sepete
// erişim hakkını taşımak demektir.
//
// Model başsız ticarette yaygındır ve burada zorunluluktan da doğar: mağaza
// yüzeyinin bugünkü tek kimliği publishable anahtardır ve o anahtar bir SIR
// DEĞİLDİR — tarayıcıda durur, tek işi isteği bir satış kanalına bağlamaktır
// (bkz. corehttp.RequireStore). Ortada müşteri OTURUMU yoktur, yani "bu sepet
// senin mi" sorusunu soracak bir özne de yoktur. Aynı beyan sipariş modülünde
// de yazılıdır (order/api storeGetOrder) ve gerçek yetkilendirme Faz 8'in
// işidir.
//
// Modelin bedava olmayan KURALLARI vardır ve bu paket onlara uyar:
//
//   - Vitrin tarafında LİSTE ucu YOKTUR. Bir liste ucu, tek bir kimliği
//     bilmeyi TÜM sepetleri okumaya çevirirdi; listeleme yalnızca /admin/v1
//     altındadır ve [ScopeRead] ister.
//   - Sepet kimliği bir SIR gibi taşınmalıdır. Log'a, Referer başlığına ya da
//     üçüncü taraf betiklerine sızması, erişimin kendisinin sızmasıdır.
//
// # Modelin KAPSAMADIĞI şey: customer_id
//
// Yetenek URL'i "elimdeki kimliğe erişebilirim" der; "ben şu müşteriyim"
// DEMEZ. Oysa POST /store/v1/carts ve POST /store/v1/carts/{id} gövdeleri
// customer_id alır ve hiçbir kanıt istemez. Servis yalnızca TEK bir sınırı
// korur: müşterisi olan bir sepet başka bir müşteriye devredilemez
// (service.CodeCustomerMismatch). Kalan iki kapı açıktır — çağıran açtığı yeni
// sepete başkasının müşteri kimliğini yazabilir, ve kimliğini bildiği bir
// MİSAFİR sepetini istediği müşteriye devredebilir.
//
// Sonucu kozmetik değildir: sepetin müşterisi siparişin sahibini belirler ve
// b2b harcama limiti O müşterinin şirket penceresinden düşülür (bkz. order
// modülünün harcama kuralı). Yani iddia, başkasının limit penceresini
// tüketebilir. Gerçek ikilide ölçüldü: yabancının tamamladığı alışveriş
// hedefin penceresinden düştü ve ARDINDAN o müşterinin kendi alışverişi 409
// aldı — yani iddia yalnızca kaçış değil, adı bilinen bir çalışanın harcama
// hakkını YAKMA yoludur.
//
// Üçüncü kapı ise iki kapının da altından geçer ve en ucuzudur: alanı HİÇ
// GÖNDERMEMEK. Müşterisiz sepet misafirindir, misafir siparişinde harcama
// kuralı SORULMAZ bile ve limit hiç uygulanmaz. Yani limitine dayanmış bir
// çalışanın yapması gereken tek şey, gövdeden bir alanı çıkarmaktır.
// "Kimliğini beyan et" demek de kapatmaz: POST /store/v1/customers publishable
// anahtarla hiçbir şirkete bağlı olmayan taze bir misafir kaydı açar.
//
// Tahmin edilemezlik bunu KAPATMAZ, çünkü korunan şey çağıranın elindeki bir
// yetenek değil, BAŞKASI hakkında yapılmış bir iddiadır. Tek doğru kapatma
// müşteri oturumudur (Faz 8): customer_id gövdeden alınmayı bırakır ve
// doğrulanmış kimlikten okunur. O mekanizma bugün YOKTUR ve bu paket onu
// uydurmaya çalışmaz; verilen karar, açığın YAZILI olmasıdır — yazılmamış bir
// güvenlik modeli, olmayan bir güvenlik modelidir.
//
// Sorumluluğun nerede durduğu ADR 0008'de karara bağlanmıştır: kimliği
// doğrulamak çerçevenin değil GÖMEN UYGULAMANIN işidir ve harcama limitinin
// hangi koşulda uygulandığı (yalnızca müşterisini BEYAN EDEN alışverişte)
// order modülünün spendingRuleFor godoc'unda ve README'nin B2B bölümünde
// yazılıdır.
//
// Handler'lar status kodu SEÇMEZ: servis core/errors tipli hatasını döner,
// corehttp.WriteError sınıfına uygun kodu yazar (plan Bölüm 8).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// maxBodyBytes istek gövdesi için üst sınırdır. Sınır olmadan tek bir istek
// sunucunun belleğini tüketebilirdi.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest gövde/parametre çözümlenemediğinde dönen hata kodudur.
const codeInvalidRequest = "cart_invalid_request"

// codeFlowUnavailable bir akışın handler'a bağlanmadığını bildirir.
//
// İstemcinin düzeltebileceği bir şey yoktur; kod bir KURULUM arızasını
// adlandırır ve bu yüzden Internal sınıfındadır.
const codeFlowUnavailable = "cart_workflow_unavailable"

// codeLineItemMissing yazılan satırın hemen ardından okunamadığını bildirir.
const codeLineItemMissing = "cart_line_item_missing"

// codeCartMissing açılan sepetin hemen ardından okunamadığını bildirir.
//
// [codeLineItemMissing]'den AYRI bir koddur çünkü kaybolan kayıt farklıdır ve
// operatörün bakacağı yer de öyle: biri sepeti, diğeri satırı arar. İkisi tek
// koda indirgenseydi teşhis logu okumadan yapılamazdı.
const codeCartMissing = "cart_missing_after_create"

// codeFlowResultInvalid akıştan dönen gövdenin çözülemediğini bildirir.
//
// Sözleşmenin iki ucu birbirini import edemez (ADR 0006), yani bir alan adı
// ayrıştığında derleyici sessiz kalır; bu kod o sessizliği çalışma zamanında
// bozar.
const codeFlowResultInvalid = "cart_workflow_result_invalid"

// URL parametre adları.
const (
	paramCartID     = "id"
	paramLineItemID = "line_item_id"
	paramMethodID   = "shipping_method_id"
)

// Carts handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç yüz satırlık bir sahte ile doğrulanabilir. Yüzeyde SetTotals
// ve MarkCompleted YOKTUR; ikisi de HTTP'ye açılmayan workflow yüzeyidir.
//
// CreateCart da YOKTUR ve sebebi farklıdır: sepet açma ucu servisi değil
// [CartOpening] akışını çağırır (bölge ülkeden orada türetilir) ve yazılan
// sepeti yanıta çevirmek için [Carts.GetCart] zaten yeter. Metodu yüzeyde
// tutmak, handler'ın akışı ATLAYIP sepeti doğrudan yazabileceği bir kapıyı
// açık bırakırdı.
//
// AddLineItem AYNI SEBEPLE yoktur ve bu bir eksiklik değildir: satır ekleyen uç
// [LinePricing] akışını çağırır, çünkü fiyatı SUNUCU belirler ve akış sepetin
// satır sayısı TAVANINI (workflows/cart içindeki MaxLineItems) uygular. Metot
// yüzeyde dursaydı, ona bağlanacak bir handler hem fiyatlandırmayı hem tavanı
// SESSİZCE atlardı; servis metodunun kendisi duruyor, akış onu çağırıyor.
type Carts interface {
	// GetCart sepeti çocuklarıyla döner.
	GetCart(ctx context.Context, cartID string) (models.CartDetail, error)
	// UpdateCart sepetin e-posta ve müşteri alanlarını günceller.
	UpdateCart(ctx context.Context, cartID string, in service.UpdateCartInput) (models.Cart, error)
	// ListCarts sepetleri sayfalar.
	ListCarts(ctx context.Context, in service.ListCartsInput) ([]models.Cart, int64, error)
	// DeleteCart sepeti yumuşak siler.
	DeleteCart(ctx context.Context, cartID string) error

	// UpdateLineItemQuantity satırın adedini yazar.
	UpdateLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error)
	// RemoveLineItem satırı kaldırır.
	RemoveLineItem(ctx context.Context, cartID, lineID string) error

	// SetShippingAddress sepetin kargo adresini yazar.
	SetShippingAddress(ctx context.Context, cartID string, in service.AddressInput) (models.CartAddress, error)
	// SetBillingAddress sepetin fatura adresini yazar.
	SetBillingAddress(ctx context.Context, cartID string, in service.AddressInput) (models.CartAddress, error)

	// AddShippingMethod sepete kargo yöntemi ekler.
	AddShippingMethod(ctx context.Context, cartID string, in service.AddShippingMethodInput) (models.ShippingMethod, error)
	// RemoveShippingMethod kargo yöntemini kaldırır.
	RemoveShippingMethod(ctx context.Context, cartID, methodID string) error
}

// CartOpening sepeti AÇAN akışın bu paketçe kullanılan yüzeyidir
// (ADR 0001/0006).
//
// # Neden burada tanımlı
//
// Somut akış internal/workflows/cart'tadır ve bu modül onu import EDEMEZ
// (ADR 0006 her iki yönde de geçerlidir). Kalıp [LinePricing] ile aynıdır:
// arayüz TÜKETİCİ tarafında ve yalnızca ilkel tiplerle tanımlanır, somut tip
// onu YAPISAL olarak karşılar ve container'dan ADLA çözülür.
//
// # Neden BÖLGE parametresi YOK
//
// Yüzeyin tamamı bu boşluk içindir ve boşluk [LinePricing]'deki fiyat
// boşluğunun aynısıdır. Sepetin bölgesi müşterinin ÜLKESİNDEN türetilir:
// müşteri bir ülke seçer (ya da tarayıcısı söyler), bölge onun sunucudaki
// karşılığıdır. Buraya bir "regionID" parametresi koymak, istemciye bir İÇ
// VARLIK KİMLİĞİ yazdırmak ve sepetin vergi oranını onun seçimine bırakmak
// olurdu — para birimi de aynı bölgeden okunduğu için bir zamanlar tam olarak
// bu oluyordu.
type CartOpening interface {
	// OpenCartForCountry sepeti açar ve sepetin KİMLİĞİNİ döner.
	//
	// Bölge ve para birimi SUNUCUDA belirlenir: ikisi de countryCode'dan
	// akış tarafından çözülür. customerID boş bırakılırsa sepet misafirindir;
	// metadata çağıranın sepete iliştirdiği serbest JSON nesnesidir ve boş
	// bırakılabilir.
	//
	// Ülkenin bölgesi yoksa errors.NotFound, kodun biçimi bozuksa
	// errors.Invalid döner; ikisi de region modülünün hatasıdır ve olduğu gibi
	// geçer.
	OpenCartForCountry(
		ctx context.Context,
		countryCode, customerID, email string,
		metadata json.RawMessage,
	) (cartID string, err error)
}

// LinePricing satır fiyatını SUNUCUDA belirleyen akışın bu paketçe kullanılan
// yüzeyidir (ADR 0001/0006).
//
// # Neden burada tanımlı
//
// Somut akış internal/workflows/cart'tadır ve bu modül onu import EDEMEZ
// (ADR 0006 her iki yönde de geçerlidir). Tüketici tarafında tanımlanan bu
// arayüz, container'dan ADLA çözülen somut tip tarafından YAPISAL olarak
// karşılanır; uyumu derleyici değil, ilk çözüm denemesi denetler.
//
// # Neden fiyat parametresi YOK
//
// Yüzeyin tamamı bu boşluk içindir. Fiyat, varyantın fiyat kümesinden ve
// sepetin para biriminden akış tarafından belirlenir; buraya bir "unitPrice"
// parametresi koymak, kaldırılan arızayı bir katman aşağıda yeniden kurmak
// olurdu.
type LinePricing interface {
	// AddPricedLineItem sepete satır ekler ve satırın kimliğini döner.
	//
	// Fiyat ve başlık SUNUCUDA belirlenir: fiyat pricing'den, başlık
	// katalogdan gelir. metadata çağıranın satıra iliştirdiği serbest JSON
	// nesnesidir ve boş bırakılabilir.
	AddPricedLineItem(
		ctx context.Context,
		cartID, variantID string,
		quantity int64,
		metadata json.RawMessage,
	) (lineItemID string, err error)

	// SetLineItemQuantity satırın adedini MUTLAK değerle yazar, toplamları
	// yeniden hesaplar ve satırın KALDIRILIP kaldırılmadığını bildirir.
	//
	// Sıfır adet satırı kaldırır; negatif adet reddedilir.
	SetLineItemQuantity(
		ctx context.Context,
		cartID, lineItemID string,
		quantity int64,
	) (removed bool, err error)
}

// CartCompletion sepeti siparişe çeviren akışın bu paketçe kullanılan
// yüzeyidir (ADR 0001/0006).
//
// İmza JSON'dur çünkü akışın girdisi de çıktısı da BİLEŞİKTİR ve iki taraf
// birbirinin tiplerini adlandıramaz; şema [completeCartFlowRequest] ve
// [completeCartFlowResult] tiplerinde, tek yerde yazılıdır.
type CartCompletion interface {
	// CompleteCartJSON sepeti siparişe çevirir: stok ayrılır, sipariş açılır,
	// ödeme yetkilendirilip tahsil edilir ve sepet kapatılır.
	CompleteCartJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// Flows handler'ın akışlardan ihtiyaç duyduğu yüzeylerin kümesidir.
//
// Üçü de ZORUNLUDUR ve eksikliği çalışma zamanında hata üretir
// (bkz. [Handler.opening], [Handler.pricing] ve [Handler.checkout]);
// route'ları hiç bağlamamak bir seçenek değildi, çünkü akışlar modüllerden
// SONRA kurulur ve Routes çağrıldığında henüz kayıtlı olmayabilirler.
type Flows struct {
	// Opening sepet açma akışıdır.
	Opening CartOpening
	// Pricing satır fiyatlandırma akışıdır.
	Pricing LinePricing
	// Checkout sepet tamamlama akışıdır.
	Checkout CartCompletion
}

// Handler cart modülünün HTTP handler kümesidir.
type Handler struct {
	svc   Carts
	flows Flows
}

// New verilen servis ve akışlar üzerinde çalışan handler kümesini üretir.
//
// Bir zamanlar üçüncü bir parametre daha vardı (bölge yüzeyi) ve sepetin para
// birimi ondan okunurdu; bugün o türetmeyi sepet açma AKIŞI yapıyor, yani
// handler'ın tanıdığı tek dış taraf [Flows]'tur.
func New(svc Carts, flows Flows) *Handler {
	return &Handler{svc: svc, flows: flows}
}

// opening sepet açma akışını döner; bağlı değilse HATA döner.
//
// # Neden KAPALI arızalanıyor
//
// Gerekçe [Handler.pricing] ile aynı yöndedir: akış yoksa doğru cevap
// "bölgesiz sepet" ya da "istemcinin dediği bölge" DEĞİLDİR. Sepetin bölgesi
// vergi oranını, ondan türetilen para birimi de hangi fiyat listesinin
// uygulanacağını seçer; ikisini de bir varsayılana düşürmek, tam olarak
// kapatılan yetki kapısını geri açardı. Akış çözülemiyorsa sepet HİÇ AÇILMAZ.
func (h *Handler) opening() (CartOpening, error) {
	if h.flows.Opening == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"sepet açma akışı bağlı değil; bölgeyi sunucu türetmeden sepet açılamaz")
	}
	return h.flows.Opening, nil
}

// pricing satır fiyatlandırma akışını döner; bağlı değilse HATA döner.
//
// # Neden KAPALI arızalanıyor
//
// Bu, order modülünün harcama limiti kuralının TERSİDİR ve fark bilinçlidir.
// Orada sağlayıcı yoksa doğru cevap "limit yok"tur: b2b kurulmamış bir
// mağazada harcama limiti diye bir kavram yoktur ve kuralsız devam etmek
// kurulumun kendi kararıdır. Burada ise sağlayıcı yoksa doğru cevap "fiyat
// yok" DEĞİLDİR — fiyatı olmayan bir satır yazmak (ne istemcinin verdiği
// tutarla, ne sıfırla) sessizce bedava mal satmak olurdu. Eksik bir
// fiyatlandırıcının tek doğru sonucu, satırın HİÇ EKLENMEMESİDİR.
func (h *Handler) pricing() (LinePricing, error) {
	if h.flows.Pricing == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"satır fiyatlandırma akışı bağlı değil; fiyatı sunucu belirlemeden satır eklenemez")
	}
	return h.flows.Pricing, nil
}

// checkout sepet tamamlama akışını döner; bağlı değilse HATA döner.
//
// Gerekçe [Handler.pricing] ile aynı yönde ama daha da açıktır: akış yoksa
// sipariş, ödeme ve stok rezervasyonu da yoktur ve "sepeti tamamlandı say"
// diye bir kestirme yol olamaz.
func (h *Handler) checkout() (CartCompletion, error) {
	if h.flows.Checkout == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"sepet tamamlama akışı bağlı değil; sepet siparişe çevrilemez")
	}
	return h.flows.Checkout, nil
}

// --- zarflar ve DTO'lar ------------------------------------------------------

// singleEnvelope tekil yanıtların zarfıdır (plan Bölüm 8).
type singleEnvelope struct {
	// Data yanıtın gövdesidir.
	Data any `json:"data"`
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count filtreye uyan TÜM kayıtların sayısıdır; sayfadaki satır sayısı değil.
	Count int64 `json:"count"`
	// Offset atlanan kayıt sayısıdır.
	Offset int64 `json:"offset"`
	// Limit istenen sayfa boyutudur.
	Limit int64 `json:"limit"`
}

// cartDTO sepetin dış gösterimidir.
//
// TotalsStale türetilmiş bir alandır ve toplamlarla BİRLİKTE sunulur: bayat bir
// tutarın doğru sanılması, bu API'nin üretebileceği en pahalı hata olurdu.
type cartDTO struct {
	ID            string         `json:"id"`
	RegionID      string         `json:"region_id"`
	CustomerID    string         `json:"customer_id,omitempty"`
	Email         string         `json:"email,omitempty"`
	CurrencyCode  string         `json:"currency_code"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	ShippingTotal int64          `json:"shipping_total"`
	Total         int64          `json:"total"`
	TotalsStale   bool           `json:"totals_stale"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// cartDetailDTO sepetin çocuklarıyla birlikte dış gösterimidir.
type cartDetailDTO struct {
	cartDTO
	Items           []lineItemDTO       `json:"items"`
	ShippingAddress *addressDTO         `json:"shipping_address,omitempty"`
	BillingAddress  *addressDTO         `json:"billing_address,omitempty"`
	ShippingMethods []shippingMethodDTO `json:"shipping_methods"`
}

// lineItemDTO sepet satırının dış gösterimidir.
type lineItemDTO struct {
	ID            string         `json:"id"`
	CartID        string         `json:"cart_id"`
	VariantID     string         `json:"variant_id"`
	Title         string         `json:"title"`
	Quantity      int64          `json:"quantity"`
	UnitPrice     int64          `json:"unit_price"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// addressDTO sepet adresinin dış gösterimidir.
type addressDTO struct {
	ID              string         `json:"id"`
	CartID          string         `json:"cart_id"`
	Type            string         `json:"type"`
	SourceAddressID string         `json:"source_address_id,omitempty"`
	FirstName       string         `json:"first_name,omitempty"`
	LastName        string         `json:"last_name,omitempty"`
	Company         string         `json:"company,omitempty"`
	Address1        string         `json:"address_1,omitempty"`
	Address2        string         `json:"address_2,omitempty"`
	City            string         `json:"city,omitempty"`
	Province        string         `json:"province,omitempty"`
	PostalCode      string         `json:"postal_code,omitempty"`
	CountryCode     string         `json:"country_code,omitempty"`
	Phone           string         `json:"phone,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// shippingMethodDTO kargo yönteminin dış gösterimidir.
type shippingMethodDTO struct {
	ID               string         `json:"id"`
	CartID           string         `json:"cart_id"`
	Name             string         `json:"name"`
	ShippingOptionID string         `json:"shipping_option_id,omitempty"`
	Amount           int64          `json:"amount"`
	Data             map[string]any `json:"data,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// toCartDTO modeli dış gösterime çevirir.
func toCartDTO(cart models.Cart) cartDTO {
	return cartDTO{
		ID:            cart.ID,
		RegionID:      cart.RegionID,
		CustomerID:    cart.CustomerID,
		Email:         cart.Email,
		CurrencyCode:  cart.CurrencyCode,
		Subtotal:      cart.Subtotal,
		DiscountTotal: cart.DiscountTotal,
		TaxTotal:      cart.TaxTotal,
		ShippingTotal: cart.ShippingTotal,
		Total:         cart.Total,
		TotalsStale:   cart.TotalsStale(),
		Metadata:      cart.Metadata,
		CompletedAt:   cart.CompletedAt,
		CreatedAt:     cart.CreatedAt,
		UpdatedAt:     cart.UpdatedAt,
	}
}

// toCartDetailDTO sepeti çocuklarıyla dış gösterime çevirir.
func toCartDetailDTO(detail models.CartDetail) cartDetailDTO {
	out := cartDetailDTO{
		cartDTO:         toCartDTO(detail.Cart),
		Items:           make([]lineItemDTO, 0, len(detail.Items)),
		ShippingMethods: make([]shippingMethodDTO, 0, len(detail.ShippingMethods)),
	}
	// Döngüler indeksle gezilir: satır ve yöntem yapıları büyüktür ve değerle
	// kopyalamak her tur birkaç yüz baytı boşuna taşır.
	for i := range detail.Items {
		out.Items = append(out.Items, toLineItemDTO(detail.Items[i]))
	}
	for i := range detail.ShippingMethods {
		out.ShippingMethods = append(out.ShippingMethods, toShippingMethodDTO(detail.ShippingMethods[i]))
	}
	if detail.ShippingAddress != nil {
		addr := toAddressDTO(*detail.ShippingAddress)
		out.ShippingAddress = &addr
	}
	if detail.BillingAddress != nil {
		addr := toAddressDTO(*detail.BillingAddress)
		out.BillingAddress = &addr
	}
	return out
}

// toLineItemDTO modeli dış gösterime çevirir.
func toLineItemDTO(item models.LineItem) lineItemDTO {
	return lineItemDTO{
		ID:            item.ID,
		CartID:        item.CartID,
		VariantID:     item.VariantID,
		Title:         item.Title,
		Quantity:      item.Quantity,
		UnitPrice:     item.UnitPrice,
		Subtotal:      item.Subtotal,
		DiscountTotal: item.DiscountTotal,
		TaxTotal:      item.TaxTotal,
		Total:         item.Total,
		Metadata:      item.Metadata,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

// toAddressDTO modeli dış gösterime çevirir.
func toAddressDTO(addr models.CartAddress) addressDTO {
	return addressDTO{
		ID:              addr.ID,
		CartID:          addr.CartID,
		Type:            addr.Type.String(),
		SourceAddressID: addr.SourceAddressID,
		FirstName:       addr.FirstName,
		LastName:        addr.LastName,
		Company:         addr.Company,
		Address1:        addr.Address1,
		Address2:        addr.Address2,
		City:            addr.City,
		Province:        addr.Province,
		PostalCode:      addr.PostalCode,
		CountryCode:     addr.CountryCode,
		Phone:           addr.Phone,
		Metadata:        addr.Metadata,
		CreatedAt:       addr.CreatedAt,
		UpdatedAt:       addr.UpdatedAt,
	}
}

// toShippingMethodDTO modeli dış gösterime çevirir.
func toShippingMethodDTO(method models.ShippingMethod) shippingMethodDTO {
	return shippingMethodDTO{
		ID:               method.ID,
		CartID:           method.CartID,
		Name:             method.Name,
		ShippingOptionID: method.ShippingOptionID,
		Amount:           method.Amount,
		Data:             method.Data,
		CreatedAt:        method.CreatedAt,
		UpdatedAt:        method.UpdatedAt,
	}
}

// --- yardımcılar -------------------------------------------------------------

// decodeBody istek gövdesini çözer.
//
// Gövde boyutu sınırlanır ve TANINMAYAN ALANLAR reddedilir: sessizce yutulan
// bir alan, istemcinin gönderdiğini sandığı ama uygulanmayan bir ayar demektir.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "istek gövdesi boş olamaz")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"istek gövdesi çözümlenemedi")
	}
	// Tek bir JSON değerinden fazlası gönderilmişse bu da bir istemci hatasıdır.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"istek gövdesi tek bir JSON nesnesi olmalı")
	}
	return nil
}

// parsePage limit/offset sorgu parametrelerini çözer.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	page := service.Page{Limit: limit, Offset: offset}
	if page.Limit == 0 {
		// Yanıttaki limit alanının gerçekten uygulanan sınırı göstermesi için
		// varsayılan burada da görünür kılınır.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param bir sorgu parametresini tam sayıya çevirir; yoksa 0 döner.
func parseInt64Param(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s tam sayı olmalı: %q", name, raw)
	}
	return value, nil
}

// addressRequest kargo ve fatura uçlarının ortak gövdesidir.
type addressRequest struct {
	SourceAddressID string         `json:"source_address_id"`
	FirstName       string         `json:"first_name"`
	LastName        string         `json:"last_name"`
	Company         string         `json:"company"`
	Address1        string         `json:"address_1"`
	Address2        string         `json:"address_2"`
	City            string         `json:"city"`
	Province        string         `json:"province"`
	PostalCode      string         `json:"postal_code"`
	CountryCode     string         `json:"country_code"`
	Phone           string         `json:"phone"`
	Metadata        map[string]any `json:"metadata"`
}

// toInput gövdeyi servis girdisine çevirir.
func (b addressRequest) toInput() service.AddressInput {
	return service.AddressInput{
		SourceAddressID: b.SourceAddressID,
		FirstName:       b.FirstName,
		LastName:        b.LastName,
		Company:         b.Company,
		Address1:        b.Address1,
		Address2:        b.Address2,
		City:            b.City,
		Province:        b.Province,
		PostalCode:      b.PostalCode,
		CountryCode:     b.CountryCode,
		Phone:           b.Phone,
		Metadata:        b.Metadata,
	}
}

// cartID istekten sepet kimliğini okur.
func cartID(r *http.Request) string {
	return chi.URLParam(r, paramCartID)
}

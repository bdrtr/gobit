// Package models order modülünün alan (domain) modellerini içerir.
//
// Buradaki tipler veritabanı sürücüsünden bağımsızdır: pgtype ve sqlc üretimi
// tipler buraya SIZMAZ. Çeviri repository katmanında yapılır; servis, API ve
// testler yalnızca bu tipleri görür.
//
// Para her yerde TAM SAYI minor unit'tir (kuruş/cent) ve para birimi ayrı
// alanda durur (plan Bölüm 8); kayan nokta hiçbir alanda kullanılmaz. Zamanlar
// UTC'dir.
package models

import "time"

// Tutar ve adet sınırları.
//
// Sınırlar keyfi değildir: satır ara toplamı birim fiyat × adet olarak
// hesaplanır ve bu çarpım int64'e SIĞMALIDIR. MaxAmount × MaxQuantity =
// 10^12 × 10^6 = 10^18 < 9.22×10^18 olduğu için taşma yapısal olarak
// imkânsızdır. Aynı sınırlar cart ve pricing modüllerindeki sınırlarla
// bilinçli olarak aynıdır; modüller birbirini import etmediği için değer
// burada tekrarlanır (ADR 0001'in kabul edilen bedeli).
const (
	// MinAmount izin verilen en küçük tutardır. Negatif tutar bir indirim
	// değildir; indirim ayrı bir alanda (discount_total) taşınır.
	MinAmount int64 = 0
	// MaxAmount izin verilen en büyük tutardır (minor unit).
	MaxAmount int64 = 1_000_000_000_000
	// MinQuantity bir satırın en küçük adedidir; sıfır adet satır demek,
	// satırın hiç olmaması demektir.
	MinQuantity int64 = 1
	// MaxQuantity bir satırın en büyük adedidir.
	MaxQuantity int64 = 1_000_000
	// MaxTotal bir TOPLAM alanının en büyük değeridir (minor unit).
	//
	// Değer MaxAmount × MaxQuantity'dir: tek bir satırın ara toplamı en fazla
	// bu kadar olabilir, dolayısıyla sipariş toplamları için de doğal tavandır.
	// Kimlik doğrulaması (subtotal + tax_total + shipping_total) en fazla
	// 3 × 10^18 üretir ve int64'e (9.22 × 10^18) sığar; taşma yapısal olarak
	// imkânsızdır.
	MaxTotal int64 = MaxAmount * MaxQuantity
)

// MinDisplayID geçerli bir sipariş numarasının en küçük değeridir.
//
// Numarayı veritabanının IDENTITY sütunu üretir ve sequence 1'den başlar;
// sıfır ya da negatif bir numara, numaranın hiç üretilmediği (ya da elle
// yazıldığı) anlamına gelir. Bu yüzden değer bir eşik değil, bir SAĞLIK
// KONTROLÜDÜR: bkz. [ValidDisplayID].
const MinDisplayID int64 = 1

// ValidDisplayID bir sipariş numarasının kullanılabilir olup olmadığını
// bildirir.
//
// Sipariş numarası müşteriye gösterilen ve destek kaydında aranan tek insan
// okunur anahtardır; sıfır bir numara "numarası olmayan sipariş" demek olurdu
// ve müşteri onu hiçbir yerde bulamazdı.
func ValidDisplayID(displayID int64) bool {
	return displayID >= MinDisplayID
}

// OrderStatus bir siparişin yaşam döngüsündeki yeridir.
type OrderStatus string

// Sipariş durumları.
//
// Geçişler tek yönlüdür ve iki uç DURAKTIR:
//
//	pending -> completed -> archived
//	pending -> canceled
//
// canceled ve archived'dan çıkış YOKTUR. İptal edilmiş bir siparişi yeniden
// açmak, iptalle birlikte serbest bırakılmış stoğun ve iade edilmiş ödemenin
// geri alınabildiğini varsaymak olurdu; ikisi de doğru değildir. Yeni bir
// talep yeni bir siparişdir.
const (
	// OrderPending sipariş alındı; henüz tamamlanmadı.
	OrderPending OrderStatus = "pending"
	// OrderCompleted siparişin ödeme ve teslimat tarafı kapandı.
	OrderCompleted OrderStatus = "completed"
	// OrderArchived tamamlanmış sipariş günlük listelerin dışına alındı.
	OrderArchived OrderStatus = "archived"
	// OrderCanceled sipariş iptal edildi; canceled_at damgalıdır.
	OrderCanceled OrderStatus = "canceled"
)

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s OrderStatus) Valid() bool {
	switch s {
	case OrderPending, OrderCompleted, OrderArchived, OrderCanceled:
		return true
	default:
		return false
	}
}

// String durumun metin gösterimini döner.
func (s OrderStatus) String() string {
	return string(s)
}

// Order bir siparişdir.
//
// # Değişmezlik
//
// Sipariş yazıldıktan sonra TUTARLARI ve SATIRLARI değişmez: sipariş, "o an ne
// satıldı ve ne kadar tutardı" sorusunun kalıcı yanıtıdır. Değişen tek şey
// [Order.Status] ve ona bağlı damgalardır. Sonradan doğan düzeltmeler ayrı
// kayıtlarda taşınır ([Return], [Exchange], [Claim]).
//
// # Başka modüllerin kimlikleri
//
// RegionID, CustomerID, CartID ve satırlardaki VariantID başka modüllerin
// kimlikleridir; serbest metin olarak saklanır ve FOREIGN KEY VERİLMEZ
// (Prensip 2.2). Varlıkları bu modülde doğrulanmaz — doğrulama, o modülleri
// tanıyan workflow'un işidir (ADR 0006).
type Order struct {
	// ID "order_" önekli, zamana göre sıralanabilir kimliktir.
	ID string
	// DisplayID müşteriye gösterilen, insan okunur ARTAN numaradır.
	//
	// Değeri UYGULAMA ÜRETMEZ; veritabanının IDENTITY sütunu üretir. Sebebi
	// eşzamanlılıktır: "en büyüğü oku, bir ekle, yaz" iki eşzamanlı siparişte
	// aynı numarayı üretirdi (bkz. migration yorumu).
	DisplayID int64
	// Status siparişin yaşam döngüsündeki yeridir.
	Status OrderStatus
	// RegionID siparişin bölgesidir; region modülüne aittir ve FOREIGN KEY
	// DEĞİLDİR. Zorunludur.
	RegionID string
	// CustomerID siparişin sahibi müşteridir; boş ise sipariş MİSAFİRE aittir.
	CustomerID string
	// Email siparişin iletişim adresidir; misafir siparişinde tek takip yoludur.
	Email string
	// CurrencyCode ISO 4217 kodudur ve daima BÜYÜK harf saklanır.
	CurrencyCode string
	// CartID siparişin doğduğu sepettir; yalnızca KÖKENİ belgeler ve boş olabilir
	// (örn. yönetim tarafından açılan sipariş).
	CartID string
	// IdempotencyKey aynı siparişin iki kez yazılmasını engelleyen anahtardır;
	// boş olabilir (bkz. Prensip 2.6).
	IdempotencyKey string
	// Subtotal satır ara toplamlarının toplamıdır (minor unit).
	Subtotal int64
	// DiscountTotal toplam indirimdir (minor unit); pozitif saklanır ve
	// toplamdan DÜŞÜLÜR.
	DiscountTotal int64
	// TaxTotal toplam vergidir (minor unit).
	TaxTotal int64
	// ShippingTotal toplam kargo tutarıdır (minor unit).
	ShippingTotal int64
	// Total ödenecek tutardır (minor unit):
	// Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
	Total int64
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// PlacedAt siparişin verildiği andır (UTC).
	PlacedAt time.Time
	// CompletedAt siparişin tamamlandığı andır; tamamlanmamışsa nil.
	CompletedAt *time.Time
	// CanceledAt siparişin iptal edildiği andır; iptal edilmemişse nil.
	CanceledAt *time.Time
	// CancelReason iptal gerekçesidir; iptal edilmemiş siparişte boştur.
	CancelReason string
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise sipariş canlıdır.
	DeletedAt *time.Time
}

// Canceled siparişin iptal edilmiş olup olmadığını bildirir.
func (o Order) Canceled() bool {
	return o.Status == OrderCanceled
}

// Completed siparişin tamamlanmış (ya da arşivlenmiş) olup olmadığını bildirir.
//
// Arşivlenmiş sipariş de tamamlanmıştır: arşivleme siparişi listelerin dışına
// çıkarır, tamamlanmışlığını geri almaz.
func (o Order) Completed() bool {
	return o.Status == OrderCompleted || o.Status == OrderArchived
}

// Guest siparişin misafire ait olup olmadığını bildirir.
func (o Order) Guest() bool {
	return o.CustomerID == ""
}

// TotalsConsistent toplam kimliğinin sağlandığını bildirir:
// Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
//
// Kontrol hem servis girişinde hem veritabanı kısıtında vardır; ikisi de aynı
// kimliği zorlar ve bir saga adımındaki hesap hatasının sessizce siparişe
// yazılmasını engeller.
func (o Order) TotalsConsistent() bool {
	return o.Total == o.Subtotal-o.DiscountTotal+o.TaxTotal+o.ShippingTotal
}

// DiscountWithinSubtotal indirimin ara toplamı aşmadığını bildirir.
//
// Aşsaydı müşteri satın aldığı maldan fazlasını geri kazanır, verginin ve
// kargonun bir kısmı indirimle karşılanırdı.
func (o Order) DiscountWithinSubtotal() bool {
	return o.DiscountTotal <= o.Subtotal
}

// OrderDetail siparişin çocuklarıyla birlikte tam hâlidir.
//
// Tip ayrı olması bilinçlidir: [Order] tek satırdır ve listeleme yollarında
// çocuk sorgusu YAPILMAZ (N+1 yasağı). Çocukların yüklü olduğu tek yer bu
// tiptir, dolayısıyla "bu siparişte satır var mı yoksa yüklenmedi mi?"
// belirsizliği hiç doğmaz.
type OrderDetail struct {
	// Order siparişin kendisidir.
	Order
	// Items siparişin satırlarıdır; oluşturulma sırasındadır.
	Items []OrderLineItem
	// Summary siparişin ödeme/iade özetidir. Özet siparişle birlikte doğduğu
	// için burada daima doludur.
	Summary OrderSummary
}

// OrderLineItem siparişteki bir satırdır.
//
// Title ve UnitPrice sepetten KOPYALANIR: katalog sonradan değişse (ya da
// varyant silinse) bile faturada görülen ad ve tutar değişmez. VariantID başka
// bir modülün (product) kimliğidir ve FOREIGN KEY DEĞİLDİR (Prensip 2.2).
type OrderLineItem struct {
	// ID "oli_" önekli kimliktir.
	ID string
	// OrderID satırın ait olduğu siparişdir.
	OrderID string
	// VariantID satırın gösterdiği ürün varyantıdır; product modülüne aittir.
	VariantID string
	// Title satırın görünen adıdır.
	Title string
	// Quantity satırdaki adettir; her zaman pozitiftir.
	Quantity int64
	// UnitPrice birim fiyattır (minor unit).
	UnitPrice int64
	// Subtotal satırın ara toplamıdır (minor unit).
	Subtotal int64
	// DiscountTotal satıra düşen indirimdir (minor unit); pozitif saklanır.
	DiscountTotal int64
	// TaxTotal satıra düşen vergidir (minor unit).
	TaxTotal int64
	// Total satırın toplamıdır (minor unit):
	// Subtotal - DiscountTotal + TaxTotal.
	Total int64
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TotalsConsistent satır toplam kimliğinin sağlandığını bildirir:
// Total = Subtotal - DiscountTotal + TaxTotal.
//
// Kargo satır düzeyinde yoktur; kargo siparişin tamamına aittir.
func (l OrderLineItem) TotalsConsistent() bool {
	return l.Total == l.Subtotal-l.DiscountTotal+l.TaxTotal
}

// DiscountWithinSubtotal satır indiriminin ara toplamı aşmadığını bildirir.
func (l OrderLineItem) DiscountWithinSubtotal() bool {
	return l.DiscountTotal <= l.Subtotal
}

// OrderSummary siparişin ödenen/iade edilen/kalan tutar özetidir.
//
// Sipariş başına tek kayıttır ve siparişle BİRLİKTE, sıfırlanmış olarak doğar;
// "özeti olmayan sipariş" diye bir durum yoktur.
//
// Kalan tutar SAKLANMAZ, [OrderSummary.Outstanding] ile hesaplanır: saklansaydı
// üç sayının birbiriyle tutarlılığını ayrı bir kısıtla korumak gerekir ve
// türetilmiş bir değerin bayatlaması mümkün olurdu.
type OrderSummary struct {
	// ID "osum_" önekli kimliktir.
	ID string
	// OrderID özetin ait olduğu siparişdir.
	OrderID string
	// PaidTotal siparişe karşılık TAHSİL EDİLEN toplam tutardır (minor unit).
	PaidTotal int64
	// RefundedTotal müşteriye GERİ ÖDENEN toplam tutardır (minor unit).
	RefundedTotal int64
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Outstanding siparişin KALAN (henüz tahsil edilmemiş) tutarını döner.
//
// Sipariş toplamı parametre olarak alınır çünkü özet onu SAKLAMAZ: aynı sayıyı
// iki tabloda tutmak, ikisinin ayrışabileceği bir yer açardı ve sipariş
// toplamının tek sahibi orders tablosudur.
//
// Sonuç NEGATİF olabilir: tahsil edilen tutar sipariş toplamını aşarsa (fazla
// tahsilat) fark müşteriye borçtur. Değer sıfıra kırpılmaz; kırpmak fazla
// tahsilatı görünmez kılardı.
func (s OrderSummary) Outstanding(orderTotal int64) int64 {
	return orderTotal - (s.PaidTotal - s.RefundedTotal)
}

// ReturnStatus bir iade kaydının durumudur.
type ReturnStatus string

// İade durumları.
const (
	// ReturnRequested iade talep edildi.
	ReturnRequested ReturnStatus = "requested"
	// ReturnReceived iade edilen mal teslim alındı.
	ReturnReceived ReturnStatus = "received"
	// ReturnCanceled iade talebi iptal edildi.
	ReturnCanceled ReturnStatus = "canceled"
)

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s ReturnStatus) Valid() bool {
	switch s {
	case ReturnRequested, ReturnReceived, ReturnCanceled:
		return true
	default:
		return false
	}
}

// String durumun metin gösterimini döner.
func (s ReturnStatus) String() string {
	return string(s)
}

// Return bir iade kaydıdır (plan Bölüm 6).
//
// Faz 6'da İSKELETTİR: kayıt tutulur ve listelenir, ama iade iş akışı (satır
// bazlı iade, stoğun geri alınması, ödemenin iadesi) sonraki fazlara aittir.
// Bu yüzden satır bazlı çocuk kaydı henüz yoktur.
type Return struct {
	// ID "ret_" önekli kimliktir.
	ID string
	// OrderID iadenin ait olduğu siparişdir.
	OrderID string
	// Status iadenin durumudur.
	Status ReturnStatus
	// RefundAmount iade edilmesi planlanan tutardır (minor unit).
	RefundAmount int64
	// Reason iade gerekçesidir.
	Reason string
	// Note serbest nottur.
	Note string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// ReceivedAt malın teslim alındığı andır; alınmadıysa nil.
	ReceivedAt *time.Time
	// CanceledAt talebin iptal edildiği andır; iptal edilmediyse nil.
	CanceledAt *time.Time
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ExchangeStatus bir değişim kaydının durumudur.
type ExchangeStatus string

// Değişim durumları.
const (
	// ExchangeRequested değişim talep edildi.
	ExchangeRequested ExchangeStatus = "requested"
	// ExchangeCompleted değişim tamamlandı.
	ExchangeCompleted ExchangeStatus = "completed"
	// ExchangeCanceled değişim talebi iptal edildi.
	ExchangeCanceled ExchangeStatus = "canceled"
)

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s ExchangeStatus) Valid() bool {
	switch s {
	case ExchangeRequested, ExchangeCompleted, ExchangeCanceled:
		return true
	default:
		return false
	}
}

// String durumun metin gösterimini döner.
func (s ExchangeStatus) String() string {
	return string(s)
}

// Exchange bir değişim kaydıdır (plan Bölüm 6).
//
// Faz 6'da İSKELETTİR; değişim iş akışı sonraki fazlara aittir.
type Exchange struct {
	// ID "exch_" önekli kimliktir.
	ID string
	// OrderID değişimin ait olduğu siparişdir.
	OrderID string
	// Status değişimin durumudur.
	Status ExchangeStatus
	// DifferenceDue değişim farkıdır (minor unit).
	//
	// NEGATİF OLABİLİR ve bu bilinçlidir: pozitifse fark müşteriden tahsil
	// edilir, negatifse müşteriye ödenir. İşaret yönü belirtir, ölçeği değil;
	// değer yine TAM SAYI minor unit'tir.
	DifferenceDue int64
	// Note serbest nottur.
	Note string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// CompletedAt değişimin tamamlandığı andır; tamamlanmadıysa nil.
	CompletedAt *time.Time
	// CanceledAt talebin iptal edildiği andır; iptal edilmediyse nil.
	CanceledAt *time.Time
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ClaimType bir hasar/eksik kaydının nasıl karşılanacağını belirtir.
type ClaimType string

// Hasar kaydı türleri.
const (
	// ClaimRefund talep para iadesiyle karşılanır.
	ClaimRefund ClaimType = "refund"
	// ClaimReplace talep ürünün yenisiyle karşılanır.
	ClaimReplace ClaimType = "replace"
)

// Valid türün tanımlı bir değer olup olmadığını bildirir.
func (t ClaimType) Valid() bool {
	switch t {
	case ClaimRefund, ClaimReplace:
		return true
	default:
		return false
	}
}

// String türün metin gösterimini döner.
func (t ClaimType) String() string {
	return string(t)
}

// ClaimStatus bir hasar/eksik kaydının durumudur.
type ClaimStatus string

// Hasar kaydı durumları.
const (
	// ClaimRequested talep açıldı.
	ClaimRequested ClaimStatus = "requested"
	// ClaimCompleted talep karşılandı.
	ClaimCompleted ClaimStatus = "completed"
	// ClaimCanceled talep iptal edildi.
	ClaimCanceled ClaimStatus = "canceled"
)

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s ClaimStatus) Valid() bool {
	switch s {
	case ClaimRequested, ClaimCompleted, ClaimCanceled:
		return true
	default:
		return false
	}
}

// String durumun metin gösterimini döner.
func (s ClaimStatus) String() string {
	return string(s)
}

// Claim bir hasar/eksik kaydıdır (plan Bölüm 6).
//
// Faz 6'da İSKELETTİR; talep iş akışı sonraki fazlara aittir.
type Claim struct {
	// ID "claim_" önekli kimliktir.
	ID string
	// OrderID talebin ait olduğu siparişdir.
	OrderID string
	// Type talebin nasıl karşılanacağıdır.
	Type ClaimType
	// Status talebin durumudur.
	Status ClaimStatus
	// RefundAmount Type ClaimRefund iken iade edilecek tutardır (minor unit).
	RefundAmount int64
	// Reason talebin gerekçesidir.
	Reason string
	// Note serbest nottur.
	Note string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// CompletedAt talebin karşılandığı andır; karşılanmadıysa nil.
	CompletedAt *time.Time
	// CanceledAt talebin iptal edildiği andır; iptal edilmediyse nil.
	CanceledAt *time.Time
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

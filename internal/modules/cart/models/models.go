// Package models cart modülünün alan (domain) modellerini içerir.
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
// imkânsızdır. Aynı sınırlar pricing modülündeki sınırlarla bilinçli olarak
// aynıdır; iki modül birbirini import etmediği için değer burada tekrarlanır
// (ADR 0001'in kabul edilen bedeli).
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
	// bu kadar olabilir, dolayısıyla sepet toplamları için de doğal tavandır.
	// Kimlik doğrulaması (subtotal + tax_total + shipping_total) en fazla
	// 3 × 10^18 üretir ve int64'e (9.22 × 10^18) sığar; taşma yapısal olarak
	// imkânsızdır.
	MaxTotal int64 = MaxAmount * MaxQuantity
)

// Cart bir alışveriş sepetidir.
//
// # Toplam alanları kime aittir
//
// Subtotal, DiscountTotal, TaxTotal, ShippingTotal ve Total bu modül tarafından
// HESAPLANMAZ. Fiyat pricing'in, vergi tax/region'ın verisidir ve ikisini bir
// araya getiren akış calculate_totals WORKFLOW'udur (plan Bölüm 2.5, ADR 0006).
// Cart servisi bu alanları yalnızca SAKLAR ve tutarlılığını DOĞRULAR
// (bkz. [Cart.TotalsConsistent]).
//
// # Bayat toplam
//
// Sepetin şeklini değiştiren her işlem [Cart.Revision] sayacını artırır;
// toplamları yazan taraf o anki sayacı [Cart.TotalsRevision] olarak damgalar.
// İkisi ayrıştığında toplamlar sepetin GÜNCEL şekline ait değildir; bunu
// [Cart.TotalsStale] bildirir. Bayatlık ne gizlenir ne uydurulur: bayat toplamı
// sessizce saklamak müşteriye yanlış tutar göstermek, toplamları sıfırlamak ise
// "bedava" demek olurdu.
type Cart struct {
	// ID "cart_" önekli, zamana göre sıralanabilir kimliktir.
	ID string
	// RegionID sepetin bölgesidir; region modülüne aittir ve FOREIGN KEY
	// DEĞİLDİR (Prensip 2.2). Zorunludur.
	RegionID string
	// CustomerID sepetin sahibi müşteridir; customer modülüne aittir ve
	// FOREIGN KEY DEĞİLDİR. Boş ise sepet MİSAFİRE aittir.
	CustomerID string
	// Email sepetin iletişim adresidir; misafir sepetinde tek takip yoludur.
	Email string
	// CurrencyCode ISO 4217 kodudur ve daima BÜYÜK harf saklanır. Değer
	// region'dan kopyalanır; kopyalayan taraf workflow'dur, cart modülü region
	// modülünü çağırmaz (ADR 0001/0006).
	CurrencyCode string
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
	// Revision sepetin şekil sayacıdır; toplamları etkileyen her yapısal
	// değişiklikte bir artar.
	Revision int64
	// TotalsRevision toplamların hangi şekil için hesaplandığını damgalar.
	TotalsRevision int64
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// CompletedAt sepetin tamamlandığı andır; dolu ise sepet DEĞİŞTİRİLEMEZ.
	CompletedAt *time.Time
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise sepet canlıdır.
	DeletedAt *time.Time
}

// Completed sepetin tamamlanmış olup olmadığını bildirir.
func (c Cart) Completed() bool {
	return c.CompletedAt != nil
}

// Guest sepetin misafire ait olup olmadığını bildirir.
func (c Cart) Guest() bool {
	return c.CustomerID == ""
}

// TotalsStale toplamların sepetin güncel şekline ait OLMADIĞINI bildirir.
//
// Ölçüt "hiç hesaplanmadı"yı AYIRT EDEMEZ: hiç dokunulmamış bir sepette
// [Cart.Revision] ve [Cart.TotalsRevision] ikisi de sıfırdır. Ayırt edilmesi
// de gerekmez — sayaç azalmaz ve satır eklemek onu mutlaka artırır, dolayısıyla
// ölçütün sessiz kaldığı tek hesapsız sepet SATIRSIZ olandır ve satırsız sepeti
// tamamlamayı ayrı bir kapı reddeder (bkz. servisteki MarkCompleted).
func (c Cart) TotalsStale() bool {
	return c.TotalsRevision != c.Revision
}

// TotalsConsistent toplam kimliğinin sağlandığını bildirir:
// Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
//
// Kontrol hem servis girişinde hem veritabanı kısıtında vardır; ikisi de aynı
// kimliği zorlar ve workflow'daki bir hesap hatasının sessizce kalıcılaşmasını
// engeller.
func (c Cart) TotalsConsistent() bool {
	return c.Total == c.Subtotal-c.DiscountTotal+c.TaxTotal+c.ShippingTotal
}

// CartDetail sepetin çocuklarıyla birlikte tam hâlidir.
//
// Tip ayrı olması bilinçlidir: [Cart] tek satırdır ve listeleme yollarında
// çocuk sorgusu YAPILMAZ (N+1 yasağı). Çocukların yüklü olduğu tek yer bu
// tiptir, dolayısıyla "bu sepette satırlar var mı yoksa yüklenmedi mi?"
// belirsizliği hiç doğmaz.
type CartDetail struct {
	// Cart sepetin kendisidir.
	Cart
	// Items sepetin satırlarıdır; oluşturulma sırasındadır.
	Items []LineItem
	// ShippingAddress sepetin kargo adresidir; yoksa nil.
	ShippingAddress *CartAddress
	// BillingAddress sepetin fatura adresidir; yoksa nil.
	BillingAddress *CartAddress
	// ShippingMethods sepete seçilmiş kargo yöntemleridir.
	ShippingMethods []ShippingMethod
}

// LineItem sepetteki bir satırdır.
//
// Title ve UnitPrice varyanttan KOPYALANIR: katalog sonradan değişse (ya da
// varyant silinse) bile sepette görülen ad ve tutar değişmez. VariantID başka
// bir modülün (product) kimliğidir ve FOREIGN KEY DEĞİLDİR (Prensip 2.2).
type LineItem struct {
	// ID "li_" önekli kimliktir.
	ID string
	// CartID satırın ait olduğu sepettir.
	CartID string
	// VariantID satırın gösterdiği ürün varyantıdır; product modülüne aittir.
	VariantID string
	// Title satırın görünen adıdır.
	Title string
	// Quantity satırdaki adettir; her zaman pozitiftir.
	Quantity int64
	// UnitPrice birim fiyattır (minor unit); pricing'den gelir ve workflow yazar.
	UnitPrice int64
	// Subtotal satırın ara toplamıdır (minor unit): UnitPrice × Quantity.
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
// Kargo satır düzeyinde yoktur; kargo sepetin tamamına aittir.
func (l LineItem) TotalsConsistent() bool {
	return l.Total == l.Subtotal-l.DiscountTotal+l.TaxTotal
}

// CartTotals bir sepetin toplam alanlarının yazılabilir kümesidir.
//
// Tip, servis ile depo arasındaki sınırda kullanılır: altı ayrı int64
// parametresi yerine adlandırılmış alanlar, çağrı yerinde iki tutarın yanlışlıkla
// yer değiştirmesini imkânsız kılar (derleyici sıra hatasını yakalayamazdı,
// çünkü hepsi aynı tiptedir).
type CartTotals struct {
	// Subtotal satır ara toplamlarının toplamıdır (minor unit).
	Subtotal int64
	// DiscountTotal toplam indirimdir (minor unit); pozitif verilir.
	DiscountTotal int64
	// TaxTotal toplam vergidir (minor unit).
	TaxTotal int64
	// ShippingTotal toplam kargo tutarıdır (minor unit).
	ShippingTotal int64
	// Total ödenecek tutardır (minor unit).
	Total int64
	// Revision toplamların hangi sepet şekli için hesaplandığıdır; kayda
	// totals_revision olarak damgalanır.
	Revision int64
}

// Consistent toplam kimliğinin sağlandığını bildirir:
// Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
func (t CartTotals) Consistent() bool {
	return t.Total == t.Subtotal-t.DiscountTotal+t.TaxTotal+t.ShippingTotal
}

// CartContact bir sepetin iletişim ve sahiplik alanlarının yazılabilir
// kümesidir.
//
// İkisi tek tipte taşınır çünkü tek bir niyetin iki yüzüdür: sepetin KİME ait
// olduğu. Ayrı ayrı iki dize parametresi olsalardı çağrı yerinde yer
// değiştirebilirlerdi ve derleyici bunu yakalayamazdı — her ikisi de string.
type CartContact struct {
	// Email sepetin iletişim adresidir; boş dize saklanan değeri TEMİZLER.
	Email string
	// CustomerID sepetin sahibidir; boş dize sepeti misafir bırakır.
	CustomerID string
}

// LineTotals bir sepet satırının para alanlarının yazılabilir kümesidir.
//
// Adet BURADA YOKTUR: adet sepet servisinin, tutarlar workflow'un verisidir.
// Ayrılık bilinçlidir — bir hesaplama turu adedi sessizce değiştiremez.
type LineTotals struct {
	// UnitPrice birim fiyattır (minor unit).
	UnitPrice int64
	// Subtotal satırın ara toplamıdır (minor unit).
	Subtotal int64
	// DiscountTotal satıra düşen indirimdir (minor unit); pozitif verilir.
	DiscountTotal int64
	// TaxTotal satıra düşen vergidir (minor unit).
	TaxTotal int64
	// Total satırın toplamıdır (minor unit).
	Total int64
}

// Consistent satır toplam kimliğinin sağlandığını bildirir:
// Total = Subtotal - DiscountTotal + TaxTotal.
func (t LineTotals) Consistent() bool {
	return t.Total == t.Subtotal-t.DiscountTotal+t.TaxTotal
}

// LineItemTotals bir satırın KİMLİĞİNİ tutarlarıyla BİRLİKTE taşır.
//
// Kimlik ile tutarların aynı değerde durması bilinçlidir: bir hesap turunun
// tamamı tek deyimle yazılır ve depo bu dilimden altı paralel dizi kurar
// (bkz. cart_line_items.sql, SetLineItemTotals). Kimlikleri ve tutarları ayrı
// dilimler olarak taşımak, çağıranın onları farklı sıralarda vermesini mümkün
// kılardı: yanlış satıra yanlış tutar yazmak müşteriye yanlış tutar tahsil
// etmektir ve aşağı akışta hiçbir kapı bunu görmez.
type LineItemTotals struct {
	// LineItemID tutarların yazılacağı satırdır.
	LineItemID string
	// Totals satırın para alanlarıdır (minor unit).
	Totals LineTotals
}

// AddressType bir sepet adresinin türüdür.
type AddressType string

// Sepet adresinin türleri.
const (
	// AddressShipping kargo adresidir.
	AddressShipping AddressType = "shipping"
	// AddressBilling fatura adresidir.
	AddressBilling AddressType = "billing"
)

// Valid türün tanımlı bir değer olup olmadığını bildirir.
func (t AddressType) Valid() bool {
	switch t {
	case AddressShipping, AddressBilling:
		return true
	default:
		return false
	}
}

// String türün metin gösterimini döner.
func (t AddressType) String() string {
	return string(t)
}

// CartAddress sepete ait kargo ya da fatura adresidir.
//
// # Neden kopya
//
// Sepetin adresi, customer modülündeki defterden KOPYALANIR; sepet kendi
// kopyasını tutar. Müşteri defterindeki kaydını sonradan değiştirdiğinde ya da
// sildiğinde geçmiş sepet (ve ondan doğan sipariş) bozulmaz: sepette "kargonun
// gönderildiği yer" yazılıdır, "müşterinin bugünkü adresi" değil.
// Referansla tutulsaydı, taşınan bir müşterinin eski siparişi yeni adresine
// gönderilmiş gibi görünürdü.
//
// [CartAddress.SourceAddressID] yalnızca KÖKENİ belgeler; okuma için
// kullanılmaz ve FOREIGN KEY DEĞİLDİR (Prensip 2.2).
type CartAddress struct {
	// ID "addr_" önekli kimliktir.
	ID string
	// CartID adresin ait olduğu sepettir.
	CartID string
	// Type adresin türüdür (kargo/fatura).
	Type AddressType
	// SourceAddressID kopyalandığı customer adresinin kimliğidir; boş olabilir.
	SourceAddressID string
	// Ad, unvan ve konum alanları; hepsi isteğe bağlıdır.
	FirstName  string
	LastName   string
	Company    string
	Address1   string
	Address2   string
	City       string
	Province   string
	PostalCode string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur (örn. "TR"); BÜYÜK harf.
	CountryCode string
	Phone       string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ShippingMethod sepete seçilmiş kargo yöntemidir.
//
// ShippingOptionID fulfillment modülünün kimliğidir (Faz 7'de gelecek) ve
// FOREIGN KEY DEĞİLDİR; Faz 5'te seçenek kataloğu henüz yok olduğu için boş
// olabilir.
type ShippingMethod struct {
	// ID "csm_" önekli kimliktir.
	ID string
	// CartID yöntemin ait olduğu sepettir.
	CartID string
	// Name yöntemin görünen adıdır.
	Name string
	// ShippingOptionID fulfillment modülündeki seçeneğin kimliğidir; boş olabilir.
	ShippingOptionID string
	// Amount kargo tutarıdır (minor unit). Sepetin ShippingTotal'ına workflow
	// tarafından toplanır; bu kayıt toplamı kendi yazmaz.
	Amount int64
	// Data sağlayıcıya özgü serbest veridir (örn. seçilen şube).
	Data map[string]any
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

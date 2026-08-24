// Package models tax modülünün domain modellerini tanımlar.
//
// Buradaki tipler veritabanı tiplerinden ARINDIRILMIŞTIR: pgtype bu pakete
// girmez, dönüşüm repository sarmalayıcısında yapılır. Böylece servis ve API
// katmanları depolama ayrıntısına bağlanmaz.
//
// # Oran neden tam sayı
//
// Vergi oranı BAZ PUANDIR (basis point): 1 baz puan = %0,01, dolayısıyla
// 10000 = %100 ve 2000 = %20. Plan Bölüm 8 para ve türevlerinde float yasaklar;
// %20'nin float karşılığı (0.2) bir tutarla çarpıldığında kuruş düzeyinde
// sessiz yuvarlama üretirdi. Bu pakette hiçbir yerde kayan nokta kullanılmaz.
//
// Zamanlar UTC'dir ve silme YUMUŞAKTIR (DeletedAt).
package models

import "time"

// Vergi oranı sınırları (baz puan).
const (
	// MinRateBps izin verilen en küçük vergi oranıdır.
	MinRateBps int32 = 0
	// MaxRateBps izin verilen en büyük vergi oranıdır: %100.
	//
	// Üst sınır bilinçlidir: %100'den büyük bir oran veri giriş hatasıdır ve
	// sepet toplamını sessizce ikiye katlardı.
	MaxRateBps int32 = 10_000
	// BpsPerPercent bir YÜZDEDEKİ baz puan sayısıdır: 100 baz puan = %1.
	//
	// Baz puan ÖLÇEĞİ (10000 = %100) DEĞİLDİR; yalnızca oranı yüzdeye çevirmek
	// için bölen olarak kullanılır (bkz. [TaxRate.RatePercent]). Ad ölçekten
	// bilinçli olarak ayrılmıştır: aynı modülde farklı değerlerde iki
	// "BpsScale" bulunması, vergiyi bu sabitle hesaplayan bir çağrının 100 KAT
	// fazla vergi üretmesi ve derleyicinin bunu yakalamaması demekti. Baz puan
	// ölçeği tek bir yerde, service.BpsScale adıyla durur.
	BpsPerPercent int32 = 100
)

// CountryCodeLength ISO 3166-1 alpha-2 kodunun harf sayısıdır.
const CountryCodeLength = 2

// MaxProvinceCodeLength eyalet/il kodunun azami karakter sayısıdır.
//
// Sınır, veritabanındaki CHECK ile aynıdır. ISO 3166-2'nin ülke içi bölümü en
// fazla üç karakterdir; on karakter, standart dışı ama yerleşik kullanımlara
// (örn. Türkiye'de plaka kodu) yer bırakacak kadar cömert, serbest metne
// dönüşmeyecek kadar dardır.
const MaxProvinceCodeLength = 10

// RuleReference bir vergi kuralının HANGİ TÜR kaleme baktığını söyler.
//
// Değerler veritabanındaki CHECK kısıtıyla birebir aynıdır; buradaki bir
// yazım hatası kayıt anında kısıt ihlali olarak döner.
type RuleReference string

// Kural referans türleri.
const (
	// ReferenceProduct kuralın tek bir ürüne baktığını bildirir.
	ReferenceProduct RuleReference = "product"
	// ReferenceProductType kuralın bir ürün TİPİNE baktığını bildirir.
	ReferenceProductType RuleReference = "product_type"
	// ReferenceShippingOption kuralın bir kargo seçeneğine baktığını bildirir.
	ReferenceShippingOption RuleReference = "shipping_option"
)

// String referansın metin karşılığını döner.
func (r RuleReference) String() string { return string(r) }

// Valid referansın tanımlı bir tür olup olmadığını bildirir.
func (r RuleReference) Valid() bool {
	switch r {
	case ReferenceProduct, ReferenceProductType, ReferenceShippingOption:
		return true
	default:
		return false
	}
}

// Specificity referansın BELİRGİNLİK derecesini döner; büyük olan daha özeldir.
//
// Aynı kaleme birden çok kural eşleştiğinde kazananı bu sıra belirler: tek bir
// ürüne yazılmış kural, o ürünün tipine yazılmış kuralı yener. Sıra olmasaydı
// hangi oranın uygulandığı harita dolaşım sırasına kalırdı ve aynı sepet iki
// çağrıda iki farklı vergi üretebilirdi.
//
// Kargo seçeneği kuralları KALEMLERLE yarışmaz — kargo satırı ayrı hesaplanır —
// bu yüzden derecesi ürün tipiyle aynı kabul edilir; ikisi asla aynı kalemde
// karşılaşmaz.
func (r RuleReference) Specificity() int {
	switch r {
	case ReferenceProduct:
		return 2
	case ReferenceProductType, ReferenceShippingOption:
		return 1
	default:
		return 0
	}
}

// TaxRegion bir vergi bölgesidir: ülke kökü ya da o kökün altındaki eyalet.
//
// Kök bölgede ParentID ve ProvinceCode nil'dir; eyalet bölgesinde İKİSİ DE
// doludur. Ara bir durum yoktur ve veritabanı kısıtı bunu zorlar
// (tax_region_hierarchy_check): ebeveynsiz bir eyalet hiç bulunamayacak,
// eyalet kodu taşıyan bir kök ise ülkenin tamamı yerine tek bir ile oran
// uygulayacak bir kayıt olurdu.
type TaxRegion struct {
	// ID "taxreg_" önekli, zaman sıralı kimliktir.
	ID string
	// CountryCode ISO 3166-1 alpha-2 kodudur; daima BÜYÜK harf saklanır.
	CountryCode string
	// ProvinceCode eyalet/il kodudur; kök bölgede nil.
	ProvinceCode *string
	// ParentID kök bölgenin kimliğidir; kök bölgede nil.
	ParentID *string
	// ProviderID vergi sağlayıcısının kimliğidir. Boş ise ülke kökünün
	// sağlayıcısı DEVRALINIR; kökünki de boşsa yerel hesaplama uygulanır.
	// Bkz. tax/service paket yorumu, "Sağlayıcı soyutlaması".
	ProviderID string
	// Metadata serbest üstveridir; bu modül içeriğini yorumlamaz.
	Metadata map[string]any
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// IsRoot bölgenin ülke kökü olup olmadığını bildirir.
func (r TaxRegion) IsRoot() bool { return r.ParentID == nil }

// Province eyalet kodunu döner; kök bölgede boş dize.
//
// İşaretçiyi dışarı sızdırmamak bilinçlidir: çağıranların çoğu yalnızca
// karşılaştırma yapar ve her çağrı yerinde nil denetimi tekrarlamak, unutulan
// tek bir denetimde panik demektir.
func (r TaxRegion) Province() string {
	if r.ProvinceCode == nil {
		return ""
	}
	return *r.ProvinceCode
}

// Parent kök bölgenin kimliğini döner; kök bölgede boş dize.
func (r TaxRegion) Parent() string {
	if r.ParentID == nil {
		return ""
	}
	return *r.ParentID
}

// TaxRate bir vergi bölgesindeki orandır.
//
// IsDefault bölgenin VARSAYILAN oranıdır ve bir bölgede en fazla bir tane
// olabilir (kısmi benzersiz indeks). Varsayılan oranın kuralı olmaz; kurallı
// bir oran yalnızca kuralıyla eşleşen kaleme uygulanır.
type TaxRate struct {
	// ID "taxrate_" önekli, zaman sıralı kimliktir.
	ID string
	// TaxRegionID oranın ait olduğu bölgedir.
	TaxRegionID string
	// Name oranın görünen adıdır (örn. "KDV"); boş olamaz.
	Name string
	// Code dış sistemlerle mutabakat kodudur; verilmediyse nil.
	Code *string
	// RateBps orandır (baz puan; 2000 = %20).
	RateBps int32
	// IsDefault bölgenin varsayılan oranı olup olmadığıdır.
	IsDefault bool
	// Metadata serbest üstveridir; bu modül içeriğini yorumlamaz.
	Metadata map[string]any
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// RateCode mutabakat kodunu döner; verilmediyse boş dize.
func (r TaxRate) RateCode() string {
	if r.Code == nil {
		return ""
	}
	return *r.Code
}

// RatePercent oranın tam yüzde kısmını ve kalan baz puanı döner.
//
// Yüzde float olarak DÖNMEZ: 2050 baz puan "%20 ve 50 baz puan" olarak, yani
// iki tam sayı hâlinde döner. Sunum katmanı bunu istediği biçimde birleştirir;
// bu paket hiçbir yerde kayan nokta üretmez (plan Bölüm 8).
//
// Bölen [BpsPerPercent]'tir (100), baz puan ölçeği (10000) DEĞİLDİR: burada
// aranan "oranın kaç yüzde ettiği"dir, tutarla çarpılacak bir ölçek değil.
func (r TaxRate) RatePercent() (percent, remainder int32) {
	return r.RateBps / BpsPerPercent, r.RateBps % BpsPerPercent
}

// TaxRatePatch bir oranın KISMİ güncellemesidir.
//
// nil alan "dokunma" demektir; dolu alan yeni değerdir. Tam gövde istenseydi,
// gövdesinde rate_bps göndermeyi unutan bir istemci oranı sessizce sıfırlardı.
type TaxRatePatch struct {
	// Name yeni addır; nil ise ad değişmez.
	Name *string
	// Code yeni mutabakat kodudur; nil ise kod değişmez.
	//
	// Kodu KALDIRMAK için işaretçi dolu, işaret ettiği değer boş dize olmalıdır;
	// servis boş dizeyi SQL NULL'a çevirir. İki seviyeli işaretçi kullanmamak
	// bilinçlidir: JSON'da "code": null ile alanın hiç gönderilmemesi arasındaki
	// farkı taşıyabilen tek yapı odur ve bedeli, her çağrı yerinde iki kat nil
	// denetimidir.
	Code *string
	// RateBps yeni orandır (baz puan); nil ise oran değişmez.
	RateBps *int32
	// IsDefault varsayılanlık bayrağıdır; nil ise değişmez.
	IsDefault *bool
	// Metadata yeni üstveridir; nil ise üstveri değişmez.
	Metadata map[string]any
}

// Empty yamanın hiçbir alan taşımadığını bildirir.
func (p TaxRatePatch) Empty() bool {
	return p.Name == nil && p.Code == nil && p.RateBps == nil &&
		p.IsDefault == nil && p.Metadata == nil
}

// Patched yamanın uygulandığı YENİ bir oran döner; alıcı değiştirilmez.
//
// Değer alıp değer döndürmesi bilinçlidir: güncelleme, kilit altında okunan
// satırın üstüne uygulanır ve saf bir dönüşüm olması onu veritabanı olmadan
// sınanabilir kılar.
func (r TaxRate) Patched(p TaxRatePatch) TaxRate {
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.Code != nil {
		if *p.Code == "" {
			r.Code = nil
		} else {
			code := *p.Code
			r.Code = &code
		}
	}
	if p.RateBps != nil {
		r.RateBps = *p.RateBps
	}
	if p.IsDefault != nil {
		r.IsDefault = *p.IsDefault
	}
	if p.Metadata != nil {
		r.Metadata = p.Metadata
	}
	return r
}

// TaxRateRule bir oranın HANGİ kaleme uygulanacağını söyler.
//
// ReferenceID başka modüllere (product, fulfillment) ait bir kimliktir ve bu
// modülde foreign key DEĞİLDİR (Prensip 2.2); varlığı burada doğrulanmaz.
type TaxRateRule struct {
	// ID "taxrule_" önekli, zaman sıralı kimliktir.
	ID string
	// TaxRateID kuralın bağlı olduğu orandır.
	TaxRateID string
	// Reference kalemin türüdür.
	Reference RuleReference
	// ReferenceID o türdeki kimliktir; boş olamaz.
	ReferenceID string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

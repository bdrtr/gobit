// Package models region modülünün domain modellerini tanımlar.
//
// Buradaki tipler veritabanı tiplerinden ARINDIRILMIŞTIR: pgtype bu pakete
// girmez, dönüşüm repository sarmalayıcısında yapılır. Böylece servis ve API
// katmanları depolama ayrıntısına bağlanmaz.
//
// Para bu modülde TUTAR olarak değil, TANIM olarak geçer: bölge bir para birimi
// KODU taşır, tutar taşımaz. Tutarın minor unit tam sayı olması (plan Bölüm 8)
// bu modülü şu noktada ilgilendirir: [Currency.DecimalDigits] o tam sayının
// hangi çarpanla insan tarafından okunabilir hâle geldiğini söyler.
// Zamanlar UTC'dir.
package models

import "time"

// Vergi oranı sınırları.
//
// Oran BAZ PUAN (basis point) cinsindendir: 1 baz puan = %0,01, dolayısıyla
// 10000 = %100. Tam sayı olması bilinçlidir — plan Bölüm 8 para ve türevlerinde
// float yasaklar; %20'nin float karşılığı (0.2) bir tutarla çarpıldığında
// kuruş düzeyinde sessiz yuvarlama üretirdi.
const (
	// MinTaxRate izin verilen en küçük vergi oranıdır (baz puan).
	MinTaxRate int32 = 0
	// MaxTaxRate izin verilen en büyük vergi oranıdır (baz puan): %100.
	MaxTaxRate int32 = 10_000
	// TaxRateScale baz puan ölçeğidir; oranı yüzdeye çevirmek için bölünür.
	TaxRateScale int32 = 100
)

// Ondalık basamak sınırları.
//
// Üst sınır dörttür: ISO 4217'de kullanımdaki en yüksek minor unit üstel değeri
// 4'tür (örn. UYW). Sınırın varlığı [Currency.MinorUnitFactor]'ın 10^n
// çarpanının makul bir aralıkta kalmasını da garanti eder.
const (
	// MinDecimalDigits izin verilen en küçük ondalık basamak sayısıdır.
	MinDecimalDigits int32 = 0
	// MaxDecimalDigits izin verilen en büyük ondalık basamak sayısıdır.
	MaxDecimalDigits int32 = 4
)

// CurrencyCodeLength ISO 4217 alfabetik kodunun harf sayısıdır.
const CurrencyCodeLength = 3

// CountryCodeLength ISO 3166-1 alpha-2 kodunun harf sayısıdır.
const CountryCodeLength = 2

// Currency bir ISO 4217 para birimidir ve REFERANS VERİDİR.
//
// Kayıtlar migration ile tohumlanır (bkz. 000002_region_seed); modülün yazma
// yüzeyi yoktur. Sebep basittir: ISO 4217 dışarıdan gelen bir standarttır ve
// her kurulumun onu elle girmesi, eksik girilen bir kodun o para biriminde
// fiyat yazılamaması demek olurdu.
type Currency struct {
	// Code ISO 4217 alfabetik kodudur; daima BÜYÜK harf saklanır (örn. "TRY").
	Code string
	// Symbol para biriminin gösterim sembolüdür (örn. "₺").
	Symbol string
	// Name para biriminin ISO'daki İngilizce adıdır; yerelleştirme vitrinin işidir.
	Name string
	// DecimalDigits birimin kaç ondalık basamağı olduğudur (TRY/USD 2, JPY 0,
	// KWD 3). Para minor unit tam sayı saklandığı için sunum katmanı bölme
	// çarpanını BURADAN öğrenir; bkz. [Currency.MinorUnitFactor].
	DecimalDigits int32
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// MinorUnitFactor bir major unit'in kaç minor unit ettiğini döner (10^basamak).
//
// Saklanan tam sayı tutarı insana gösterilebilir hâle getiren çarpan budur:
// 1999 TRY minor unit -> 1999 / 100 = 19,99 ₺; 1999 JPY minor unit -> 1999 / 1
// = 1999 ¥. Sabit 100 varsayan bir sunum katmanı yen tutarını yüz kat küçük,
// dinar tutarını on kat büyük gösterirdi.
//
// Bölme işini ÇAĞIRAN yapar ve tam sayıyla yapmalıdır; bu paket hiçbir yerde
// float üretmez (plan Bölüm 8). Tanımsız (aralık dışı) bir basamak sayısında
// 1 döner: çarpanın sıfır olması, çağıranda sıfıra bölme demekti.
func (c Currency) MinorUnitFactor() int64 {
	if c.DecimalDigits < MinDecimalDigits || c.DecimalDigits > MaxDecimalDigits {
		return 1
	}
	factor := int64(1)
	for i := int32(0); i < c.DecimalDigits; i++ {
		factor *= 10
	}
	return factor
}

// Country bir ISO 3166-1 alpha-2 ülkesidir ve REFERANS VERİDİR.
//
// Kayıtlar migration ile tohumlanır; modülün yazma yüzeyi yalnızca ülkenin
// hangi bölgeye ait olduğunu değiştirir.
type Country struct {
	// Code ISO 3166-1 alpha-2 kodudur; daima BÜYÜK harf saklanır (örn. "TR").
	Code string
	// Name ülkenin ISO'daki İngilizce kısa adıdır.
	Name string
	// RegionID ülkenin bağlı olduğu bölgedir; nil ise ülke hiçbir bölgeye ait
	// değildir. Alan TEK olduğu için "bir ülke en fazla bir bölgeye ait olur"
	// kuralı yapısaldır — ikinci bir bölgeye ait olmanın yeri yoktur.
	RegionID *string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// Region bir satış bölgesidir: para birimi ve vergi davranışı.
//
// Sepet para birimini ve vergi bölgesini buradan alır; bölge bu yüzden sepet
// akışının temelidir. Bölge, sepetleri ya da siparişleri BİLMEZ — bağ Module
// Links üzerinden kurulur ve region o bağı hiç görmez (Prensip 2.1/2.2).
type Region struct {
	// ID "reg_" önekli, zaman sıralı kimliktir.
	ID string
	// Name bölgenin görünen adıdır; boş olamaz.
	Name string
	// CurrencyCode bölgenin para birimidir (ISO 4217, BÜYÜK harf). Tanımlı bir
	// para birimine işaret etmek zorundadır; doğrulama veritabanındaki foreign
	// key ile de ikinci kez yapılır.
	CurrencyCode string
	// AutomaticTaxes verginin sepet toplamına otomatik uygulanıp
	// uygulanmayacağını belirtir.
	AutomaticTaxes bool
	// TaxRate GEÇİCİ vergi oranıdır (baz puan; 2000 = %20).
	//
	// GEÇİCİ: plan Faz 7'de tax modülü vergi hesabını devralacak ve oran
	// TaxRegion/TaxRate kayıtlarına taşınacaktır. O güne kadar sepet akışının
	// çalışabilmesi için bölge üstünde tek ve basit bir oran taşınır; kural
	// karmaşıklaştığında (ürün türüne göre oran, muafiyet, kayıtlı vergi
	// numarası) bu alan KALDIRILACAKTIR ve buraya yeni kural eklenmemelidir.
	TaxRate int32
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// TaxRatePercent oranın tam yüzde kısmını ve kalan baz puanı döner.
//
// Yüzde float olarak DÖNMEZ: 2050 baz puan "%20 ve 50 baz puan" olarak, yani
// iki tam sayı hâlinde döner. Sunum katmanı bunu istediği biçimde birleştirir;
// bu paket hiçbir yerde kayan nokta üretmez (plan Bölüm 8).
func (r Region) TaxRatePercent() (percent, remainder int32) {
	return r.TaxRate / TaxRateScale, r.TaxRate % TaxRateScale
}

// RegionPatch bir bölgenin KISMİ güncellemesidir.
//
// nil alan "dokunma" demektir; dolu alan yeni değerdir. Kısmi güncellemenin
// alternatifi tüm gövdeyi istemek olurdu ve o durumda gövdesinde tax_rate
// göndermeyi unutan bir istemci, oranı sessizce sıfırlardı.
type RegionPatch struct {
	// Name yeni addır; nil ise ad değişmez.
	Name *string
	// CurrencyCode yeni para birimi kodudur; nil ise para birimi değişmez.
	CurrencyCode *string
	// AutomaticTaxes verginin otomatik uygulanıp uygulanmayacağıdır; nil ise değişmez.
	AutomaticTaxes *bool
	// TaxRate yeni vergi oranıdır (baz puan); nil ise oran değişmez.
	TaxRate *int32
}

// Empty yamanın hiçbir alan taşımadığını bildirir.
func (p RegionPatch) Empty() bool {
	return p.Name == nil && p.CurrencyCode == nil && p.AutomaticTaxes == nil && p.TaxRate == nil
}

// Patched yamanın uygulandığı YENİ bir bölge döner; alıcı değiştirilmez.
//
// Değer alıp değer döndürmesi bilinçlidir: güncelleme, kilit altında okunan
// satırın üstüne uygulanır ve saf bir dönüşüm olması onu veritabanı olmadan
// sınanabilir kılar.
func (r Region) Patched(p RegionPatch) Region {
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.CurrencyCode != nil {
		r.CurrencyCode = *p.CurrencyCode
	}
	if p.AutomaticTaxes != nil {
		r.AutomaticTaxes = *p.AutomaticTaxes
	}
	if p.TaxRate != nil {
		r.TaxRate = *p.TaxRate
	}
	return r
}

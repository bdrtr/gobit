// Package models b2b modülünün domain modellerini tanımlar.
//
// Buradaki tipler veritabanı tiplerinden ARINDIRILMIŞTIR: pgtype bu pakete
// girmez, dönüşüm repository sarmalayıcısında yapılır. Böylece servis ve API
// katmanları depolama ayrıntısına bağlanmaz. Zamanlar UTC'dir, para TAM SAYI
// minor unit'tir ve silme SOFT'tur.
package models

import (
	"strings"
	"time"
)

// Alan uzunluk sınırları.
//
// Sınırlar keyfi değildir: e-posta için 320 karakter RFC 5321'in yerel bölüm
// (64) + "@" + alan adı (255) üst sınırıdır. Diğerleri, tek bir isteğin
// veritabanına sınırsız metin yazmasını engelleyen makul tavanlardır ve
// migration'daki CHECK kısıtlarıyla ikinci kez zorlanır.
const (
	// MaxEmailLen bir e-posta adresinin azami uzunluğudur.
	MaxEmailLen = 320
	// MaxNameLen ad gibi kısa metin alanlarının azami uzunluğudur.
	MaxNameLen = 255
	// MaxPhoneLen telefon numarasının azami uzunluğudur.
	MaxPhoneLen = 32
	// MaxAddressLen adres ve şehir alanlarının azami uzunluğudur.
	MaxAddressLen = 255
	// MaxPostalCodeLen posta kodunun azami uzunluğudur.
	MaxPostalCodeLen = 32
)

// SpendingResetPeriod harcama limitinin hangi ARALIKLA sıfırlandığıdır.
//
// # Hangi zaman penceresi kastediliyor
//
// Bu alan tek başına bir sayı değil, bir PENCERE TANIMIDIR: çalışanın
// [CompanyEmployee.SpendingLimit] değeri, pencerenin başlangıcından şimdiye
// kadar verilmiş siparişlerin toplamıyla karşılaştırılır. Pencerenin başlangıcı
// [SpendingResetPeriod.WindowStart] ile hesaplanır ve TAKVİME göredir, kaydın
// oluşturulma tarihine göre değil: aylık bir limit, şirket ayın 20'sinde
// açılmış olsa bile her ayın 1'inde sıfırlanır. Bu seçim bilinçlidir — muhasebe
// dönemleri takvimle yürür ve "şirketin açılış gününe göre kayan ay" hiçbir
// mali raporla örtüşmezdi.
//
// Pencere UTC'dir. Yerel saat dilimi kullanmak, aynı şirketin iki farklı
// ülkedeki çalışanı için ayın farklı anlarda başlaması demek olurdu.
//
// # Kuralı KİM uygular
//
// Bu modül değil, order modülü. Pencerenin başlangıcı ve limit
// "b2b.interop" yüzeyinden yayımlanır; harcamayı (pencere içinde verilmiş
// siparişlerin toplamını) hesaplayıp limitle karşılaştıran taraf, o toplamın
// sahibi olan modüldür. Ayrıntı için bkz. internal/modules/b2b/service,
// Interop.SpendingLimitJSON.
type SpendingResetPeriod string

// Tanımlı sıfırlama periyotları. Değerler veritabanındaki CHECK kısıtıyla
// birebir aynıdır (bkz. migrations/000001_b2b_init.up.sql).
const (
	// ResetMonthly limiti her takvim ayının 1'inde sıfırlar.
	ResetMonthly SpendingResetPeriod = "monthly"
	// ResetYearly limiti her takvim yılının 1 Ocak'ında sıfırlar.
	ResetYearly SpendingResetPeriod = "yearly"
	// ResetNever limiti hiç sıfırlamaz; pencere çalışanın TÜM geçmişidir.
	ResetNever SpendingResetPeriod = "never"
)

// Valid periyodun tanımlı olup olmadığını bildirir.
//
// Tip bir dizedir ve çağıran enum dışında bir değer kurabilir; böyle bir değer
// sessizce "never"a düşseydi, aylık limit koyduğunu sanan şirket hiç
// sıfırlanmayan bir limitle kalırdı.
func (p SpendingResetPeriod) Valid() bool {
	return p == ResetMonthly || p == ResetYearly || p == ResetNever
}

// WindowStart geçerli harcama penceresinin BAŞLANGIÇ anını döner.
//
// [ResetNever] için nil döner: pencere yoktur, limit çalışanın tüm geçmişine
// uygulanır. Diğer periyotlarda dönen an, içinde bulunulan takvim ayının ya da
// yılının ilk gününün 00:00 UTC'sidir ve pencere bu andan ŞİMDİYE kadardır
// (başlangıç dâhil, üst uç açık).
//
// Fonksiyon zamanı parametre olarak alır; time.Now'a doğrudan bağlanmak, sınır
// anlarını (ayın ilk saniyesi, yıl dönümü) test edilemez kılardı.
func (p SpendingResetPeriod) WindowStart(now time.Time) *time.Time {
	utc := now.UTC()
	switch p {
	case ResetMonthly:
		start := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
		return &start
	case ResetYearly:
		start := time.Date(utc.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
		return &start
	case ResetNever:
		return nil
	default:
		// Tanımsız bir değer veritabanına giremez (CHECK) ve servis onu
		// reddeder; buraya düşülürse en güvenli davranış pencereyi hiç
		// açmamaktır — "sınırsız pencere" demek, limiti sessizce genişletmek
		// olurdu.
		return nil
	}
}

// Company alışverişi bir birey adına değil bir TÜZEL KİŞİ adına yapan
// müşteridir.
//
// Şirketin kendisi alışveriş yapmaz; onun adına [CompanyEmployee] kayıtları
// yapar. Bu yüzden şirkette bir harcama limiti YOKTUR — limit çalışan başınadır
// (bkz. [CompanyEmployee.SpendingLimit]); şirket yalnızca limitin hangi
// ARALIKLA sıfırlandığını belirler, çünkü sıfırlama dönemi muhasebe dönemidir
// ve çalışandan çalışana değişemez.
type Company struct {
	// ID "comp_" önekli, zaman sıralı kimliktir.
	ID string
	// Name şirketin ticari unvanıdır; zorunludur.
	Name string
	// Email şirketin iletişim adresidir; daima KÜÇÜK harfe normalize edilmiş
	// hâlde saklanır. BENZERSİZ DEĞİLDİR (bkz. migration belgesi).
	Email string
	// Phone şirketin telefonudur; boş olabilir.
	Phone string
	// Address fatura adresinin sokak satırıdır; boş olabilir.
	Address string
	// City şehirdir; boş olabilir.
	City string
	// PostalCode posta kodudur; boş olabilir.
	PostalCode string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur (BÜYÜK harf); boş olabilir.
	// Kodun gerçekten var olan bir ülkeye karşılık geldiği BURADA denetlenmez;
	// ülke listesinin sahibi region modülüdür.
	CountryCode string
	// CurrencyCode ISO 4217 para birimi kodudur (BÜYÜK harf); zorunludur.
	// Harcama limitleri bu para biriminde ifade edilir.
	CurrencyCode string
	// SpendingLimitResetPeriod çalışan limitlerinin sıfırlanma aralığıdır.
	SpendingLimitResetPeriod SpendingResetPeriod
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// CompanyEmployee bir şirket adına alışveriş yapabilen çalışandır.
//
// Kayıt, B2B'nin B2C varsayımını kırdığı yerdir: alıcı bir birey değil,
// HARCAMA YETKİSİ SINIRLI bir çalışandır. Kimliği yine de bir müşteridir —
// sepeti, siparişi ve adresi customer modülünde durur; bu kayıt yalnızca o
// müşterinin hangi şirket adına ve NE KADARA kadar alışveriş yapabileceğini
// söyler.
type CompanyEmployee struct {
	// ID "compemp_" önekli kimliktir.
	ID string
	// CompanyID çalışanın bağlı olduğu şirkettir; modül içi foreign key'dir.
	CompanyID string
	// CustomerID çalışanın MÜŞTERİ kaydıdır (customer modülü).
	//
	// DİKKAT: bu alanın veritabanında bir SÜTUNU YOKTUR. Değer
	// "b2b_employee_customer" link'inden okunur ve servis katmanı doldurur;
	// repository katmanı onu BOŞ bırakır. Sütun ile link'in aynı ilişkiyi iki
	// yerde tutması, ikisinin ayrışması demek olurdu ve ayrışma vitrinde
	// "kendi şirketim" sorusuna iki farklı cevap üretirdi.
	CustomerID string
	// SpendingLimit çalışanın pencere başına harcayabileceği azami tutardır
	// (minor unit, şirketin para biriminde).
	//
	// nil SINIRSIZ demektir; 0 ise gerçek bir sıfır limittir ve çalışan hiç
	// harcayamaz. İkisini tek değere indirmek, "limit koymadım" ile "limiti
	// sıfırladım" arasındaki farkı yok ederdi. Pencerenin tanımı için bkz.
	// [SpendingResetPeriod].
	SpendingLimit *int64
	// IsCompanyAdmin çalışanın şirket yöneticisi olup olmadığını bildirir.
	IsCompanyAdmin bool
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// HasSpendingLimit çalışanın sınırlı bir harcama yetkisi olup olmadığını
// bildirir.
//
// Sıfır limitli bir çalışan da SINIRLIDIR: kontrol nil'e bakar, değere değil.
func (e CompanyEmployee) HasSpendingLimit() bool { return e.SpendingLimit != nil }

// NormalizeEmail e-postayı saklama biçimine çevirir: kırpılır ve KÜÇÜK harfe
// indirilir.
//
// Normalizasyon SAKLAMADA yapılır, okumada değil: "Muhasebe@X.com" ile
// "muhasebe@x.com" aynı adresi göstermeliyse ikisinin de aynı baytlara inmesi
// gerekir, aksi hâlde e-posta süzgeci ikisini farklı sanardı.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormalizeCountryCode ülke kodunu saklama biçimine çevirir: kırpılır ve BÜYÜK
// harfe çıkarılır. Doğrulama çağırana aittir.
func NormalizeCountryCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// NormalizeCurrencyCode para birimi kodunu saklama biçimine çevirir: kırpılır
// ve BÜYÜK harfe çıkarılır. Doğrulama çağırana aittir.
func NormalizeCurrencyCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

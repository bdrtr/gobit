// Package models auth modülünün domain modellerini tanımlar.
//
// Buradaki tipler veritabanı tiplerinden ARINDIRILMIŞTIR: pgtype bu pakete
// girmez, dönüşüm repository sarmalayıcısında yapılır. Böylece servis ve API
// katmanları depolama ayrıntısına bağlanmaz. Zamanlar UTC'dir; silme SOFT'tur.
//
// # Sırlar
//
// Bu pakette iki sır alanı vardır ve ikisi de yalnızca HASH taşır:
// [AuthIdentity.PasswordHash] (bcrypt) ve [APIKey.TokenHash] (SHA-256).
// Düz parola hiçbir tipte alan olarak BULUNMAZ — bulunsaydı bir yapının
// "%+v" ile loglanması parolayı diske yazardı. Anahtarın düz metni yalnızca
// oluşturma çağrısının DÖNÜŞ DEĞERİ olarak, hiçbir yapıya konmadan taşınır.
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
	// MaxNameLen ad/soyad/başlık gibi kısa metin alanlarının azami uzunluğudur.
	MaxNameLen = 255
	// MaxURLLen avatar adresi gibi URL alanlarının azami uzunluğudur.
	MaxURLLen = 2048
	// MaxDescriptionLen açıklama alanlarının azami uzunluğudur.
	MaxDescriptionLen = 1024
	// MaxScopeLen tek bir yetki adının azami uzunluğudur.
	MaxScopeLen = 64
	// MaxScopeCount bir kimliğe verilebilecek azami yetki sayısıdır.
	MaxScopeCount = 64
)

// ProviderEmailPass e-posta + parola ile giriş sağlayıcısının adıdır.
//
// Şimdilik tek sağlayıcı budur. İleride "google", "github" gibi OAuth
// sağlayıcıları eklendiğinde AYNI kullanıcıya ikinci bir [AuthIdentity]
// bağlanır; kullanıcı kaydına dokunulmaz.
const ProviderEmailPass = "emailpass"

// User bir YÖNETİM kullanıcısıdır (admin yüzeyine giren kişi).
//
// Mağazadan alışveriş yapan kişiyle karıştırılmamalıdır: o, customer modülünün
// verisidir. İki kavramın ayrı modüllerde durması bilinçlidir — bir müşterinin
// yönetim yetkisi kazanması diye bir yol yoktur.
//
// Parola BURADA DEĞİLDİR: kimlik doğrulama yöntemi [AuthIdentity] kaydındadır
// (gerekçe orada yazılıdır).
type User struct {
	// ID "user_" önekli, zaman sıralı kimliktir.
	ID string
	// Email kullanıcının e-posta adresidir; daima KÜÇÜK harfe normalize
	// edilmiş hâlde saklanır (bkz. [NormalizeEmail]) ve canlı kullanıcılar
	// arasında benzersizdir.
	Email string
	// FirstName kullanıcının adıdır; boş olabilir.
	FirstName string
	// LastName kullanıcının soyadıdır; boş olabilir.
	LastName string
	// AvatarURL profil görselinin adresidir; boş olabilir.
	AvatarURL string
	// Scopes kullanıcının yetkileridir. Varsayılan tek yetki [ScopeAdmin]'dir;
	// daha ince taneli roller bu dilime yeni ad eklenerek tanımlanır.
	Scopes []string
	// Metadata çağıranın serbestçe yazdığı yapısal bağlamdır; boş olabilir.
	Metadata map[string]any
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// ScopeAdmin tüm yetkileri kapsayan üst yetkidir.
//
// Değer çekirdekteki corehttp.ScopeAdmin ile AYNI olmalıdır. Sabit burada
// tekrar edilir çünkü models paketi HTTP katmanını tanımaz; eşitliği
// service paketindeki bir test kanıtlar.
const ScopeAdmin = "admin"

// FullName kullanıcının görünen adını döner; ad ve soyad boşsa e-postaya düşer.
func (u User) FullName() string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		return u.Email
	}
	return name
}

// AuthIdentity bir kullanıcının TEK bir kimlik doğrulama yöntemidir.
//
// # Neden User'dan ayrı
//
// Bir kullanıcının birden çok giriş yolu olabilir. Bugün yalnızca
// [ProviderEmailPass] vardır; yarın OAuth eklendiğinde aynı kullanıcıya ikinci
// bir kimlik satırı bağlanır ve kullanıcı kaydına hiç dokunulmaz. Parola alanı
// [User] üzerinde olsaydı, parolasız (yalnızca OAuth ile giren) bir kullanıcı
// ya ifade edilemez ya da boş parola ile temsil edilirdi; ikincisi, boş
// parolayla giriş denemesini bir kod hatası uzaklığına indirirdi.
//
// # PasswordHash
//
// Alan bcrypt çıktısıdır; düz parola ne saklanır ne loglanır ne de hata
// mesajlarında geçer. bcrypt maliyeti hash'in İÇİNDE kodludur, bu yüzden
// maliyet ileride artırıldığında eski hash'ler kendi maliyetleriyle
// doğrulanmaya devam eder.
type AuthIdentity struct {
	// ID "authid_" önekli kimliktir.
	ID string
	// UserID kimliğin bağlı olduğu kullanıcıdır.
	UserID string
	// Provider kimlik doğrulama sağlayıcısıdır (örn. [ProviderEmailPass]).
	Provider string
	// ProviderIdentity sağlayıcı nezdindeki kimliktir; emailpass için
	// kullanıcının normalize edilmiş e-postasıdır.
	ProviderIdentity string
	// PasswordHash bcrypt hash'idir; parola atanmamışsa boştur ve giriş
	// REDDEDİLİR.
	PasswordHash string
	// FailedAttempts art arda başarısız giriş sayısıdır; başarılı girişte
	// sıfırlanır.
	FailedAttempts int
	// LockedUntil geçici kilidin bitiş anıdır; nil ise kilit yoktur.
	LockedUntil *time.Time
	// LastLoginAt son BAŞARILI girişin anıdır; nil ise hiç giriş yapılmamıştır.
	LastLoginAt *time.Time
	// Metadata çağıranın serbestçe yazdığı yapısal bağlamdır; boş olabilir.
	Metadata map[string]any
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// IsLocked kimliğin verilen anda geçici kilitli olup olmadığını bildirir.
func (i AuthIdentity) IsLocked(at time.Time) bool {
	return i.LockedUntil != nil && at.Before(*i.LockedUntil)
}

// APIKeyType bir API anahtarının türüdür.
type APIKeyType string

// API anahtarı türleri. İkisi AYNI ŞEY DEĞİLDİR ve birbirinin yerine
// KULLANILAMAZ; ayrımın uygulanması [APIKey] godoc'unda anlatılır.
const (
	// APIKeyPublishable mağaza yüzeyinde kullanılan, SIR OLMAYAN anahtardır.
	APIKeyPublishable APIKeyType = "publishable"
	// APIKeySecret yönetim yüzeyine erişen SIRDIR.
	APIKeySecret APIKeyType = "secret"
)

// Valid türün tanımlı olup olmadığını bildirir.
//
// Tip dışa açıktır ve çağıran enum dışında bir değer kurabilir; doğrulanmayan
// bir değer veritabanındaki CHECK kısıtına takılırdı ve istemci anlamsız bir
// kısıt hatası görürdü.
func (t APIKeyType) Valid() bool {
	return t == APIKeyPublishable || t == APIKeySecret
}

// String türün metin karşılığını döner.
func (t APIKeyType) String() string { return string(t) }

// APIKey bir makine kimliğidir.
//
// # İki tür, iki farklı güven modeli
//
// [APIKeySecret] bir SIRDIR: yönetim yüzeyine erişir, sunucuda saklanır,
// tarayıcıya asla verilmez ve sızması admin erişimi demektir.
//
// [APIKeyPublishable] bir SIR DEĞİLDİR: tarayıcıda, storefront paketinin
// içinde, hatta sayfa kaynağında görünür. Tek işi isteği bir satış kanalına
// BAĞLAMAKTIR; hiçbir yetki taşımaz ve tek başına hiçbir veriyi açmaz. Bu
// yüzden "sızması" diye bir olayı yoktur — yanlış kullanımı, birinin başka bir
// mağazanın kanal kimliğiyle o mağazanın vitrin kataloğunu okumasıdır ve o
// katalog zaten herkese açıktır.
//
// Ayrım iki bağımsız kapıyla uygulanır: düz metnin ÖNEKİ ("sk_" / "pk_") ve
// bu kayıttaki [APIKey.Type] alanı. Yönetim yüzeyi yalnızca secret, mağaza
// yüzeyi yalnızca publishable kabul eder; birini diğerinin yerine sunmak her
// iki kapıda da reddedilir.
//
// # Düz metin
//
// Anahtarın kendisi SAKLANMAZ; yalnızca [APIKey.TokenHash] (SHA-256) tutulur.
// Düz metin YALNIZCA oluşturma çağrısının dönüş değeri olarak bir kez verilir
// ve bir daha hiçbir yerden okunamaz. Kaybedilen anahtar geri getirilemez;
// yapılacak şey iptal edip yenisini üretmektir.
//
// Karar publishable anahtarlar için de aynıdır — sır olmadıkları hâlde onların
// da yalnızca hash'i saklanır. Tek biçimli saklama bir hata sınıfını topyekûn
// kaldırır: "düz metni dönen" bir kod yolu, tür alanındaki bir hata yüzünden
// yanlışlıkla gizli bir anahtarı gösteremez, çünkü döndürecek düz metin
// hiçbir satırda yoktur.
type APIKey struct {
	// ID "apikey_" önekli kimliktir.
	ID string
	// Type anahtarın türüdür: [APIKeyPublishable] ya da [APIKeySecret].
	Type APIKeyType
	// Title anahtarın insan tarafından okunan adıdır (örn. "Web storefront").
	Title string
	// TokenHash düz metnin SHA-256 hash'idir (küçük harf hex, 64 karakter).
	TokenHash string
	// Redacted gösterim için maskelenmiş hâldir (örn. "pk_…a1b2"); düz metnin
	// yerine geçmez ve onunla doğrulama YAPILAMAZ.
	Redacted string
	// Scopes anahtarın yetkileridir. Publishable anahtarlarda daima BOŞTUR.
	Scopes []string
	// CreatedBy anahtarı üretenin kimliğidir; bir kullanıcı ya da başka bir
	// gizli anahtar olabilir, bu yüzden foreign key taşımaz.
	CreatedBy string
	// LastUsedAt anahtarın son kullanım anıdır; nil ise hiç kullanılmamıştır.
	// Değer YAKLAŞIKTIR (bkz. service, usageThrottle).
	LastUsedAt *time.Time
	// RevokedAt iptal anıdır; nil değilse anahtar artık kabul edilmez.
	RevokedAt *time.Time
	// RevokedBy iptali yapanın kimliğidir; boş olabilir.
	RevokedBy string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// IsRevoked anahtarın iptal edilip edilmediğini bildirir.
func (k APIKey) IsRevoked() bool { return k.RevokedAt != nil }

// SalesChannel bir satış kanalıdır (örn. "Web", "Mobil uygulama", "Bayi").
//
// Publishable anahtarlar kanallara bağlanır; mağaza isteği hangi kanaldan
// geldiğini bu bağdan öğrenir. Hangi ürünün hangi kanalda göründüğü ise
// product ↔ sales_channel linkiyle kurulur ve auth o linki hiç görmez
// (Prensip 2.2).
type SalesChannel struct {
	// ID "sc_" önekli kimliktir.
	ID string
	// Name kanalın görünen adıdır; canlı kanallar arasında benzersizdir.
	Name string
	// Description kanalın açıklamasıdır; boş olabilir.
	Description string
	// IsDisabled kanalın devre dışı olduğunu bildirir. Devre dışı bir kanal
	// mağaza kimlik doğrulamasında YOK SAYILIR.
	IsDisabled bool
	// Metadata çağıranın serbestçe yazdığı yapısal bağlamdır; boş olabilir.
	Metadata map[string]any
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// NormalizeEmail e-postayı saklama biçimine çevirir: kırpılır ve KÜÇÜK harfe
// indirilir.
//
// Normalizasyon SAKLAMADA yapılır, okumada değil. Benzersizlik indeksi ham
// sütun üzerindedir; "Ali@X.com" ile "ali@x.com" aynı kullanıcıyı
// göstermeliyse ikisinin de aynı baytlara inmesi gerekir. Okuma anında
// normalize etmek, tabloya iki farklı yazımın girmesini engellemezdi.
//
// Küçük harfe indirme yerel bölüm (@ öncesi) için teknik olarak RFC'ye aykırı
// sayılabilir — RFC 5321 yerel bölümü büyük/küçük harfe duyarlı bırakır — ama
// pratikte hiçbir sağlayıcı bu ayrımı kullanmaz ve duyarlı bırakmak aynı kişiye
// iki yönetim hesabı açtırırdı. Girişin tek bir satırla eşleşmesi bu
// eşitlemeye bağlıdır.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

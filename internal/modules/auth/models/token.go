package models

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// API anahtarı düz metninin önekleri.
//
// Önek bir SÜS DEĞİLDİR: yönetim ve mağaza yüzeyleri gelen kimlik bilgisini
// veritabanına hiç gitmeden ayırt eder ve yanlış türdeki anahtarı daha ilk
// adımda reddeder. İkinci kapı [APIKey.Type] alanıdır; iki kapı birbirinden
// bağımsızdır ve ikisi de geçilmeden kimlik kurulmaz.
//
// Önekler ayrıca sızıntı taramalarının işine yarar: "sk_" ile başlayan bir
// dize bir depoya ya da log'a düştüğünde desen eşleşmesiyle bulunabilir.
const (
	// SecretKeyPrefix gizli anahtarların önekidir.
	SecretKeyPrefix = "sk_"
	// PublishableKeyPrefix publishable anahtarların önekidir.
	PublishableKeyPrefix = "pk_"
)

// tokenEntropyBytes bir anahtarın rastgele gövdesinin bayt sayısıdır.
//
// 32 bayt = 256 bit. Bu, saklanan SHA-256 özetinin genişliğiyle eşittir ve
// tahmin edilmesi hesaplama olarak imkânsızdır; anahtarın parola gibi yavaş
// bir hash'e ihtiyaç duymamasının nedeni de budur (bkz. [HashToken]).
const tokenEntropyBytes = 32

// redactedTailLen maskelenmiş gösterimde açıkta bırakılan son karakter
// sayısıdır.
//
// Dört karakter, iki anahtarı listede birbirinden ayırmaya yeter ve 256 bitlik
// bir gövdeden 24 bitlik bir ipucu verir — kalan arama uzayı hâlâ 2^232'dir,
// yani ipucunun pratik bir değeri yoktur.
const redactedTailLen = 4

// redactedMask maskelenmiş gösterimde gizlenen kısmın yerine konan işarettir.
const redactedMask = "..."

// ErrUnknownKeyType tanınmayan bir anahtar türü verildiğini bildirir.
//
// Paket errors'ın tipli hatalarını kullanmaz: models katmanı HTTP durum
// kodlarını tanımaz ve bu hata çağıran servis tarafından sınıflandırılır.
var ErrUnknownKeyType = errors.New("auth: tanınmayan api anahtarı türü")

// TokenPrefix verilen türün düz metin önekini döner.
func TokenPrefix(t APIKeyType) (string, error) {
	switch t {
	case APIKeySecret:
		return SecretKeyPrefix, nil
	case APIKeyPublishable:
		return PublishableKeyPrefix, nil
	default:
		return "", ErrUnknownKeyType
	}
}

// TypeForToken düz metnin önekinden anahtar türünü çıkarır.
//
// Bu, kimlik doğrulamanın İLK kapısıdır: mağaza yüzeyine gelen "sk_" önekli
// bir dize, hiçbir veritabanı okuması yapılmadan reddedilir.
func TypeForToken(plaintext string) (APIKeyType, error) {
	switch {
	case strings.HasPrefix(plaintext, SecretKeyPrefix):
		return APIKeySecret, nil
	case strings.HasPrefix(plaintext, PublishableKeyPrefix):
		return APIKeyPublishable, nil
	default:
		return "", ErrUnknownKeyType
	}
}

// NewToken verilen tür için yeni bir düz metin API anahtarı üretir.
//
// Gövde 32 bayt kriptografik rastgeleliktir ve dolgusuz base64url ile
// kodlanır; sonuç URL'de, başlıkta ve ortam değişkeninde kaçışsız taşınabilir.
// [NewID]'nin aksine zaman damgası TAŞIMAZ: sıralanabilirlik bir kimlik
// özelliğidir, bir sırra eklendiğinde arama uzayını daraltırdı.
//
// Dönen değer çağıranın elindeki TEK kopyadır; hiçbir yapıya konmaz ve
// saklanmaz (bkz. [APIKey]).
func NewToken(t APIKeyType) (string, error) {
	prefix, err := TokenPrefix(t)
	if err != nil {
		return "", err
	}

	buf := make([]byte, tokenEntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand.Read hata dönmezse de dönen hâli sessizce geçilemez:
		// zayıf rastgelelikle üretilmiş bir anahtar tahmin edilebilir olurdu.
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken düz metnin saklanan özetini üretir: SHA-256, küçük harf hex.
//
// # Neden bcrypt değil
//
// Parola hash'leri KASTEN yavaştır; koruduğu şey, insan tarafından seçilmiş ve
// düşük entropili bir sırra karşı çevrimdışı sözlük saldırısıdır. API anahtarı
// öyle bir sır değildir: [NewToken] onu 256 bit rastgelelikle ÜRETİR, yani
// veritabanı tümüyle sızsa bile kaba kuvvet hesaplama olarak imkânsızdır ve
// yavaş hash'in eklediği koruma sıfırdır.
//
// Buna karşılık maliyeti sıfır değildir: bu özet HER İSTEKTE hesaplanır.
// bcrypt her yönetim isteğine ~250 ms eklerdi ve kimlik doğrulamanın kendisi
// bir hizmet dışı bırakma yüzeyine dönüşürdü. Dahası bcrypt'in satır başına
// tuzu, gelen anahtarın hangi satıra ait olduğunu bulmak için TÜM tabloyu
// taramayı ve her satırda bir bcrypt çalıştırmayı gerektirirdi; SHA-256 tek ve
// indekslenebilir bir aramadır.
//
// Karşılaştırma [TokenHashesEqual] ile SABİT ZAMANDA yapılır.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// TokenHashesEqual iki anahtar özetini SABİT ZAMANDA karşılaştırır.
//
// Aramanın kendisi indeks üzerinden yapıldığı hâlde bu karşılaştırma yine de
// gereklidir: eşitliği yalnızca veritabanına bırakmak, sorgunun bir gün önek
// eşleşmesine ya da büyük/küçük harf duyarsız bir karşılaştırmaya dönüşmesi
// riskini taşır. Buradaki kontrol, saklanan özetin gelen özetle BAYT BAYT aynı
// olduğunu uygulama tarafında da doğrular ve bunu erken çıkışsız yapar.
func TokenHashesEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RedactToken düz metnin gösterim için maskelenmiş hâlini üretir
// (örn. "pk_...a1b2").
//
// Maskelenmiş değer bir anahtar DEĞİLDİR ve onunla kimlik doğrulanamaz; tek
// işi bir listede iki anahtarı birbirinden ayırt etmektir.
func RedactToken(plaintext string) string {
	prefix := ""
	body := plaintext
	if t, err := TypeForToken(plaintext); err == nil {
		prefix, _ = TokenPrefix(t)
		body = strings.TrimPrefix(plaintext, prefix)
	}

	if len(body) <= redactedTailLen {
		// Beklenen uzunlukta bir anahtarda bu dala düşülmez; düşülüyorsa
		// gövdenin tamamını göstermektense hiçbirini göstermemek doğrudur.
		return prefix + redactedMask
	}
	return prefix + redactedMask + body[len(body)-redactedTailLen:]
}

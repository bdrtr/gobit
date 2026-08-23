package service

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8: prefix'li kimlikler).
//
// Önek kimliğin türünü kendi içinde taşır: bir log satırında ya da bir link
// tablosunda "variant_01J..." gördüğünüzde hangi tabloya bakacağınızı bilirsiniz.
// Ayrıca yanlış türden bir kimliğin yanlış uca geçmesi (ürün kimliğinin
// varyant beklenen yere verilmesi) gözle görülür hâle gelir.
const (
	prefixProduct     = "prod_"
	prefixVariant     = "variant_"
	prefixOption      = "popt_"
	prefixOptionValue = "poptval_"
	prefixCategory    = "pcat_"
	prefixCollection  = "pcol_"
	prefixTag         = "ptag_"
	prefixImage       = "pimg_"
)

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır.
//
// 16 baytlık gövde bu alfabeyle tam 26 karakter eder.
//
// Alfabe ASCII'de artan sırada olduğu için kodlanmış dize, kodlanan baytlarla
// AYNI sözlüksel sırayı korur; zaman damgası başta olduğundan kimlikler
// oluşturma sırasına göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newID verilen önekle zaman sıralı ve tekil bir kimlik üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
// Zaman damgasının başta olması, kimliğin kendisinin kabaca oluşturma sırasını
// taşıması demektir; kayıtlar birincil anahtar indeksinde de doğal sırada durur
// ve rastgele UUID'lerin B-tree'de yarattığı dağınık yazma yükü oluşmaz.
//
// Kimlik ÜRETİMİ modülün kendisindedir (çekirdekteki üretici İMPORT EDİLMEZ):
// modül izolasyonu, ortak bir yardımcı paket kurma dürtüsüne direnmeyi gerektirir.
func newID(prefix string) string {
	return prefix + idEncoding.EncodeToString(idBytes(time.Now()))
}

// idBytes kimliğin 16 baytlık gövdesini üretir.
func idBytes(t time.Time) []byte {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// 1970 öncesi bir zaman damgası katalog için anlamlı değildir; sırayı
		// bozmamak için tabana çekilir.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	buf := make([]byte, 16)
	// UnixMilli 48 bite sığar; ilk iki bayt daima sıfırdır ve atılır.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read hata dönmez; yine de bir gün dönerse kimlik
		// nanosaniye çözünürlüğüne dayanır — tekillik zayıflar ama kayıt
		// açma başarısız olmaz.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}
	return buf
}

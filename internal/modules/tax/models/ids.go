package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8: önekli, zaman sıralı kimlikler).
//
// Önek, bir kimliğe bakıldığında hangi kayda ait olduğunu tek bakışta söyler ve
// yanlış türde bir kimlikle yapılan çağrıyı "bulunamadı" yerine açık bir
// doğrulama hatası hâline getirir.
//
// TaxRateRuleIDPrefix planın önek listesinde YOKTUR: liste Faz 7'nin üç
// modülünü birlikte sayar ve "prule_" promotion modülünün kural kaydına aittir.
// Vergi kuralı planda ayrıca adlandırılmadığı için burada "taxrule_" seçildi;
// oranın önekiyle ("taxrate_") karışmayacak kadar farklı, ama aynı aileden
// olduğu okunabilir.
const (
	// TaxRegionIDPrefix vergi bölgesi kimliklerinin önekidir.
	TaxRegionIDPrefix = "taxreg_"
	// TaxRateIDPrefix vergi oranı kimliklerinin önekidir.
	TaxRateIDPrefix = "taxrate_"
	// TaxRateRuleIDPrefix vergi oranı kuralı kimliklerinin önekidir.
	TaxRateRuleIDPrefix = "taxrule_"
)

// idBodyLen önek dışındaki gövdenin karakter sayısıdır: 16 bayt Crockford
// Base32 ile dolgusuz kodlandığında tam 26 karakter eder.
const idBodyLen = 26

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. Alfabe ASCII'de
// artan sırada olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel
// sırayı korur; kimlikler bu sayede zamana göre sıralanabilir kalır ve
// "ORDER BY id" doğal olarak oluşturma sırasını verir.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID verilen önekle zaman sıralı ve tekil bir kimlik üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
// Zaman damgasının başta olması, kimliğin kendisinin kabaca oluşturma sırasını
// taşıması demektir; vergi hesabındaki eşitlik bozma kuralı ("en eski oran
// kazanır") tam olarak bu sıraya dayanır.
//
// Diğer modüllerdeki üretici aynı yapıdadır; modül izolasyonu gereği o paketler
// import EDİLMEZ (Prensip 2.4, ADR 0001), üretici burada tekrar edilir.
func NewID(prefix string, t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// 1970 öncesi bir zaman damgası kayıt için anlamlı değildir;
		// sıralamayı bozmamak için tabana çekilir.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli 48 bite sığar; ilk iki bayt daima sıfırdır ve atılır.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read hata dönmez; yine de bir gün dönerse kimlik
		// yalnızca nanosaniye çözünürlüğüne dayanır — tekillik zayıflar ama
		// kayıt açma başarısız olmaz.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return prefix + idEncoding.EncodeToString(buf[:])
}

// NewTaxRegionID yeni bir vergi bölgesi kimliği üretir.
func NewTaxRegionID(t time.Time) string { return NewID(TaxRegionIDPrefix, t) }

// NewTaxRateID yeni bir vergi oranı kimliği üretir.
func NewTaxRateID(t time.Time) string { return NewID(TaxRateIDPrefix, t) }

// NewTaxRateRuleID yeni bir vergi oranı kuralı kimliği üretir.
func NewTaxRateRuleID(t time.Time) string { return NewID(TaxRateRuleIDPrefix, t) }

// IDBodyLength önek dışındaki gövde uzunluğunu döner; testler ve doğrulama
// için tek doğruluk kaynağıdır.
func IDBodyLength() int { return idBodyLen }

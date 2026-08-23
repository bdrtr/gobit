package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8: önekli, sıralanabilir kimlikler).
// Önek, bir kimliğe bakıldığında hangi kayda ait olduğunu tek bakışta söyler ve
// yanlış kimlikle yapılan bir çağrıyı log'da görünür kılar.
const (
	// PriceSetIDPrefix price set kimliklerinin önekidir.
	PriceSetIDPrefix = "pset_"
	// PriceIDPrefix fiyat kimliklerinin önekidir.
	PriceIDPrefix = "price_"
	// PriceListIDPrefix fiyat listesi kimliklerinin önekidir.
	PriceListIDPrefix = "plist_"
	// PriceRuleIDPrefix fiyat kuralı kimliklerinin önekidir.
	PriceRuleIDPrefix = "prule_"
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
// taşıması demektir.
//
// internal/core/workflow/pgstore'daki üretici aynı yapıdadır; modül izolasyonu
// gereği o paket import EDİLMEZ (Prensip 2.4), üretici burada tekrar edilir.
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

// NewPriceSetID yeni bir price set kimliği üretir.
func NewPriceSetID(t time.Time) string { return NewID(PriceSetIDPrefix, t) }

// NewPriceID yeni bir fiyat kimliği üretir.
func NewPriceID(t time.Time) string { return NewID(PriceIDPrefix, t) }

// NewPriceListID yeni bir fiyat listesi kimliği üretir.
func NewPriceListID(t time.Time) string { return NewID(PriceListIDPrefix, t) }

// NewPriceRuleID yeni bir fiyat kuralı kimliği üretir.
func NewPriceRuleID(t time.Time) string { return NewID(PriceRuleIDPrefix, t) }

// IDBodyLength önek dışındaki gövde uzunluğunu döner; testler ve doğrulama
// için tek doğruluk kaynağıdır.
func IDBodyLength() int { return idBodyLen }

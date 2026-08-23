package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8). Önek, bir kimliğin hangi varlığa ait
// olduğunu tabloya bakmadan okunabilir kılar: logda görülen "li_..." için
// hangi tabloya bakılacağı bellidir.
const (
	// CartIDPrefix sepet kimliklerinin önekidir.
	CartIDPrefix = "cart_"
	// LineItemIDPrefix sepet satırı kimliklerinin önekidir.
	LineItemIDPrefix = "li_"
	// AddressIDPrefix sepet adresi kimliklerinin önekidir.
	AddressIDPrefix = "addr_"
	// ShippingMethodIDPrefix kargo yöntemi kimliklerinin önekidir.
	//
	// Plan Bölüm 8 bu varlık için bir önek saymaz; "csm_" (cart shipping
	// method) burada seçilmiştir. "sm_" tercih edilmedi, çünkü Faz 7'de
	// fulfillment modülü kendi ShippingOption/ShippingProfile kayıtlarını
	// üretecek ve iki modülün önekleri logda birbirine karışırdı.
	ShippingMethodIDPrefix = "csm_"
)

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. 16 baytlık gövde
// bu kodlamayla tam 26 karaktere iner. Alfabe ASCII'de artan sırada olduğundan
// kodlanmış dize, kodlanan baytlarla aynı sözlüksel sırayı korur; kimlikler bu
// sayede zamana göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewCartID yeni bir sepet kimliği üretir.
func NewCartID() string { return newID(CartIDPrefix, time.Now()) }

// NewLineItemID yeni bir sepet satırı kimliği üretir.
func NewLineItemID() string { return newID(LineItemIDPrefix, time.Now()) }

// NewAddressID yeni bir sepet adresi kimliği üretir.
func NewAddressID() string { return newID(AddressIDPrefix, time.Now()) }

// NewShippingMethodID yeni bir kargo yöntemi kimliği üretir.
func NewShippingMethodID() string { return newID(ShippingMethodIDPrefix, time.Now()) }

// newID önekli, zaman sıralı ve tekil bir kimlik üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır. Zaman
// damgasının başta olması, kimliğin kendisinin kabaca oluşturma sırasını
// taşıması demektir; kayıtlar birincil anahtar taramasında da doğal sırada
// durur ve B-tree eklemeleri sona yapılır.
//
// Diğer modüllerdeki üretici ile aynı yapıdadır; o paketler İMPORT EDİLMEZ
// (ADR 0001), yapı burada modülün kendi kodu olarak tekrarlanır.
func newID(prefix string, t time.Time) string {
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

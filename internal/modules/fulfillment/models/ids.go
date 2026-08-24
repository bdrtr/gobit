package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8). Önek, bir kimliğin hangi varlığa ait
// olduğunu tabloya bakmadan okunabilir kılar: logda görülen "sopt_..." için
// şemayı açmak gerekmez.
const (
	// FulfillmentIDPrefix gönderi kimliklerinin önekidir.
	FulfillmentIDPrefix = "ful_"
	// ShippingOptionIDPrefix kargo seçeneği kimliklerinin önekidir.
	ShippingOptionIDPrefix = "sopt_"
	// ShippingProfileIDPrefix kargo profili kimliklerinin önekidir.
	ShippingProfileIDPrefix = "sprof_"
	// ShippingOptionRuleIDPrefix kargo seçeneği kuralı kimliklerinin önekidir.
	//
	// Plan Bölüm 8 bu varlık için bir önek saymaz. "prule_" ALINAMAZ: onu
	// pricing modülü fiyat kuralları için kullanıyor ve aynı öneki iki farklı
	// varlığa vermek, logdaki bir kimliğin hangi tabloya ait olduğunu belirsiz
	// bırakırdı. "sorule_" (shipping option rule) seçildi.
	ShippingOptionRuleIDPrefix = "sorule_"
	// FulfillmentItemIDPrefix gönderi kalemi kimliklerinin önekidir.
	//
	// Plan Bölüm 8 bu varlık için de bir önek saymaz; "fulitem_" seçildi ve
	// gönderi kimliğinin ("ful_") önekiyle karışmaması için tam sözcük
	// kullanıldı.
	FulfillmentItemIDPrefix = "fulitem_"
	// ManualShipmentIDPrefix manuel sağlayıcının kendi gönderi kimliklerinin
	// önekidir. Bu kimlik SAĞLAYICIYA aittir ve modülün gönderi kaydında
	// external_id olarak durur.
	ManualShipmentIDPrefix = "manful_"
)

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. 16 baytlık
// gövde bu kodlamayla tam 26 karaktere iner. Alfabe ASCII'de artan sırada
// olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel sırayı korur;
// kimlikler bu sayede zamana göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewFulfillmentID yeni bir gönderi kimliği üretir.
func NewFulfillmentID() string { return newID(FulfillmentIDPrefix, time.Now()) }

// NewShippingOptionID yeni bir kargo seçeneği kimliği üretir.
func NewShippingOptionID() string { return newID(ShippingOptionIDPrefix, time.Now()) }

// NewShippingProfileID yeni bir kargo profili kimliği üretir.
func NewShippingProfileID() string { return newID(ShippingProfileIDPrefix, time.Now()) }

// NewShippingOptionRuleID yeni bir kargo seçeneği kuralı kimliği üretir.
func NewShippingOptionRuleID() string { return newID(ShippingOptionRuleIDPrefix, time.Now()) }

// NewFulfillmentItemID yeni bir gönderi kalemi kimliği üretir.
func NewFulfillmentItemID() string { return newID(FulfillmentItemIDPrefix, time.Now()) }

// NewManualShipmentID manuel sağlayıcı için yeni bir gönderi kimliği üretir.
func NewManualShipmentID() string { return newID(ManualShipmentIDPrefix, time.Now()) }

// newID önekli, zaman sıralı ve tekil bir kimlik üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
// Zaman damgasının başta olması, kimliğin kendisinin kabaca oluşturma sırasını
// taşıması demektir; kayıtlar birincil anahtar taramasında da doğal sırada
// durur ve B-tree eklemeleri sona yapılır.
//
// Diğer modüllerdeki üretici ile aynı yapıdadır; o paketler İMPORT EDİLMEZ
// (Prensip 2.4), yapı burada modülün kendi kodu olarak tekrarlanır.
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

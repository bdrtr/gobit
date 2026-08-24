package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8). Önek, bir kimliğin hangi varlığa ait
// olduğunu tabloya bakmadan okunabilir kılar: logda görülen "oli_..." için
// hangi tabloya bakılacağı bellidir.
const (
	// OrderIDPrefix sipariş kimliklerinin önekidir.
	OrderIDPrefix = "order_"
	// LineItemIDPrefix sipariş satırı kimliklerinin önekidir.
	LineItemIDPrefix = "oli_"
	// SummaryIDPrefix sipariş özeti kimliklerinin önekidir.
	//
	// Plan Bölüm 8 bu varlık için bir önek saymaz; "osum_" (order summary)
	// burada seçilmiştir. "sum_" tercih edilmedi, çünkü önek yalnız başına
	// hangi modülün kaydı olduğunu söylemezdi.
	SummaryIDPrefix = "osum_"
	// ReturnIDPrefix iade kimliklerinin önekidir.
	ReturnIDPrefix = "ret_"
	// ExchangeIDPrefix değişim kimliklerinin önekidir.
	ExchangeIDPrefix = "exch_"
	// ClaimIDPrefix hasar kaydı kimliklerinin önekidir.
	ClaimIDPrefix = "claim_"
)

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. 16 baytlık gövde
// bu kodlamayla tam 26 karaktere iner. Alfabe ASCII'de artan sırada olduğundan
// kodlanmış dize, kodlanan baytlarla aynı sözlüksel sırayı korur; kimlikler bu
// sayede zamana göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// IDBodyLen önekten sonraki gövdenin karakter sayısıdır.
//
// Dışa açıktır çünkü kimliğin BİÇİMİ bir sözleşmedir: sipariş kimliği logda,
// destek kaydında ve saga'nın anlık görüntüsünde taşınır. Testler biçimi bu
// sabitle doğrular.
const IDBodyLen = 26

// NewOrderID yeni bir sipariş kimliği üretir.
func NewOrderID() string { return newID(OrderIDPrefix, time.Now()) }

// NewLineItemID yeni bir sipariş satırı kimliği üretir.
func NewLineItemID() string { return newID(LineItemIDPrefix, time.Now()) }

// NewSummaryID yeni bir sipariş özeti kimliği üretir.
func NewSummaryID() string { return newID(SummaryIDPrefix, time.Now()) }

// NewReturnID yeni bir iade kimliği üretir.
func NewReturnID() string { return newID(ReturnIDPrefix, time.Now()) }

// NewExchangeID yeni bir değişim kimliği üretir.
func NewExchangeID() string { return newID(ExchangeIDPrefix, time.Now()) }

// NewClaimID yeni bir hasar kaydı kimliği üretir.
func NewClaimID() string { return newID(ClaimIDPrefix, time.Now()) }

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

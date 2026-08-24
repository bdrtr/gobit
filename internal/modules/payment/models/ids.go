package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8). Önek, bir kimliğin hangi varlığa ait
// olduğunu tabloya bakmadan okunabilir kılar: logda görülen "payses_..." için
// şemayı açmak gerekmez.
const (
	// PaymentCollectionIDPrefix ödeme koleksiyonu kimliklerinin önekidir.
	PaymentCollectionIDPrefix = "paycol_"
	// PaymentSessionIDPrefix ödeme oturumu kimliklerinin önekidir.
	PaymentSessionIDPrefix = "payses_"
	// PaymentIDPrefix tahsilat kimliklerinin önekidir.
	PaymentIDPrefix = "pay_"
	// RefundIDPrefix iade kimliklerinin önekidir.
	RefundIDPrefix = "refund_"
	// ManualSessionIDPrefix manuel sağlayıcının kendi oturum kimliklerinin
	// önekidir. Bu kimlik SAĞLAYICIYA aittir ve modülün oturum kaydında
	// external_id olarak durur.
	ManualSessionIDPrefix = "manses_"
)

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. 16 baytlık
// gövde bu kodlamayla tam 26 karaktere iner. Alfabe ASCII'de artan sırada
// olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel sırayı korur;
// kimlikler bu sayede zamana göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewPaymentCollectionID yeni bir ödeme koleksiyonu kimliği üretir.
func NewPaymentCollectionID() string { return newID(PaymentCollectionIDPrefix, time.Now()) }

// NewPaymentSessionID yeni bir ödeme oturumu kimliği üretir.
func NewPaymentSessionID() string { return newID(PaymentSessionIDPrefix, time.Now()) }

// NewPaymentID yeni bir tahsilat kimliği üretir.
func NewPaymentID() string { return newID(PaymentIDPrefix, time.Now()) }

// NewRefundID yeni bir iade kimliği üretir.
func NewRefundID() string { return newID(RefundIDPrefix, time.Now()) }

// NewManualSessionID manuel sağlayıcı için yeni bir oturum kimliği üretir.
func NewManualSessionID() string { return newID(ManualSessionIDPrefix, time.Now()) }

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

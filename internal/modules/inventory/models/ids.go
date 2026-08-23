package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// Kimlik önekleri (plan Bölüm 8). Önek, bir kimliğin hangi varlığa ait
// olduğunu taşımadan da olsa okunabilir kılar: logda görülen "invres_..."
// için tabloya bakmak gerekmez.
const (
	// StockLocationIDPrefix stok lokasyonu kimliklerinin önekidir.
	StockLocationIDPrefix = "sloc_"
	// InventoryItemIDPrefix stok kalemi kimliklerinin önekidir.
	InventoryItemIDPrefix = "invitem_"
	// InventoryLevelIDPrefix stok seviyesi kimliklerinin önekidir.
	InventoryLevelIDPrefix = "invlevel_"
	// ReservationIDPrefix rezervasyon kimliklerinin önekidir.
	ReservationIDPrefix = "invres_"
)

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. 16 baytlık
// gövde bu kodlamayla tam 26 karaktere iner. Alfabe ASCII'de
// artan sırada olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel
// sırayı korur; kimlikler bu sayede zamana göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewStockLocationID yeni bir stok lokasyonu kimliği üretir.
func NewStockLocationID() string { return newID(StockLocationIDPrefix, time.Now()) }

// NewInventoryItemID yeni bir stok kalemi kimliği üretir.
func NewInventoryItemID() string { return newID(InventoryItemIDPrefix, time.Now()) }

// NewInventoryLevelID yeni bir stok seviyesi kimliği üretir.
func NewInventoryLevelID() string { return newID(InventoryLevelIDPrefix, time.Now()) }

// NewReservationID yeni bir rezervasyon kimliği üretir.
func NewReservationID() string { return newID(ReservationIDPrefix, time.Now()) }

// newID önekli, zaman sıralı ve tekil bir kimlik üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
// Zaman damgasının başta olması, kimliğin kendisinin kabaca oluşturma sırasını
// taşıması demektir; kayıtlar birincil anahtar taramasında da doğal sırada
// durur ve B-tree eklemeleri sona yapılır.
//
// internal/core/workflow/pgstore/ids.go'daki üretici ile aynı yapıdadır; o
// paket İMPORT EDİLMEZ (çekirdeğin özel yüzeyi değildir), yapı burada modülün
// kendi kodu olarak tekrarlanır.
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

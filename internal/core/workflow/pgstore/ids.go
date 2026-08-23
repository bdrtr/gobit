package pgstore

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// idPrefix üretilen yürütme kimliklerinin önekidir (plan Bölüm 8: prefix'li
// kimlikler). "wfx" = workflow execution.
const idPrefix = "wfx_"

// idBodyLen önek dışındaki gövdenin karakter sayısıdır: 16 bayt Crockford
// Base32 ile dolgusuz kodlandığında tam 26 karakter eder.
const idBodyLen = 26

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. Alfabe ASCII'de
// artan sırada olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel
// sırayı korur; kimlikler bu sayede zamana göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newExecutionID zaman sıralı ve tekil bir yürütme kimliği üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır ve
// "wfx_" önekini alır. Zaman damgasının başta olması, kimliğin kendisinin
// kabaca oluşturma sırasını taşıması demektir; yürütmeler indeks taramasında
// da doğal sırada durur.
//
// Kimlik ÇAĞIRAN tarafından da verilebilir: Create, boş olmayan bir ID'yi
// olduğu gibi kullanır ve yalnızca boş bırakıldığında bunu çağırır.
func newExecutionID(t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// 1970 öncesi bir zaman damgası yürütme için anlamlı değildir;
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

	return idPrefix + idEncoding.EncodeToString(buf[:])
}

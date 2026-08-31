// Package models notification modülünün alan modelleridir.
//
// Tipler yalnızca VERİYİ ve onun kendi içindeki tutarlılığını taşır; hiçbir
// veritabanı ya da HTTP ayrıntısı burada bilinmez.
package models

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"time"
)

// DeliveryIDPrefix teslim günlüğü kayıtlarının kimlik önekidir
// (plan Bölüm 8: önekli, zaman sıralı kimlikler).
//
// Önek, bir kimliğe bakıldığında hangi kayda ait olduğunu tek bakışta söyler
// ve yanlış türde bir kimlikle yapılan çağrıyı "bulunamadı" yerine açık bir
// doğrulama hatası hâline getirir.
const DeliveryIDPrefix = "notif_"

// idBodyLen önek dışındaki gövdenin karakter sayısıdır: 16 bayt Crockford
// Base32 ile dolgusuz kodlandığında tam 26 karakter eder.
const idBodyLen = 26

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. Alfabe ASCII'de
// artan sırada olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel
// sırayı korur; kimlikler bu sayede zamana göre sıralanabilir kalır ve
// "ORDER BY created_at DESC, id DESC" eşit damgalarda da kararlı çalışır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID verilen önekle zaman sıralı ve tekil bir kimlik üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
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

// NewDeliveryID yeni bir teslim günlüğü kimliği üretir.
func NewDeliveryID(t time.Time) string { return NewID(DeliveryIDPrefix, t) }

// IDBodyLength önek dışındaki gövde uzunluğunu döner; testler ve doğrulama
// için tek doğruluk kaynağıdır.
func IDBodyLength() int { return idBodyLen }

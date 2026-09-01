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
//
// # "prule_" öneki pricing ile ORTAKTIR
//
// pricing modülünün PriceRule kayıtları da "prule_" önekini kullanır. Çakışma
// bilinçlidir: plan Bölüm 8 promosyon kuralı için bu öneki adlandırır ve iki
// modül birbirinin kimliğini hiçbir zaman görmez (Prensip 2.1 — modüller
// birbirinin verisine erişemez). Önek bu yüzden modüller arası bir ayraç değil,
// modül İÇİNDE tür ayracıdır: bir fiyat kuralı kimliği promosyon modülüne
// verilirse doğrulamayı geçer ama okuma errors.NotFound döner.
const (
	// CampaignIDPrefix kampanya kimliklerinin önekidir.
	CampaignIDPrefix = "camp_"
	// PromotionIDPrefix promosyon kimliklerinin önekidir.
	PromotionIDPrefix = "promo_"
	// PromotionRuleIDPrefix promosyon kuralı kimliklerinin önekidir.
	PromotionRuleIDPrefix = "prule_"
	// ApplicationMethodIDPrefix uygulama yöntemi kimliklerinin önekidir.
	//
	// Plan Bölüm 8 bu kayıt için bir önek adlandırmaz; "appm_" burada seçilmiştir
	// ve depodaki hiçbir önekle çakışmaz.
	ApplicationMethodIDPrefix = "appm_"
	// RedemptionIDPrefix kupon kullanım kaydının önekidir.
	//
	// Plan Bölüm 8 bu kayıt için de bir önek adlandırmaz; kullanım sayacının
	// defteri bu fazda ortaya çıkmıştır (bkz. [Redemption]).
	RedemptionIDPrefix = "predeem_"
)

// idBodyLen önek dışındaki gövdenin karakter sayısıdır: 16 bayt Crockford
// Base32 ile dolgusuz kodlandığında tam 26 karakter eder.
const idBodyLen = 26

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. Alfabe ASCII'de
// artan sırada olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel
// sırayı korur; kimlikler bu sayede zamana göre sıralanabilir kalır ve
// "ORDER BY id" doğal olarak oluşturma sırasını verir.
//
// Sıralanabilirlik bu modülde bir SÜSLEME DEĞİLDİR:
// [github.com/bdrtr/gobit/internal/modules/promotion/service.ComputeResult]
// içindeki uygulama sırası kimliğe göre belirlenir (bkz. service paketindeki
// sıralama kuralı) ve "önce yazılan promosyon önce uygulanır" iddiası ancak
// kimliğin zaman sırasını taşımasıyla anlamlıdır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewID verilen önekle zaman sıralı ve tekil bir kimlik üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
//
// internal/core/workflow/pgstore'daki ve diğer modüllerdeki üreticiler aynı
// yapıdadır; modül izolasyonu gereği o paketler import EDİLMEZ (Prensip 2.4),
// üretici burada tekrar edilir.
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

// NewCampaignID yeni bir kampanya kimliği üretir.
func NewCampaignID(t time.Time) string { return NewID(CampaignIDPrefix, t) }

// NewPromotionID yeni bir promosyon kimliği üretir.
func NewPromotionID(t time.Time) string { return NewID(PromotionIDPrefix, t) }

// NewPromotionRuleID yeni bir promosyon kuralı kimliği üretir.
func NewPromotionRuleID(t time.Time) string { return NewID(PromotionRuleIDPrefix, t) }

// NewApplicationMethodID yeni bir uygulama yöntemi kimliği üretir.
func NewApplicationMethodID(t time.Time) string { return NewID(ApplicationMethodIDPrefix, t) }

// NewRedemptionID yeni bir kullanım kaydı kimliği üretir.
func NewRedemptionID(t time.Time) string { return NewID(RedemptionIDPrefix, t) }

// IDBodyLength önek dışındaki gövde uzunluğunu döner; testler ve doğrulama
// için tek doğruluk kaynağıdır.
func IDBodyLength() int { return idBodyLen }

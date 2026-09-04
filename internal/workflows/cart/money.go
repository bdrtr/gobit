package cart

import (
	"math"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Tutar, adet ve oran sınırları.
//
// Sınırlar cart ve pricing modüllerindekilerle bilinçli olarak AYNIDIR; iki
// taraf birbirini import etmediği için değerler burada tekrarlanır (ADR 0001'in
// kabul edilen bedeli). Aynı olmaları şart değil, YETERLİ olmaları şarttır:
// buradaki tavan modülünkinden büyük olsaydı, bu paketin geçirdiği bir tutar
// modülde reddedilir ve hata hesabın sonunda çıkardı.
//
// Sınırlar keyfi değildir: satır ara toplamı birim fiyat × adettir ve bu çarpım
// int64'e SIĞMALIDIR. MaxAmount × MaxQuantity = 10^12 × 10^6 = 10^18 <
// 9.22 × 10^18 olduğu için taşma yapısal olarak imkânsızdır.
const (
	// MinQuantity bir satırın en küçük adedidir.
	MinQuantity int64 = 1
	// MaxQuantity bir satırın en büyük adedidir.
	MaxQuantity int64 = 1_000_000
	// MaxAmount izin verilen en büyük birim tutardır (minor unit).
	MaxAmount int64 = 1_000_000_000_000
	// MaxTotal bir toplam alanının en büyük değeridir (minor unit).
	MaxTotal = MaxAmount * MaxQuantity
	// BpsScale baz puan ölçeğidir: 10000 baz puan = %100.
	BpsScale int64 = 10_000
	// MaxTaxRateBps izin verilen en büyük vergi oranıdır (%100).
	MaxTaxRateBps int32 = 10_000
	// maxIDLen dışarıdan gelen kimlikler için üst sınırdır; core/link ve cart
	// modülü de aynı sınırı uygular.
	maxIDLen = 255
)

// addAmount iki tutarı TAŞMADAN toplar.
//
// Toplam [MaxTotal]'ı aşarsa hata döner. Taşan bir toplama sessizce NEGATİF bir
// tutar üretir ve negatif toplam, cart'ın tutarlılık kontrolünü yanlışlıkla
// geçebilirdi.
func addAmount(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Internal(CodeAmountOverflow,
			"tutarlar negatif olamaz: %d + %d", a, b)
	}
	if a > MaxTotal-b {
		return 0, errors.Invalid(CodeAmountOverflow,
			"tutar toplamı sınırı aşıyor: %d + %d > %d", a, b, MaxTotal)
	}
	return a + b, nil
}

// mulAmount birim fiyatı adetle TAŞMADAN çarpar.
func mulAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice < 0 || quantity < 0 {
		return 0, errors.Internal(CodeAmountOverflow,
			"the unit price and the quantity cannot be negative: %d x %d", unitPrice, quantity)
	}
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity > MaxTotal/unitPrice {
		return 0, errors.Invalid(CodeAmountOverflow,
			"the line subtotal exceeds the limit: %d x %d > %d", unitPrice, quantity, MaxTotal)
	}
	return unitPrice * quantity, nil
}

// taxOf verilen taban üzerinden baz puan oranıyla vergiyi hesaplar.
//
// Sonuç AŞAĞI yuvarlanır; gerekçesi ve verginin tabanının ne olduğu paket
// yorumundaki "Vergi sözleşmesi" başlığındadır.
//
// # Neden önce bölünüyor
//
// Doğrudan base × rate hesabı taşardı: taban en fazla [MaxTotal] (10^18) ve
// oran en fazla 10^4 olduğu için çarpım 10^22'ye kadar çıkar, int64 ise
// 9.22 × 10^18'de biter. Bölmeyi öne almak sonucu DEĞİŞTİRMEZ — base = q ×
// 10000 + r yazılırsa base × rate / 10000 = q × rate + (r × rate) / 10000'dir
// ve q × rate zaten tam sayı olduğu için aşağı yuvarlama yalnızca ikinci terime
// düşer. Her iki terim de int64'e rahatça sığar (q × rate ≤ 10^18,
// r × rate < 10^8).
func taxOf(base int64, rateBps int32) (int64, error) {
	if base < 0 {
		return 0, errors.Internal(CodeAmountOverflow, "vergi tabanı negatif olamaz: %d", base)
	}
	if base > MaxTotal {
		return 0, errors.Invalid(CodeAmountOverflow,
			"vergi tabanı sınırı aşıyor: %d > %d", base, MaxTotal)
	}
	if rateBps < 0 || rateBps > MaxTaxRateBps {
		return 0, errors.Internal(CodeTaxRateInvalid,
			"vergi oranı [0, %d] baz puan aralığında olmalı, %d bildirildi", MaxTaxRateBps, rateBps)
	}
	if base == 0 || rateBps == 0 {
		return 0, nil
	}

	rate := int64(rateBps)
	whole := (base / BpsScale) * rate
	remainder := ((base % BpsScale) * rate) / BpsScale
	return whole + remainder, nil
}

// quantity32 sepet adedini pricing'in beklediği int32'ye çevirir.
//
// Çevrim ancak sınır denetiminden SONRA yapılır: [MaxQuantity] (10^6)
// int32'nin tavanının çok altındadır, dolayısıyla denetimi geçen her değer
// kayıpsız sığar. Denetimsiz bir çevrim, milyarlık bir adedi sessizce küçük
// (hatta negatif) bir sayıya indirir ve fiyat kademesini yanlış seçerdi.
func quantity32(quantity int64) (int32, error) {
	if quantity < MinQuantity || quantity > MaxQuantity {
		return 0, errors.Invalid(CodeInvalidInput,
			"adet [%d, %d] aralığında olmalı, %d verildi", MinQuantity, MaxQuantity, quantity)
	}
	if quantity > math.MaxInt32 {
		// Ulaşılamaz: MaxQuantity zaten çok daha küçüktür. Denetim, sabitin
		// ileride büyütülmesi hâlinde çevrimin sessizce bozulmamasını sağlar.
		return 0, errors.Internal(CodeAmountOverflow,
			"adet int32'ye sığmıyor: %d", quantity)
	}
	return int32(quantity), nil
}

// checkAmount bir tutarın izin verilen aralıkta olduğunu doğrular.
func checkAmount(label string, value, upper int64) error {
	if value < 0 {
		return errors.Invalid(CodeAmountOverflow, "%s negatif olamaz: %d", label, value)
	}
	if value > upper {
		return errors.Invalid(CodeAmountOverflow,
			"%s can be at most %d: %d", label, upper, value)
	}
	return nil
}

// requireID dışarıdan gelen bir kimliğin kullanılabilir olduğunu doğrular.
//
// Kimlik KIRPILMAZ, reddedilir: kırpma çağıranın gönderdiği kimlikle saklanan
// kimliği ayırır ve fark ancak veri bozulduktan sonra görünür. Aynı sözleşme
// core/link ve cart modülünde de geçerlidir.
func requireID(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	if strings.TrimSpace(value) != value {
		return errors.Invalid(CodeInvalidInput, "%s cannot contain leading/trailing whitespace: %q", label, value)
	}
	if len(value) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxIDLen, len(value))
	}
	return nil
}

package checkout

import (
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Tutar ve adet sınırları.
//
// Sınırlar cart, order ve payment modüllerindekilerle bilinçli olarak
// UYUMLUDUR; taraflar birbirini import etmediği için değerler burada
// tekrarlanır (ADR 0001'in kabul edilen bedeli). Aynı olmaları şart değil,
// YETERLİ olmaları şarttır: buradaki tavan modülünkinden büyük olsaydı, bu
// paketin geçirdiği bir tutar modülde reddedilir ve hata ancak stok
// ayrıldıktan sonra çıkardı.
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
	// maxIDLen dışarıdan gelen kimlikler için üst sınırdır; core/link, cart ve
	// order modülleri de aynı sınırı uygular.
	maxIDLen = 255
)

// MaxCartIDLen bu akışın kabul ettiği en uzun sepet kimliğidir.
//
// Sınır idempotency anahtarından gelir: anahtar [IdempotencyKeyPrefix] ile
// sepet kimliğinin birleşimidir ve motor onu MaxIdempotencyKeyLen baytla
// sınırlar. Kimliği burada reddetmek, hiçbir yan etki uygulanmadan ve anlaşılır
// bir mesajla dönmeyi sağlar; sınırın motorda yakalanması ise "idempotency
// anahtarı çok uzun" gibi, çağıranın gönderdiği alanla ilgisi kurulamayan bir
// hata üretirdi.
const MaxCartIDLen = workflow.MaxIdempotencyKeyLen - len(IdempotencyKeyPrefix)

// requireID dışarıdan gelen bir kimliğin kullanılabilir olduğunu doğrular.
//
// Kimlik KIRPILMAZ, reddedilir: kırpma çağıranın gönderdiği kimlikle saklanan
// kimliği ayırır ve fark ancak veri bozulduktan sonra görünür. Aynı sözleşme
// core/link, cart ve order modüllerinde de geçerlidir.
func requireID(label, value string, upper int) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s boş olamaz", label)
	}
	if strings.TrimSpace(value) != value {
		return errors.Invalid(CodeInvalidInput, "%s baş/son boşluk içeremez: %q", label, value)
	}
	if len(value) > upper {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d bayt olabilir: %d", label, upper, len(value))
	}
	return nil
}

// checkAmount bir tutarın izin verilen aralıkta olduğunu doğrular.
func checkAmount(label string, value, upper int64) error {
	if value < 0 {
		return errors.Internal(CodeAmountInvalid, "%s negatif olamaz: %d", label, value)
	}
	if value > upper {
		return errors.Internal(CodeAmountInvalid,
			"%s en fazla %d olabilir: %d", label, upper, value)
	}
	return nil
}

// mulAmount birim fiyatı adetle TAŞMADAN çarpar.
//
// Çarpım yalnızca hesabın DOĞRULANMASI için yapılır; tutarı bu paket üretmez.
func mulAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice < 0 || quantity < 0 {
		return 0, errors.Internal(CodeAmountInvalid,
			"birim fiyat ve adet negatif olamaz: %d × %d", unitPrice, quantity)
	}
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity > MaxTotal/unitPrice {
		return 0, errors.Internal(CodeAmountInvalid,
			"satır ara toplamı sınırı aşıyor: %d × %d > %d", unitPrice, quantity, MaxTotal)
	}
	return unitPrice * quantity, nil
}

// addAmount iki tutarı TAŞMADAN toplar.
func addAmount(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Internal(CodeAmountInvalid, "tutarlar negatif olamaz: %d + %d", a, b)
	}
	if a > MaxTotal-b {
		return 0, errors.Internal(CodeAmountInvalid,
			"tutar toplamı sınırı aşıyor: %d + %d > %d", a, b, MaxTotal)
	}
	return a + b, nil
}

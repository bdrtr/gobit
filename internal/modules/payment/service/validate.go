package service

import (
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// currencyCodeLength ISO 4217 kodunun harf sayısıdır.
const currencyCodeLength = 3

// requireText zorunlu bir metin alanını doğrular.
func requireText(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen metin alanının uzunluk sınırını doğrular.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxTextLen, len(value))
	}
	return nil
}

// normalizeCurrency para birimi kodunu doğrular ve BÜYÜK harfe çevirir.
//
// Kod her yerde büyük harf saklanır; aksi hâlde "try" ve "TRY" iki farklı para
// birimi gibi davranır ve tutarlar sessizce ayrışırdı.
func normalizeCurrency(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if len(normalized) != currencyCodeLength {
		return "", errors.Invalid(CodeInvalidInput,
			"para birimi üç harfli ISO 4217 kodu olmalı: %q", code)
	}
	for _, r := range normalized {
		if r < 'A' || r > 'Z' {
			return "", errors.Invalid(CodeInvalidInput,
				"para birimi yalnızca harf içerebilir: %q", code)
		}
	}
	return normalized, nil
}

// requireAmount bir tutarın izin verilen aralıkta olduğunu doğrular.
//
// Üst sınır keyfi değildir: koleksiyonun bloke, tahsil ve iade tutarları aynı
// tavana tabidir ve toplamları int64'e sığmalıdır (bkz. [models.MaxAmount]).
// Sınırsız bir tutar, toplama sırasında sessizce negatife sarabilirdi.
func requireAmount(label string, amount int64) error {
	if amount < models.MinAmount || amount > models.MaxAmount {
		return errors.Invalid(CodeInvalidInput,
			"%s %d ile %d arasında olmalı: %d", label, models.MinAmount, models.MaxAmount, amount)
	}
	return nil
}

// invalidStatus tanınmayan bir koleksiyon durumu için ortak hatayı üretir.
func invalidStatus(value string) error {
	return errors.Invalid(CodeInvalidInput,
		"%q tanınmayan bir ödeme koleksiyonu durumu; geçerli olanlar: %s",
		value, strings.Join(collectionStatusNames(), ", "))
}

// collectionStatusNames geçerli koleksiyon durumlarını sabit sırada döner.
//
// Sıra bilinçli olarak sabittir ve ödemenin yaşam döngüsünü izler; hata
// mesajının okunması, alfabetik ya da rastgele bir listeden kolaydır.
func collectionStatusNames() []string {
	return []string{
		models.CollectionNotPaid.String(),
		models.CollectionAwaiting.String(),
		models.CollectionAuthorized.String(),
		models.CollectionPartiallyCaptured.String(),
		models.CollectionCaptured.String(),
		models.CollectionPartiallyRefunded.String(),
		models.CollectionRefunded.String(),
		models.CollectionCanceled.String(),
	}
}

// requireOptionalAmount sıfırın "belirtilmedi" anlamına geldiği bir tutarı
// doğrular.
//
// Sıfır, sağlayıcı sözleşmesinde de "tamamı" demektir (bkz.
// internal/core/provider: Capture ve Refund). Aynı anlam servis yüzeyinde de
// korunur ki çağıran iki farklı sıfır kuralı öğrenmek zorunda kalmasın.
func requireOptionalAmount(label string, amount int64) error {
	if amount == 0 {
		return nil
	}
	if amount < 0 {
		return errors.Invalid(CodeInvalidInput, "%s negatif olamaz: %d", label, amount)
	}
	return requireAmount(label, amount)
}

package manual

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// currencyCodeLength ISO 4217 kodunun harf sayısıdır.
const currencyCodeLength = 3

// shipmentData sağlayıcının davranışını yönlendiren Data alanlarıdır.
//
// Tanınmayan alanlar YOK SAYILIR: Data, çağıranın sağlayıcıya ilettiği serbest
// veridir (adres, şube, kalem listesi) ve sağlayıcının anlamadığı bir alan
// hata değildir.
//
// Tutar alanları İŞARETÇİDİR: sıfır tutarlı bir bileşen ile "alan hiç
// verilmedi" ayrımı korunmalıdır. Değer tipi kullanılsaydı ikisi ayırt
// edilemez ve [DataKeyQuoteAmount] ile açıkça sıfır (ücretsiz) fiyat
// verilemezdi.
type shipmentData struct {
	Outcome           string `json:"manual_outcome"`
	QuoteAmount       *int64 `json:"manual_quote_amount"`
	BaseAmount        *int64 `json:"manual_base_amount"`
	PerItemAmount     *int64 `json:"manual_per_item_amount"`
	PerKilogramAmount *int64 `json:"manual_per_kilogram_amount"`
	TrackingNumber    string `json:"manual_tracking_number"`
	TrackingURL       string `json:"manual_tracking_url"`
}

// parseData serbest veriden davranış ve fiyat anahtarlarını çözer.
//
// Girdi haritadır (çekirdek sözleşmesindeki Data alanı); JSON'a kodlanıp geri
// çözülür. Ara adım, aynı çözümlemenin hem haritadan hem defterde saklanan ham
// gövdeden yapılabilmesini sağlar ve iki yolun ayrışmasını engeller.
func parseData(data map[string]any) (shipmentData, error) {
	if len(data) == 0 {
		return shipmentData{}, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return shipmentData{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"gönderi verisi kodlanamadı")
	}
	return parseRawData(raw)
}

// parseRawData ham JSON gövdesinden davranış ve fiyat anahtarlarını çözer.
func parseRawData(raw []byte) (shipmentData, error) {
	var out shipmentData
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return shipmentData{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"gönderi verisi çözümlenemedi")
	}

	out.Outcome = strings.TrimSpace(out.Outcome)
	switch out.Outcome {
	case "", OutcomeOK, OutcomeError:
	default:
		return shipmentData{}, errors.Invalid(CodeInvalidInput,
			"%q tanınmayan bir %s değeri; %q ya da %q olmalı",
			out.Outcome, DataKeyOutcome, OutcomeOK, OutcomeError)
	}

	for _, field := range []struct {
		key   string
		value *int64
	}{
		{DataKeyQuoteAmount, out.QuoteAmount},
		{DataKeyBaseAmount, out.BaseAmount},
		{DataKeyPerItemAmount, out.PerItemAmount},
		{DataKeyPerKilogramAmount, out.PerKilogramAmount},
	} {
		if field.value == nil {
			continue
		}
		if *field.value < models.MinAmount || *field.value > models.MaxAmount {
			return shipmentData{}, errors.Invalid(CodeInvalidInput,
				"%s %d ile %d arasında olmalı: %d",
				field.key, models.MinAmount, models.MaxAmount, *field.value)
		}
	}
	return out, nil
}

// quoteAmount fiyat bileşenlerinden ücreti hesaplar.
//
// SAF fonksiyondur: veritabanına, saate ve loglamaya dokunmaz. Formülün her
// dalı bu yüzden tek tek sınanabilir (bkz. [Provider.Quote] godoc'u).
//
// Hesap TAM SAYI aritmetiğiyle yapılır ve her adımda TAŞMA denetlenir. Denetim
// gereklidir: kalem adedi ve ağırlık dışarıdan gelir, ve taşan bir çarpım
// sessizce NEGATİF bir kargo ücreti üretirdi — yani müşteriye para ödeyen bir
// sipariş.
//
// Denetim ARA ADIMLARI da kapsar: kilograma yuvarlama önce toplama yapılıp
// sonra bölünseydi (totalWeight + 999) TAŞARDI ve elde edilen NEGATİF kilogram,
// [mulChecked]'in yalnızca pozitif operanda bakan denetiminden geçip sonucu
// negatife çevirirdi. Bu yüzden yuvarlama taşmasız yazılır (önce böl, kalan
// varsa bir artır) ve iki yardımcı negatif operandı AÇIKÇA reddeder.
//
// Sonuç [models.MinAmount] ile [models.MaxAmount] arasında değilse hata döner;
// alt sınır denetimi de gereklidir, çünkü yalnızca üst sınıra bakan bir kontrol
// negatif bir toplamı sessizce geçirirdi.
func quoteAmount(config shipmentData, itemCount, totalWeight int64) (int64, error) {
	if config.QuoteAmount != nil {
		return *config.QuoteAmount, nil
	}

	total := valueOrZero(config.BaseAmount)

	perItem := valueOrZero(config.PerItemAmount)
	if perItem > 0 && itemCount > 0 {
		part, err := mulChecked(perItem, itemCount, DataKeyPerItemAmount)
		if err != nil {
			return 0, err
		}
		total, err = addChecked(total, part)
		if err != nil {
			return 0, err
		}
	}

	perKilogram := valueOrZero(config.PerKilogramAmount)
	if perKilogram > 0 && totalWeight > 0 {
		// Başlanan her kilogram ücretlendirilir; yön YUKARIDIR ve gerekçesi
		// [Provider.Quote] godoc'undadır. Yuvarlama önce BÖLER, kalan varsa
		// bir artırır: (totalWeight + gramsPerKilogram - 1) biçimi, ağırlık
		// int64'ün tepesine yakınken TAŞAR ve negatif bir kilogram üretirdi.
		kilograms := totalWeight / gramsPerKilogram
		if totalWeight%gramsPerKilogram != 0 {
			kilograms++
		}
		part, err := mulChecked(perKilogram, kilograms, DataKeyPerKilogramAmount)
		if err != nil {
			return 0, err
		}
		total, err = addChecked(total, part)
		if err != nil {
			return 0, err
		}
	}

	if total < models.MinAmount || total > models.MaxAmount {
		return 0, errors.Invalid(CodeInvalidInput,
			"hesaplanan kargo ücreti %d ile %d arasında olmalı: %d",
			models.MinAmount, models.MaxAmount, total)
	}
	return total, nil
}

// valueOrZero işaretçi bir tutarı değere çevirir; nil sıfırdır.
func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// mulChecked iki NEGATİF OLMAYAN tam sayıyı taşmayı denetleyerek çarpar.
//
// Negatif operand AÇIKÇA reddedilir. Denetim yalnızca "b > MaxInt64/a"ya
// baksaydı negatif bir b onu koşulsuz geçer ve çarpım negatif bir ücret
// üretirdi; bu, godoc'un "iki pozitif tam sayı" varsayımını kodun kendisinin
// zorlamaması demekti.
func mulChecked(a, b int64, label string) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"%s çarpımının iki tarafı da negatif olamaz: %d × %d", label, a, b)
	}
	if a != 0 && b > math.MaxInt64/a {
		return 0, errors.Invalid(CodeInvalidInput,
			"%s çarpımı taşıyor: %d × %d", label, a, b)
	}
	return a * b, nil
}

// addChecked iki NEGATİF OLMAYAN tam sayıyı taşmayı denetleyerek toplar.
//
// Negatif operand AÇIKÇA reddedilir; gerekçe [mulChecked] ile aynıdır.
func addChecked(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"kargo ücreti toplamının iki tarafı da negatif olamaz: %d + %d", a, b)
	}
	if b > math.MaxInt64-a {
		return 0, errors.Invalid(CodeInvalidInput,
			"kargo ücreti toplamı taşıyor: %d + %d", a, b)
	}
	return a + b, nil
}

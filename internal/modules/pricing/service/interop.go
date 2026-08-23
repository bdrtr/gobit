package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// Bu dosya pricing'in MODÜLLER ARASI yüzeyidir (ADR 0001).
//
// Buradaki imzalar YALNIZCA ilkel ve stdlib tipleri kullanır. Sebebi Go'nun
// yapısal uyum kuralıdır: tüketici modül pricing'i import edemediği için
// imzasında [models.PriceSet] gibi bir tipi adlandıramaz; adlandırdığı an o,
// kendi paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin
// arayüzünü karşılamaz. İlkel tiplerle yazılmış bir imza ise tüketicinin kendi
// paketinde birebir tekrarlanabilir ve container'dan adla çözülür.
//
// Modül içi zengin yüzey (models tipleriyle) service.go ve calculate.go'dadır;
// onu yalnızca pricing'in kendi API katmanı ve query sağlayıcısı çağırır.

// CreateEmptyPriceSet fiyatsız bir price set oluşturur ve KİMLİĞİNİ döner.
//
// product modülü bir varyant yaratırken bunu çağırır ve dönen kimliği
// "product_variant_price_set" linkine yazar; pricing o linki hiç görmez ve
// varyantın varlığından haberdar olmaz (Prensip 2.1/2.3).
//
// Tüketici tarafındaki karşılığı:
//
//	type PriceSetCreator interface {
//	    CreateEmptyPriceSet(ctx context.Context) (string, error)
//	}
func (s *Service) CreateEmptyPriceSet(ctx context.Context) (string, error) {
	set, err := s.CreatePriceSet(ctx, nil)
	if err != nil {
		return "", err
	}
	return set.ID, nil
}

// SetBasePrices bir kabın TABAN fiyatlarını para birimi -> tutar eşlemesiyle
// topluca yazar.
//
// "Taban" demek listesiz ve kuralsız demektir; kampanya ve segment fiyatları bu
// yüzeyden yazılamaz (onlar pricing'in kendi admin API'sinin işidir). Yazma
// [Service.SetPrices] gibi YERİNE KOYMADIR: eşlemede olmayan para birimlerinin
// fiyatları silinir.
//
// Eşlemenin dolaşım sırası rastgele olduğu için para birimleri SIRALANIR; aynı
// girdi her çağrıda aynı sırada yazılır ve hata mesajındaki indeks anlamlı olur.
//
// Tüketici tarafındaki karşılığı:
//
//	type BasePriceWriter interface {
//	    SetBasePrices(ctx context.Context, priceSetID string, amountsByCurrency map[string]int64) error
//	}
func (s *Service) SetBasePrices(ctx context.Context, priceSetID string, amountsByCurrency map[string]int64) error {
	inputs := make([]PriceInput, 0, len(amountsByCurrency))
	for _, currency := range slices.Sorted(maps.Keys(amountsByCurrency)) {
		inputs = append(inputs, PriceInput{
			CurrencyCode: currency,
			Amount:       amountsByCurrency[currency],
			MinQuantity:  models.MinQuantity,
		})
	}

	_, err := s.SetPrices(ctx, priceSetID, inputs)
	return err
}

// CalculateAmount seçilen fiyatın BİRİM tutarını minor unit olarak döner.
//
// Seçim kuralı [Service.CalculatePrice] ile birebir aynıdır; bu yalnızca
// modüller arası geçebilen dar bir imzadır. Hesaplama anı "şimdi"dir: tüketici
// modülün geçmişe dönük fiyat sorması bir rapor ihtiyacıdır ve pricing'in kendi
// API'sinden yapılır.
//
// quantity 0 verilirse 1 kabul edilir. attributes nil olabilir; o durumda
// kurallı fiyatlar elenir ve taban fiyat seçilir.
//
// Tüketici tarafındaki karşılığı (Faz 5'te cart bunu tanımlayacaktır):
//
//	type PriceCalculator interface {
//	    CalculateAmount(ctx context.Context, priceSetID, currencyCode string,
//	        quantity int32, attributes map[string]string) (int64, error)
//	}
func (s *Service) CalculateAmount(
	ctx context.Context,
	priceSetID, currencyCode string,
	quantity int32,
	attributes map[string]string,
) (int64, error) {
	calculated, err := s.CalculatePrice(ctx, priceSetID, CalculateParams{
		CurrencyCode: currencyCode,
		Quantity:     quantity,
		Attributes:   attributes,
	})
	if err != nil {
		return 0, err
	}
	return calculated.Amount, nil
}

package service

import (
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// normalizeCreateOrder sipariş girdisini DOĞRULAR ve tekleştirilmiş hâlini
// döner.
//
// Doğrulama ile normalleştirme tek yerdedir: para birimi ve e-posta
// tekleştirilmeden doğrulanamaz ("try" ile "TRY" aynı koddur), doğrulanmadan da
// saklanamaz. İkisini ayırmak, çağrı yerinde birinin unutulmasına açık kapı
// bırakırdı.
//
// Girdi DEĞER olarak alınıp değiştirilmiş kopyası dönülür; çağıranın verdiği
// yapı değişmez. Items dilimi paylaşılır ama elemanları YAZILMAZ, yalnızca
// okunur.
//
// Katmanların sırası ve gerekçeleri için bkz. [Service.CreateOrder].
func normalizeCreateOrder(in CreateOrderInput) (CreateOrderInput, error) {
	if err := requireID("region_id", in.RegionID); err != nil {
		return CreateOrderInput{}, err
	}
	if err := optionalID("customer_id", in.CustomerID); err != nil {
		return CreateOrderInput{}, err
	}
	if err := optionalID("cart_id", in.CartID); err != nil {
		return CreateOrderInput{}, err
	}
	if err := optionalID("idempotency_key", in.IdempotencyKey); err != nil {
		return CreateOrderInput{}, err
	}

	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return CreateOrderInput{}, err
	}
	in.CurrencyCode = currency

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return CreateOrderInput{}, err
	}
	in.Email = email

	if err := validateOrderTotals(in); err != nil {
		return CreateOrderInput{}, err
	}
	if err := validateOrderItems(in); err != nil {
		return CreateOrderInput{}, err
	}
	return in, nil
}

// validateOrderTotals sipariş düzeyindeki tutarların aralığını ve kimliğini
// doğrular.
func validateOrderTotals(in CreateOrderInput) error {
	for _, field := range []struct {
		label string
		value int64
	}{
		{"subtotal", in.Subtotal},
		{"discount_total", in.DiscountTotal},
		{"tax_total", in.TaxTotal},
		{"shipping_total", in.ShippingTotal},
		{"total", in.Total},
	} {
		if err := checkAmount(field.label, field.value, models.MaxTotal); err != nil {
			return err
		}
	}

	// İndirim ara toplamı AŞAMAZ.
	//
	// Kimlik kontrolü (aşağıda) tek başına yetmez: aşırı bir indirim, vergi ve
	// kargo tarafından yutulduğunda kimlik SAĞLANIR ve toplam negatif bile
	// olmaz. Örnek: subtotal=1000, discount=3000, shipping=2500 -> total=500.
	// Müşteri 1000'lik mala 2500'lük kargoyla birlikte 500 öder ve ne servis
	// ne de orders_totals_consistent kısıtı bunu görür.
	//
	// Kargo indirimi bu kuralın DIŞINDADIR: kargoyu indirmek isteyen akış
	// shipping_total'ı düşürerek yapar, indirimi şişirerek değil.
	if in.DiscountTotal > in.Subtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"indirim ara toplamı aşamaz: discount_total=%d, subtotal=%d",
			in.DiscountTotal, in.Subtotal)
	}

	expected := in.Subtotal - in.DiscountTotal + in.TaxTotal + in.ShippingTotal
	if in.Total != expected {
		return errors.Invalid(CodeTotalsInconsistent,
			"sipariş toplamı tutarsız: total=%d verildi, subtotal(%d) - discount_total(%d) + tax_total(%d) + shipping_total(%d) = %d",
			in.Total, in.Subtotal, in.DiscountTotal, in.TaxTotal, in.ShippingTotal, expected)
	}
	return nil
}

// validateOrderItems satırları doğrular ve ara toplamlarının siparişin ara
// toplamını verdiğini kontrol eder.
func validateOrderItems(in CreateOrderInput) error {
	if len(in.Items) == 0 {
		return errors.Invalid(CodeOrderEmpty,
			"sipariş en az bir satır içermeli: satırsız siparişte hiçbir şey satılmamıştır")
	}
	if len(in.Items) > maxOrderItems {
		return errors.Invalid(CodeInvalidInput,
			"sipariş en fazla %d satır içerebilir: %d", maxOrderItems, len(in.Items))
	}

	var sum int64
	// Döngü indeksle gezilir: satır girdisi büyüktür ve değerle kopyalamak her
	// tur birkaç yüz baytı boşuna taşır.
	for i := range in.Items {
		if err := validateOrderItem(i, in.Items[i]); err != nil {
			return err
		}
		next, err := addAmount(sum, in.Items[i].Subtotal)
		if err != nil {
			return err
		}
		sum = next
	}

	// Siparişin ara toplamı satırların ara toplamlarının TOPLAMIDIR. İndirim ve
	// vergi sipariş düzeyinde de doğabileceği için (kampanya, kargo vergisi)
	// yalnızca ara toplam bu kurala tabidir.
	//
	// Kontrol, "satırları göndermeyi unutmak" hatasının karşılığıdır: kimlik
	// kontrolü subtotal=0 ve total=0 olan bir siparişi TUTARLI sayardı ve
	// müşteriye hiçbir şey ödemediği bir sipariş yazılırdı.
	if in.Subtotal != sum {
		return errors.Invalid(CodeTotalsInconsistent,
			"siparişin ara toplamı satırların ara toplamlarına eşit olmalı: %d verildi, satırlar %d ediyor",
			in.Subtotal, sum)
	}
	return nil
}

// validateOrderItem tek bir sipariş satırını doğrular.
//
// Satırın sırası (index) hata mesajına yazılır: satırların henüz kimliği yoktur
// — kimlikler yazma anında üretilir — ve "hangi satır" sorusunun tek yanıtı
// çağıranın gönderdiği sıradır.
func validateOrderItem(index int, item CreateOrderItemInput) error {
	if err := requireID("items[].variant_id", item.VariantID); err != nil {
		return err
	}
	if err := requireText("items[].title", item.Title); err != nil {
		return err
	}
	if err := checkQuantity(item.Quantity); err != nil {
		return err
	}
	if err := checkAmount("items[].unit_price", item.UnitPrice, models.MaxAmount); err != nil {
		return err
	}
	for _, field := range []struct {
		label string
		value int64
	}{
		{"items[].subtotal", item.Subtotal},
		{"items[].discount_total", item.DiscountTotal},
		{"items[].tax_total", item.TaxTotal},
		{"items[].total", item.Total},
	} {
		if err := checkAmount(field.label, field.value, models.MaxTotal); err != nil {
			return err
		}
	}

	// Satır ara toplamı = birim fiyat × adet. Adet ile fiyatın birlikte
	// bulunduğu tek yer burasıdır; yanlış adetle fiyatlanmış bir satır başka
	// hiçbir kapıda yakalanmazdı.
	expectedSubtotal, err := multiplyAmount(item.UnitPrice, item.Quantity)
	if err != nil {
		return err
	}
	if item.Subtotal != expectedSubtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"satır ara toplamı tutarsız (%d. satır, %s): subtotal=%d verildi, unit_price(%d) × quantity(%d) = %d",
			index, item.VariantID, item.Subtotal, item.UnitPrice, item.Quantity, expectedSubtotal)
	}

	// Satır düzeyinde de indirim ara toplamı aşamaz; sipariş düzeyindekiyle
	// aynı gerekçe (vergi, aşırı indirimi yutup kimliği sağlar hâle getirebilir).
	if item.DiscountTotal > item.Subtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"satır indirimi ara toplamı aşamaz (%d. satır, %s): discount_total=%d, subtotal=%d",
			index, item.VariantID, item.DiscountTotal, item.Subtotal)
	}

	expectedTotal := item.Subtotal - item.DiscountTotal + item.TaxTotal
	if item.Total != expectedTotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"satır toplamı tutarsız (%d. satır, %s): total=%d verildi, subtotal(%d) - discount_total(%d) + tax_total(%d) = %d",
			index, item.VariantID, item.Total, item.Subtotal, item.DiscountTotal, item.TaxTotal, expectedTotal)
	}
	return nil
}

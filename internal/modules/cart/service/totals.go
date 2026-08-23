package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// Totals workflow'un hesapladığı sepet toplamlarıdır.
//
// Tüm alanlar TAM SAYI minor unit'tir (plan Bölüm 8). DiscountTotal POZİTİF
// verilir ve toplamdan düşülür; negatif bir indirim, indirim değil zamdır.
type Totals struct {
	// Revision hesabın DAYANDIĞI sepet şeklidir ([models.Cart.Revision]);
	// ZORUNLUDUR.
	//
	// Workflow sepeti okur, hesabını KİLİDİN DIŞINDA yapar ve sonucu bu metotla
	// yazar; okuma ile yazma arasında sepet değişmiş olabilir. Bu alan, hesabın
	// hangi şekle ait olduğunu çağıranın BEYAN ETMESİNİ zorunlu kılar
	// (bkz. [Service.SetTotals]).
	//
	// Varsayılan sıfır GEÇERLİ bir değerdir — hiç değiştirilmemiş bir sepetin
	// şeklidir — bu yüzden "verilmedi" diye yorumlanmaz ve eski davranışa
	// düşülmez: alanı doldurmayı unutan bir çağıran, sepet değişmişse hata alır.
	Revision int64
	// Subtotal satır ara toplamlarının toplamıdır.
	Subtotal int64
	// DiscountTotal toplam indirimdir; pozitif verilir.
	DiscountTotal int64
	// TaxTotal toplam vergidir.
	TaxTotal int64
	// ShippingTotal toplam kargo tutarıdır.
	ShippingTotal int64
	// Total ödenecek tutardır:
	// Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
	Total int64
	// Lines satır başına hesaplanan tutarlardır ve sepetin TÜM satırlarını
	// KAPSAMALIDIR; her satır TAM BİR KEZ verilir.
	//
	// Eksik bırakılan satır kabul EDİLMEZ (bkz. [Service.SetTotals]). Satırsız
	// bir sepette bu alan boş kalır; "yalnızca kargoyu yaz" çağrısı ancak o
	// durumda satırsız yapılabilir.
	Lines []LineTotals
}

// LineTotals tek bir sepet satırının hesaplanan tutarlarıdır.
//
// Adet BURADA YOKTUR: adet sepet servisinin, tutarlar workflow'un verisidir.
// Ayrılık bilinçlidir — bir hesaplama turu adedi sessizce değiştiremez.
type LineTotals struct {
	// LineItemID tutarların ait olduğu satırdır; ZORUNLUDUR.
	LineItemID string
	// UnitPrice birim fiyattır (minor unit).
	UnitPrice int64
	// Subtotal satırın ara toplamıdır: UnitPrice × Quantity.
	Subtotal int64
	// DiscountTotal satıra düşen indirimdir; pozitif verilir.
	DiscountTotal int64
	// TaxTotal satıra düşen vergidir.
	TaxTotal int64
	// Total satırın toplamıdır: Subtotal - DiscountTotal + TaxTotal.
	Total int64
}

// SetTotals workflow'un hesapladığı toplamları sepete yazar.
//
// Bu metot, cart modülünün calculate_totals workflow'una açtığı TEK yazma
// yüzeyidir. Modül fiyatı ya da vergiyi kendisi hesaplamaz (ADR 0006); burada
// yaptığı şey, gelen sonucun BİR BÜTÜN OLARAK tutarlı olduğunu doğrulamak ve
// tutarlıysa saklamaktır.
//
// # Çağrı TAM BİR HESAP TURUDUR
//
// SetTotals kısmi güncelleme kabul etmez: [Totals.Lines] sepetin O ANDAKİ tüm
// satırlarını kapsamalıdır. Sözleşme bilinçli olarak bu kadar dardır, çünkü
// alternatifi sessizce yanlış tutar üretiyordu: eksik bırakılan satırın SAKLI
// tutarları olduğu gibi korunuyordu ve yeni açılmış bir satırın saklı ara
// toplamı SIFIRDIR ([Service.AddLineItem] yalnızca adet ve ilk birim fiyatı
// yazar). Yani "satırları göndermeyi unutmak" — workflow'un yapabileceği en
// olası hata — fiyatlanmamış bir sepeti `Subtotal: 0, Total: 0` ile TUTARLI
// gösteriyor, sepet 0 tutarla tamamlanabiliyordu. Kapsama zorunluluğu bu yolu
// kapatır: her satır, tutarını çağıranın AÇIKÇA beyan etmesiyle yazılır ve
// beyan edilen her satır çarpım kontrolünden geçer.
//
// Bedeli, "yalnızca kargoyu güncelle" gibi bir kısa yolun olmamasıdır. Bedel
// gerçekte yoktur: sepetin şeklini değiştiren her işlem toplamları zaten
// bayatlatır ([models.Cart.TotalsStale]) ve calculate_totals'ın baştan
// koşmasını gerektirir; workflow zaten her turda tüm satırları fiyatlar.
//
// # Hesabın dayandığı şekli ÇAĞIRAN bildirir
//
// [Totals.Revision] zorunludur ve sepetin yazma anındaki şekliyle BİREBİR
// eşleşmelidir; eşleşmezse errors.Conflict (CodeTotalsStale) döner ve hiçbir
// şey yazılmaz. Damga da çağıranın bildirdiği şekille atılır.
//
// Sebep, hesabın kilidin DIŞINDA yapılmasıdır: workflow önce sepeti okur, sonra
// pricing ve tax'ı çağırır, en sonunda buraya yazar. Damga yazma anındaki
// şekilden alınsaydı, okuma ile yazma arasında sepete satır eklenmesi ya da
// kargo yöntemi seçilmesi BAYAT bir hesabı GÜNCEL diye damgalardı;
// [Service.MarkCompleted]'ın bayatlık kapısı da açılır ve müşteri sepetindeki
// maldan azını öderdi. Şekli çağıranın bildirmesiyle o tur reddedilir ve
// workflow yeniden hesaplar.
//
// # Doğrulama katmanları
//
// Sıra ucuzdan pahalıya doğrudur; kilit yalnızca gerekli olduğu kadar tutulur.
//
//  1. Aralık: her tutar negatif olamaz ve üst sınırı aşamaz (taşma koruması).
//  2. Sepet kimliği: Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
//     Sağlanmazsa errors.Invalid; workflow'daki bir hesap hatası veritabanına
//     SESSİZCE yazılamaz.
//  3. Şekil: [Totals.Revision] sepetin güncel şekline eşit olmalı.
//  4. Kapsama: sepetin her satırı TAM BİR KEZ verilmiş olmalı; bilinmeyen,
//     tekrarlanan ya da atlanan satır reddedilir.
//  5. Satır ara toplamı: Subtotal = UnitPrice × Quantity. Adet sepetin kendi
//     verisi olduğu için bu çarpımı doğrulayabilen tek yer burasıdır; yanlış
//     adetle fiyatlanmış bir satır başka hiçbir kapıda yakalanmazdı.
//  6. Satır kimliği: her satır için Total = Subtotal - DiscountTotal + TaxTotal.
//  7. Sepet ara toplamı: Subtotal, satırların ara toplamlarının TOPLAMIDIR.
//     İndirim ve vergi sepet düzeyinde de doğabileceği için (kampanya, kargo
//     vergisi) yalnızca ara toplam bu kurala tabidir.
//
// Doğrulamanın tamamı YAZMADAN ÖNCE yapılır: kısmen yazılmış bir hesap turu
// yoktur. Yazma tek bir işlemde ve sepetin kilidi altında gerçekleşir.
//
// Tamamlanmış sepete yazılamaz: errors.Conflict döner.
func (s *Service) SetTotals(ctx context.Context, cartID string, in Totals) error {
	if err := requireID("cart_id", cartID); err != nil {
		return err
	}
	if err := validateCartTotals(in); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		if cart.Revision != in.Revision {
			return errors.Conflict(CodeTotalsStale,
				"hesap sepetin güncel şekline ait değil: hesap %d için yapılmış, sepet %d; calculate_totals yeniden çalıştırılmalı",
				in.Revision, cart.Revision)
		}

		stored, err := s.store.ListLineItems(ctx, cart.ID)
		if err != nil {
			return err
		}
		applied, err := applyLineTotals(stored, in.Lines)
		if err != nil {
			return err
		}
		sum, err := sumSubtotals(applied)
		if err != nil {
			return err
		}
		if in.Subtotal != sum {
			return errors.Invalid(CodeTotalsInconsistent,
				"sepetin ara toplamı satırların ara toplamlarına eşit olmalı: %d verildi, satırlar %d ediyor",
				in.Subtotal, sum)
		}

		for _, line := range in.Lines {
			if _, err := s.store.SetLineItemTotals(ctx, cart.ID, line.LineItemID, models.LineTotals{
				UnitPrice:     line.UnitPrice,
				Subtotal:      line.Subtotal,
				DiscountTotal: line.DiscountTotal,
				TaxTotal:      line.TaxTotal,
				Total:         line.Total,
			}); err != nil {
				return err
			}
		}

		_, err = s.store.UpdateCartTotals(ctx, cart.ID, models.CartTotals{
			Subtotal:      in.Subtotal,
			DiscountTotal: in.DiscountTotal,
			TaxTotal:      in.TaxTotal,
			ShippingTotal: in.ShippingTotal,
			Total:         in.Total,
			Revision:      in.Revision,
		})
		return err
	})
}

// validateCartTotals sepet düzeyindeki tutarların aralığını ve kimliğini
// doğrular.
//
// Kilit alınmadan ÖNCE çağrılır: kendi içinde tutarsız bir istek için sepeti
// kilitlemenin anlamı yoktur ve kilit süresi boşuna uzardı.
func validateCartTotals(in Totals) error {
	if in.Revision < 0 {
		return errors.Invalid(CodeInvalidInput,
			"revision negatif olamaz: %d", in.Revision)
	}
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
	// ne de carts_totals_consistent kısıtı bunu görür.
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
			"sepet toplamı tutarsız: total=%d verildi, subtotal(%d) - discount_total(%d) + tax_total(%d) + shipping_total(%d) = %d",
			in.Total, in.Subtotal, in.DiscountTotal, in.TaxTotal, in.ShippingTotal, expected)
	}
	return nil
}

// applyLineTotals verilen satır tutarlarını saklı satırlara UYGULAR ve
// sonucu döner; hiçbir şey yazmaz.
//
// Dönen dilim, yazma yapılsaydı satırların alacağı ara toplamları taşır; sepet
// ara toplamı kontrolü bunun üzerinden yapılır.
//
// Satır kümesi TAM eşleşmelidir: bilinmeyen, tekrarlanan ya da tutarı hiç
// verilmemiş bir satır burada yakalanır — yazma başlamadan önce. Atlanan
// satırın reddedilmesi sözleşmenin özüdür; saklı tutarına güvenmek,
// fiyatlanmamış bir satırı sıfır tutarla geçerli sayardı
// (bkz. [Service.SetTotals]).
func applyLineTotals(stored []models.LineItem, updates []LineTotals) ([]models.LineItem, error) {
	byID := make(map[string]int, len(stored))
	for i := range stored {
		byID[stored[i].ID] = i
	}

	applied := make([]models.LineItem, len(stored))
	copy(applied, stored)

	seen := make(map[string]struct{}, len(updates))
	for _, line := range updates {
		if err := requireID("line_item_id", line.LineItemID); err != nil {
			return nil, err
		}
		if _, dup := seen[line.LineItemID]; dup {
			return nil, errors.Invalid(CodeTotalsInconsistent,
				"aynı satır için birden çok tutar verildi: %s", line.LineItemID)
		}
		seen[line.LineItemID] = struct{}{}

		idx, ok := byID[line.LineItemID]
		if !ok {
			return nil, errors.NotFound(CodeLineItemNotFound,
				"tutar yazılacak satır sepette yok: %s", line.LineItemID)
		}
		if err := validateLineTotals(line, applied[idx].Quantity); err != nil {
			return nil, err
		}

		applied[idx].UnitPrice = line.UnitPrice
		applied[idx].Subtotal = line.Subtotal
		applied[idx].DiscountTotal = line.DiscountTotal
		applied[idx].TaxTotal = line.TaxTotal
		applied[idx].Total = line.Total
	}

	// Bilinmeyen ve tekrarlanan kimlikler yukarıda elendiği için, sayıların
	// eşitliği kapsamanın TAM olduğunu söyler.
	if len(seen) != len(stored) {
		return nil, errors.Invalid(CodeTotalsInconsistent,
			"hesap sepetin TÜM satırlarını kapsamalı; tutarı verilmeyen satırlar: %s",
			strings.Join(missingIDs(stored, seen), ", "))
	}
	return applied, nil
}

// missingIDs tutarı verilmemiş satırların kimliklerini saklı sıralarıyla döner.
//
// Sıranın saklı listeden gelmesi hatayı yeniden üretilebilir kılar: harita
// üzerinde dönmek aynı girdide farklı sıralı mesajlar üretirdi.
func missingIDs(stored []models.LineItem, seen map[string]struct{}) []string {
	out := make([]string, 0, len(stored)-len(seen))
	for i := range stored {
		if _, ok := seen[stored[i].ID]; !ok {
			out = append(out, stored[i].ID)
		}
	}
	return out
}

// validateLineTotals tek bir satırın tutarlarını doğrular.
func validateLineTotals(line LineTotals, quantity int64) error {
	if err := checkAmount("unit_price", line.UnitPrice, models.MaxAmount); err != nil {
		return err
	}
	for _, field := range []struct {
		label string
		value int64
	}{
		{"subtotal", line.Subtotal},
		{"discount_total", line.DiscountTotal},
		{"tax_total", line.TaxTotal},
		{"total", line.Total},
	} {
		if err := checkAmount(field.label, field.value, models.MaxTotal); err != nil {
			return err
		}
	}

	expectedSubtotal, err := multiplyAmount(line.UnitPrice, quantity)
	if err != nil {
		return err
	}
	if line.Subtotal != expectedSubtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"satır ara toplamı tutarsız (%s): subtotal=%d verildi, unit_price(%d) × quantity(%d) = %d",
			line.LineItemID, line.Subtotal, line.UnitPrice, quantity, expectedSubtotal)
	}

	// Satır düzeyinde de indirim ara toplamı aşamaz; sepet düzeyindekiyle aynı
	// gerekçe (vergi, aşırı indirimi yutup kimliği sağlar hâle getirebilir).
	if line.DiscountTotal > line.Subtotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"satır indirimi ara toplamı aşamaz (%s): discount_total=%d, subtotal=%d",
			line.LineItemID, line.DiscountTotal, line.Subtotal)
	}

	expectedTotal := line.Subtotal - line.DiscountTotal + line.TaxTotal
	if line.Total != expectedTotal {
		return errors.Invalid(CodeTotalsInconsistent,
			"satır toplamı tutarsız (%s): total=%d verildi, subtotal(%d) - discount_total(%d) + tax_total(%d) = %d",
			line.LineItemID, line.Total, line.Subtotal, line.DiscountTotal, line.TaxTotal, expectedTotal)
	}
	return nil
}

// multiplyAmount birim fiyat ile adedi TAŞMADAN çarpar.
//
// Çarpanlar servis doğrulamasından geçtiğinde sonuç zaten [models.MaxTotal]
// altındadır; kontrol, doğrudan SQL ile yazılmış anormal bir adede karşı son
// savunmadır. Taşan bir çarpım sessizce negatif bir ara toplam üretir ve
// tutarlılık kontrolünü YANLIŞLIKLA geçebilirdi.
func multiplyAmount(unitPrice, quantity int64) (int64, error) {
	if unitPrice == 0 || quantity == 0 {
		return 0, nil
	}
	if quantity < 0 || unitPrice < 0 {
		return 0, errors.Invalid(CodeInvalidInput,
			"birim fiyat ve adet negatif olamaz: %d × %d", unitPrice, quantity)
	}
	if quantity > models.MaxTotal/unitPrice {
		return 0, errors.Invalid(CodeInvalidInput,
			"satır ara toplamı sınırı aşıyor: %d × %d > %d", unitPrice, quantity, models.MaxTotal)
	}
	return unitPrice * quantity, nil
}

// sumSubtotals satırların ara toplamlarını TAŞMADAN toplar.
func sumSubtotals(lines []models.LineItem) (int64, error) {
	var sum int64
	// Döngü indeksle gezilir: satır yapısı büyüktür ve değerle kopyalamak her
	// tur birkaç yüz baytı boşuna taşır.
	for i := range lines {
		subtotal := lines[i].Subtotal
		if subtotal < 0 {
			return 0, errors.Invalid(CodeTotalsInconsistent,
				"satır ara toplamı negatif: %s (%d)", lines[i].ID, subtotal)
		}
		if sum > models.MaxTotal-subtotal {
			return 0, errors.Invalid(CodeTotalsInconsistent,
				"satırların ara toplamı sınırı aşıyor (%d)", models.MaxTotal)
		}
		sum += subtotal
	}
	return sum, nil
}

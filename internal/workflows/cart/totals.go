package cart

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// MaxTotalsAttempts bir hesap turunun kaç kez tekrarlanabileceğidir.
//
// Hesap sepetin KİLİDİ DIŞINDA yapılır: önce sepet okunur, sonra pricing ve
// region çağrılır, en sonunda sonuç yazılır. Arada sepet değişirse yazma
// errors.Conflict döner ve hesabın baştan yapılması gerekir
// (bkz. [Workflows.CalculateTotals]).
//
// Üç deneme, gerçek yarışın boyuna göre seçilmiştir: çakışma ancak müşteri
// hesap uçarken sepetine dokunduğunda olur ve bir insanın peş peşe iki kez
// araya girmesi zaten olağan dışıdır. Sınırın var olması şarttır — sınırsız
// bir döngü, sürekli değişen bir sepette (bozuk bir istemci ya da takılmış bir
// yeniden deneme çevrimi) pricing'i sonsuza kadar meşgul ederdi. Sınır
// aşılırsa çağıran errors.Conflict alır ve KENDİ temposunda yeniden dener;
// bekleyen istek sayısını sunucu değil çağıran belirlemelidir.
const MaxTotalsAttempts = 3

// Totals bir hesap turunun sonucudur ve sepete JSON olarak yazılır.
//
// Tüm alanlar TAM SAYI minor unit'tir (plan Bölüm 8). Kimlik her zaman
// sağlanır: Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
type Totals struct {
	// Revision hesabın DAYANDIĞI sepet şeklidir; yazma anında sepetin şekliyle
	// eşleşmezse hesap reddedilir.
	Revision int64 `json:"revision"`
	// Subtotal satır ara toplamlarının toplamıdır.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal toplam indirimdir; pozitif taşınır ve toplamdan düşülür.
	// Satır indirimlerinin TOPLAMIDIR (bkz. paket yorumu, "İndirim").
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal toplam vergidir; satır vergilerinin toplamıdır.
	TaxTotal int64 `json:"tax_total"`
	// ShippingTotal sepetin kargo yöntemlerinin toplamıdır.
	ShippingTotal int64 `json:"shipping_total"`
	// Total ödenecek tutardır.
	Total int64 `json:"total"`
	// TaxSource verginin HANGİ kaynaktan geldiğidir; değerleri
	// [TaxSourceTax], [TaxSourceTaxUnconfigured] ve [TaxSourceRegion].
	//
	// Alan PARA DEĞİLDİR ve sepete yazılan gövdede cart modülünce yok sayılır
	// (sepetin toplam şeması onu tanımaz). Yine de gövdenin parçasıdır ve
	// çağırana döner: bir tutarın hangi otoriteye dayandığı, tutarın kendisi
	// kadar önemlidir. Vergisi 0 çıkan bir sepette "oran sıfırdı" ile
	// "yapılandırma yoktu" ancak burada ayrılır.
	TaxSource string `json:"tax_source"`
	// Lines satır başına hesaplanan tutarlardır ve sepetin TÜM satırlarını
	// kapsar.
	Lines []LineTotals `json:"lines"`
}

// LineTotals tek bir sepet satırının hesaplanan tutarlarıdır.
//
// Adet BURADA YOKTUR: adet sepetin verisidir ve bir hesap turu onu
// değiştiremez. Satırın ara toplamı adede DAYANIR ama adedi YAZMAZ.
type LineTotals struct {
	// LineItemID tutarların ait olduğu satırdır.
	LineItemID string `json:"line_item_id"`
	// UnitPrice pricing'in seçtiği birim fiyattır.
	UnitPrice int64 `json:"unit_price"`
	// Subtotal satırın ara toplamıdır: UnitPrice × Quantity.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal satıra düşen indirimdir; satırın ara toplamını ASLA aşmaz.
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal satıra düşen vergidir.
	TaxTotal int64 `json:"tax_total"`
	// Total satırın toplamıdır: Subtotal - DiscountTotal + TaxTotal.
	Total int64 `json:"total"`
}

// CalculateTotals sepetin toplamlarını baştan hesaplar ve sepete yazar.
//
// Bu, Faz 5'in kalbidir ve tek bir turu şu adımlardan oluşur:
//
//  1. Sepetin anlık görüntüsü TEK okumada alınır; şekil sayacı (revision)
//     not edilir.
//  2. Satırların varyantları TEK link sorgusuyla fiyat kümelerine çevrilir
//     (N+1 yoktur).
//  3. Her satırın birim fiyatı pricing'den YENİDEN alınır; saklı tutara asla
//     güvenilmez.
//  4. İndirim promotion modülünden KALEM BAŞINA alınır ve satırlara yazılır
//     (bkz. [Workflows.applyDiscounts]).
//  5. Vergi, İNDİRİM SONRASI taban üzerinden satır başına hesaplanır; hesabı
//     tax modülü yapar, yapamıyorsa region'ın oranı kullanılır ve kullanılan
//     kaynak [Totals.TaxSource] alanında bildirilir
//     (bkz. [Workflows.applyTaxes]).
//  6. Kargo, sepetin kargo yöntemlerinin toplamıdır ve vergi tabanına GİRMEZ.
//  7. Sonuç, 1. adımda not edilen şekille damgalanarak yazılır.
//
// # Çakışma
//
// Hesap sepetin kilidi dışında yapılır. Yazma anında sepetin şekli değişmişse
// sepet modülü errors.Conflict döner; o durumda tur BAŞTAN yapılır, en fazla
// [MaxTotalsAttempts] kez. Sınır aşılırsa hata errors.Conflict olarak çağırana
// geçer: bayat bir hesabı yazmak, müşteriye sepetindeki maldan azını ödetmek
// olurdu.
//
// # Tamamlanmış sepet
//
// Tamamlanmış sepette hesap yapılmaz ve errors.Conflict döner. Sepet modülü
// yazmayı zaten reddederdi; burada erken dönmenin sebebi, sonucu baştan belli
// bir tur için pricing'i boşuna çağırmamaktır.
//
// # Satırsız sepet
//
// Geçerlidir: ara toplam ve vergi sıfır, toplam yalnızca kargodur. Hata
// dönmez — sepete satır eklenmeden kargo seçilmesi mümkün bir durumdur ve
// satırsız sepetin SİPARİŞ olmasını cart modülü ayrıca reddeder.
func (w *Workflows) CalculateTotals(ctx context.Context, cartID string) (Totals, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return Totals{}, err
	}

	var lastErr error
	for attempt := 1; attempt <= MaxTotalsAttempts; attempt++ {
		totals, stale, err := w.totalsRound(ctx, cartID)
		if err == nil {
			return totals, nil
		}
		if !stale {
			return Totals{}, err
		}

		lastErr = err
		w.log.DebugContext(ctx, "sepet hesabı çakıştı, tur yenileniyor",
			"cart_id", cartID, "attempt", attempt, "max_attempts", MaxTotalsAttempts)
	}

	return Totals{}, errors.Wrap(lastErr, errors.KindConflict, CodeTotalsConflict,
		"sepet %s hesap yazılamayacak kadar sık değişti (%d deneme); istek yeniden gönderilmeli",
		cartID, MaxTotalsAttempts)
}

// totalsRound tek bir hesap turudur.
//
// İkinci dönüş değeri, hatanın YENİDEN DENEMEYE değer bir şekil çakışması olup
// olmadığını söyler. Ayrımın burada yapılması bilinçlidir: çağıran döngü,
// sepet modülünün hata KODLARINI tanımak zorunda kalmaz — tanısaydı, bu paket
// import edemediği bir modülün kod dizgelerini kopyalar ve o kodlar sessizce
// ayrışabilirdi.
func (w *Workflows) totalsRound(ctx context.Context, cartID string) (out Totals, stale bool, err error) {
	snap, err := w.snapshot(ctx, cartID)
	if err != nil {
		return Totals{}, false, err
	}
	if snap.Completed {
		return Totals{}, false, errors.Conflict(CodeCartCompleted,
			"tamamlanmış sepette hesap yapılamaz: %s", cartID)
	}

	totals, err := w.computeTotals(ctx, snap)
	if err != nil {
		return Totals{}, false, err
	}

	payload, err := json.Marshal(totals)
	if err != nil {
		return Totals{}, false, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"sepet toplamları JSON'a çevrilemedi: %s", cartID)
	}
	if err := w.carts.SetCartTotalsJSON(ctx, cartID, payload); err != nil {
		// Çakışma, okuma ile yazma arasında sepetin değiştiği anlamına gelir:
		// tur baştan yapılabilir. Tamamlanma da Conflict'tir ve o durumda
		// yeni turun anlık görüntüsü sepeti kapalı görüp erken döner.
		return Totals{}, errors.IsConflict(err), err
	}
	return totals, false, nil
}

// computeTotals anlık görüntüden toplamları üretir; HİÇBİR ŞEY YAZMAZ.
//
// Ayrılık bilinçlidir: hesabın tamamı yan etkisizdir ve tek başına
// sınanabilir. Yazma yalnızca [Workflows.totalsRound] içindedir.
//
// # Adımların SIRASI sözleşmedir
//
// Ara toplam -> indirim -> vergi. İndirim vergiden önce gelmek ZORUNDADIR
// çünkü vergi tabanı indirim sonrasıdır (bkz. paket yorumu, "Vergi
// sözleşmesi"); sıra bozulursa hiçbir denetim patlamaz, yalnızca müşteri
// ödemediği paranın vergisini öder.
func (w *Workflows) computeTotals(ctx context.Context, snap Snapshot) (Totals, error) {
	lines, err := w.lineSubtotals(ctx, snap)
	if err != nil {
		return Totals{}, err
	}
	shippingTotal, err := shippingTotalOf(snap)
	if err != nil {
		return Totals{}, err
	}
	if err := w.applyDiscounts(ctx, snap, lines); err != nil {
		return Totals{}, err
	}
	taxSource, err := w.applyTaxes(ctx, snap, shippingTotal, lines)
	if err != nil {
		return Totals{}, err
	}
	return assembleTotals(snap, lines, shippingTotal, taxSource)
}

// lineSubtotals her satırın birim fiyatını ve ara toplamını hesaplar.
//
// İndirim ve vergi alanları SIFIR bırakılır; onları [Workflows.applyDiscounts]
// ve [Workflows.applyTaxes] doldurur. Dönen dilim, anlık görüntüdeki satırlarla
// AYNI SIRADA ve AYNI UZUNLUKTADIR; iki modüle giden istekler ile dönen
// yanıtların eşleşmesi bu değişmeze dayanır.
func (w *Workflows) lineSubtotals(ctx context.Context, snap Snapshot) ([]LineTotals, error) {
	priceSets, err := w.priceSetsFor(ctx, snap.VariantIDs())
	if err != nil {
		return nil, err
	}

	lines := make([]LineTotals, 0, len(snap.Items))
	for i := range snap.Items {
		item := snap.Items[i]

		unitPrice, priceErr := w.unitPrice(ctx, priceSets[item.VariantID], snap, item)
		if priceErr != nil {
			return nil, priceErr
		}
		subtotal, mulErr := mulAmount(unitPrice, item.Quantity)
		if mulErr != nil {
			return nil, mulErr
		}

		lines = append(lines, LineTotals{
			LineItemID: item.ID,
			UnitPrice:  unitPrice,
			Subtotal:   subtotal,
		})
	}
	return lines, nil
}

// shippingTotalOf kargo yöntemlerinin toplamını TAŞMADAN hesaplar.
func shippingTotalOf(snap Snapshot) (int64, error) {
	var total int64
	for i := range snap.ShippingMethods {
		next, err := addAmount(total, snap.ShippingMethods[i].Amount)
		if err != nil {
			return 0, err
		}
		total = next
	}
	return total, nil
}

// assembleTotals doldurulmuş satırlardan sepet toplamlarını üretir ve satır
// toplamlarını yazar.
//
// # Σ kimlikleri BURADA doğar
//
// Sepetin indirimi ve vergisi, satır değerlerinin TOPLANMASIYLA üretilir;
// promotion ve tax'ın bildirdiği toplamlar yeniden kullanılmaz (onlar kendi
// yerlerinde satır değerleriyle karşılaştırılmıştır). Böylece
// Σ(satır indirimi) = sepet indirimi ve Σ(satır vergisi) = sepet vergisi
// kimlikleri hesap yapısı gereği sağlanır, bir denetimin hatırlanmasına bağlı
// kalmaz.
//
// # Kuruş artığı NEREDE kalır
//
// Bu işlev hiçbir bölme yapmaz, dolayısıyla hiçbir artık ÜRETMEZ. Artık
// yalnızca oran hesaplarında doğar ve doğduğu SATIRDA düşer: indirim yüzdesi
// ve vergi oranı satır başına AŞAĞI yuvarlanır, kalan kesir başka bir satıra
// TAŞINMAZ ve sepet düzeyinde yeniden dağıtılmaz. Yönleri terstir ve ikisi de
// satır başına bir minor unit'ten küçüktür: aşağı yuvarlanan vergi müşteri
// LEHİNE (daha az vergi), aşağı yuvarlanan indirim satıcı lehinedir (daha az
// indirim). Taşımanın reddedilme sebebi de aynıdır — artığı bir satıra
// eklemek, o satırın vergisini ya da indirimini kendi oranının söylediğinden
// farklı yapar ve fatura satır satır açıklanamaz hâle gelirdi.
func assembleTotals(snap Snapshot, lines []LineTotals, shippingTotal int64, taxSource string) (Totals, error) {
	totals := Totals{
		Revision:      snap.Revision,
		ShippingTotal: shippingTotal,
		TaxSource:     taxSource,
		Lines:         lines,
	}

	var err error
	for i := range lines {
		line := &lines[i]

		if totals.Subtotal, err = addAmount(totals.Subtotal, line.Subtotal); err != nil {
			return Totals{}, err
		}
		if totals.DiscountTotal, err = addAmount(totals.DiscountTotal, line.DiscountTotal); err != nil {
			return Totals{}, err
		}
		if totals.TaxTotal, err = addAmount(totals.TaxTotal, line.TaxTotal); err != nil {
			return Totals{}, err
		}
		if line.Total, err = addAmount(line.Subtotal-line.DiscountTotal, line.TaxTotal); err != nil {
			return Totals{}, err
		}
	}

	total, err := addAmount(totals.Subtotal-totals.DiscountTotal, totals.TaxTotal)
	if err != nil {
		return Totals{}, err
	}
	if total, err = addAmount(total, totals.ShippingTotal); err != nil {
		return Totals{}, err
	}
	totals.Total = total
	return totals, nil
}

// unitPrice satırın birim fiyatını pricing'den alır.
//
// Fiyat bağlamına yalnızca bölge konur; müşteri segmentinin neden dışarıda
// kaldığı paket yorumundadır.
//
// Uygun fiyat yoksa pricing errors.NotFound döner ve hata BURADA errors.Invalid
// olarak yeniden sınıflandırılır: satır sepette DURUYOR, eksik olan onun bu
// para birimindeki fiyatıdır. NotFound olarak geçseydi istemci "sepet/satır
// yok" (404) okur ve gerçekte düzeltilebilir olan durumu kayıp sanardı.
func (w *Workflows) unitPrice(ctx context.Context, priceSetID string, snap Snapshot, item SnapshotItem) (int64, error) {
	quantity, err := quantity32(item.Quantity)
	if err != nil {
		return 0, err
	}

	amount, err := w.prices.CalculateAmount(ctx, priceSetID, snap.CurrencyCode, quantity,
		map[string]string{attrRegionID: snap.RegionID})
	if err != nil {
		if errors.IsNotFound(err) {
			return 0, errors.Wrap(err, errors.KindInvalid, CodePriceUnavailable,
				"%s varyantının %s para biriminde ve %d adette fiyatı yok (satır: %s)",
				item.VariantID, snap.CurrencyCode, item.Quantity, item.ID)
		}
		return 0, err
	}
	if err := checkAmount("unit_price", amount, MaxAmount); err != nil {
		return 0, err
	}
	return amount, nil
}

// snapshot sepetin anlık görüntüsünü okur ve çözer.
func (w *Workflows) snapshot(ctx context.Context, cartID string) (Snapshot, error) {
	payload, err := w.carts.CartSnapshotJSON(ctx, cartID)
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(cartID, payload)
}

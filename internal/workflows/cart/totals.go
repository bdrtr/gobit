package cart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

	unitPrices, err := w.unitPrices(ctx, snap, priceSets)
	if err != nil {
		return nil, err
	}

	lines := make([]LineTotals, 0, len(snap.Items))
	for i := range snap.Items {
		item := snap.Items[i]

		subtotal, mulErr := mulAmount(unitPrices[i], item.Quantity)
		if mulErr != nil {
			return nil, mulErr
		}

		lines = append(lines, LineTotals{
			LineItemID: item.ID,
			UnitPrice:  unitPrices[i],
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

// priceRequest pricing modülüne giden TOPLU fiyat isteğinin JSON şemasıdır.
//
// Alan adları pricing'in interop şemasıyla BİREBİR aynı olmak ZORUNDADIR: iki
// paket birbirini import edemediği için derleyici uyumu göremez (ADR 0006'nın
// kabul edilen bedeli) ve uyum ancak entegrasyon testiyle kanıtlanabilir
// (bkz. internal/e2e/sepet_toplam_test.go).
//
// Para birimi ve bağlam KALEM BAŞINA taşınmaz: bir sepetin tüm satırları aynı
// para biriminde ve aynı bölgededir, alanı kalem başına tekrarlamak iki satırın
// farklı bağlamla fiyatlanabildiği izlenimi verirdi.
type priceRequest struct {
	// CurrencyCode sepetin para birimidir (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Attributes fiyat kurallarının bakacağı bağlamdır; bugün yalnızca bölge
	// konur ve müşteri segmentinin neden dışarıda kaldığı paket yorumundadır.
	Attributes map[string]string `json:"attributes"`
	// Items fiyatlanacak kalemlerdir ve sepetteki satır SIRASIYLA gider.
	Items []priceRequestItem `json:"items"`
}

// priceRequestItem toplu fiyat isteğindeki tek bir kalemin şemasıdır.
type priceRequestItem struct {
	// PriceSetID satırın varyantının bağlı olduğu fiyat kabıdır.
	PriceSetID string `json:"price_set_id"`
	// Quantity satırın GÜNCEL adedidir; fiyat kademesi buna göre seçilir.
	Quantity int32 `json:"quantity"`
}

// priceResponse pricing modülünden dönen toplu fiyat sonucunun JSON şemasıdır.
//
// Bilinmeyen alanlar SESSİZCE ATLANIR (isteğin tersine): pricing şemasını
// büyüttüğünde bu paketin aynı turda güncellenmesi gerekmemelidir. Sessizlik
// yalnızca TANINMAYAN alanlar içindir; tanınanların taşıdığı değişmezler
// [Workflows.unitPrices] içinde tek tek doğrulanır.
type priceResponse struct {
	// Items istekteki kalemlerle AYNI SIRADA ve AYNI UZUNLUKTA sonuçlardır.
	Items []priceResponseItem `json:"items"`
}

// priceResponseItem toplu fiyat yanıtındaki tek bir kalemin şemasıdır.
type priceResponseItem struct {
	// Amount seçilen birim fiyattır (minor unit); Priced false ise anlamsızdır.
	Amount int64 `json:"amount"`
	// Priced kalem için geçerli bir fiyat BULUNUP bulunmadığını bildirir.
	//
	// Ayrı bir bayrak ŞARTTIR: sıfır GEÇERLİ bir fiyattır (bedava kalem gerçek
	// bir senaryodur), dolayısıyla "tutar 0" ile "fiyat yok" tutarın kendisinden
	// ayırt edilemez. Bayrak olmasaydı fiyatı olmayan bir varyant sepete BEDAVA
	// girerdi.
	Priced bool `json:"priced"`
}

// unitPrices sepetin TÜM satırlarının birim fiyatını TEK turda alır.
//
// Dönen dilim anlık görüntüdeki satırlarla AYNI SIRADA ve AYNI UZUNLUKTADIR;
// [Workflows.lineSubtotals] indeksle eşler.
//
// # Neden toplu
//
// Hesap turu satır başına iki sorgu açıyordu (fiyat adayları + kuralları) ve
// her satır ekleme kendinden önceki TÜM satırları yeniden fiyatlıyordu, yani
// bir sepeti kurmanın maliyeti satır sayısıyla KARESEL büyüyordu. Ölçüldü
// (bu paketin sahteleriyle, çağrılar sayılarak): 100 satırlık bir sepeti
// kurmak 5150 fiyat çağrısı — 10.300 sorgu — ediyordu; toplu yolla aynı sepet
// 200 çağrı ve 400 sorgudur.
//
// Sorgunun kendisi de ölçüldü (gobit_load, 54.000 kap, localhost TCP, yedi
// turun en iyisi): 50 kap için kap başına yol 4,9 ms, toplu yol 0,25 ms;
// 100 kap için 9,9 ms ve 0,33 ms. TEK kapta toplu yolun bir üstünlüğü yoktur
// (aday sorgusu 500 turun medyanıyla 66 µs'ye karşı 77 µs; iki plan da aynı
// kısmi indeksi tarar, dizi parametreli olan üstüne bir sıralama adımı ekler),
// bu yüzden satır AÇILIRKEN sorulan tek fiyat hâlâ tekil metotla sorulur
// (bkz. [Workflows.AddLineItem]).
//
// # Seçilen tutar DEĞİŞMEZ
//
// İki yol pricing'in AYNI saf seçim fonksiyonunu çalıştırır ve aynı aday
// satırlarını görür; tek fark, toplu yolun saati bir kez okumasıdır ve bu fark
// toplu yolun lehinedir — tam o sırada biten bir kampanya, aynı sepetin iki
// satırını farklı dünyalardan fiyatlayamaz. Eşitlik iddiası pricing'in kendi
// testinde kanıtlanır (TestCalculateAmountsJSONMatchesCalculateAmount).
//
// # Fiyatı olmayan satır
//
// pricing toplu yolda hata değil bayrak döner; hata sınıflandırması BURADA
// yapılır ve tekil yoldakiyle birebir aynıdır (errors.Invalid,
// [CodePriceUnavailable]): satır sepette DURUYOR, eksik olan onun bu para
// birimindeki fiyatıdır. NotFound olarak geçseydi istemci "sepet/satır yok"
// (404) okur ve gerçekte düzeltilebilir olan durumu kayıp sanardı.
//
// Bayrağın kazandırdığı şey burada harcanır: fiyatsız satırların HEPSİ tek
// hatada sayılır (bkz. [priceUnavailable]), ilk fiyatsız satırda dönülmez.
//
// # Yanıt DOĞRULANIR
//
// Uzunluk ve tutar aralığı denetlenir. Sınırın öteki tarafını derleyici
// denetlemez; hizasını kaybetmiş bir yanıt sessizce geçseydi sepetin her satırı
// BAŞKA bir varyantın fiyatıyla yazılırdı ve hiçbir kapı bunu görmezdi.
//
// # İstek BÖLÜNMEZ ve bunun bir sınırı vardır
//
// Sepetin tüm satırları tek istekte gider. pricing'in kendi kalem tavanı
// (MaxCalculateItems, bugün 1000) aşılırsa istek reddedilir ve o sepetin
// toplamı HİÇ hesaplanamaz. Bugün ulaşılamaz bir durumdur: satır açan tek yol
// [MaxLineItems] (100) tavanına tabidir ve 1000'in üstüne çıkmanın tek yolu o
// sabiti büyütmektir — büyütülürse pricing'in tavanı da onunla birlikte
// büyütülmelidir. Büyütmeden önce MaxCalculateItems godoc'undaki plan tablosuna
// bakılmalı: pricing'in toplu okuması 280 ile 300 kimlik arasında indeksi
// bırakıp tabloyu taramaya geçiyor, yani maliyet 1000'e kadar doğrusal değil.
//
// İstek kalem tavanına göre BÖLÜNMEZ, çünkü bölmek yukarıdaki "tek an"
// güvencesini geri alırdı: her parça saati yeniden okur ve tam o sırada biten
// bir kampanya sepetin ilk parçasını başka, ikinci parçasını başka bir dünyadan
// fiyatlardı.
func (w *Workflows) unitPrices(ctx context.Context, snap Snapshot, priceSets map[string]string) ([]int64, error) {
	if len(snap.Items) == 0 {
		return nil, nil
	}

	req := priceRequest{
		CurrencyCode: snap.CurrencyCode,
		Attributes:   map[string]string{attrRegionID: snap.RegionID},
		Items:        make([]priceRequestItem, 0, len(snap.Items)),
	}
	for i := range snap.Items {
		quantity, err := quantity32(snap.Items[i].Quantity)
		if err != nil {
			return nil, err
		}
		req.Items = append(req.Items, priceRequestItem{
			PriceSetID: priceSets[snap.Items[i].VariantID],
			Quantity:   quantity,
		})
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"toplu fiyat isteği JSON'a çevrilemedi: %s", snap.ID)
	}

	raw, err := w.prices.CalculateAmountsJSON(ctx, payload)
	if err != nil {
		// Sınıf ve kod KORUNUR: pricing'in hatası tekil yolda da olduğu gibi
		// geçer, sarmalanırsa "fiyat yok" ile "fiyat sorulamadı" ayrımı kaybolur.
		return nil, err
	}

	var resp priceResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"toplu fiyat sonucu çözülemedi: %s", snap.ID)
	}
	if len(resp.Items) != len(snap.Items) {
		return nil, errors.Internal(CodePriceResponseInvalid,
			"toplu fiyat sonucu %d satır için %d kayıt döndürdü (%s)",
			len(snap.Items), len(resp.Items), snap.ID)
	}

	var unpriced []int
	for i := range resp.Items {
		if !resp.Items[i].Priced {
			unpriced = append(unpriced, i)
		}
	}
	if len(unpriced) > 0 {
		return nil, priceUnavailable(snap, unpriced)
	}

	out := make([]int64, 0, len(resp.Items))
	for i := range resp.Items {
		if err := checkAmount("unit_price", resp.Items[i].Amount, MaxAmount); err != nil {
			return nil, err
		}
		out = append(out, resp.Items[i].Amount)
	}
	return out, nil
}

// priceUnavailable fiyatı olmayan satırların HEPSİNİ tek hatada bildirir.
//
// İlk fiyatsız satırda durup dönmek, elde olan bilgiyi ATMAK olurdu: toplu
// yanıt satırların hepsini birden taşır, dolayısıyla iki ölü varyantı olan bir
// sepetin sahibi ikisini de bu istekte öğrenebilir — tek tek dönseydi her
// düzeltmeden sonra bir sonrakini keşfeder, sepetini istek istek onarırdı.
// Toplu yolun kalem başına BAYRAK dönmesinin (hata değil) burada karşılığı
// budur; bayrak bir tur atlatmak için değil, bu cümleyi kurabilmek içindir.
//
// Sınıf ve kod tekil yoldakiyle aynı kalır (errors.Invalid,
// [CodePriceUnavailable]): satır sepette DURUYOR, eksik olan onun bu para
// birimindeki fiyatıdır. Tek satırlık mesaj da aynen korunur — çoğul biçim
// yalnızca gerçekten birden çok satır fiyatsızken kurulur.
func priceUnavailable(snap Snapshot, unpriced []int) error {
	if len(unpriced) == 1 {
		item := snap.Items[unpriced[0]]
		return errors.Invalid(CodePriceUnavailable,
			"%s varyantının %s para biriminde ve %d adette fiyatı yok (satır: %s)",
			item.VariantID, snap.CurrencyCode, item.Quantity, item.ID)
	}

	parts := make([]string, 0, len(unpriced))
	for _, i := range unpriced {
		item := snap.Items[i]
		parts = append(parts, fmt.Sprintf("%s (satır: %s, adet: %d)",
			item.VariantID, item.ID, item.Quantity))
	}
	return errors.Invalid(CodePriceUnavailable,
		"%d satırın %s para biriminde fiyatı yok: %s",
		len(unpriced), snap.CurrencyCode, strings.Join(parts, ", "))
}

// snapshot sepetin anlık görüntüsünü okur ve çözer.
func (w *Workflows) snapshot(ctx context.Context, cartID string) (Snapshot, error) {
	payload, err := w.carts.CartSnapshotJSON(ctx, cartID)
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(cartID, payload)
}

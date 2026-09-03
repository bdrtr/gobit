package cart

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// MaxLineItems bir sepetin taşıyabileceği en fazla FARKLI satır sayısıdır.
//
// Sınır SESSİZ DEĞİLDİR ve kırpma yoktur: tavana ulaşmış bir sepete YENİ satır
// açmak isteyen istek errors.Invalid ([CodeCartLineLimit]) ile reddedilir ve
// mesaj hem tavanı hem sepetteki satır sayısını yazar.
//
// # Neden bir tavan var
//
// Satır ekleyen her istek sepetin TÜM satırlarını yeniden fiyatlar ve TÜM
// satırların tutarını yeniden YAZAR, yani N satırlık bir sepeti kurmanın
// maliyeti N ile değil N² ile büyür. Fiyat okuması toplu hâle getirilerek
// doğrusala indirildi (bkz. [Workflows.unitPrices]) ama YAZMA tarafı hâlâ satır
// başınadır: cart modülünün SetTotals'ı her satırın tutarını ayrı bir UPDATE
// ile ve sepetin kilidi altında yazar. Ölçüldü (bu paketin sahteleriyle,
// çağrılar sayılarak): 100 satırlık bir sepeti kurmak 5.050 satır tutarı
// yazımı eder; 1.000 satırlık bir sepet 500.500 eder. Tavansız bir sepet, tek
// bir istemcinin veritabanını meşgul edebileceği süreyi sınırsız bırakırdı.
//
// # Neden 100
//
// Değer cart modülünün sayfa boyutu tavanıyla (MaxLimit) aynıdır — tavana
// dayanmış bir sepetin satırları o modülün TEK sayfasına sığar — ve pricing'in
// toplu fiyat isteği tavanının (MaxCalculateItems, bugün 1000) onda biridir;
// aradaki boşluk bilinçlidir ve aşağıdaki paragrafın konusudur.
//
// # Tavan yalnızca satır AÇAN yolda uygulanır
//
// Sepette zaten duran bir varyantı yeniden eklemek yeni satır açmaz, var olan
// satırın adedini artırır ve tavana TAKILMAZ; takılsaydı dolu bir sepetin
// sahibi kendi satırının adedini bile artıramazdı. Aynı gerekçeyle hesap turu,
// adet güncellemesi ve sipariş yolu tavanı hiç sormaz: tavan KONMADAN önce
// açılmış ve bugün 100'ün üstünde satır taşıyan bir sepet hesaplanabilir ve
// tamamlanabilir kalmalıdır — reddetmek, müşterinin var olan sepetini
// ödenemez hâle getirirdi.
const MaxLineItems = 100

// AddLineItemInput sepete eklenecek satırın girdisidir.
type AddLineItemInput struct {
	// CartID satırın ekleneceği sepettir; ZORUNLUDUR.
	CartID string
	// VariantID eklenecek ürün varyantıdır; ZORUNLUDUR.
	VariantID string
	// Quantity eklenecek adettir; POZİTİF olmalıdır.
	//
	// Değer MUTLAK değil EKLENECEK adettir: aynı varyant sepette zaten varsa
	// yeni satır açılmaz, var olan satırın adedi bu kadar ARTAR.
	Quantity int64
	// Metadata satıra iliştirilecek serbest JSON nesnesidir; OPSİYONELDİR.
	//
	// Akış onu okumaz ve hesaba katmaz; yalnızca sepet modülüne taşır. Alan,
	// vitrinin satır başına niyetini (hediye notu, kişiselleştirme) tutar ve
	// satırı açan tek yol bu akış olduğu için başka bir taşıyıcısı yoktur.
	//
	// Birleştirmede YAZILMAZ: aynı varyant sepette zaten varsa cart modülü
	// yalnızca adedi artırır ve var olan satırın metadata'sını korur
	// (bkz. cart servisindeki AddLineItem).
	Metadata json.RawMessage
}

// AddLineItemResult eklenen satırın ve yeniden hesaplanan toplamların
// sonucudur.
type AddLineItemResult struct {
	// LineItemID eklenen (ya da adedi artırılan) satırın kimliğidir.
	LineItemID string
	// VariantID satırın gösterdiği varyanttır.
	VariantID string
	// Title satırın katalogdan kopyalanan başlığıdır.
	Title string
	// UnitPrice satır AÇILIRKEN yazılan birim fiyattır.
	//
	// Nihai fiyat değildir: satır açıldıktan sonra koşan hesap turu, sepetin
	// SON hâlindeki adede göre fiyatı yeniden seçer ve satıra onu yazar. İkisi
	// yalnızca birleştirme olduğunda ayrışır (bkz. [Workflows.AddLineItem]).
	UnitPrice int64
	// Totals satır eklendikten sonraki sepet toplamlarıdır.
	Totals Totals
}

// AddLineItem varyantın fiyatını bulur, satırı ekler ve toplamları yeniden
// hesaplar.
//
// Sıra: sepetin para birimi okunur -> varyantın başlığı katalogdan alınır ->
// varyantın fiyat kümesi link üzerinden bulunur -> birim fiyat pricing'den
// hesaplanır -> satır yazılır -> [Workflows.CalculateTotals] koşar.
//
// # Fiyatı olmayan varyant
//
// Reddedilir (errors.Invalid); gerekçe [Workflows.priceSetsFor] godoc'undadır.
// Fiyat kümesi var ama sepetin para biriminde geçerli fiyat yoksa hata yine
// errors.Invalid'dir ve mesaj para birimini yazar.
//
// # Birleştirme ve fiyat kademesi
//
// Aynı varyant sepette zaten varsa cart modülü yeni satır açmaz, adedi artırır.
// Bu durumda BURADA hesaplanan birim fiyat eklenen adede aittir ve birleşmiş
// adede ait olmayabilir — pricing fiyatı adet aralığına göre seçer, yani 3 + 2
// birleşince satır "5+" kademesine geçebilir. Fark önemsizdir çünkü satır
// yazıldıktan hemen sonra koşan hesap turu TÜM satırları sepetteki GÜNCEL
// adetle yeniden fiyatlar; buradaki değer yalnızca satırın açılış değeridir ve
// hiçbir zaman müşteriye gösterilen tutar olmaz.
//
// # Toplam hesabı patlarsa
//
// Satır YAZILMIŞTIR ve geri alınmaz. Hata [CodeTotalsAfterChange] koduyla
// sarılarak dönülür; sepet, cart modelinin tanıdığı "bayat toplam" durumunda
// kalır ve o sepetin sipariş olması ayrıca reddedilir. Satırı silmek, geçici
// bir pricing/region arızası yüzünden müşterinin isteğini yok etmek olurdu
// (bkz. paket yorumu, "Neden hiçbir akış saga değil").
func (w *Workflows) AddLineItem(ctx context.Context, in AddLineItemInput) (AddLineItemResult, error) {
	if err := requireID("cart_id", in.CartID); err != nil {
		return AddLineItemResult{}, err
	}
	if err := requireID("variant_id", in.VariantID); err != nil {
		return AddLineItemResult{}, err
	}
	quantity, err := quantity32(in.Quantity)
	if err != nil {
		return AddLineItemResult{}, err
	}

	snap, err := w.snapshot(ctx, in.CartID)
	if err != nil {
		return AddLineItemResult{}, err
	}
	if snap.Completed {
		return AddLineItemResult{}, errors.Conflict(CodeCartCompleted,
			"tamamlanmış sepete satır eklenemez: %s", in.CartID)
	}
	// Tavan, katalog ve fiyat okumalarından ÖNCE denetlenir: sonucu baştan belli
	// bir istek için iki modülü meşgul etmenin anlamı yoktur.
	if err := checkLineLimit(snap, in.VariantID); err != nil {
		return AddLineItemResult{}, err
	}

	title, err := w.variantTitle(ctx, in.VariantID)
	if err != nil {
		return AddLineItemResult{}, err
	}
	priceSets, err := w.priceSetsFor(ctx, []string{in.VariantID})
	if err != nil {
		return AddLineItemResult{}, err
	}

	unitPrice, err := w.prices.CalculateAmount(ctx, priceSets[in.VariantID], snap.CurrencyCode, quantity,
		map[string]string{attrRegionID: snap.RegionID})
	if err != nil {
		if errors.IsNotFound(err) {
			return AddLineItemResult{}, errors.Wrap(err, errors.KindInvalid, CodePriceUnavailable,
				"%s varyantının %s para biriminde ve %d adette fiyatı yok",
				in.VariantID, snap.CurrencyCode, in.Quantity)
		}
		return AddLineItemResult{}, err
	}
	if err := checkAmount("unit_price", unitPrice, MaxAmount); err != nil {
		return AddLineItemResult{}, err
	}

	lineID, err := w.carts.AddCartLineItem(ctx, in.CartID, in.VariantID, title, in.Quantity, unitPrice, in.Metadata)
	if err != nil {
		return AddLineItemResult{}, err
	}

	totals, err := w.CalculateTotals(ctx, in.CartID)
	if err != nil {
		return AddLineItemResult{}, totalsAfterChange(err, in.CartID, "satır eklendi")
	}

	return AddLineItemResult{
		LineItemID: lineID,
		VariantID:  in.VariantID,
		Title:      title,
		UnitPrice:  unitPrice,
		Totals:     totals,
	}, nil
}

// checkLineLimit isteğin sepete YENİ bir satır açıp açmayacağını ve açacaksa
// [MaxLineItems] tavanına sığıp sığmadığını denetler.
//
// Varyant sepette zaten varsa istek birleştirmedir, satır sayısını
// DEĞİŞTİRMEZ ve tavandan muaftır; gerekçe [MaxLineItems] godoc'undadır.
// Karşılaştırmanın kaynağı anlık görüntüdür ve görüntü ile yazma arasında
// başka bir istek araya girebilir: tavan bu yüzden KESİN bir üst sınır değil,
// bir sepetin sınırsız büyümesini kesen bir kapıdır — birkaç satırlık bir
// aşımın maliyeti, satır eklemeyi sepetin kilidi altına almanın maliyetinden
// çok daha düşüktür.
func checkLineLimit(snap Snapshot, variantID string) error {
	if len(snap.Items) < MaxLineItems {
		return nil
	}
	for i := range snap.Items {
		if snap.Items[i].VariantID == variantID {
			return nil
		}
	}
	return errors.Invalid(CodeCartLineLimit,
		"sepet en fazla %d satır taşıyabilir; %s sepetinde %d satır var (var olan bir satırın adedi artırılabilir)",
		MaxLineItems, snap.ID, len(snap.Items))
}

// totalsAfterChange sepet DEĞİŞTİKTEN sonra patlayan hesabın hatasını sarar.
//
// Sarmalama, çağıranın iki durumu ayırt edebilmesi içindir: istek reddedildi
// (sepet değişmedi) ile istek uygulandı ama tutar hesaplanamadı. İkincisinde
// isteği tekrarlamak satırı İKİNCİ KEZ eklerdi; doğru davranış yalnızca hesabı
// yeniden çalıştırmaktır. Hatanın SINIFI korunur ki durum koduna çeviren
// katman doğru kodu yazsın.
func totalsAfterChange(err error, cartID, what string) error {
	return errors.Wrap(err, errors.KindOf(err), CodeTotalsAfterChange,
		"%s (%s) ama toplamlar hesaplanamadı; sepetin toplamları bayat, hesap yeniden çalıştırılmalı",
		what, cartID)
}

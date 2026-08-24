package cart

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
)

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

	lineID, err := w.carts.AddCartLineItem(ctx, in.CartID, in.VariantID, title, in.Quantity, unitPrice)
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

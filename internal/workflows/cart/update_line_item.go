package cart

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// UpdateLineItemInput satır adedi güncellemesinin girdisidir.
type UpdateLineItemInput struct {
	// CartID satırın ait olduğu sepettir; ZORUNLUDUR.
	CartID string
	// LineItemID güncellenecek satırdır; ZORUNLUDUR.
	LineItemID string
	// Quantity satırın YENİ adedidir (mutlak değer, eklenecek değil).
	//
	// Sıfır verilirse satır KALDIRILIR; negatif değer reddedilir. Gerekçe
	// [Workflows.UpdateLineItem] godoc'undadır.
	Quantity int64
}

// UpdateLineItemResult güncellemenin ve yeniden hesaplanan toplamların
// sonucudur.
type UpdateLineItemResult struct {
	// LineItemID güncellenen (ya da kaldırılan) satırdır.
	LineItemID string
	// Quantity satırın yeni adedidir; satır kaldırıldıysa sıfırdır.
	Quantity int64
	// Removed satırın kaldırılıp kaldırılmadığını bildirir.
	Removed bool
	// Totals güncellemeden sonraki sepet toplamlarıdır.
	Totals Totals
}

// UpdateLineItem satırın adedini yazar (ya da satırı kaldırır) ve toplamları
// yeniden hesaplar.
//
// # Sıfır adet satırı KALDIRIR
//
// Karar bilinçlidir ve cart modülünün kararıyla ÇELİŞMEZ; onu tamamlar. Modülün
// UpdateLineItemQuantity metodu sıfırı reddeder, çünkü orası mutlak değer yazan
// bir SETTER'dır ve adet alanına yanlışlıkla sıfır gönderen bir programlama
// hatasının sessizce veri silmesi kabul edilemez. Bu akış ise vitrinin niyet
// katmanıdır: her sepet arayüzünde adet seçiciyi sıfıra indirmek "bunu
// kaldır" demektir.
//
// Bu yüzden niyet burada AÇIKÇA çevrilir — sıfır görünce ayrı bir kaldırma
// çağrısı yapılır, modülün kuralı gevşetilmez ve sonuç [UpdateLineItemResult.Removed]
// ile çağırana BİLDİRİLİR. Alternatif, her vitrinin bu dallanmayı kendi
// yazmasıydı; her biri "kaldırdıktan sonra toplamları yeniden hesapla"
// kısmını farklı biçimde unuturdu.
//
// Negatif adet reddedilir (errors.Invalid): negatif adedin hiçbir niyeti
// yoktur ve sıfıra yuvarlanması, işaret hatası taşıyan bir isteğe satır
// sildirirdi.
//
// # Satış kanalı kapsamı burada YENİDEN sorulmaz
//
// Kapsam denetimi giriş kapısındadır ([Workflows.AddLineItem]): kimliğin
// kanallarında görünmeyen bir varyant sepete HİÇ giremez. Bu metot yeni varyant
// sokamaz, yalnızca sepette ZATEN duran bir satırın adedini yazar.
//
// Bunun ölçülmüş bir sonucu vardır ve saklanmıyor: bir ürün sepete girdikten
// SONRA yönetim ucundan başka bir kanala taşınırsa, müşteri o satırın adedini
// artırmaya ve sepeti tamamlamaya devam edebilir. Aynı varyantı yeniden EKLEMEK
// ise reddedilir (404) — giriş kapısı kapalıdır.
//
// Bu bir açık değil, sepetin ANLIK GÖRÜNTÜ olmasının sonucudur ve bilinçlidir:
// alternatifi, bir yöneticinin katalog düzenlemesinin müşterilerin dolu
// sepetlerini ödenemez kılmasıdır. Saldırganın eline geçen bir şey de yoktur —
// satırın sepete girebilmesi için o an kapsamda olması GEREKMİŞTİR ve taşımayı
// yapan taraf saldırgan değil operatördür. Kapsamı satır ömrü boyunca sürekli
// dayatmak isteyen bir kurulum, kapsam kontrolünü tamamlama adımına da koymayı
// seçebilir; o zaman ödenecek bedel yukarıdaki cümledir.
//
// # Toplam hesabı patlarsa
//
// Adet YAZILMIŞTIR ve geri alınmaz; hata [CodeTotalsAfterChange] koduyla
// sarılır. Gerekçe [Workflows.AddLineItem] ile aynıdır.
func (w *Workflows) UpdateLineItem(ctx context.Context, in UpdateLineItemInput) (UpdateLineItemResult, error) {
	if err := requireID("cart_id", in.CartID); err != nil {
		return UpdateLineItemResult{}, err
	}
	if err := requireID("line_item_id", in.LineItemID); err != nil {
		return UpdateLineItemResult{}, err
	}
	if in.Quantity < 0 {
		return UpdateLineItemResult{}, errors.Invalid(CodeInvalidInput,
			"adet negatif olamaz: %d (satırı kaldırmak için 0 verin)", in.Quantity)
	}
	if in.Quantity > MaxQuantity {
		return UpdateLineItemResult{}, errors.Invalid(CodeInvalidInput,
			"the quantity can be at most %d: %d", MaxQuantity, in.Quantity)
	}

	removed := in.Quantity == 0
	var err error
	if removed {
		err = w.carts.RemoveLineItem(ctx, in.CartID, in.LineItemID)
	} else {
		err = w.carts.SetCartLineItemQuantity(ctx, in.CartID, in.LineItemID, in.Quantity)
	}
	if err != nil {
		return UpdateLineItemResult{}, err
	}

	what := "satır adedi güncellendi"
	if removed {
		what = "satır kaldırıldı"
	}

	totals, err := w.CalculateTotals(ctx, in.CartID)
	if err != nil {
		return UpdateLineItemResult{}, totalsAfterChange(err, in.CartID, what)
	}

	return UpdateLineItemResult{
		LineItemID: in.LineItemID,
		Quantity:   in.Quantity,
		Removed:    removed,
		Totals:     totals,
	}, nil
}

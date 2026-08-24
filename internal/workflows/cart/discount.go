package cart

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya sepet hesabının İNDİRİM ayağıdır (plan Faz 7).
//
// Faz 5'te indirim DAİMA SIFIRDI ve alan "promotion devralacak" notuyla
// bırakılmıştı; devralma burada olur. Hesabı promotion modülü yapar, bu paket
// yalnızca sepetin şeklini onun sözleşmesine çevirir, sonucu DOĞRULAR ve
// satırlara yazar.

// attrVariantID indirim kurallarının bakabileceği kalem özniteliğidir.
//
// Sepet satırının varyantı, bu akışın kalem hakkında BİLDİĞİ tek katalog
// gerçeğidir. Ürün ve kategori kimlikleri de kural hedefi olabilir ama sepet
// onları taşımaz; katalogdan okunmaları satır başına ek bir tur demektir ve
// bugün hiçbir kural onları istemiyor. Eklenecekleri gün yer bellidir:
// [Workflows.discountRequestFor] içindeki öznitelik haritası.
const attrVariantID = "variant_id"

// discountRequest promotion modülüne giden indirim isteğinin JSON şemasıdır.
//
// Alan adları promotion'ın interop şemasıyla BİREBİR aynı olmak ZORUNDADIR:
// karşı taraf bilinmeyen alanları REDDEDER ve iki paket birbirini import
// edemediği için derleyici uyumu göremez (ADR 0006'nın kabul edilen bedeli).
// Uyum ancak entegrasyon testiyle kanıtlanabilir.
//
// Tüm tutarlar TAM SAYI minor unit'tir (plan Bölüm 8).
type discountRequest struct {
	// CurrencyCode sepetin para birimidir; sabit tutarlı promosyonların
	// elenmesi buna dayanır.
	CurrencyCode string `json:"currency_code"`
	// Context bağlam kurallarının bakacağı alanlardır.
	Context map[string]string `json:"context"`
	// Items sepetin satırlarıdır ve sepetteki SIRAYLA gider.
	Items []discountRequestItem `json:"items"`
	// ShippingMethods DAİMA BOŞ gider; gerekçe
	// [Workflows.discountRequestFor] godoc'undadır.
	ShippingMethods []discountRequestShipping `json:"shipping_methods"`
	// Codes uygulanacak kupon kodlarıdır ve bu fazda DAİMA BOŞTUR; gerekçe
	// paket yorumundaki "Kupon kodları" başlığındadır.
	Codes []string `json:"codes"`
	// At hesabın anıdır; boş bırakılır ve promotion "şimdi"yi kullanır.
	//
	// Sepet hesabı DAİMA şimdiye aittir: geçmişe dönük bir hesap, bugün
	// bitmiş bir kampanyayı sepette canlı gösterirdi.
	At string `json:"at"`
}

// discountRequestItem istekteki tek bir sepet satırının şemasıdır.
type discountRequestItem struct {
	// ID sepet satırının kimliğidir; indirim aynı kimlikle geri döner.
	ID string `json:"id"`
	// Amount satırın İNDİRİM ÖNCESİ ara toplamıdır (birim × adet).
	Amount int64 `json:"amount"`
	// Quantity satırdaki adettir; "birim başına sabit tutar" indiriminin kaç
	// birime uygulanacağını belirler.
	Quantity int64 `json:"quantity"`
	// Attributes hedef kurallarının bakacağı satır öznitelikleridir.
	Attributes map[string]string `json:"attributes"`
}

// discountRequestShipping istekteki tek bir kargo yönteminin şemasıdır.
//
// Tip yalnızca ŞEMANIN TAM olması için vardır; bu paket hiç kargo yöntemi
// göndermez.
type discountRequestShipping struct {
	// ID kargo yönteminin kimliğidir.
	ID string `json:"id"`
	// Amount kargo tutarıdır (minor unit).
	Amount int64 `json:"amount"`
	// Attributes hedef kurallarının bakacağı özniteliklerdir.
	Attributes map[string]string `json:"attributes"`
}

// discountResponse promotion modülünden dönen indirim sonucunun JSON
// şemasıdır.
//
// Bilinmeyen alanlar SESSİZCE ATLANIR (isteğin tersine): promotion şemasını
// büyüttüğünde bu paketin aynı turda güncellenmesi gerekmemelidir. Sessizlik
// yalnızca TANINMAYAN alanlar içindir — tanınan alanların taşıdığı
// değişmezler [Workflows.applyDiscounts] içinde tek tek DOĞRULANIR.
type discountResponse struct {
	// CurrencyCode hesabın para birimidir (BÜYÜK harf).
	CurrencyCode string `json:"currency_code"`
	// Items kalem başına indirimlerdir; istekteki HER kalem için bir kayıt
	// taşır ve istekle AYNI sıradadır.
	Items []discountLine `json:"items"`
	// ShippingMethods kargo yöntemi başına indirimlerdir; bu paket kargo
	// göndermediği için BOŞ beklenir.
	ShippingMethods []discountLine `json:"shipping_methods"`
	// ItemsDiscountTotal kalemlere düşen toplam indirimdir.
	ItemsDiscountTotal int64 `json:"items_discount_total"`
	// ShippingDiscountTotal kargoya düşen toplam indirimdir; sıfır beklenir.
	ShippingDiscountTotal int64 `json:"shipping_discount_total"`
	// DiscountTotal toplam indirimdir.
	DiscountTotal int64 `json:"discount_total"`
}

// discountLine yanıttaki tek bir satır indiriminin şemasıdır.
type discountLine struct {
	// ID indirimin ait olduğu satırdır.
	ID string `json:"id"`
	// Amount satıra düşen TOPLAM indirimdir (minor unit).
	Amount int64 `json:"amount"`
}

// applyDiscounts satırların indirimini promotion modülünden alır ve satırlara
// YAZAR.
//
// Satırların ara toplamı hesaplanmış olmalıdır; vergi ise HENÜZ
// hesaplanmamıştır. Sıra sözleşmenin kendisidir: vergi tabanı indirim
// SONRASIDIR (bkz. paket yorumu, "Vergi sözleşmesi") ve indirim vergiden önce
// bilinmezse taban yanlış kalırdı.
//
// # promotion kayıtlı DEĞİLSE
//
// İndirim SIFIR kalır ve hesap devam eder. Aynı örüntü product modülünün
// vitrin listelemesinde de vardır (fiyat/stok sağlayıcısı yoksa katalog
// fiyatsız döner): modülerliğin kendisi bunu gerektirir — promotion'ı
// kurmayan bir dağıtımda sepet çalışmalıdır. Yön de güvenlidir; eksik bir
// indirim müşteriden FAZLA tahsil eder ve müşteri bunu görüp söyler. Ters yön
// (eksik vergi) sessizce satıcının cebinden çıkardı ve vergi bu yüzden
// sıfıra düşmez (bkz. [Workflows.applyTaxes]).
//
// Yüzeyin varlığı KURULUŞTA bir kez loglanır ([FromContainer]); burada tur
// başına uyarı üretilmez.
//
// # Dönen sonuç DOĞRULANIR
//
// promotion'ın godoc'u üç değişmez vaat eder: her istek satırı için AYNI
// SIRADA bir yanıt satırı, satır indiriminin satır tutarını aşmaması ve
// toplamların kimliği. Vaat edilen bir şeyi doğrulamak gereksiz görünebilir
// ama sınırın öteki tarafını derleyici denetlemez ve BOZUK bir indirim
// sessizce geçerse sonuç, cart modülünün toplam kontrolüne takılan ya da daha
// kötüsü takılmayıp müşteriye yanlış tutar gösteren bir sepettir. Sözleşme
// ihlali errors.Internal'dır: çağıranın düzeltebileceği bir şey yoktur.
func (w *Workflows) applyDiscounts(ctx context.Context, snap Snapshot, lines []LineTotals) error {
	if w.discounts == nil {
		return nil
	}
	if len(lines) != len(snap.Items) {
		return errors.Internal(CodeDiscountInvalid,
			"satır sayısı anlık görüntüyle uyuşmuyor: %d hesaplanan, %d satır (%s)",
			len(lines), len(snap.Items), snap.ID)
	}

	payload, err := json.Marshal(w.discountRequestFor(snap, lines))
	if err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeDiscountFailed,
			"indirim isteği JSON'a çevrilemedi: %s", snap.ID)
	}

	raw, err := w.discounts.ComputeDiscountsJSON(ctx, payload)
	if err != nil {
		// Sınıf KORUNUR: promotion'ın Invalid'i bir sözleşme uyuşmazlığıdır ve
		// Internal'a çevrilseydi düzeltilebilir bir kablolama hatası sunucu
		// arızası gibi görünürdü.
		return errors.Wrap(err, errors.KindOf(err), CodeDiscountFailed,
			"sepet indirimi hesaplanamadı: %s (%d satır)", snap.ID, len(lines))
	}

	var resp discountResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeDiscountInvalid,
			"indirim sonucu çözülemedi: %s", snap.ID)
	}
	return applyDiscountResponse(snap, lines, resp)
}

// discountRequestFor sepetin şeklini promotion'ın istek şemasına çevirir.
//
// # Kargo yöntemleri GÖNDERİLMEZ
//
// promotion kargoya da indirim uygulayabilir ama [Totals] şemasında kargo
// indirimini taşıyacak bir alan YOKTUR: sepetin indirimi satır indirimlerinin
// toplamıdır ve cart modülü "indirim ara toplamı aşamaz" kuralını ara toplama
// (kargosuz) göre uygular. Kargo indirimini sepet indirimine katmak, ucuz mallı
// ama pahalı kargolu bir sepette indirimi ara toplamın üstüne çıkarır ve cart
// TÜM hesabı reddederdi. [Totals.ShippingTotal] değerinden düşmek ise indirimi
// hiç görünmeyen bir yere saklamak olurdu — müşteri kargonun neden ucuzladığını
// hiçbir satırda okuyamazdı.
//
// Bu yüzden kargo hesaba SOKULMAZ; kargo indirimi, [Totals] bir
// "shipping_discount_total" alanı kazandığı gün açılır ve bağlanacağı yer
// istekteki bu boş dilimdir.
//
// # Bağlama yalnızca bölge konur
//
// Müşteri grubu bağlama KONMAZ; gerekçe fiyat bağlamıyla aynıdır (bkz. paket
// yorumu, "Müşteri segmenti fiyatları"): sepet müşterinin gruplarını bilmez ve
// birini sessizce seçmek indirimi harita dolaşım sırasına bağlardı. Grup
// bağlamı, customer yüzeyi grup listesini yayımladığı gün buraya eklenir.
func (w *Workflows) discountRequestFor(snap Snapshot, lines []LineTotals) discountRequest {
	items := make([]discountRequestItem, 0, len(lines))
	for i := range lines {
		items = append(items, discountRequestItem{
			ID:         lines[i].LineItemID,
			Amount:     lines[i].Subtotal,
			Quantity:   snap.Items[i].Quantity,
			Attributes: map[string]string{attrVariantID: snap.Items[i].VariantID},
		})
	}

	return discountRequest{
		CurrencyCode:    snap.CurrencyCode,
		Context:         map[string]string{attrRegionID: snap.RegionID},
		Items:           items,
		ShippingMethods: []discountRequestShipping{},
		Codes:           []string{},
	}
}

// applyDiscountResponse yanıtı DOĞRULAR ve satırlara yazar.
//
// Doğrulamanın hepsi bir yerde durur ki bir kuralın unutulması gözle
// görülebilsin. Yazma, doğrulamanın TAMAMI geçtikten sonra yapılır: yarı
// yazılmış satırlar, hata dönse bile çağıranın elinde tutarsız bir dilim
// bırakırdı.
func applyDiscountResponse(snap Snapshot, lines []LineTotals, resp discountResponse) error {
	if !strings.EqualFold(resp.CurrencyCode, snap.CurrencyCode) {
		return errors.Internal(CodeDiscountInvalid,
			"indirim başka bir para biriminde hesaplandı: sepet %q, sonuç %q (%s)",
			snap.CurrencyCode, resp.CurrencyCode, snap.ID)
	}
	if len(resp.Items) != len(lines) {
		return errors.Internal(CodeDiscountInvalid,
			"indirim sonucu %d satır için %d kayıt döndürdü (%s)",
			len(lines), len(resp.Items), snap.ID)
	}
	if resp.ShippingDiscountTotal != 0 || len(resp.ShippingMethods) != 0 {
		return errors.Internal(CodeDiscountInvalid,
			"kargo yöntemi gönderilmediği hâlde kargo indirimi döndü: %d (%s)",
			resp.ShippingDiscountTotal, snap.ID)
	}

	var sum int64
	for i := range resp.Items {
		line := resp.Items[i]
		if line.ID != lines[i].LineItemID {
			return errors.Internal(CodeDiscountInvalid,
				"indirim sonucu istekteki sırayı korumadı: %d. kayıt %q, beklenen %q (%s)",
				i, line.ID, lines[i].LineItemID, snap.ID)
		}
		if line.Amount < 0 || line.Amount > lines[i].Subtotal {
			return errors.Internal(CodeDiscountInvalid,
				"satır indirimi [0, %d] aralığında olmalı: %q -> %d (%s)",
				lines[i].Subtotal, line.ID, line.Amount, snap.ID)
		}

		var err error
		if sum, err = addAmount(sum, line.Amount); err != nil {
			return err
		}
	}

	// Sepet indirimi Σ satır indirimidir. promotion aynı kimliği kendi
	// toplamıyla da bildirir; ikisinin ayrışması, satırlara yazılan indirimle
	// sepete yazılanın farklı olması demektir ve cart'ın toplam kontrolü bunu
	// ancak yazma anında yakalardı.
	if sum != resp.ItemsDiscountTotal || resp.DiscountTotal != resp.ItemsDiscountTotal {
		return errors.Internal(CodeDiscountInvalid,
			"indirim toplamı satır indirimleriyle uyuşmuyor: Σ=%d, kalem toplamı=%d, genel toplam=%d (%s)",
			sum, resp.ItemsDiscountTotal, resp.DiscountTotal, snap.ID)
	}

	for i := range resp.Items {
		lines[i].DiscountTotal = resp.Items[i].Amount
	}
	return nil
}

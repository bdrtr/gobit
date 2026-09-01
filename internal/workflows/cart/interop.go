package cart

import (
	"context"
	"encoding/json"
)

// InteropName sepet akışlarının container'daki adıdır (ADR 0001/0006).
//
// Adı bu paket bildirir ama kaydı BİLEŞİM KÖKÜ yapar (cmd/server): akışlar
// kendi bağımlılıklarını yine container'dan çözer ve ancak tüm modüller
// Register olduktan SONRA kurulabilirler — bir modülün Register'ında kurulmaya
// çalışılsalardı henüz var olmayan servisleri arıyor olurlardı.
//
// Tüketici cart MODÜLÜDÜR: vitrin satır uçlarının sahibi odur ve akışı bu adla
// çözer (bkz. cart modülündeki LinePricingName). Modül bu paketi import
// edemediği için ad orada DİZE olarak tekrarlanır; tekrarın bedeli izolasyonun
// kabul edilen bedelidir ve yazım hatası sessiz kalmaz — ad çözülemezse satır
// ekleme ucu KAPALI arızalanır.
const InteropName = "workflows.cart.interop"

// Interop sepet akışlarını modüller arası İLKEL yüzeye çevirir.
//
// # Neden ayrı bir tip
//
// [Workflows]'un imzaları bu paketin kendi tiplerini kullanır
// ([AddLineItemInput], [Totals] …) ve o tipleri hiçbir modül ADLANDIRAMAZ:
// modüller internal/workflows'u import etmez (ADR 0006, her iki yönde de).
// Tüketici tarafında tanımlanan dar bir arayüzün yapısal olarak
// karşılanabilmesi için imzaların yalnızca İLKEL ve stdlib tiplerinden
// oluşması gerekir; bu tip tam olarak o çeviriyi yapar ve başka hiçbir şey
// yapmaz. Modüllerin interop.go dosyalarıyla aynı örüntüdür.
//
// # Neden akışların TAMAMI değil
//
// Yüzey yalnızca vitrinin HTTP ucu olan iki akışı taşır. [Workflows.CreateCart]
// ve [Workflows.CalculateTotals] burada YOKTUR: birincisinin vitrin ucu bugün
// sepet modülünün kendi servisine bağlıdır, ikincisi ise HTTP'ye açılan bir
// yetenek değildir — hesabı istemcinin istediği anda çalıştırmak, tutarı
// istemcinin zamanlamasına bağlardı. Kullanılmayan metotları buraya yazmak,
// tüketicisi olmayan bir sözleşme üretmek olurdu.
type Interop struct {
	w *Workflows
}

// NewInterop verilen akışlar için modüller arası yüzeyi kurar.
func NewInterop(w *Workflows) *Interop { return &Interop{w: w} }

// AddPricedLineItem sepete satır ekler ve satırın KİMLİĞİNİ döner.
//
// Birim fiyatı ÇAĞIRAN VERMEZ: fiyat, varyantın fiyat kümesinden ve sepetin
// para biriminden bu akış tarafından belirlenir (bkz. [Workflows.AddLineItem]).
// Yüzeyin fiyat parametresi olmaması bilinçlidir ve bu değişikliğin çekirdek
// gerekçesidir — parametre olsaydı, çağıranın onu doldurmasının önünde hiçbir
// şey kalmazdı.
//
// metadata satıra iliştirilecek serbest JSON nesnesidir; boş bırakılabilir.
//
// Satır eklendikten sonra sepet toplamları YENİDEN HESAPLANIR. Hesap patlarsa
// satır yazılmış olarak kalır ve hata [CodeTotalsAfterChange] koduyla döner;
// çağıran isteği TEKRARLAMAMALIDIR (satır ikinci kez eklenirdi).
func (i *Interop) AddPricedLineItem(
	ctx context.Context,
	cartID, variantID string,
	quantity int64,
	metadata json.RawMessage,
) (string, error) {
	sonuc, err := i.w.AddLineItem(ctx, AddLineItemInput{
		CartID:    cartID,
		VariantID: variantID,
		Quantity:  quantity,
		Metadata:  metadata,
	})
	if err != nil {
		return "", err
	}
	return sonuc.LineItemID, nil
}

// SetLineItemQuantity satırın adedini MUTLAK değerle yazar ve toplamları
// yeniden hesaplar; satırın kaldırılıp kaldırılmadığını bildirir.
//
// Sıfır adet satırı KALDIRIR ve dönen ilk değer bunu söyler; gerekçe
// [Workflows.UpdateLineItem] godoc'undadır. Negatif adet reddedilir.
//
// Adet değiştiğinde fiyat da değişebilir (pricing fiyatı adet aralığına göre
// seçer), bu yüzden yeniden hesap bir kolaylık değil GEREKLİLİKTİR: adedi
// yazıp hesabı çalıştırmamak, satırı eski kademenin fiyatıyla bırakırdı.
func (i *Interop) SetLineItemQuantity(
	ctx context.Context,
	cartID, lineItemID string,
	quantity int64,
) (bool, error) {
	sonuc, err := i.w.UpdateLineItem(ctx, UpdateLineItemInput{
		CartID:     cartID,
		LineItemID: lineItemID,
		Quantity:   quantity,
	})
	if err != nil {
		return false, err
	}
	return sonuc.Removed, nil
}

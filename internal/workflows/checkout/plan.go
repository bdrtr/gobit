package checkout

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Snapshot siparişe dönüşecek sepet şeklidir.
//
// Tip, [Carts.CartSnapshotJSON] gövdesinin şemasıdır: sepet modülü bu alanları
// üretir, bu paket okur. Şema BİLİNÇLİ OLARAK dardır — siparişe giren ne varsa
// odur ve fazlası yoktur. Tanınmayan alanlar (kargo yöntemleri gibi) sessizce
// atlanır ki sepet modülü şemayı büyüttüğünde bu paketin güncellenmesi
// gerekmesin.
//
// Satırların TUTARLARI burada yoktur ve olmamalıdır; onları hesap üretir
// (bkz. [CartTotals]). İki kaynağın aynı ana ait olduğu [Snapshot.Revision]
// ile kanıtlanır.
type Snapshot struct {
	// ID sepetin kimliğidir.
	ID string `json:"id"`
	// RegionID sepetin bölgesidir; sipariş de aynı bölgeye yazılır.
	RegionID string `json:"region_id"`
	// CustomerID sepetin sahibidir; boşsa sipariş misafirindir.
	CustomerID string `json:"customer_id"`
	// CurrencyCode sepetin para birimidir (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Revision sepetin şekil sayacıdır; hesabın damgası budur.
	Revision int64 `json:"revision"`
	// Completed sepetin tamamlanmış olup olmadığını bildirir.
	Completed bool `json:"completed"`
	// Items sepetin satırlarıdır.
	Items []SnapshotItem `json:"items"`
}

// SnapshotItem bir sepet satırının siparişe giren alanlarıdır.
type SnapshotItem struct {
	// ID satırın kimliğidir; rezervasyon bu kimliğe bağlanır.
	ID string `json:"id"`
	// VariantID satırın gösterdiği ürün varyantıdır.
	VariantID string `json:"variant_id"`
	// Quantity satırdaki adettir.
	Quantity int64 `json:"quantity"`
}

// VariantIDs satırların varyant kimliklerini TEKRARSIZ ve satır sırasında
// döner.
//
// Sıra korunur ki toplu link ve katalog sorgularının girdisi (dolayısıyla
// üretilen hata mesajları) yeniden üretilebilir olsun; tekrarsızlık ise aynı
// varyanttan iki satır bulunan bir sepette sorguyu gereksiz büyütmemek içindir.
func (s Snapshot) VariantIDs() []string {
	seen := make(map[string]struct{}, len(s.Items))
	out := make([]string, 0, len(s.Items))
	for i := range s.Items {
		id := s.Items[i].VariantID
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// checkoutPlan saga'nın DEĞİŞMEZ girdisidir: hazırlık aşamasında çözülmüş her
// şey burada durur.
//
// Plan motora girdi olarak verilir ve yürütme kaydına JSON olarak yazılır;
// elle müdahale gerektiren bir yürütmede operatörün "ne yapılmak isteniyordu"
// sorusunun cevabı odur. Adımlar plana İŞARETÇİ ile erişir ve onu DEĞİŞTİRMEZ;
// adımlar arası akan tek şey [workflow.StepContext].Shared'dır.
type checkoutPlan struct {
	// CartID siparişin doğduğu sepettir.
	CartID string `json:"cart_id"`
	// RegionID siparişin bölgesidir.
	RegionID string `json:"region_id"`
	// CustomerID siparişin sahibidir; misafir siparişinde boştur.
	CustomerID string `json:"customer_id"`
	// Email siparişin iletişim adresidir; boş olabilir.
	Email string `json:"email"`
	// CurrencyCode siparişin para birimidir (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Revision hesabın ve anlık görüntünün ORTAK şekil sayacıdır.
	Revision int64 `json:"revision"`
	// LocationID stoğun ayrılacağı lokasyondur.
	LocationID string `json:"location_id"`
	// PaymentProviderID ödemenin açılacağı sağlayıcıdır.
	PaymentProviderID string `json:"payment_provider_id"`
	// Amount tahsil edilecek toplam tutardır (minor unit).
	Amount int64 `json:"amount"`
	// Subtotal satır ara toplamlarının toplamıdır.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal toplam indirimdir; pozitif taşınır ve toplamdan düşülür.
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal toplam vergidir.
	TaxTotal int64 `json:"tax_total"`
	// ShippingTotal toplam kargo tutarıdır.
	ShippingTotal int64 `json:"shipping_total"`
	// Lines siparişe ve rezervasyona girecek satırlardır.
	Lines []planLine `json:"lines"`

	// PaymentData sağlayıcıya iletilecek serbest veridir ve KAYDA YAZILMAZ.
	//
	// Alan kart tokenı gibi hassas veri taşıyabilir; yürütme kaydı ise kalıcı
	// bir defterdir ve elle müdahale sırasında okunur. Plan Bölüm 8 hassas
	// verinin taşınmamasını ister, bu yüzden alan JSON'dan DIŞLANIR ve yalnızca
	// bellekte, adımın çağrısına kadar yaşar.
	PaymentData json.RawMessage `json:"-"`
}

// planLine bir sepet satırının siparişe ve rezervasyona giren hâlidir.
type planLine struct {
	// LineItemID sepet satırının kimliğidir; rezervasyon buna bağlanır.
	LineItemID string `json:"line_item_id"`
	// VariantID satırın gösterdiği ürün varyantıdır.
	VariantID string `json:"variant_id"`
	// InventoryItemID varyantın bağlı olduğu stok kalemidir.
	InventoryItemID string `json:"inventory_item_id"`
	// Title satırın görünen adıdır; katalogdan KOPYALANIR.
	Title string `json:"title"`
	// Quantity satırdaki adettir.
	Quantity int64 `json:"quantity"`
	// UnitPrice birim fiyattır (minor unit).
	UnitPrice int64 `json:"unit_price"`
	// Subtotal satırın ara toplamıdır: UnitPrice × Quantity.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal satıra düşen indirimdir; pozitif taşınır.
	DiscountTotal int64 `json:"discount_total"`
	// TaxTotal satıra düşen vergidir.
	TaxTotal int64 `json:"tax_total"`
	// Total satırın toplamıdır: Subtotal - DiscountTotal + TaxTotal.
	Total int64 `json:"total"`
}

// prepare saga'nın girdisini kurar ve HİÇBİR geri alınabilir yan etki bırakmaz.
//
// Sıra bilinçlidir:
//
//  1. Hesap YENİLENİR. İki şey için zorunludur: siparişin satır başına tutara
//     ihtiyacı vardır ve sepet modülü BAYAT toplamlı bir sepeti tamamlamayı
//     reddeder. Hesap saga'nın son adımında yenilenseydi, para çekildikten
//     sonra düşen bir MarkCompleted ile karşılaşırdık.
//  2. Anlık görüntü hesaptan SONRA okunur ve iki tarafın şekil sayacı
//     karşılaştırılır. Eşit değilse sepet ikisinin arasında değişmiştir ve
//     hesap artık o sepete ait değildir; çağrı errors.Conflict ile durur.
//  3. Başlıklar ve stok kalemleri TOPLU okunur (N+1 yoktur).
//
// Yazma sayılabilecek tek işlem hesabın sepete yazılmasıdır ve o telafi
// GEREKTİRMEZ: toplam yazmak idempotenttir, bayatlık zaten görünür bir
// durumdur ve müşterinin sepetini geçici bir arıza yüzünden eski tutara
// döndürmek hiçbir şeyi düzeltmezdi.
func (w *Workflows) prepare(ctx context.Context, in CompleteCartInput) (*checkoutPlan, error) {
	totals, err := w.totals.CalculateTotals(ctx, in.CartID)
	if err != nil {
		return nil, err
	}

	snap, err := w.snapshot(ctx, in.CartID)
	if err != nil {
		return nil, err
	}
	if snap.Completed {
		return nil, errors.Conflict(CodeCartCompleted,
			"tamamlanmış sepetten sipariş oluşturulamaz: %s", in.CartID)
	}
	if len(snap.Items) == 0 {
		return nil, errors.Conflict(CodeCartEmpty,
			"satırsız sepetten sipariş oluşturulamaz: %s", in.CartID)
	}
	if snap.Revision != totals.Revision {
		return nil, errors.Conflict(CodeCartChanged,
			"sepet hesap ile okuma arasında değişti: %s (hesap %d, sepet %d); istek yeniden gönderilmeli",
			in.CartID, totals.Revision, snap.Revision)
	}

	lines, err := w.planLines(ctx, snap, totals)
	if err != nil {
		return nil, err
	}

	plan := &checkoutPlan{
		CartID:            snap.ID,
		RegionID:          snap.RegionID,
		CustomerID:        snap.CustomerID,
		Email:             in.Email,
		CurrencyCode:      snap.CurrencyCode,
		Revision:          snap.Revision,
		LocationID:        in.LocationID,
		PaymentProviderID: in.PaymentProviderID,
		Amount:            totals.Total,
		Subtotal:          totals.Subtotal,
		DiscountTotal:     totals.DiscountTotal,
		TaxTotal:          totals.TaxTotal,
		ShippingTotal:     totals.ShippingTotal,
		Lines:             lines,
		PaymentData:       in.PaymentData,
	}
	if err := plan.validate(); err != nil {
		return nil, err
	}
	if in.ExpectedTotal > 0 && in.ExpectedTotal != plan.Amount {
		return nil, errors.Conflict(CodeTotalMismatch,
			"sepetin tutarı onaylanan tutardan farklı: onaylanan %d, hesaplanan %d (%s)",
			in.ExpectedTotal, plan.Amount, plan.CartID)
	}
	return plan, nil
}

// snapshot sepetin anlık görüntüsünü okur ve çözer.
func (w *Workflows) snapshot(ctx context.Context, cartID string) (Snapshot, error) {
	payload, err := w.carts.CartSnapshotJSON(ctx, cartID)
	if err != nil {
		return Snapshot{}, err
	}
	return decodeSnapshot(cartID, payload)
}

// decodeSnapshot sepet modülünden gelen gövdeyi çözer ve DOĞRULAR.
//
// Doğrulama, gövdenin sepet modülünden gelmesine rağmen yapılır: bu sınır
// derleyicinin denetleyemediği tek sınırdır (ADR 0006'nın kabul edilen bedeli)
// ve bozuk bir alan sessizce siparişin içine girerse hata, müşterinin
// faturasında görünürdü. Bozuk gövde errors.Internal'dır — çağıranın
// düzeltebileceği bir şey yoktur, sağlayıcı sözleşmeyi çiğnemiştir.
func decodeSnapshot(cartID string, payload json.RawMessage) (Snapshot, error) {
	var snap Snapshot
	if len(payload) == 0 {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
			"sepet anlık görüntüsü boş geldi: %s", cartID)
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		return Snapshot{}, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"sepet anlık görüntüsü çözülemedi: %s", cartID)
	}
	if snap.ID != cartID {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
			"anlık görüntü başka bir sepete ait: istenen %s, gelen %q", cartID, snap.ID)
	}
	if snap.RegionID == "" {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid, "sepetin bölgesi boş: %s", cartID)
	}
	if snap.CurrencyCode == "" {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid, "sepetin para birimi boş: %s", cartID)
	}

	for i := range snap.Items {
		if snap.Items[i].ID == "" {
			return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
				"sepette kimliksiz satır var: %s", cartID)
		}
		if snap.Items[i].VariantID == "" {
			return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
				"satırın varyantı boş: %s (%q)", cartID, snap.Items[i].ID)
		}
	}
	return snap, nil
}

// planLines anlık görüntü ile hesabı BİRLEŞTİRİR.
//
// Birleştirme satır kimliği üzerinden yapılır; hesabın satır sırasına
// güvenilmez. Sepette olup hesapta bulunmayan bir satır errors.Internal'dır:
// hesap sepetin TÜM satırlarını kapsamak zorundadır (bkz. cart modülünde
// SetTotals) ve eksik bir satır, müşterinin ödemediği bir mal demektir.
func (w *Workflows) planLines(ctx context.Context, snap Snapshot, totals cartwf.Totals) ([]planLine, error) {
	byLine := make(map[string]cartwf.LineTotals, len(totals.Lines))
	for i := range totals.Lines {
		byLine[totals.Lines[i].LineItemID] = totals.Lines[i]
	}
	if len(byLine) != len(snap.Items) {
		return nil, errors.Internal(CodeTotalsInvalid,
			"hesap sepetin satırlarını kapsamıyor: %s (sepet %d satır, hesap %d satır)",
			snap.ID, len(snap.Items), len(byLine))
	}

	variantIDs := snap.VariantIDs()
	titles, err := w.variantTitles(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	items, err := w.inventoryItems(ctx, variantIDs)
	if err != nil {
		return nil, err
	}

	lines := make([]planLine, 0, len(snap.Items))
	for i := range snap.Items {
		item := snap.Items[i]
		amounts, ok := byLine[item.ID]
		if !ok {
			return nil, errors.Internal(CodeTotalsInvalid,
				"satırın hesabı yok: %s (%q)", snap.ID, item.ID)
		}

		lines = append(lines, planLine{
			LineItemID:      item.ID,
			VariantID:       item.VariantID,
			InventoryItemID: items[item.VariantID],
			Title:           titles[item.VariantID],
			Quantity:        item.Quantity,
			UnitPrice:       amounts.UnitPrice,
			Subtotal:        amounts.Subtotal,
			DiscountTotal:   amounts.DiscountTotal,
			TaxTotal:        amounts.TaxTotal,
			Total:           amounts.Total,
		})
	}
	return lines, nil
}

// variantTitles varyantların katalogdaki başlıklarını TEK sorguda okur.
//
// # Neden başlık katalogdan okunuyor
//
// Sipariş satırının başlığı ZORUNLUDUR ve varyanttan KOPYALANIR: katalog
// sonradan değişse bile siparişte görülen ad değişmez. Sepet modülü başlığı
// kendi satırında saklar ama modüller arası yüzeyinde yayımlamaz, order modülü
// ise product'ı tanımaz; kopyalayabilecek tek taraf bu akıştır.
//
// Okuma Query üzerinden yapılır çünkü product servisinin okuma imzaları kendi
// model tipleriyle konuşur ve modüller arası çağrıya kapalıdır; Query tam bu
// boşluk için vardır (ADR 0004).
func (w *Workflows) variantTitles(ctx context.Context, variantIDs []string) (map[string]string, error) {
	if len(variantIDs) == 0 {
		return map[string]string{}, nil
	}

	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{query.IDField, FieldTitle},
		Filters: map[string]any{FilterIDs: variantIDs},
		Limit:   len(variantIDs),
	})
	if err != nil {
		// Altyapı arızası İŞ durumu gibi raporlanmaz: "varyant katalogda yok"
		// kalıcı bir durumdur ve istemci ona göre dallanır, geçici bir okuma
		// arızası ise yeniden denenebilir. Alttaki hatanın sınıfı KORUNUR.
		return nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"varyantlar katalogdan okunamadı (%d varyant)", len(variantIDs))
	}

	titles := make(map[string]string, len(records))
	for i := range records {
		id, idOK := records[i][query.IDField].(string)
		title, titleOK := records[i][FieldTitle].(string)
		if !idOK || !titleOK || title == "" {
			return nil, errors.Internal(CodeVariantUnknown,
				"katalog kaydı okunamadı: %v", records[i])
		}
		titles[id] = title
	}

	for _, variantID := range variantIDs {
		if titles[variantID] == "" {
			return nil, errors.NotFound(CodeVariantUnknown,
				"%s varyantı katalogda yok; sipariş satırı başlıksız yazılamaz", variantID)
		}
	}
	return titles, nil
}

// inventoryItems varyantların stok kalemlerini TEK link sorgusuyla çözer.
//
// # Stok kalemi olmayan varyant REDDEDİLİR
//
// Karar errors.Invalid'dir. Stok kalemi olmayan bir varyant için rezervasyon
// açılamaz; onu sessizce ATLAMAK, stoğu hiç ayrılmamış bir malın satılması
// demek olurdu. Hata NotFound değildir çünkü varyant VARDIR; eksik olan, stok
// takibine bağlanmış olmasıdır ve çağıran isteği düzeltebilir.
//
// # Birden çok kalem
//
// "product_variant_inventory" tanımı tekildir. Yine de birden çok kalem
// görülürse hangisinden stok ayrılacağı belirsizdir; sessizce ilkini seçmek
// satılan malı sıralama tesadüfüne bağlardı. Bu yüzden durum errors.Internal
// ile bildirilir: veri, kısıtın arkasından bozulmuştur.
func (w *Workflows) inventoryItems(ctx context.Context, variantIDs []string) (map[string]string, error) {
	if len(variantIDs) == 0 {
		return map[string]string{}, nil
	}

	linked, err := w.links.ListMany(ctx, LinkVariantInventory, variantIDs)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeLinkReadFailed,
			"%q bağı okunamadı (%d varyant)", LinkVariantInventory, len(variantIDs))
	}

	out := make(map[string]string, len(variantIDs))
	for _, variantID := range variantIDs {
		items := linked[variantID]
		switch len(items) {
		case 0:
			return nil, errors.Invalid(CodeVariantNotStocked,
				"%s varyantı hiçbir stok kalemine bağlı değil; stoğu ayrılamayan ürün sipariş edilemez",
				variantID)
		case 1:
			out[variantID] = items[0]
		default:
			return nil, errors.Internal(CodeVariantInventoryAmbiguous,
				"%s varyantı %d stok kalemine bağlı görünüyor; %q tanımı tekil olmalı",
				variantID, len(items), LinkVariantInventory)
		}
	}
	return out, nil
}

// validate planın aritmetiğini ve sınırlarını doğrular.
//
// Doğrulama order modülünde de yapılır ve tekrar BİLİNÇLİDİR: oradaki denetim
// stok ayrıldıktan sonra çalışır, buradaki ise HİÇBİR yan etki uygulanmadan.
// Bozuk bir hesabın bedeli, ayrılıp geri bırakılan stok ve boşuna açılmış bir
// yürütme kaydı olmamalıdır.
//
// Denetlenen kimlikler şunlardır: satır ara toplamı = birim fiyat × adet,
// satır toplamı = ara toplam - indirim + vergi, sepetin ara toplamı satırların
// ara toplamlarının toplamı ve tahsil edilecek tutar = ara toplam - indirim +
// vergi + kargo.
//
// # Kimlik sınanmadan ÖNCE her terim aralığa sokulur
//
// Sepet düzeyindeki indirim ve vergi de satır düzeyindekiler gibi
// [checkAmount]'tan geçer ve bu ZORUNLUDUR: kimlik ham int64 aritmetiğiyle
// sınanır, yani denetlenmemiş terimlerle kendi kendini doğrulayan bir hesap
// üretilebilir. İki somut kaçak vardı — negatif bir sepet indirimi kimliği
// bozmadan tahsil edilecek tutarı ŞİŞİRİYOR (2500 - (-100000) + … müşteriden
// fazla çekiliyor), taşan bir vergi ile taşan bir indirim ise birbirini
// götürüp kimliği "sağlıyor" ve sipariş MaxInt64 vergiyle açılıyordu.
// Her terim [0, MaxTotal] aralığına sokulduğunda dört terimin toplamı en fazla
// 3 × 10^18 olur ve int64'e sığar; taşma yapısal olarak imkânsızlaşır.
func (p *checkoutPlan) validate() error {
	if len(p.Lines) == 0 {
		return errors.Conflict(CodeCartEmpty,
			"satırsız sepetten sipariş oluşturulamaz: %s", p.CartID)
	}

	var subtotal int64
	for i := range p.Lines {
		line := p.Lines[i]
		if line.Quantity < MinQuantity || line.Quantity > MaxQuantity {
			return errors.Internal(CodeAmountInvalid,
				"satır adedi [%d, %d] aralığında olmalı: %s -> %d",
				MinQuantity, MaxQuantity, line.LineItemID, line.Quantity)
		}
		if err := checkAmount("unit_price", line.UnitPrice, MaxAmount); err != nil {
			return err
		}
		if err := checkAmount("line_tax_total", line.TaxTotal, MaxTotal); err != nil {
			return err
		}
		if err := checkAmount("line_discount_total", line.DiscountTotal, MaxTotal); err != nil {
			return err
		}

		expected, err := mulAmount(line.UnitPrice, line.Quantity)
		if err != nil {
			return err
		}
		if expected != line.Subtotal {
			return errors.Internal(CodeAmountInvalid,
				"satır ara toplamı birim fiyat × adet değil: %s (%d × %d ≠ %d)",
				line.LineItemID, line.UnitPrice, line.Quantity, line.Subtotal)
		}
		if line.Total != line.Subtotal-line.DiscountTotal+line.TaxTotal {
			return errors.Internal(CodeAmountInvalid,
				"satır toplamı kimliği sağlamıyor: %s (%d ≠ %d - %d + %d)",
				line.LineItemID, line.Total, line.Subtotal, line.DiscountTotal, line.TaxTotal)
		}

		subtotal, err = addAmount(subtotal, line.Subtotal)
		if err != nil {
			return err
		}
	}

	if subtotal != p.Subtotal {
		return errors.Internal(CodeAmountInvalid,
			"sepetin ara toplamı satırların ara toplamı değil: %s (%d ≠ %d)",
			p.CartID, p.Subtotal, subtotal)
	}
	if err := checkAmount("discount_total", p.DiscountTotal, MaxTotal); err != nil {
		return err
	}
	if err := checkAmount("tax_total", p.TaxTotal, MaxTotal); err != nil {
		return err
	}
	if err := checkAmount("shipping_total", p.ShippingTotal, MaxTotal); err != nil {
		return err
	}
	if p.Amount != p.Subtotal-p.DiscountTotal+p.TaxTotal+p.ShippingTotal {
		return errors.Internal(CodeAmountInvalid,
			"sepetin toplam kimliği sağlamıyor: %s (%d ≠ %d - %d + %d + %d)",
			p.CartID, p.Amount, p.Subtotal, p.DiscountTotal, p.TaxTotal, p.ShippingTotal)
	}
	if p.Amount <= 0 {
		// payment modülü sıfır tutarlı koleksiyonu reddeder ve haklıdır: hiçbir
		// zaman "captured" olamayacak bir koleksiyon, sonsuza kadar ödeme
		// bekleyen ölü bir kayıttır. Bedelsiz sipariş (tamamı indirimli sepet)
		// ödeme adımı OLMAYAN ayrı bir akıştır ve plan Faz 7+'ya aittir.
		return errors.Invalid(CodeAmountInvalid,
			"tahsil edilecek tutar pozitif olmalı: %s -> %d", p.CartID, p.Amount)
	}
	return checkAmount("amount", p.Amount, MaxTotal)
}

// orderSnapshot siparişe dönüşecek görüntünün JSON şemasıdır.
//
// Alan adları order modülünün beklediği şemayla BİREBİR aynı olmak
// ZORUNDADIR; bu paket o modülü import edemediği için derleyici uyumu
// denetleyemez ve uyum ancak entegrasyon testiyle kanıtlanabilir (ADR 0006'nın
// kabul edilen bedeli).
type orderSnapshot struct {
	CartID         string              `json:"cart_id"`
	RegionID       string              `json:"region_id"`
	CustomerID     string              `json:"customer_id"`
	Email          string              `json:"email"`
	CurrencyCode   string              `json:"currency_code"`
	IdempotencyKey string              `json:"idempotency_key"`
	Subtotal       int64               `json:"subtotal"`
	DiscountTotal  int64               `json:"discount_total"`
	TaxTotal       int64               `json:"tax_total"`
	ShippingTotal  int64               `json:"shipping_total"`
	Total          int64               `json:"total"`
	Items          []orderSnapshotItem `json:"items"`
}

// orderSnapshotItem bir sipariş satırının JSON şemasıdır.
type orderSnapshotItem struct {
	VariantID     string `json:"variant_id"`
	Title         string `json:"title"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// orderSnapshotJSON planı siparişin beklediği gövdeye çevirir.
//
// idempotencyKey yürütmenin kimliğidir: aynı yürütmede tekrarlanan bir çağrı
// yeni sipariş açmaz, mevcut siparişin kimliğini döner. Yeni bir yürütme yeni
// bir kimlik alır, yani telafi edilmiş bir denemeden sonra başlatılan akış
// yeni bir sipariş açabilir.
func (p *checkoutPlan) orderSnapshotJSON(idempotencyKey string) (json.RawMessage, error) {
	items := make([]orderSnapshotItem, 0, len(p.Lines))
	for i := range p.Lines {
		items = append(items, orderSnapshotItem{
			VariantID:     p.Lines[i].VariantID,
			Title:         p.Lines[i].Title,
			Quantity:      p.Lines[i].Quantity,
			UnitPrice:     p.Lines[i].UnitPrice,
			Subtotal:      p.Lines[i].Subtotal,
			DiscountTotal: p.Lines[i].DiscountTotal,
			TaxTotal:      p.Lines[i].TaxTotal,
			Total:         p.Lines[i].Total,
		})
	}

	payload, err := json.Marshal(orderSnapshot{
		CartID:         p.CartID,
		RegionID:       p.RegionID,
		CustomerID:     p.CustomerID,
		Email:          p.Email,
		CurrencyCode:   p.CurrencyCode,
		IdempotencyKey: idempotencyKey,
		Subtotal:       p.Subtotal,
		DiscountTotal:  p.DiscountTotal,
		TaxTotal:       p.TaxTotal,
		ShippingTotal:  p.ShippingTotal,
		Total:          p.Amount,
		Items:          items,
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"sipariş görüntüsü JSON'a çevrilemedi: %s", p.CartID)
	}
	return payload, nil
}

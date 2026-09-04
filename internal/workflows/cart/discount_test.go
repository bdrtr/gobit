package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// twoLineCart iki satırlı bir anlık görüntü üretir (A: 2 adet, B: 3 adet).
//
// Ara toplamlar 2000 ve 750'dir; testlerin çoğu bu iki sayı üzerinden konuşur.
func twoLineCart(revision int64) Snapshot {
	return snapshotOf(revision, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 2},
		{ID: testLineB, VariantID: testVariantB, Quantity: 3},
	}, nil)
}

// TestCalculateTotalsIndirimSatirlaraVeSepeteYazilir promotion'ın kalem başına
// verdiği indirimin hem satırlara hem sepete işlendiğini doğrular.
func TestCalculateTotalsIndirimSatirlaraVeSepeteYazilir(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 500, testLineB: 100}
	serveSnapshot(h.carts, twoLineCart(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, totals.Lines, 2)
	assert.Equal(t, int64(500), totals.Lines[0].DiscountTotal)
	assert.Equal(t, int64(100), totals.Lines[1].DiscountTotal)
	assert.Equal(t, int64(600), totals.DiscountTotal)
	assert.Equal(t, int64(2750), totals.Subtotal)

	// Vergi indirim SONRASI tabandan: (2000-500)×%20 = 300, (750-100)×%20 = 130.
	assert.Equal(t, int64(300), totals.Lines[0].TaxTotal)
	assert.Equal(t, int64(130), totals.Lines[1].TaxTotal)
	assert.Equal(t, int64(430), totals.TaxTotal)
	assert.Equal(t, int64(2580), totals.Total, "2750 - 600 + 430")
	requireIdentity(t, totals)
}

// TestCalculateTotalsVergiTabaniIndirimSonrasidir tabanın indirim ÖNCESİ
// tutar olmadığını AYIRT EDİCİ biçimde kanıtlar.
//
// İndirim öncesi taban vergilenseydi vergi 400 çıkardı; sonrası vergilendiğinde
// 300'dür. İki sayının farkı, sözleşmenin hangi dalının uygulandığını tek
// başına gösterir.
func TestCalculateTotalsVergiTabaniIndirimSonrasidir(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 500}
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.taxes.requests, 1)
	require.Len(t, h.taxes.requests[0].Items, 1)
	assert.Equal(t, int64(1500), h.taxes.requests[0].Items[0].Amount,
		"vergi tabanı satır ara toplamı EKSİ satır indirimidir")

	assert.Equal(t, int64(300), totals.TaxTotal)
	assert.NotEqual(t, int64(400), totals.TaxTotal, "indirim öncesi taban vergilenmemeli")
	requireIdentity(t, totals)
}

// TestCalculateTotalsPromotionKayitsizsaIndirimSifir promotion yüzeyi
// kayıtlı değilken hesabın DÜŞMEDİĞİNİ ve indirimin sıfır kaldığını doğrular.
func TestCalculateTotalsPromotionKayitsizsaIndirimSifir(t *testing.T) {
	h := newHarnessWith(t, nil, newStubTaxes())
	serveSnapshot(h.carts, twoLineCart(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Zero(t, totals.DiscountTotal, "promotion yoksa indirim üretecek kaynak da yoktur")
	for _, line := range totals.Lines {
		assert.Zero(t, line.DiscountTotal)
	}
	// Vitrin çalışmaya devam eder: vergi ve toplam hesaplanmıştır.
	assert.Equal(t, int64(550), totals.TaxTotal)
	assert.Equal(t, int64(3300), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsIndirimIstegininSekli promotion'a giden gövdenin
// sözleşmeye uyduğunu doğrular.
//
// Gövdeyi tek tek denetlemek gereklidir: karşı taraf bilinmeyen alanı REDDEDER,
// eksik alanı ise sessizce sıfır sayar ve sessiz olan hâl testsiz kalırsa
// üretimde indirimsiz bir sepet olarak görünür.
func TestCalculateTotalsIndirimIstegininSekli(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.discounts.requests, 1)
	req := h.discounts.requests[0]
	assert.Equal(t, testCurrency, req.CurrencyCode)
	assert.Equal(t, map[string]string{attrRegionID: testRegionID}, req.Context)
	require.Len(t, req.Items, 2)
	assert.Equal(t, discountRequestItem{
		ID:         testLineA,
		Amount:     2000,
		Quantity:   2,
		Attributes: map[string]string{attrVariantID: testVariantA},
	}, req.Items[0], "kalem tutarı İNDİRİM ÖNCESİ ara toplamdır ve adet kademe için taşınır")
	assert.Equal(t, testLineB, req.Items[1].ID, "sıra sepetin sırasıdır")
}

// TestCalculateTotalsKuponKoduGonderilmez sepette kupon alanı olmadığı için
// hiçbir kodun gönderilmediğini SABİTLER.
//
// Karar paket yorumundaki "Kupon kodları" başlığındadır: yalnızca OTOMATİK
// promosyonlar uygulanır. Test o kararın bekçisidir — biri CalculateTotals'a
// kod parametresi eklerse burası düşer ve kararın yeniden verilmesi gerekir.
func TestCalculateTotalsKuponKoduGonderilmez(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.discounts.requests, 1)
	assert.Empty(t, h.discounts.requests[0].Codes)
	assert.Empty(t, h.discounts.requests[0].At, "hesap anı DAİMA şimdidir")
}

// TestCalculateTotalsKargoIndirimeGonderilmez kargo yöntemlerinin promotion'a
// hiç gitmediğini doğrular.
//
// Gerekçe [Workflows.discountRequestFor] godoc'undadır: [Totals] şeması kargo
// indirimini taşıyamaz ve sepet indirimine katmak cart'ın "indirim ara toplamı
// aşamaz" kuralını ihlal edebilirdi.
func TestCalculateTotalsKargoIndirimeGonderilmez(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 4990}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.discounts.requests, 1)
	assert.Empty(t, h.discounts.requests[0].ShippingMethods)
	assert.Equal(t, int64(4990), totals.ShippingTotal, "kargo indirimsiz geçer")
	requireIdentity(t, totals)
}

// TestCalculateTotalsIndirimAraToplamiAsamaz satır tutarını aşan bir indirimin
// sessizce kabul edilmediğini doğrular.
//
// promotion bunu zaten vaat eder; test, vaadin BOZULDUĞU durumda hesabın
// düştüğünü sabitler. Kabul edilseydi satırın toplamı negatife düşer ve cart
// modülünün tutarlılık kontrolü ancak yazma anında devreye girerdi.
func TestCalculateTotalsIndirimAraToplamiAsamaz(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.fn = func(req discountRequest) (discountResponse, error) {
		asiri := req.Items[0].Amount + 1
		return discountResponse{
			CurrencyCode:       req.CurrencyCode,
			Items:              []discountLine{{ID: req.Items[0].ID, Amount: asiri}},
			ItemsDiscountTotal: asiri,
			DiscountTotal:      asiri,
		}, nil
	}
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeDiscountInvalid, errors.CodeOf(err))
	assert.Empty(t, h.carts.written, "sözleşme dışı indirimle hiçbir şey yazılmamalı")
}

// TestCalculateTotalsBozukIndirimSonucuReddedilir promotion'ın sözleşmeyi
// çiğneyen yanıtlarının hesaba girmediğini doğrular.
func TestCalculateTotalsBozukIndirimSonucuReddedilir(t *testing.T) {
	tests := map[string]func(req discountRequest) (discountResponse, error){
		"sıra korunmadı": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode: req.CurrencyCode,
				Items: []discountLine{
					{ID: req.Items[1].ID, Amount: 0},
					{ID: req.Items[0].ID, Amount: 0},
				},
			}, nil
		},
		"satır eksik": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode: req.CurrencyCode,
				Items:        []discountLine{{ID: req.Items[0].ID, Amount: 0}},
			}, nil
		},
		"toplam satırlarla uyuşmuyor": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode:       req.CurrencyCode,
				Items:              []discountLine{{ID: req.Items[0].ID}, {ID: req.Items[1].ID}},
				ItemsDiscountTotal: 100,
				DiscountTotal:      100,
			}, nil
		},
		"gönderilmeyen kargoya indirim": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode:          req.CurrencyCode,
				Items:                 []discountLine{{ID: req.Items[0].ID}, {ID: req.Items[1].ID}},
				ShippingDiscountTotal: 50,
			}, nil
		},
		"başka para birimi": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode: "EUR",
				Items:        []discountLine{{ID: req.Items[0].ID}, {ID: req.Items[1].ID}},
			}, nil
		},
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			h := newModuleHarness(t)
			h.discounts.fn = script
			serveSnapshot(h.carts, twoLineCart(1))

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.Error(t, err)
			assert.Equal(t, CodeDiscountInvalid, errors.CodeOf(err))
			assert.Empty(t, h.carts.written)
		})
	}
}

// TestCalculateTotalsIndirimHatasiSinifiKorunur promotion'ın hata SINIFININ
// yolda Internal'a çevrilmediğini doğrular.
//
// Sınıf korunmazsa düzeltilebilir bir kablolama hatası (örn. sözleşme dışı
// istek) istemciye sunucu arızası olarak ulaşır ve kimse düzeltmeye kalkmaz.
func TestCalculateTotalsIndirimHatasiSinifiKorunur(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.err = errors.Invalid("promotion_interop_request_invalid", "istek çözümlenemedi")
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeDiscountFailed, errors.CodeOf(err))
	assert.Empty(t, h.carts.written)
	assert.Zero(t, h.taxes.calls, "indirim düştüyse vergi hiç çağrılmamalı")
}

// TestCalculateTotalsIndirimliSepetteSigmaTutar Σ kimliklerinin indirim ve
// vergi gerçek değerler taşırken de sağlandığını doğrular.
//
// Faz 5'te iki alan da sıfırdı ve kimlikler kendiliğinden sağlanıyordu; bu
// test onların ilk kez GERÇEKTEN sınandığı yerdir.
func TestCalculateTotalsIndirimliSepetteSigmaTutar(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 333, testLineB: 77}
	serveSnapshot(h.carts, twoLineCart(9))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	var indirimToplami, vergiToplami int64
	for _, line := range totals.Lines {
		indirimToplami += line.DiscountTotal
		vergiToplami += line.TaxTotal
	}
	assert.Equal(t, indirimToplami, totals.DiscountTotal, "Σ satır indirimi = sepet indirimi")
	assert.Equal(t, vergiToplami, totals.TaxTotal, "Σ satır vergisi = sepet vergisi")
	requireIdentity(t, totals)

	require.Len(t, h.carts.written, 1)
	assert.Equal(t, totals, h.carts.written[0], "sepete yazılan gövde dönen sonuçla aynıdır")
}

// TestCalculateTotalsIndirimTasmayiYakalar taşan bir indirim toplamının
// sessizce negatife dönmediğini ve taşmanın VERGİDEN ÖNCE yakalandığını
// doğrular.
//
// İki satırın da indirimi tavana yakın olduğunda toplamları int64'ü aşar;
// denetimsiz bir toplama negatif bir "indirim" üretir ve negatif indirim,
// sepetin toplamını sınırsız BÜYÜTÜRDÜ. Taşmanın indirim adımında yakalanması
// ayrıca şunu sağlar: sözleşme dışı büyüklükteki bir sepet tax modülüne hiç
// gönderilmez ve oradaki taban denetimine boşuna çarpmaz.
func TestCalculateTotalsIndirimTasmayiYakalar(t *testing.T) {
	h := newModuleHarness(t)
	h.prices.amounts[testPriceSetA] = MaxAmount
	h.prices.amounts[testPriceSetB] = MaxAmount
	h.discounts.fn = func(req discountRequest) (discountResponse, error) {
		// Her satır kendi ara toplamı kadar indirilir: tek tek geçerli,
		// toplamları [MaxTotal] üstü.
		items := make([]discountLine, 0, len(req.Items))
		var sum int64
		for i := range req.Items {
			items = append(items, discountLine{ID: req.Items[i].ID, Amount: req.Items[i].Amount})
			sum += req.Items[i].Amount
		}
		return discountResponse{
			CurrencyCode:       req.CurrencyCode,
			Items:              items,
			ItemsDiscountTotal: sum,
			DiscountTotal:      sum,
		}, nil
	}

	snap := snapshotOf(1, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: MaxQuantity},
		{ID: testLineB, VariantID: testVariantB, Quantity: MaxQuantity},
	}, nil)

	_, err := h.wf.computeTotals(context.Background(), snap)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
	assert.Zero(t, h.taxes.calls, "taşan sepet vergi modülüne gönderilmemeli")
}

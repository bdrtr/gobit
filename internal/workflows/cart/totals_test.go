package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// requireIdentity toplam kimliğinin hem sepet hem satır düzeyinde
// sağlandığını doğrular.
//
// Kimlik her testte tek tek yazılsaydı, bir testte unutulması sessiz kalırdı;
// tek yardımcı, her senaryonun aynı değişmezden geçmesini zorunlu kılar.
func requireIdentity(t *testing.T, totals Totals) {
	t.Helper()

	assert.Equal(t, totals.Subtotal-totals.DiscountTotal+totals.TaxTotal+totals.ShippingTotal, totals.Total,
		"sepet kimliği: total = subtotal - discount + tax + shipping")
	assert.LessOrEqual(t, totals.DiscountTotal, totals.Subtotal, "indirim ara toplamı aşamaz")

	var lineSum int64
	for _, line := range totals.Lines {
		assert.Equal(t, line.Subtotal-line.DiscountTotal+line.TaxTotal, line.Total,
			"satır kimliği: total = subtotal - discount + tax (%s)", line.LineItemID)
		lineSum += line.Subtotal
	}
	assert.Equal(t, lineSum, totals.Subtotal, "sepet ara toplamı satır ara toplamlarının toplamıdır")
}

// TestCalculateTotalsBosSepet satırsız sepette toplamların sıfır olduğunu
// doğrular.
func TestCalculateTotalsBosSepet(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, Totals{Revision: 0, Lines: []LineTotals{}}, totals)
	requireIdentity(t, totals)
}

// TestCalculateTotalsSatirsizSepetteKargoToplamaGirer satırsız ama kargolu
// sepette toplamın yalnızca kargo olduğunu doğrular.
func TestCalculateTotalsSatirsizSepetteKargoToplamaGirer(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(3, nil, []SnapshotShippingMethod{{ID: "csm_1", Amount: 4990}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, int64(0), totals.Subtotal)
	assert.Equal(t, int64(0), totals.TaxTotal, "kargo vergi tabanına girmez")
	assert.Equal(t, int64(4990), totals.ShippingTotal)
	assert.Equal(t, int64(4990), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsTekSatir tek satırlı sepetin toplamlarını doğrular.
func TestCalculateTotalsTekSatir(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// Birim 1000, adet 2 -> ara toplam 2000; %20 vergi -> 400.
	require.Len(t, totals.Lines, 1)
	assert.Equal(t, LineTotals{
		LineItemID: testLineA,
		UnitPrice:  1000,
		Subtotal:   2000,
		TaxTotal:   400,
		Total:      2400,
	}, totals.Lines[0])
	assert.Equal(t, int64(2000), totals.Subtotal)
	assert.Equal(t, int64(400), totals.TaxTotal)
	assert.Equal(t, int64(2400), totals.Total)
	assert.Equal(t, int64(1), totals.Revision)
	requireIdentity(t, totals)

	require.Len(t, h.carts.written, 1)
	assert.Equal(t, totals, h.carts.written[0], "sepete yazılan gövde dönen sonuçla aynıdır")
}

// TestCalculateTotalsCokSatirVeKargo çok satırlı, kargolu sepetin toplamlarını
// doğrular.
func TestCalculateTotalsCokSatirVeKargo(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(7, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 2},
		{ID: testLineB, VariantID: testVariantB, Quantity: 3},
	}, []SnapshotShippingMethod{
		{ID: "csm_1", Amount: 2000},
		{ID: "csm_2", Amount: 500},
	}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// A: 1000 × 2 = 2000, vergi 400. B: 250 × 3 = 750, vergi 150.
	assert.Equal(t, int64(2750), totals.Subtotal)
	assert.Equal(t, int64(550), totals.TaxTotal)
	assert.Equal(t, int64(2500), totals.ShippingTotal)
	assert.Equal(t, int64(5800), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsIndirimSifirdir Faz 7'ye kadar indirimin sıfır kaldığını
// sabitler.
func TestCalculateTotalsIndirimSifirdir(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Zero(t, totals.DiscountTotal)
	require.Len(t, totals.Lines, 1)
	assert.Zero(t, totals.Lines[0].DiscountTotal)
}

// TestCalculateTotalsVergiAsagiYuvarlar baz puan bölmesinin aşağı yuvarladığını
// doğrular.
func TestCalculateTotalsVergiAsagiYuvarlar(t *testing.T) {
	h := newHarness(t)
	h.regions.rateBps = 1850 // %18,5
	h.prices.amounts[testPriceSetA] = 101
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// 101 × 1850 / 10000 = 18,685 -> 18 (aşağı; müşteri lehine).
	assert.Equal(t, int64(18), totals.TaxTotal)
	assert.Equal(t, int64(119), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsVergiSatirBasinaHesaplanir verginin satır başına
// yuvarlandığını, sepet tabanı üzerinden TEK SEFERDE hesaplanmadığını
// kanıtlar.
//
// İki satırın da tabanı 101 ve oran %18,5: satır başına 18 + 18 = 36. Sepet
// tabanı (202) tek seferde vergilenseydi 37 çıkardı. Fark, sözleşmenin hangi
// dalının uygulandığını AYIRT EDER.
func TestCalculateTotalsVergiSatirBasinaHesaplanir(t *testing.T) {
	h := newHarness(t)
	h.regions.rateBps = 1850
	h.prices.amounts[testPriceSetA] = 101
	h.prices.amounts[testPriceSetB] = 101
	serveSnapshot(h.carts, snapshotOf(2, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 1},
	}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, int64(36), totals.TaxTotal, "satır başına yuvarlama: 18 + 18")
	assert.NotEqual(t, int64(37), totals.TaxTotal, "sepet tabanı tek seferde vergilenmemeli")
	requireIdentity(t, totals)
}

// TestCalculateTotalsKargoVergiTabaninaGirmez kargonun vergilenmediğini
// doğrular.
func TestCalculateTotalsKargoVergiTabaninaGirmez(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 5000}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// Yalnızca 1000'lik mal vergilenir: 200. Kargo dâhil olsaydı 1200 olurdu.
	assert.Equal(t, int64(200), totals.TaxTotal)
	assert.Equal(t, int64(6200), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsOtomatikDegilseVergiYok bölge otomatik vergi uygulamıyorsa
// verginin sıfır kaldığını doğrular.
func TestCalculateTotalsOtomatikDegilseVergiYok(t *testing.T) {
	h := newHarness(t)
	h.regions.automatic = false
	h.regions.rateBps = 2000
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Zero(t, totals.TaxTotal)
	assert.Equal(t, int64(1000), totals.Total)
}

// TestCalculateTotalsSozlesmeDisiVergiOraniReddedilir bölgenin aralık dışı
// oranının hesaba girmediğini doğrular.
func TestCalculateTotalsSozlesmeDisiVergiOraniReddedilir(t *testing.T) {
	h := newHarness(t)
	h.regions.rateBps = MaxTaxRateBps + 1
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeTaxRateInvalid, errors.CodeOf(err))
	assert.Empty(t, h.carts.written, "geçersiz oranla hiçbir şey yazılmamalı")
}

// TestCalculateTotalsFiyatBaglamiBolgeyiTasir pricing çağrısının kural bağlamına
// bölgeyi koyduğunu doğrular.
//
// Bağlam boş gitseydi bölgeye özgü fiyat kuralı eşleşmez ve sessizce taban
// fiyat seçilirdi; hata hiçbir yerde patlamaz, yalnızca tutar yanlış olurdu.
func TestCalculateTotalsFiyatBaglamiBolgeyiTasir(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 4}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.prices.seen, 1)
	assert.Equal(t, testPriceSetA, h.prices.seen[0].priceSetID)
	assert.Equal(t, testCurrency, h.prices.seen[0].currencyCode)
	assert.Equal(t, int32(4), h.prices.seen[0].quantity, "kademe seçimi için adet taşınmalı")
	assert.Equal(t, map[string]string{attrRegionID: testRegionID}, h.prices.seen[0].attributes)
}

// TestCalculateTotalsLinkSorgusuTopludur satır sayısı ne olursa olsun tek bir
// link sorgusu yapıldığını ve varyantların tekrarsız gittiğini doğrular.
func TestCalculateTotalsLinkSorgusuTopludur(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(3, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 1},
		{ID: "li_c", VariantID: testVariantA, Quantity: 5},
	}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.links.batches, 1, "satır başına değil, tek toplu sorgu")
	assert.Equal(t, []string{testVariantA, testVariantB}, h.links.batches[0])
	assert.Len(t, totals.Lines, 3, "aynı varyantın iki satırı da fiyatlanır")
}

// TestCalculateTotalsFiyatKumesiOlmayanVaryantReddedilir fiyatsız varyantın
// hesabı düşürdüğünü doğrular.
func TestCalculateTotalsFiyatKumesiOlmayanVaryantReddedilir(t *testing.T) {
	h := newHarness(t)
	delete(h.links.links, testVariantA)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "fiyatsız varyant istemcinin düzeltebileceği bir durumdur")
	assert.Equal(t, CodeVariantNotPriced, errors.CodeOf(err))
	assert.Empty(t, h.carts.written)
}

// TestCalculateTotalsBirdenCokFiyatKumesiReddedilir tekil olması gereken bağın
// çoğullandığı durumda sessizce fiyat seçilmediğini doğrular.
func TestCalculateTotalsBirdenCokFiyatKumesiReddedilir(t *testing.T) {
	h := newHarness(t)
	h.links.links[testVariantA] = []string{testPriceSetA, testPriceSetB}
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeVariantPriceSetAmbiguous, errors.CodeOf(err))
}

// TestCalculateTotalsParaBirimindeFiyatYoksaInvalid pricing'in NotFound
// hatasının istemciye 404 olarak sızmadığını doğrular.
func TestCalculateTotalsParaBirimindeFiyatYoksaInvalid(t *testing.T) {
	h := newHarness(t)
	delete(h.prices.amounts, testPriceSetA)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.False(t, errors.IsNotFound(err), "satır duruyor; eksik olan yalnızca fiyatıdır")
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodePriceUnavailable, errors.CodeOf(err))
}

// TestCalculateTotalsTamamlanmisSepetiReddeder kapanmış sepette hesap
// yapılmadığını ve pricing'in hiç çağrılmadığını doğrular.
func TestCalculateTotalsTamamlanmisSepetiReddeder(t *testing.T) {
	h := newHarness(t)
	snap := snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil)
	snap.Completed = true
	serveSnapshot(h.carts, snap)

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.Empty(t, h.prices.seen, "sonucu baştan belli tur için pricing çağrılmamalı")
}

// TestCalculateTotalsRevisionCakismasindaYenidenHesaplar çakışan turun
// atılıp sepetin YENİ şekliyle yeniden hesaplandığını doğrular.
func TestCalculateTotalsRevisionCakismasindaYenidenHesaplar(t *testing.T) {
	h := newHarness(t)
	// İlk tur 4. şekli okur; yazarken sepet 5'e geçmiştir. İkinci tur 5'i okur
	// ve ikinci satır artık sepettedir.
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil),
		snapshotOf(5, []SnapshotItem{
			{ID: testLineA, VariantID: testVariantA, Quantity: 1},
			{ID: testLineB, VariantID: testVariantB, Quantity: 2},
		}, nil),
	)
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		if len(h.carts.written) == 1 {
			return errors.Conflict("cart_totals_stale", "hesap sepetin güncel şekline ait değil")
		}
		return nil
	}

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, 2, h.carts.snapshotCalls, "çakışan tur sepeti YENİDEN okumalı")
	assert.Equal(t, int64(5), totals.Revision)
	assert.Len(t, totals.Lines, 2)
	assert.Equal(t, int64(1500), totals.Subtotal, "1000 + 250 × 2")
	requireIdentity(t, totals)

	require.Len(t, h.carts.written, 2)
	assert.Equal(t, int64(4), h.carts.written[0].Revision, "ilk tur eski şekille damgalanmıştı")
	assert.Equal(t, int64(5), h.carts.written[1].Revision)
}

// TestCalculateTotalsCakismaSurerseVazgecer yeniden denemenin SINIRLI
// olduğunu ve sınır aşılınca çakışma hatası döndüğünü doğrular.
func TestCalculateTotalsCakismaSurerseVazgecer(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Conflict("cart_totals_stale", "sepet yine değişti")
	}

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeTotalsConflict, errors.CodeOf(err))
	assert.Equal(t, MaxTotalsAttempts, h.carts.snapshotCalls, "sınır kadar denenir, daha fazla değil")
}

// TestCalculateTotalsCakismaDisiHataYenidenDenenmez çakışma OLMAYAN bir
// yazma hatasında turun tekrarlanmadığını doğrular.
func TestCalculateTotalsCakismaDisiHataYenidenDenenmez(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Invalid("cart_totals_inconsistent", "toplam tutarsız")
	}

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, 1, h.carts.snapshotCalls, "girdi hatası tekrarlanınca da aynı sonucu verir")
}

// TestCalculateTotalsBozukAnlikGoruntuReddedilir sepet modülünün sözleşme dışı
// gövdesinin hesaba girmediğini doğrular.
func TestCalculateTotalsBozukAnlikGoruntuReddedilir(t *testing.T) {
	tests := map[string]Snapshot{
		"para birimi boş": {ID: testCartID, RegionID: testRegionID, Revision: 1},
		"bölge boş":       {ID: testCartID, CurrencyCode: testCurrency, Revision: 1},
		"başka sepet":     {ID: "cart_other", RegionID: testRegionID, CurrencyCode: testCurrency},
		"adet sıfır": {
			ID: testCartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			Items: []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 0}},
		},
		"varyantsız satır": {
			ID: testCartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			Items: []SnapshotItem{{ID: testLineA, Quantity: 1}},
		},
		"negatif kargo": {
			ID: testCartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			ShippingMethods: []SnapshotShippingMethod{{ID: "csm_1", Amount: -1}},
		},
	}

	for name, snap := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.carts.snapshotFn = func(_ context.Context, _ string) (json.RawMessage, error) {
				return json.Marshal(snap)
			}

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.Error(t, err)
			assert.Equal(t, CodeSnapshotInvalid, errors.CodeOf(err))
			assert.Empty(t, h.carts.written)
		})
	}
}

// TestCalculateTotalsGecersizSepetKimligiReddedilir kimlik doğrulamasının
// sepet modülüne hiç gitmeden yapıldığını doğrular.
func TestCalculateTotalsGecersizSepetKimligiReddedilir(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CalculateTotals(context.Background(), "  ")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, h.carts.snapshotCalls)
}

// TestComputeTotalsTasmayiYakalar taşan bir ara toplamın sessizce negatife
// dönmediğini doğrular.
func TestComputeTotalsTasmayiYakalar(t *testing.T) {
	h := newHarness(t)
	h.prices.amounts[testPriceSetA] = MaxAmount
	h.prices.amounts[testPriceSetB] = MaxAmount

	snap := snapshotOf(1, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: MaxQuantity},
		{ID: testLineB, VariantID: testVariantB, Quantity: MaxQuantity},
	}, nil)

	_, err := h.wf.computeTotals(context.Background(), snap)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
}

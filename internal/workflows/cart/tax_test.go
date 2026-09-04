package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// TestCalculateTotalsVergiTaxModulundenGelir tax yüzeyi kayıtlıyken verginin
// oradan hesaplandığını ve kaynağın sonuçta bildirildiğini doğrular.
func TestCalculateTotalsVergiTaxModulundenGelir(t *testing.T) {
	h := newModulHarness(t)
	h.taxes.rateBps = 1000 // %10; region sahtesi %20 taşır ve karışması ayırt edilebilir.
	serveSnapshot(h.carts, ikiSatirliSepet(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, TaxSourceTax, totals.TaxSource)
	assert.Equal(t, int64(275), totals.TaxTotal, "2000×%10 + 750×%10")
	assert.NotEqual(t, int64(550), totals.TaxTotal, "region'ın %20 oranı kullanılmamalı")
	assert.Equal(t, 1, h.taxes.calls)
	requireIdentity(t, totals)
}

// TestCalculateTotalsVergiIstegininSekli tax'a giden gövdenin sözleşmeye
// uyduğunu doğrular.
func TestCalculateTotalsVergiIstegininSekli(t *testing.T) {
	h := newModulHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 3000}, {ID: "csm_2", Amount: 1990}}))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.taxes.requests, 1)
	req := h.taxes.requests[0]
	assert.Equal(t, "TR", req.CountryCode, "ülke bölgenin ülkesinden gelir")
	assert.Empty(t, req.ProvinceCode, "sepet eyalet taşımaz")
	require.Len(t, req.Items, 1)
	assert.Equal(t, taxRequestItem{ID: testLineA, Amount: 2000}, req.Items[0])
	assert.Equal(t, taxRequestShipping{Amount: 4990, Taxable: false}, req.Shipping,
		"kargo tutarı bildirilir ama tabana GİRMEZ")
}

// TestCalculateTotalsKargoModulYolundaDaVergilenmez kargonun tax modülü
// yolunda da vergi tabanına girmediğini doğrular.
func TestCalculateTotalsKargoModulYolundaDaVergilenmez(t *testing.T) {
	h := newModulHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 5000}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, int64(200), totals.TaxTotal, "yalnızca 1000'lik mal vergilenir")
	assert.Equal(t, int64(6200), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsTaxKayitsizsaRegionaDuser tax yüzeyi kayıtlı değilken
// verginin SIFIRA düşmediğini, region'ın oranıyla hesaplandığını ve kaynağın
// bildirildiğini doğrular.
//
// Karar [Workflows.applyTaxes] godoc'undadır: eksik vergi satıcının cebinden
// sessizce çıkar; region, Faz 5'in hâlâ geçerli yetkilisidir.
func TestCalculateTotalsTaxKayitsizsaRegionaDuser(t *testing.T) {
	h := newHarnessWith(t, &stubDiscounts{perLine: map[string]int64{}}, nil)
	serveSnapshot(h.carts, ikiSatirliSepet(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, TaxSourceRegion, totals.TaxSource)
	assert.Equal(t, int64(550), totals.TaxTotal, "region'ın %20 oranı")
	requireIdentity(t, totals)
}

// TestCalculateTotalsUlkeCozulemezseRegionaDuser bölgenin tek bir ülkeye
// çözülemediği durumlarda tax'a HİÇ SORULMADIĞINI ve region'a düşüldüğünü
// doğrular.
//
// Ayrım [Workflows.countryForRegion] godoc'undadır: hangi yargı bölgesinin
// sorulacağı bilinmiyorsa cevabı olmayan bir otorite, önceki otoriteyi
// devirmez.
func TestCalculateTotalsUlkeCozulemezseRegionaDuser(t *testing.T) {
	tests := map[string]func(h *harness){
		"bölge birden çok ülkeye bağlı": func(h *harness) {
			h.catalog.countries[testRegionID] = []string{"TR", "DE"}
		},
		"bölgeye bağlı ülke yok": func(h *harness) {
			h.catalog.countries[testRegionID] = nil
		},
		"bölge Query'de bulunamadı": func(h *harness) {
			delete(h.catalog.countries, testRegionID)
		},
		"bölge sağlayıcısı kayıtlı değil": func(h *harness) {
			h.catalog.regionErr = errors.NotFound(codeProviderNotFound,
				"%q sağlayıcısı kayıtlı değil", EntityRegion+query.ProviderSuffix)
		},
	}

	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			h := newModulHarness(t)
			setup(h)
			serveSnapshot(h.carts, ikiSatirliSepet(1))

			totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.NoError(t, err)

			assert.Equal(t, TaxSourceRegion, totals.TaxSource)
			assert.Equal(t, int64(550), totals.TaxTotal, "region'ın %20 oranı")
			assert.Zero(t, h.taxes.calls, "ülke bilinmeden tax çağrılmamalı")
			requireIdentity(t, totals)
		})
	}
}

// TestCalculateTotalsBolgeOkunamazsaHataDoner Query katmanının GEÇİCİ bir
// arızasının sessizce kaynak değiştirmediğini doğrular.
//
// Kayıtsız bir sağlayıcı ile erişilemeyen bir veritabanı aynı kapıdan
// geçseydi, bir kesinti boyunca tüm sepetler sessizce region oranıyla
// vergilenir ve kimse fark etmezdi.
func TestCalculateTotalsBolgeOkunamazsaHataDoner(t *testing.T) {
	h := newModulHarness(t)
	h.catalog.regionErr = errors.Unavailable("query_provider_failed", "veritabanı erişilemez")
	serveSnapshot(h.carts, ikiSatirliSepet(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeRegionReadFailed, errors.CodeOf(err))
	assert.Zero(t, h.taxes.calls)
	assert.Empty(t, h.carts.written)
}

// TestCalculateTotalsVergiBolgesiYapilandirilmamis tax'ın "bu ülkenin vergi
// bölgesi yok" cevabının OLDUĞU GİBİ kabul edildiğini ve region'a
// DÜŞÜLMEDİĞİNİ doğrular.
//
// Sıfır verginin sebebi sonuçta okunur: [TaxSourceTaxUnconfigured] ile
// [TaxSourceTax] arasındaki fark, "oran sıfırdı" ile "yapılandırma yoktu"
// arasındaki farktır.
func TestCalculateTotalsVergiBolgesiYapilandirilmamis(t *testing.T) {
	h := newModulHarness(t)
	h.taxes.regionFound = false
	serveSnapshot(h.carts, ikiSatirliSepet(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, TaxSourceTaxUnconfigured, totals.TaxSource)
	assert.Zero(t, totals.TaxTotal)
	assert.NotEqual(t, int64(550), totals.TaxTotal, "region'ın oranına geri DÜŞÜLMEZ")
	assert.Equal(t, 1, h.taxes.calls)
	requireIdentity(t, totals)
}

// TestCalculateTotalsVergiKurusArtigiSatirdaKalir aşağı yuvarlamanın satır
// başına yapıldığını ve artığın başka bir satıra TAŞINMADIĞINI kanıtlar.
//
// Satır tabanları 999 ve 750, oran %18,5: satır başına 184 + 138 = 322. Sepet
// tabanı (1749) tek seferde vergilenseydi 323 çıkardı. Aradaki 1 minor unit,
// doğduğu satırlarda düşen artıktır ve müşteri lehinedir.
func TestCalculateTotalsVergiKurusArtigiSatirdaKalir(t *testing.T) {
	h := newModulHarness(t)
	h.taxes.rateBps = 1850
	h.discounts.perLine = map[string]int64{testLineA: 1}
	serveSnapshot(h.carts, snapshotOf(1, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 3},
	}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, totals.Lines, 2)
	assert.Equal(t, int64(184), totals.Lines[0].TaxTotal, "999 × %18,5 = 184,815 -> 184")
	assert.Equal(t, int64(138), totals.Lines[1].TaxTotal, "750 × %18,5 = 138,75 -> 138")
	assert.Equal(t, int64(322), totals.TaxTotal)
	assert.NotEqual(t, int64(323), totals.TaxTotal, "sepet tabanı tek seferde vergilenmemeli")
	requireIdentity(t, totals)
}

// TestCalculateTotalsBozukVergiSonucuReddedilir tax'ın sözleşmeyi çiğneyen
// yanıtlarının hesaba girmediğini doğrular.
func TestCalculateTotalsBozukVergiSonucuReddedilir(t *testing.T) {
	tests := map[string]func(req taxRequest) (taxResponse, error){
		"sıra korunmadı": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				Items: []taxResponseLine{
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount},
				},
			}, nil
		},
		"satır eksik": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				Items:       []taxResponseLine{{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount}},
			}, nil
		},
		"taban gönderilenden farklı": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount + 1},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
			}, nil
		},
		"vergi tabanı aşıyor": func(req taxRequest) (taxResponse, error) {
			asiri := req.Items[0].Amount + 1
			return taxResponse{
				RegionFound: true,
				TaxTotal:    asiri,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount, TaxAmount: asiri},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
			}, nil
		},
		"toplam satırlarla uyuşmuyor": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				TaxTotal:    100,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
			}, nil
		},
		"istenmeyen kargo vergisi": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				TaxTotal:    7,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
				Shipping: taxResponseLine{ID: "_shipping", TaxAmount: 7},
			}, nil
		},
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			h := newModulHarness(t)
			h.taxes.fn = script
			serveSnapshot(h.carts, ikiSatirliSepet(1))

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.Error(t, err)
			assert.Equal(t, CodeTaxInvalid, errors.CodeOf(err))
			assert.Empty(t, h.carts.written)
		})
	}
}

// TestCalculateTotalsVergiHatasiSinifiKorunur tax'ın hata SINIFININ yolda
// Internal'a çevrilmediğini doğrular.
func TestCalculateTotalsVergiHatasiSinifiKorunur(t *testing.T) {
	h := newModulHarness(t)
	h.taxes.err = errors.Unavailable("tax_unconfigured", "the tax service is not configured")
	serveSnapshot(h.carts, ikiSatirliSepet(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
	assert.Equal(t, CodeTaxFailed, errors.CodeOf(err))
	assert.Empty(t, h.carts.written, "vergi hesaplanamadıysa bayat bir toplam yazılmamalı")
}

// TestCountryCodes ülke alt kayıtlarının üç şekilde de okunabildiğini
// doğrular.
//
// Şekil, Query katmanından geçerken değişebilir (sağlayıcı []map[string]any
// yazar, bir JSON turu []any'ye çevirir); tek bir tip iddiası kodu sessizce
// yutar ve bölge "ülkesiz" görünürdü.
func TestCountryCodes(t *testing.T) {
	tests := map[string]struct {
		value any
		want  []string
	}{
		"sağlayıcı şekli": {
			value: []map[string]any{{FieldCode: "TR"}, {FieldCode: "DE"}},
			want:  []string{"TR", "DE"},
		},
		"query kaydı": {
			value: []query.Record{{FieldCode: "TR"}},
			want:  []string{"TR"},
		},
		"JSON turundan geçmiş": {
			value: []any{map[string]any{FieldCode: "TR"}},
			want:  []string{"TR"},
		},
		"kodsuz alt kayıt atlanır": {
			value: []map[string]any{{"name": "Türkiye"}, {FieldCode: ""}},
			want:  []string{},
		},
		"tanınmayan şekil": {value: "TR", want: nil},
		"alan yok":         {value: nil, want: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, countryCodes(tc.value))
		})
	}
}

// TestCalculateTotalsBolgeSorgusuTekVeDar bölge sorgusunun tek kayıt ve tek
// alan istediğini doğrular.
//
// Alan seçimi yapılmasaydı sağlayıcı bölgenin tüm alanlarını (ve para birimi
// alt kaydını) toplamak için fazladan sorgu koştururdu; hesap her tur çalışır.
func TestCalculateTotalsBolgeSorgusuTekVeDar(t *testing.T) {
	h := newModulHarness(t)
	serveSnapshot(h.carts, ikiSatirliSepet(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	var bolgeSpecs []query.GraphSpec
	for _, spec := range h.catalog.specs {
		if spec.Entity == EntityRegion {
			bolgeSpecs = append(bolgeSpecs, spec)
		}
	}
	require.Len(t, bolgeSpecs, 1, "hesap turu başına tek bölge sorgusu")
	assert.Equal(t, []string{query.IDField, FieldCountries}, bolgeSpecs[0].Fields)
	assert.Equal(t, map[string]any{query.IDField: testRegionID}, bolgeSpecs[0].Filters)
	assert.Equal(t, 1, bolgeSpecs[0].Limit)
	assert.Empty(t, bolgeSpecs[0].Expand, "genişletme istenmez")
}

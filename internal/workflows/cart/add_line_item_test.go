package cart

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// addedLine sahte sepete yazılan satırın argümanlarıdır.
type addedLine struct {
	cartID    string
	variantID string
	title     string
	quantity  int64
	unitPrice int64
	metadata  json.RawMessage
	calls     int
}

// recordAddLine sahte sepet servisini, eklenen satırı kaydedecek biçimde
// betikler.
func recordAddLine(carts *stubCarts, lineID string) *addedLine {
	seen := &addedLine{}
	carts.addLineFn = func(
		_ context.Context,
		cartID, variantID, title string,
		quantity, unitPrice int64,
		metadata json.RawMessage,
	) (string, error) {
		seen.calls++
		seen.cartID, seen.variantID, seen.title = cartID, variantID, title
		seen.quantity, seen.unitPrice, seen.metadata = quantity, unitPrice, metadata
		return lineID, nil
	}
	return seen
}

// TestAddLineItemFiyatiBulurVeToplamlariYeniler mutlu yolu uçtan uca doğrular.
func TestAddLineItemFiyatiBulurVeToplamlariYeniler(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 3}}, nil),
	)

	out, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID:    testCartID,
		VariantID: testVariantA,
		Quantity:  3,
	})
	require.NoError(t, err)

	assert.Equal(t, testLineA, out.LineItemID)
	assert.Equal(t, "Kırmızı Tişört / M", out.Title, "başlık katalogdan kopyalanır")
	assert.Equal(t, int64(1000), out.UnitPrice)

	assert.Equal(t, addedLine{
		cartID:    testCartID,
		variantID: testVariantA,
		title:     "Kırmızı Tişört / M",
		quantity:  3,
		unitPrice: 1000,
		calls:     1,
	}, *seen)

	// Satır yazıldıktan sonra hesap koşar: 1000 × 3 = 3000, %20 vergi 600.
	assert.Equal(t, int64(3000), out.Totals.Subtotal)
	assert.Equal(t, int64(600), out.Totals.TaxTotal)
	assert.Equal(t, int64(3600), out.Totals.Total)
	assert.Equal(t, int64(1), out.Totals.Revision)
	requireIdentity(t, out.Totals)
	require.Len(t, h.carts.written, 1)
}

// TestAddLineItemMetadataOlduguGibiTasinir satır metadata'sının akış
// tarafından OKUNMADAN taşındığını doğrular.
//
// Akış onu hesaba katmaz ve katmamalıdır; ama taşımak zorundadır: satırı açan
// tek yol bu akıştır ve taşınmasaydı vitrinin gönderdiği alan sessizce
// düşerdi — "gönderildiği sanılan ama uygulanmayan ayar" tam olarak bu API'nin
// tanımadığı alanları reddetme sebebidir.
func TestAddLineItemMetadataOlduguGibiTasinir(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil),
	)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID:    testCartID,
		VariantID: testVariantA,
		Quantity:  1,
		Metadata:  json.RawMessage(`{"not":"hediye paketi"}`),
	})
	require.NoError(t, err)

	assert.JSONEq(t, `{"not":"hediye paketi"}`, string(seen.metadata))
}

// TestAddLineItemFiyatBaglamiAdediTasir açılış fiyatının istenen adede göre
// seçildiğini doğrular (kademeli fiyatlandırma).
func TestAddLineItemFiyatBaglamiAdediTasir(t *testing.T) {
	h := newHarness(t)
	recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 5}}, nil),
	)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 5,
	})
	require.NoError(t, err)

	require.NotEmpty(t, h.prices.seen)
	assert.Equal(t, int32(5), h.prices.seen[0].quantity)
	assert.Equal(t, map[string]string{attrRegionID: testRegionID}, h.prices.seen[0].attributes)
}

// TestAddLineItemFiyatiOlmayanVaryantReddedilir fiyat kümesi olmayan varyantın
// sepete GİRMEDİĞİNİ doğrular.
func TestAddLineItemFiyatiOlmayanVaryantReddedilir(t *testing.T) {
	h := newHarness(t)
	delete(h.links.links, testVariantA)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeVariantNotPriced, errors.CodeOf(err))
	assert.Zero(t, seen.calls, "fiyatı olmayan ürün sepete yazılmamalı")
}

// TestAddLineItemParaBirimindeFiyatYoksaReddedilir kümesi olan ama sepetin
// para biriminde fiyatı olmayan varyantın reddedildiğini doğrular.
func TestAddLineItemParaBirimindeFiyatYoksaReddedilir(t *testing.T) {
	h := newHarness(t)
	delete(h.prices.amounts, testPriceSetA)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodePriceUnavailable, errors.CodeOf(err))
	assert.Zero(t, seen.calls)
}

// TestAddLineItemBilinmeyenVaryantReddedilir katalogda olmayan varyantın
// yetim bir fiyat bağı üzerinden sepete giremeyeceğini doğrular.
func TestAddLineItemBilinmeyenVaryantReddedilir(t *testing.T) {
	h := newHarness(t)
	// Varyant silinmiş ama fiyat bağı temizlenememiş: yetim bağ senaryosu.
	delete(h.catalog.titles, testVariantA)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.Zero(t, seen.calls)
}

// TestAddLineItemTamamlanmisSepetiReddeder kapanmış sepete satır
// eklenemediğini doğrular.
func TestAddLineItemTamamlanmisSepetiReddeder(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	snap := snapshotOf(2, nil, nil)
	snap.Completed = true
	serveSnapshot(h.carts, snap)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.Zero(t, seen.calls)
	assert.Empty(t, h.prices.seen, "sonucu belli bir istek için pricing çağrılmamalı")
}

// TestAddLineItemGecersizAdetReddedilir adet sınırlarının sepete gitmeden
// uygulandığını doğrular.
func TestAddLineItemGecersizAdetReddedilir(t *testing.T) {
	tests := map[string]int64{
		"sıfır":            0,
		"negatif":          -1,
		"tavanın üstünde":  MaxQuantity + 1,
		"aşırı büyük adet": 1 << 40,
	}

	for name, quantity := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			seen := recordAddLine(h.carts, testLineA)

			_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
				CartID: testCartID, VariantID: testVariantA, Quantity: quantity,
			})
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Zero(t, seen.calls)
			assert.Zero(t, h.carts.snapshotCalls, "girdi hatası için sepet okunmamalı")
		})
	}
}

// TestAddLineItemToplamPatlarsaSatirKALIR ikinci yazmanın patlamasının satırı
// geri ALMADIĞINI ve hatanın ayırt edilebilir olduğunu doğrular.
func TestAddLineItemToplamPatlarsaSatirKALIR(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil),
	)
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Unavailable("cart_db_unavailable", "veritabanı erişilemez")
	}

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.Equal(t, CodeTotalsAfterChange, errors.CodeOf(err),
		"çağıran, isteğin UYGULANDIĞINI ama hesabın düştüğünü ayırt edebilmeli")
	assert.Equal(t, 1, seen.calls, "satır eklendi ve geri alınmadı")
	assert.Empty(t, h.carts.removed, "telafi olarak satır silinmemeli")
}

// TestAddLineItemGecersizKimlikReddedilir biçimsiz kimliğin hiçbir modüle
// ulaşmadığını doğrular.
func TestAddLineItemGecersizKimlikReddedilir(t *testing.T) {
	tests := map[string]AddLineItemInput{
		"sepet boş":            {VariantID: testVariantA, Quantity: 1},
		"varyant boş":          {CartID: testCartID, Quantity: 1},
		"varyant boşluk taşır": {CartID: testCartID, VariantID: "var_a\n", Quantity: 1},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.wf.AddLineItem(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Zero(t, h.carts.snapshotCalls)
		})
	}
}

// tavanaKadarSatir sepeti verilen sayıda satırla doldurur.
//
// Satırların hepsi AYNI varyanta bakar; tavan denetimi satır SAYISINA ve
// eklenmek istenen varyantın sepette olup olmadığına bakar, varyantların
// çeşitliliğine değil.
func tavanaKadarSatir(variantID string, count int) []SnapshotItem {
	items := make([]SnapshotItem, 0, count)
	for i := range count {
		items = append(items, SnapshotItem{
			ID:        "li_" + strconv.Itoa(i),
			VariantID: variantID,
			Quantity:  1,
		})
	}
	return items
}

// TestAddLineItemSatirTavaniniAsanYeniSatiriReddeder tavana dayanmış sepette
// YENİ bir satır açılamadığını doğrular.
//
// Tavan sessiz değildir: istek reddedilir, kırpılmaz ve mesaj tavanı yazar.
func TestAddLineItemSatirTavaniniAsanYeniSatiriReddeder(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1, tavanaKadarSatir(testVariantA, MaxLineItems), nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantB, Quantity: 1,
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "beklenen Invalid: %v", err)
	assert.Equal(t, CodeCartLineLimit, errors.CodeOf(err))
	assert.Contains(t, err.Error(), strconv.Itoa(MaxLineItems), "tavan operatöre görünmeli")
	assert.Empty(t, h.catalog.specs, "sonucu belli istek katalogu meşgul etmemeli")
	assert.Empty(t, h.prices.seen, "sonucu belli istek pricing'i meşgul etmemeli")
	assert.Empty(t, h.carts.written)
}

// TestAddLineItemTavaninAltindaYeniSatirAcilir tavanın BİR ALTINDAKİ sepette
// yeni satırın hâlâ açılabildiğini doğrular.
//
// Sınırın kendisi kadar sınırın YERİ de sözleşmedir: bir eksik karşılaştırma,
// müşterinin ekleyebileceği son satırı sessizce reddederdi.
func TestAddLineItemTavaninAltindaYeniSatirAcilir(t *testing.T) {
	h := newHarness(t)
	dolu := tavanaKadarSatir(testVariantA, MaxLineItems-1)
	seen := recordAddLine(h.carts, testLineB)
	serveSnapshot(h.carts,
		snapshotOf(1, dolu, nil),
		snapshotOf(2, append(dolu, SnapshotItem{ID: testLineB, VariantID: testVariantB, Quantity: 1}), nil),
	)

	out, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantB, Quantity: 1,
	})

	require.NoError(t, err)
	assert.Equal(t, testLineB, out.LineItemID)
	assert.Equal(t, 1, seen.calls)
	assert.Len(t, out.Totals.Lines, MaxLineItems)
}

// TestAddLineItemTavandakiSepetteVarOlanSatirArtabilir tavana dayanmış bir
// sepette BİRLEŞTİRMENİN reddedilmediğini doğrular.
//
// Birleştirme yeni satır açmaz; reddedilseydi dolu bir sepetin sahibi kendi
// satırının adedini bile artıramazdı.
func TestAddLineItemTavandakiSepetteVarOlanSatirArtabilir(t *testing.T) {
	h := newHarness(t)
	dolu := tavanaKadarSatir(testVariantA, MaxLineItems)
	seen := recordAddLine(h.carts, dolu[0].ID)
	serveSnapshot(h.carts, snapshotOf(1, dolu, nil), snapshotOf(2, dolu, nil))

	out, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 2,
	})

	require.NoError(t, err)
	assert.Equal(t, dolu[0].ID, out.LineItemID)
	assert.Equal(t, 1, seen.calls, "birleştirme yazılmalı")
}

// TestCalculateTotalsTavanUstundekiSepetiHesaplayabilir tavan KONMADAN önce
// açılmış, tavanın üstünde satır taşıyan bir sepetin hesabının yapılabildiğini
// doğrular.
//
// Hesabın reddedilmesi, müşterinin var olan sepetini ödenemez hâle getirirdi;
// tavan yalnızca satır AÇAN yolda uygulanır.
func TestCalculateTotalsTavanUstundekiSepetiHesaplayabilir(t *testing.T) {
	h := newHarness(t)
	buyuk := tavanaKadarSatir(testVariantA, MaxLineItems+5)
	serveSnapshot(h.carts, snapshotOf(9, buyuk, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)

	require.NoError(t, err)
	assert.Len(t, totals.Lines, MaxLineItems+5)
	assert.Equal(t, int64(1000)*int64(MaxLineItems+5), totals.Subtotal)
	requireIdentity(t, totals)
}

// buyuyenSepet akışın gerçekten büyütebildiği bellek içi bir sepettir.
//
// Var olan sahteler betiklenmiş görüntüler döner; satır eklemenin MALİYETİ ise
// ancak sepet gerçekten büyürken sayılabilir.
type buyuyenSepet struct {
	items    []SnapshotItem
	revision int64
}

// buyuyenSepetDuzenegi verilen sayıda varyant tanıyan ve satır ekledikçe büyüyen
// bir düzenek kurar.
func buyuyenSepetDuzenegi(t *testing.T, variants int) *harness {
	t.Helper()

	h := newHarness(t)
	state := &buyuyenSepet{}

	h.carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		snap := snapshotOf(state.revision, append([]SnapshotItem(nil), state.items...), nil)
		snap.ID = cartID
		return json.Marshal(snap)
	}
	h.carts.addLineFn = func(
		_ context.Context,
		_ string, variantID, _ string,
		quantity, _ int64,
		_ json.RawMessage,
	) (string, error) {
		id := "li_" + strconv.Itoa(len(state.items)+1)
		state.items = append(state.items, SnapshotItem{ID: id, VariantID: variantID, Quantity: quantity})
		state.revision++
		return id, nil
	}

	for i := range variants {
		variant := "var_" + strconv.Itoa(i)
		set := "pset_" + strconv.Itoa(i)
		h.prices.amounts[set] = 1000
		h.links.links[variant] = []string{set}
		h.catalog.titles[variant] = "Ürün " + strconv.Itoa(i)
	}
	return h
}

// TestAddLineItemSepetKurmaMaliyetiDogrusaldir N satırlık bir sepet kurmanın
// fiyat turu sayısının N ile büyüdüğünü doğrular.
//
// İddia bu değişikliğin kendisidir. Her satır ekleme, sepetin TÜM satırlarını
// yeniden fiyatlayan bir hesap turu koşturur; fiyat satır başına sorulduğunda
// bir sepeti kurmanın maliyeti N² idi (ölçüldü: 100 satırlık sepet için 5150
// fiyat çağrısı). Toplu okumayla satır ekleme başına TAM İKİ tur kalır: satır
// açılırken sorulan tek fiyat ve hesap turunun tek toplu sorusu.
//
// Süre değil TUR SAYISI denetlenir; süre testi makineye bağlar, tur sayısı
// bağlamaz.
func TestAddLineItemSepetKurmaMaliyetiDogrusaldir(t *testing.T) {
	const satir = 25

	h := buyuyenSepetDuzenegi(t, satir)

	for i := range satir {
		_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
			CartID: testCartID, VariantID: "var_" + strconv.Itoa(i), Quantity: 2,
		})
		require.NoError(t, err)
	}

	assert.Len(t, h.prices.seen, satir, "satır başına tek açılış fiyatı")
	assert.Len(t, h.prices.requests, satir, "hesap turu başına tek toplu soru")

	// Turların kaleme değil SATIR EKLEMEYE bağlı olduğu, son turun sepetin
	// tamamını tek soruda taşımasıyla görülür.
	assert.Len(t, h.prices.requests[satir-1].Items, satir)
}

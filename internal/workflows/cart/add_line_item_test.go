package cart

import (
	"context"
	"encoding/json"
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
	calls     int
}

// recordAddLine sahte sepet servisini, eklenen satırı kaydedecek biçimde
// betikler.
func recordAddLine(carts *stubCarts, lineID string) *addedLine {
	seen := &addedLine{}
	carts.addLineFn = func(_ context.Context, cartID, variantID, title string, quantity, unitPrice int64) (string, error) {
		seen.calls++
		seen.cartID, seen.variantID, seen.title = cartID, variantID, title
		seen.quantity, seen.unitPrice = quantity, unitPrice
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

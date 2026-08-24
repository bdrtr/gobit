package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestUpdateLineItemAdediYazarVeToplamlariYeniler pozitif adedin sepete
// yazıldığını ve hesabın koştuğunu doğrular.
func TestUpdateLineItemAdediYazarVeToplamlariYeniler(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 5}}, nil))

	out, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID:     testCartID,
		LineItemID: testLineA,
		Quantity:   5,
	})
	require.NoError(t, err)

	assert.False(t, out.Removed)
	assert.Equal(t, int64(5), out.Quantity)
	assert.Equal(t, map[string]int64{testLineA: 5}, h.carts.quantities)
	assert.Empty(t, h.carts.removed)

	assert.Equal(t, int64(5000), out.Totals.Subtotal)
	assert.Equal(t, int64(1000), out.Totals.TaxTotal)
	assert.Equal(t, int64(6000), out.Totals.Total)
	requireIdentity(t, out.Totals)
}

// TestUpdateLineItemSifirAdetSatiriKaldirir sıfırın "kaldır" niyetine
// çevrildiğini ve bunun çağırana BİLDİRİLDİĞİNİ doğrular.
func TestUpdateLineItemSifirAdetSatiriKaldirir(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts,
		snapshotOf(6, []SnapshotItem{{ID: testLineB, VariantID: testVariantB, Quantity: 2}}, nil))

	out, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID:     testCartID,
		LineItemID: testLineA,
		Quantity:   0,
	})
	require.NoError(t, err)

	assert.True(t, out.Removed)
	assert.Zero(t, out.Quantity)
	assert.Equal(t, []string{testLineA}, h.carts.removed, "kaldırma AYRI çağrıyla yapılır")
	assert.Empty(t, h.carts.quantities, "sıfır adet sepete YAZILMAZ")

	// Kalan satır yeniden fiyatlanır: 250 × 2 = 500, %20 vergi 100.
	assert.Equal(t, int64(500), out.Totals.Subtotal)
	assert.Equal(t, int64(600), out.Totals.Total)
	requireIdentity(t, out.Totals)
}

// TestUpdateLineItemSonSatirKaldirilinca toplamların sıfırlandığını doğrular.
func TestUpdateLineItemSonSatirKaldirilinca(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(7, nil, nil))

	out, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: 0,
	})
	require.NoError(t, err)

	assert.True(t, out.Removed)
	assert.Equal(t, Totals{Revision: 7, TaxSource: TaxSourceRegion, Lines: []LineTotals{}}, out.Totals)
}

// TestUpdateLineItemNegatifAdetReddedilir negatif adedin satır SİLMEDİĞİNİ
// doğrular.
//
// Sıfır "kaldır" demektir, negatif ise hiçbir niyeti olmayan bir işaret
// hatasıdır; sıfıra yuvarlanması, o hatanın veri silmesi olurdu.
func TestUpdateLineItemNegatifAdetReddedilir(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: -1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
	assert.Empty(t, h.carts.removed, "negatif adet satırı SİLMEMELİ")
	assert.Empty(t, h.carts.quantities)
	assert.Zero(t, h.carts.snapshotCalls)
}

// TestUpdateLineItemTavaninUstundekiAdetReddedilir adet tavanının sepete
// gitmeden uygulandığını doğrular.
func TestUpdateLineItemTavaninUstundekiAdetReddedilir(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: MaxQuantity + 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Empty(t, h.carts.quantities)
}

// TestUpdateLineItemYazmaHatasiToplamiDenemez adet yazılamadıysa hesabın hiç
// koşmadığını doğrular.
func TestUpdateLineItemYazmaHatasiToplamiDenemez(t *testing.T) {
	h := newHarness(t)
	h.carts.setQtyFn = func(_ context.Context, _, lineItemID string, _ int64) error {
		return errors.NotFound("cart_line_item_not_found", "satır sepette yok: %s", lineItemID)
	}

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: 2,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.NotEqual(t, CodeTotalsAfterChange, errors.CodeOf(err),
		"sepet DEĞİŞMEDİ; hata 'uygulandı ama hesaplanamadı' olarak etiketlenmemeli")
	assert.Zero(t, h.carts.snapshotCalls)
}

// TestUpdateLineItemToplamPatlarsaAdetKALIR ikinci yazmanın patlamasının adet
// değişikliğini geri ALMADIĞINI doğrular.
func TestUpdateLineItemToplamPatlarsaAdetKALIR(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}}, nil))
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Unavailable("cart_db_unavailable", "veritabanı erişilemez")
	}

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: 2,
	})
	require.Error(t, err)
	assert.Equal(t, CodeTotalsAfterChange, errors.CodeOf(err))
	assert.Equal(t, map[string]int64{testLineA: 2}, h.carts.quantities, "adet geri alınmaz")
}

// TestUpdateLineItemGecersizKimlikReddedilir biçimsiz kimliğin hiçbir modüle
// ulaşmadığını doğrular.
func TestUpdateLineItemGecersizKimlikReddedilir(t *testing.T) {
	tests := map[string]UpdateLineItemInput{
		"sepet boş": {LineItemID: testLineA, Quantity: 1},
		"satır boş": {CartID: testCartID, Quantity: 1},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.wf.UpdateLineItem(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Empty(t, h.carts.quantities)
			assert.Empty(t, h.carts.removed)
		})
	}
}

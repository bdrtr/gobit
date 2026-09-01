package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestHarcamaLimitiAsilirsaOdemeCekilmez limite takılan bir sepette paraya HİÇ
// dokunulmadığını sabitler.
//
// Harcama limitini bu paket UYGULAMAZ; kural order modülündedir çünkü limitin
// karşılaştırıldığı harcama onun verisidir ve kontrol ancak siparişin
// yazıldığı işlemde yarışa kapanır (bkz. order modülünde service/spending.go).
// Buradan sabitlenen şey, o reddin saga üzerindeki SONUCUDUR ve asıl güvence
// odur: create_order adımı authorize_payment'tan ÖNCE geldiği için limite
// takılan bir sepette ödeme koleksiyonu bile AÇILMAZ.
//
// Test bu sıranın korunmasına bağlıdır. Adımların sırası değişip ödeme
// siparişten önce açılırsa burası kırmızı yanar — ve yanmalıdır: reddedilecek
// bir alışverişin parasını çekip sonra iade etmek, iadenin bu akışın telafisi
// OLMADIĞI bir tasarımda geri alınamaz bir hatadır.
func TestHarcamaLimitiAsilirsaOdemeCekilmez(t *testing.T) {
	h := newHarness(t)
	h.orders.placeFn = func(context.Context, json.RawMessage) (string, error) {
		return "", errors.Conflict("order_spending_limit_exceeded",
			"harcama limiti aşılıyor: dönem içi harcama 5000, sipariş 6100, limit 10000 (TRY)")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())

	require.Error(t, err)
	// SINIF korunur (motor adım hatasını kendi koduyla sarar ama sınıfı
	// taşır): çağıran, ödemesi reddedilmiş bir sepetle limite takılmış bir
	// sepeti aynı dalda — "sipariş verilemez" — karşılar ve ikisi de 409'dur.
	assert.True(t, errors.IsConflict(err),
		"limit aşımı bir çakışmadır: istemci sepeti küçültüp yeniden deneyebilir")
	assert.Contains(t, err.Error(), "harcama limiti aşılıyor",
		"modülün gerekçesi çağırana ULAŞMALI: aksi hâlde müşteri neden reddedildiğini öğrenemez")

	calls := h.rec.snapshot()

	// PARAYA HİÇ DOKUNULMADI.
	assert.Equal(t, 0, h.rec.count("payment:collection"))
	assert.Equal(t, 0, h.rec.count("payment:session"))
	assert.Equal(t, 0, h.rec.count("payment:authorize"))
	assert.Equal(t, 0, h.rec.count("payment:capture"))

	// Sipariş açılmadığı için iptal edilecek bir sipariş de yoktur.
	assert.Empty(t, h.orders.canceled)

	// Ayrılan stok GERİ BIRAKILDI: reddedilen bir sepetin malı raftan
	// düşmemelidir.
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("inventory:confirm:res_"+testLineA))

	// Sepet AÇIK kaldı: müşteri satır çıkarıp yeniden deneyebilmelidir.
	assert.Equal(t, 0, h.rec.count("cart:complete"))

	// Sipariş denemesi ödemeden ÖNCE gelmiştir; testin dayandığı sıra budur.
	assert.Less(t, indexOf(calls, "order:place"), len(calls))
	assert.Equal(t, -1, indexOf(calls, "payment:authorize"))
}

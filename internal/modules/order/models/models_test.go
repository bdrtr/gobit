package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// crockford kimlik gövdesinde izin verilen alfabedir (Crockford Base32).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// TestKimlikBicimi üretilen kimliklerin önek + 26 karakterlik gövde biçimini
// koruduğunu doğrular.
//
// Biçim bir SÖZLEŞMEDİR: sipariş kimliği logda, destek kaydında ve saga'nın
// anlık görüntüsünde taşınır; önekin kaybolması ya da gövdenin kısalması
// (tekilliğin zayıflaması) sessizce geçmemelidir.
func TestKimlikBicimi(t *testing.T) {
	testler := map[string]struct {
		uret func() string
		onek string
	}{
		"sipariş": {uret: models.NewOrderID, onek: models.OrderIDPrefix},
		"satır":   {uret: models.NewLineItemID, onek: models.LineItemIDPrefix},
		"özet":    {uret: models.NewSummaryID, onek: models.SummaryIDPrefix},
		"iade":    {uret: models.NewReturnID, onek: models.ReturnIDPrefix},
		"değişim": {uret: models.NewExchangeID, onek: models.ExchangeIDPrefix},
		"hasar":   {uret: models.NewClaimID, onek: models.ClaimIDPrefix},
	}

	for ad, tc := range testler {
		t.Run(ad, func(t *testing.T) {
			id := tc.uret()

			govde, ok := strings.CutPrefix(id, tc.onek)
			require.True(t, ok, "%q kimliği %q önekiyle başlamalı", id, tc.onek)
			assert.Len(t, govde, models.IDBodyLen,
				"gövde %d karakter olmalı: %q", models.IDBodyLen, id)
			for _, r := range govde {
				assert.Contains(t, crockford, string(r),
					"gövde yalnızca Crockford Base32 alfabesini içermeli: %q", id)
			}
		})
	}
}

// TestKimliklerZamanaGoreSiralanir kimliğin kendisinin oluşturma sırasını
// taşıdığını doğrular.
//
// Sıralanabilirlik boş bir süs değildir: birincil anahtar taramasında kayıtlar
// doğal sırada durur ve B-tree eklemeleri sona yapılır. Kimlik tamamen
// rastgele olsaydı her ekleme indeksin ortasına düşerdi.
//
// Çözünürlük MİLİSANİYEDİR: aynı milisaniyede üretilen iki kimliğin sırası
// rastgele gövdeye kalır ve garanti edilmez. Test bu yüzden turlar arasında
// bekler; iddia "her kimlik bir öncekinden büyüktür" değil, "farklı
// milisaniyelerde üretilen kimlikler zaman sırasını korur"dur.
func TestKimliklerZamanaGoreSiralanir(t *testing.T) {
	const (
		tur     = 12
		bekleme = 2 * time.Millisecond
	)

	onceki := models.NewOrderID()
	for range tur {
		time.Sleep(bekleme)
		sonraki := models.NewOrderID()
		assert.Less(t, onceki, sonraki,
			"sonraki milisaniyede üretilen kimlik sözlüksel olarak büyük olmalı")
		onceki = sonraki
	}
}

// TestKimliklerTekildir aynı milisaniyede üretilen kimliklerin çakışmadığını
// doğrular.
func TestKimliklerTekildir(t *testing.T) {
	const adet = 1000

	gorulen := make(map[string]struct{}, adet)
	for range adet {
		id := models.NewOrderID()
		_, tekrar := gorulen[id]
		require.False(t, tekrar, "kimlik tekrarlandı: %s", id)
		gorulen[id] = struct{}{}
	}
}

// TestValidDisplayID sipariş numarasının geçerlilik eşiğini doğrular.
//
// Sıfır ya da negatif numara "numarası olmayan sipariş" demektir; müşteri onu
// hiçbir yerde bulamaz. Servis bu ölçütü sipariş yazıldıktan SONRA uygular ve
// sağlamayan siparişi geri alır.
func TestValidDisplayID(t *testing.T) {
	assert.False(t, models.ValidDisplayID(0), "sıfır numara geçersiz olmalı")
	assert.False(t, models.ValidDisplayID(-1), "negatif numara geçersiz olmalı")
	assert.True(t, models.ValidDisplayID(models.MinDisplayID))
	assert.True(t, models.ValidDisplayID(1042))
}

// TestOrderToplamKimligi sipariş toplam kimliğinin ve indirim sınırının
// modelden okunabildiğini doğrular.
func TestOrderToplamKimligi(t *testing.T) {
	tutarli := models.Order{Subtotal: 3000, DiscountTotal: 500, TaxTotal: 600, ShippingTotal: 2500, Total: 5600}
	assert.True(t, tutarli.TotalsConsistent())
	assert.True(t, tutarli.DiscountWithinSubtotal())

	tutarsiz := tutarli
	tutarsiz.Total = 5599
	assert.False(t, tutarsiz.TotalsConsistent())

	// Kimlik SAĞLANIR ama indirim ara toplamı aşar: iki kontrolün ayrı olması
	// tam olarak bu durum içindir.
	asiriIndirim := models.Order{Subtotal: 1000, DiscountTotal: 3000, ShippingTotal: 2500, Total: 500}
	assert.True(t, asiriIndirim.TotalsConsistent(), "kimlik bu durumda sağlanır")
	assert.False(t, asiriIndirim.DiscountWithinSubtotal(), "indirim sınırı ihlal edilmeli")
}

// TestOrderDurumYardimcilari durum tabanlı yardımcıların doğru yanıt verdiğini
// doğrular.
func TestOrderDurumYardimcilari(t *testing.T) {
	assert.True(t, models.Order{Status: models.OrderCanceled}.Canceled())
	assert.False(t, models.Order{Status: models.OrderPending}.Canceled())

	assert.True(t, models.Order{Status: models.OrderCompleted}.Completed())
	assert.True(t, models.Order{Status: models.OrderArchived}.Completed(),
		"arşivlenmiş sipariş de tamamlanmıştır")
	assert.False(t, models.Order{Status: models.OrderPending}.Completed())

	assert.True(t, models.Order{}.Guest())
	assert.False(t, models.Order{CustomerID: "cus_1"}.Guest())
}

// TestOrderStatusValid tanımsız durumların reddedildiğini doğrular.
func TestOrderStatusValid(t *testing.T) {
	for _, durum := range []models.OrderStatus{
		models.OrderPending, models.OrderCompleted, models.OrderArchived, models.OrderCanceled,
	} {
		assert.True(t, durum.Valid(), "%q tanımlı olmalı", durum)
	}
	assert.False(t, models.OrderStatus("shipped").Valid())
	assert.False(t, models.OrderStatus("").Valid())
}

// TestOrderSummaryOutstanding kalan tutarın hesabını doğrular.
//
// Değerin NEGATİF olabilmesi bilinçlidir: fazla tahsilat gerçek bir olgudur ve
// sıfıra kırpmak onu görünmez kılardı.
func TestOrderSummaryOutstanding(t *testing.T) {
	const siparisToplami int64 = 6100

	assert.Equal(t, siparisToplami,
		models.OrderSummary{}.Outstanding(siparisToplami),
		"hiç ödeme yokken tüm tutar kalır")
	assert.Equal(t, int64(0),
		models.OrderSummary{PaidTotal: 6100}.Outstanding(siparisToplami))
	assert.Equal(t, int64(1000),
		models.OrderSummary{PaidTotal: 6100, RefundedTotal: 1000}.Outstanding(siparisToplami),
		"iade edilen tutar yeniden borç hâline gelir")
	assert.Equal(t, int64(-400),
		models.OrderSummary{PaidTotal: 6500}.Outstanding(siparisToplami),
		"fazla tahsilat negatif kalan olarak görünmeli")
}

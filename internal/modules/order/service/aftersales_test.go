package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestCreateReturnKayitAcarVeListeler iade iskeletinin temel akışını doğrular.
func TestCreateReturnKayitAcarVeListeler(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	iade, err := o.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      siparis.ID,
		RefundAmount: 3600,
		Reason:       "beden uymadı",
		Metadata:     map[string]any{"kanal": "destek"},
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(iade.ID, models.ReturnIDPrefix))
	assert.Equal(t, models.ReturnRequested, iade.Status,
		"iade bir TALEP olarak doğmalı")
	assert.Equal(t, int64(3600), iade.RefundAmount)

	okunan, err := o.svc.GetReturn(ctx, iade.ID)
	require.NoError(t, err)
	assert.Equal(t, iade.ID, okunan.ID)

	kayitlar, sayi, err := o.svc.ListReturns(ctx, siparis.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), sayi)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, iade.ID, kayitlar[0].ID)
}

// TestSatisSonrasiTutariSiparisToplaminiAsamaz iade/hasar kaydının tutarının
// siparişe BAĞLANDIĞINI doğrular.
//
// Aralık kontrolü (0..MaxTotal) tek başına yetmez: toplamı 6100 olan bir
// siparişe milyonluk bir iade kaydı açılabilirdi. Kayıt bugün para hareketi
// doğurmuyor, ama iade akışı sonraki fazda bu tutarı okuyacak ve hata ancak
// para geri ödenmeye çalışıldığında — kaydın açılmasından çok sonra —
// görünecekti.
func TestSatisSonrasiTutariSiparisToplaminiAsamaz(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	require.Equal(t, int64(6100), siparis.Total)

	_, err = o.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      siparis.ID,
		RefundAmount: siparis.Total + 1,
	})
	require.Error(t, err, "sipariş toplamını aşan iade kaydı açılamamalı")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeRefundExceedsOrder, errors.CodeOf(err))

	_, err = o.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      siparis.ID,
		Type:         models.ClaimRefund,
		RefundAmount: siparis.Total + 1,
	})
	require.Error(t, err, "sipariş toplamını aşan hasar kaydı açılamamalı")
	assert.Equal(t, service.CodeRefundExceedsOrder, errors.CodeOf(err))

	// Tam toplam kadar iade GEÇERLİDİR: sınır dışlayıcı değildir.
	tam, err := o.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      siparis.ID,
		RefundAmount: siparis.Total,
	})
	require.NoError(t, err)
	assert.Equal(t, siparis.Total, tam.RefundAmount)

	// Hiçbir kayıt yazılmamış olmalı: reddedilen iki çağrıdan geriye yalnızca
	// geçerli olan kalır.
	_, sayi, err := o.svc.ListReturns(ctx, siparis.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), sayi)
}

// TestCreateExchangeNegatifFarkKabulEder değişim farkının iki yönde de
// doğabildiğini doğrular.
//
// Negatif fark "müşteriye ödenecek" demektir; reddetmek, ucuz bir ürünle
// yapılan değişimi kaydedilemez kılardı.
func TestCreateExchangeNegatifFarkKabulEder(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	degisim, err := o.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID:       siparis.ID,
		DifferenceDue: -500,
		Note:          "daha ucuz modelle değişim",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(-500), degisim.DifferenceDue)
	assert.Equal(t, models.ExchangeRequested, degisim.Status)

	kayitlar, sayi, err := o.svc.ListExchanges(ctx, siparis.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), sayi)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, degisim.ID, kayitlar[0].ID)
}

// TestCreateClaimTurunuDogrular hasar kaydı türünün kurallarını doğrular.
func TestCreateClaimTurunuDogrular(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	iadeli, err := o.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      siparis.ID,
		Type:         models.ClaimRefund,
		RefundAmount: 1200,
		Reason:       "kırık geldi",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ClaimRequested, iadeli.Status)
	assert.True(t, strings.HasPrefix(iadeli.ID, models.ClaimIDPrefix))

	// Yerine yenisi gönderilen talepte iade edilecek para YOKTUR; dolu bir
	// tutar müşterinin hem malı hem parayı aldığı sessiz bir çift ödeme olurdu.
	_, err = o.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      siparis.ID,
		Type:         models.ClaimReplace,
		RefundAmount: 1200,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "refund_amount")

	_, err = o.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: siparis.ID,
		Type:    models.ClaimType("iade"),
	})
	require.Error(t, err, "tanımsız tür reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSatisSonrasiKayitlariIptalEdilmisSiparisteAcilamaz iptal edilmiş
// siparişte iade/değişim/hasar kaydı açılamadığını doğrular.
//
// İptal edilmiş siparişte teslim edilmiş mal yoktur: iade edilecek, değişecek
// ya da hasar görecek bir şey de yoktur.
func TestSatisSonrasiKayitlariIptalEdilmisSiparisteAcilamaz(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	require.NoError(t, o.svc.CancelOrder(ctx, siparis.ID, "ödeme reddedildi"))

	testler := map[string]func() error{
		"iade": func() error {
			_, err := o.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: siparis.ID})
			return err
		},
		"değişim": func() error {
			_, err := o.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: siparis.ID})
			return err
		},
		"hasar": func() error {
			_, err := o.svc.CreateClaim(ctx, service.CreateClaimInput{
				OrderID: siparis.ID, Type: models.ClaimRefund,
			})
			return err
		},
	}

	for ad, cagir := range testler {
		t.Run(ad, func(t *testing.T) {
			err := cagir()

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
		})
	}

	kayitlar, sayi, err := o.svc.ListReturns(ctx, siparis.ID, service.Page{})
	require.NoError(t, err)
	assert.Zero(t, sayi)
	assert.Empty(t, kayitlar)
}

// TestSatisSonrasiKayitlariSiparisKilidiniAlir kontrolün YARIŞSIZ yapıldığını
// doğrular.
//
// Kilitsiz bir kontrol yalnızca "o an" doğru olurdu: kontrol ile yazma arasında
// sipariş iptal edilebilir ve kayıt iptal edilmiş bir siparişe bağlanabilirdi.
func TestSatisSonrasiKayitlariSiparisKilidiniAlir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	_, err = o.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: siparis.ID})
	require.NoError(t, err)

	assert.Contains(t, o.store.lockedOrders, siparis.ID,
		"iade kaydı siparişin kilidini almalı")
}

// TestSatisSonrasiKayitlariOlmayanSiparisteNotFound eksik siparişte NotFound
// döndüğünü doğrular.
func TestSatisSonrasiKayitlariOlmayanSiparisteNotFound(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	_, err := o.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: "order_YOK"})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestSatisSonrasiListelemeSayfalamaDogrular sayfalama parametrelerinin
// doğrulandığını gösterir.
func TestSatisSonrasiListelemeSayfalamaDogrular(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	_, _, err := o.svc.ListClaims(ctx, "", service.Page{})
	require.Error(t, err, "boş sipariş kimliği reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, _, err = o.svc.ListExchanges(ctx, "order_X", service.Page{Limit: -1})
	require.Error(t, err, "negatif limit reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

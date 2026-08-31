package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// TestNewEksikBagimlilikliKurulumuReddeder eksik bağımlılığın kurulumda
// söylendiğini doğrular.
//
// nil bir depoyla kurulmuş servis ilk OLAYDA panik üretirdi ve hata,
// kurulumdan çok sonra — ilk siparişin verildiği anda — ortaya çıkardı.
func TestNewEksikBagimlilikliKurulumuReddeder(t *testing.T) {
	tam := service.Options{
		Store:      newFakeStore(),
		Providers:  service.NewProviderRegistry(),
		ProviderID: "log",
		Contacts:   &fakeContacts{},
	}

	tests := map[string]func(o *service.Options){
		"deposuz":      func(o *service.Options) { o.Store = nil },
		"kayıtsız":     func(o *service.Options) { o.Providers = nil },
		"sağlayıcısız": func(o *service.Options) { o.ProviderID = "" },
		"okuyucusuz":   func(o *service.Options) { o.Contacts = nil },
	}

	for ad, boz := range tests {
		t.Run(ad, func(t *testing.T) {
			opts := tam
			boz(&opts)

			_, err := service.New(opts)

			require.Error(t, err)
			assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
		})
	}

	svc, err := service.New(tam)
	require.NoError(t, err)
	assert.Equal(t, "log", svc.ProviderID())
}

// TestListDeliveriesTaninmayanDurumuReddeder yanlış yazılmış bir durum
// süzgecinin sessizce boş liste döndürmediğini doğrular.
//
// Sessiz boş liste, "hiç başarısız bildirim yok" ile "durum adını yanlış
// yazdım"ı ayırt edilemez hâle getirirdi — ilki rahatlatıcı, ikincisi
// yanıltıcıdır.
func TestListDeliveriesTaninmayanDurumuReddeder(t *testing.T) {
	svc, _, _ := kurulum(t)
	durum := "gonderildi"

	_, _, err := svc.ListDeliveries(context.Background(),
		service.ListDeliveriesInput{Status: &durum})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata: %v", err)
	assert.Contains(t, err.Error(), "gonderildi")
}

// TestListDeliveriesSayfalamaSinirlariniZorlar limit/offset doğrulamasını ve
// varsayılanı doğrular.
func TestListDeliveriesSayfalamaSinirlariniZorlar(t *testing.T) {
	svc, _, _ := kurulum(t)
	ctx := context.Background()

	_, _, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})
	require.Error(t, err, "tavanı aşan limit reddedilmeli")

	_, _, err = svc.ListDeliveries(ctx, service.ListDeliveriesInput{
		Page: service.Page{Offset: -1},
	})
	require.Error(t, err, "negatif offset reddedilmeli")

	require.NoError(t, svc.Notify(ctx, testGirdi()))

	kayitlar, toplam, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{})
	require.NoError(t, err)
	assert.Len(t, kayitlar, 1)
	assert.Equal(t, int64(1), toplam)
}

// TestListDeliveriesReferansaGoreSuzer bir siparişin bildirimlerini bulma
// yolunu doğrular; günlüğün en sık sorusu budur.
func TestListDeliveriesReferansaGoreSuzer(t *testing.T) {
	svc, _, _ := kurulum(t)
	ctx := context.Background()

	ilk := testGirdi()
	ikinci := testGirdi()
	ikinci.Reference = "order_DIGER"

	require.NoError(t, svc.Notify(ctx, ilk))
	require.NoError(t, svc.Notify(ctx, ikinci))

	referans := "order_DIGER"
	kayitlar, toplam, err := svc.ListDeliveries(ctx,
		service.ListDeliveriesInput{Reference: &referans})

	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, int64(1), toplam)
	assert.Equal(t, "order_DIGER", kayitlar[0].Reference)
	assert.Equal(t, models.DeliverySent, kayitlar[0].Status)
}

// TestGetDeliveryBosKimligiReddeder boş kimliğin depoya hiç gitmediğini
// doğrular.
func TestGetDeliveryBosKimligiReddeder(t *testing.T) {
	svc, _, _ := kurulum(t)

	_, err := svc.GetDelivery(context.Background(), "")

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata: %v", err)
}

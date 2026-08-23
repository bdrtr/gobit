package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestProviderEntityAdiSablonaUyar sağlayıcının sunduğu entity adının,
// container'a kaydedildiği adın öneki olduğunu doğrular.
//
// Query sağlayıcıyı "<entity>.query" adıyla arar ve Entity() ile örtüşmeyi
// DOĞRULAR (ADR 0004); iki ad ayrışırsa hata çalışma zamanına kalır.
func TestProviderEntityAdiSablonaUyar(t *testing.T) {
	svc, _, _ := yeniServis(t)

	provider := service.NewQueryProvider(svc)

	assert.Equal(t, "cart", provider.Entity())
	assert.Equal(t, "cart.query", provider.Entity()+query.ProviderSuffix)
}

// TestProviderListKayitDoner listelemenin kayıt döndürdüğünü ve birleştirme
// anahtarını (id) taşıdığını doğrular.
func TestProviderListKayitDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)
	provider := service.NewQueryProvider(svc)

	records, err := provider.List(ctx, query.ListOptions{})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, cart.ID, records[0][query.IDField])
	assert.Equal(t, regionID, records[0][service.FieldRegionID])
	assert.Equal(t, customerID, records[0][service.FieldCustomerID])
	assert.Equal(t, currency, records[0][service.FieldCurrencyCode])
	assert.Equal(t, false, records[0][service.FieldCompleted])
	assert.Equal(t, false, records[0][service.FieldTotalsStale])
	assert.Nil(t, records[0][service.FieldCompletedAt])
}

// TestProviderBayatToplamiBildirir toplamların bayat olduğunun sağlayıcı
// kaydında GÖRÜNDÜĞÜNÜ doğrular.
//
// Bayatlık toplamlarla birlikte sunulmasaydı, cross-module bir okuma eski bir
// tutarı güncel sanırdı.
func TestProviderBayatToplamiBildirir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(ctx, []string{cart.ID},
		[]string{query.IDField, service.FieldTotal, service.FieldTotalsStale})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, true, records[0][service.FieldTotalsStale])
	assert.Equal(t, int64(0), records[0][service.FieldTotal])
}

// TestProviderAlanSecimiUygulanir istenen alan kümesinin birebir döndüğünü
// doğrular.
func TestProviderAlanSecimiUygulanir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(ctx, []string{cart.ID},
		[]string{query.IDField, service.FieldCurrencyCode})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2, "yalnızca istenen alanlar dönmeli")
	assert.Contains(t, records[0], query.IDField)
	assert.Contains(t, records[0], service.FieldCurrencyCode)
}

// TestProviderTanimsizAlanReddedilir sunulmayan bir alanın errors.Invalid ile
// reddedildiğini doğrular (ADR 0004).
func TestProviderTanimsizAlanReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	provider := service.NewQueryProvider(svc)

	_, err := provider.List(ctx, query.ListOptions{Fields: []string{"gizli_alan"}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = provider.FetchByIDs(ctx, []string{"cart_X"}, []string{"gizli_alan"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderFiltreleriUygular desteklenen filtrelerin çalıştığını,
// desteklenmeyenin reddedildiğini doğrular.
func TestProviderFiltreleriUygular(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	provider := service.NewQueryProvider(svc)

	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)
	_, err = svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionOther, CurrencyCode: currency,
	})
	require.NoError(t, err)

	records, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCustomerID: customerID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCompleted: false},
	})
	require.NoError(t, err)
	assert.Len(t, records, 2)

	_, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"email": "a@b.c"},
	})
	require.Error(t, err, "desteklenmeyen filtre reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCompleted: "evet"},
	})
	require.Error(t, err, "yanlış tipli filtre reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderSinirsizIstekTavanaKirpilir çekirdeğin "0 = sınırsız"
// sözleşmesinin sağlayıcı tavanına çevrildiğini doğrular.
//
// Sınırsız bir kök sorgu tüm sepet tablosunu belleğe alırdı; kırpma sessizdir
// ve hata dönmez, çünkü limit burada istemci girdisi değil sorgu tanımıdır.
func TestProviderSinirsizIstekTavanaKirpilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	provider := service.NewQueryProvider(svc)

	for range 3 {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CurrencyCode: currency,
		})
		require.NoError(t, err)
	}

	for _, limit := range []int{0, -5, int(service.MaxLimit) + 1000} {
		records, err := provider.List(ctx, query.ListOptions{Limit: limit})
		require.NoError(t, err, "limit %d hata üretmemeli", limit)
		assert.Len(t, records, 3)
	}
}

// TestProviderBulunamayanKimlikHataDegil eksik kimliğin kayıt döndürmediğini
// ama hata da üretmediğini doğrular (ADR 0004 sözleşmesi).
func TestProviderBulunamayanKimlikHataDegil(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(ctx, []string{cart.ID, "cart_YOK"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, cart.ID, records[0][query.IDField])
}

// TestProviderBosKimlikListesiBosDilim boş kimlik listesinin boş (nil olmayan)
// dilim döndürdüğünü doğrular.
func TestProviderBosKimlikListesiBosDilim(t *testing.T) {
	svc, _, _ := yeniServis(t)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(context.Background(), nil, nil)

	require.NoError(t, err)
	assert.NotNil(t, records)
	assert.Empty(t, records)
}

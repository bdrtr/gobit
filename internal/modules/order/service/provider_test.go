package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestQueryProviderEntityAdi sağlayıcının kaydedildiği adla örtüştüğünü
// doğrular.
//
// Query sağlayıcıyı "<entity>.query" adıyla arar ve Entity() ile adın
// örtüştüğünü denetler (ADR 0004); ikisinin ayrışması çalışma zamanında
// NotFound demek olurdu.
func TestQueryProviderEntityAdi(t *testing.T) {
	o := yeniOrtam(t)

	assert.Equal(t, service.EntityName, service.NewQueryProvider(o.svc).Entity())
	assert.Equal(t, "order", service.EntityName)
}

// TestQueryProviderAlanlariUretir istenen alanların üretildiğini doğrular.
func TestQueryProviderAlanlariUretir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	p := service.NewQueryProvider(o.svc)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	kayitlar, err := p.List(ctx, query.ListOptions{
		Fields: []string{service.FieldID, service.FieldDisplayID, service.FieldStatus, service.FieldTotal},
	})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)

	assert.Equal(t, query.Record{
		service.FieldID:        siparis.ID,
		service.FieldDisplayID: siparis.DisplayID,
		service.FieldStatus:    models.OrderPending.String(),
		service.FieldTotal:     int64(6100),
	}, kayitlar[0])
}

// TestQueryProviderAlansizIstekTumAlanlariDoner alan seçilmeyen istekte
// varsayılan kümenin döndüğünü doğrular.
func TestQueryProviderAlansizIstekTumAlanlariDoner(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	p := service.NewQueryProvider(o.svc)

	_, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	kayitlar, err := p.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)

	for _, alan := range []string{
		service.FieldID, service.FieldDisplayID, service.FieldStatus,
		service.FieldRegionID, service.FieldCustomerID, service.FieldEmail,
		service.FieldCurrencyCode, service.FieldCartID, service.FieldSubtotal,
		service.FieldDiscountTotal, service.FieldTaxTotal, service.FieldShippingTotal,
		service.FieldTotal, service.FieldPlacedAt, service.FieldCompletedAt,
		service.FieldCanceledAt, service.FieldCreatedAt, service.FieldUpdatedAt,
	} {
		assert.Contains(t, kayitlar[0], alan)
	}
	assert.Nil(t, kayitlar[0][service.FieldCompletedAt],
		"tamamlanmamış siparişte damga nil olmalı")
}

// TestQueryProviderTanimsizAlaniReddeder sunulmayan alanın Invalid ile
// reddedildiğini doğrular (ADR 0004).
func TestQueryProviderTanimsizAlaniReddeder(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	p := service.NewQueryProvider(o.svc)

	_, err := p.List(ctx, query.ListOptions{Fields: []string{"cancel_reason"}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.FetchByIDs(ctx, []string{"order_1"}, []string{"gizli_alan"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestQueryProviderFiltreleri desteklenen ve desteklenmeyen filtreleri
// doğrular.
func TestQueryProviderFiltreleri(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	p := service.NewQueryProvider(o.svc)

	misafir := gecerliGirdi()
	misafir.CustomerID = ""
	_, err := o.svc.CreateOrder(ctx, misafir)
	require.NoError(t, err)
	_, err = o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	kayitlar, err := p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCustomerID: testCustomerID},
		Fields:  []string{service.FieldID},
	})
	require.NoError(t, err)
	assert.Len(t, kayitlar, 1)

	kayitlar, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldStatus: models.OrderPending.String()},
		Fields:  []string{service.FieldID},
	})
	require.NoError(t, err)
	assert.Len(t, kayitlar, 2)

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCustomerID: 42},
	})
	require.Error(t, err, "yanlış tipli filtre reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{"email": "a@b.com"},
	})
	require.Error(t, err, "desteklenmeyen filtre reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestQueryProviderFetchByIDsBatchOkur toplu okumanın davranışını doğrular.
//
// Bulunamayan kimlik HATA DEĞİLDİR; yalnızca kayıt dönmez (ADR 0004).
func TestQueryProviderFetchByIDsBatchOkur(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	p := service.NewQueryProvider(o.svc)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	kayitlar, err := p.FetchByIDs(ctx, []string{siparis.ID, "order_YOK"}, []string{service.FieldID})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, siparis.ID, kayitlar[0][service.FieldID])

	bos, err := p.FetchByIDs(ctx, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, bos)
}

// TestQueryProviderSinirsizIstegiTavanaKirpar çekirdeğin "0 = sınırsız"
// sözleşmesinin sağlayıcı tavanına indirildiğini doğrular.
//
// Sınırsız bir kök sorgu tüm sipariş tablosunu belleğe alırdı. İstek
// reddedilmez, KIRPILIR: çağıran açıkça "hepsini istiyorum" demiştir ve
// alabileceğinin en fazlasını almalıdır.
func TestQueryProviderSinirsizIstegiTavanaKirpar(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	p := service.NewQueryProvider(o.svc)

	_, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	for _, limit := range []int{0, -5, int(service.MaxLimit) + 1000} {
		kayitlar, err := p.List(ctx, query.ListOptions{Limit: limit, Fields: []string{service.FieldID}})
		require.NoError(t, err, "limit=%d reddedilmemeli", limit)
		assert.Len(t, kayitlar, 1)
	}
}

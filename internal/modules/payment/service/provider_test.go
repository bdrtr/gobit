package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestQueryProviderEntityAdi sağlayıcının kayıtlı olduğu adla örtüştüğünü
// doğrular; Query bunu kayıt anında denetler (ADR 0004).
func TestQueryProviderEntityAdi(t *testing.T) {
	svc, _, _ := yeniServis(t)

	assert.Equal(t, "payment_collection", service.NewQueryProvider(svc).Entity())
	assert.Equal(t, service.EntityName, service.NewQueryProvider(svc).Entity())
}

// TestQueryProviderListAlanlariUretir alan seçiminin çalıştığını doğrular.
func TestQueryProviderListAlanlariUretir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	p := service.NewQueryProvider(svc)

	records, err := p.List(ctx, query.ListOptions{
		Fields: []string{service.FieldID, service.FieldAmount, service.FieldStatus},
	})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, col.ID, records[0][service.FieldID])
	assert.Equal(t, tutar, records[0][service.FieldAmount])
	assert.Equal(t, models.CollectionNotPaid.String(), records[0][service.FieldStatus])
	assert.Len(t, records[0], 3, "yalnızca istenen alanlar dönmeli")
}

// TestQueryProviderAlansizIstekTumAlanlariDoner alan verilmediğinde sunulan
// tüm alanların döndüğünü doğrular.
func TestQueryProviderAlansizIstekTumAlanlariDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)
	koleksiyonAc(t, svc, tutar)
	p := service.NewQueryProvider(svc)

	records, err := p.List(context.Background(), query.ListOptions{})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Contains(t, records[0], service.FieldReference)
	assert.Contains(t, records[0], service.FieldCurrencyCode)
	assert.Contains(t, records[0], service.FieldAuthorizedAmount)
	assert.Contains(t, records[0], service.FieldCapturedAmount)
	assert.Contains(t, records[0], service.FieldRefundedAmount)
	assert.Contains(t, records[0], service.FieldCreatedAt)
	assert.Contains(t, records[0], service.FieldUpdatedAt)
	assert.NotContains(t, records[0], "metadata", "metadata bilinçli olarak sunulmaz")
}

// TestQueryProviderTaninmayanAlanReddedilir ADR 0004'ün şartını doğrular:
// sağlayıcı desteklemediği bir alan görürse errors.Invalid dönmelidir.
func TestQueryProviderTaninmayanAlanReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	p := service.NewQueryProvider(svc)
	ctx := context.Background()

	_, err := p.List(ctx, query.ListOptions{Fields: []string{"metadata"}})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)

	_, err = p.FetchByIDs(ctx, []string{"paycol_X"}, []string{"secret"})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
}

// TestQueryProviderSuzgecleri desteklenen ve desteklenmeyen süzgeçleri sınar.
func TestQueryProviderSuzgecleri(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	koleksiyonAc(t, svc, tutar)
	p := service.NewQueryProvider(svc)

	records, err := p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldReference: referans},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldReference: "cart_YOK"},
	})
	require.NoError(t, err)
	assert.Empty(t, records)

	_, err = p.List(ctx, query.ListOptions{Filters: map[string]any{"amount": "x"}})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)

	_, err = p.List(ctx, query.ListOptions{Filters: map[string]any{service.FieldStatus: 42}})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "süzgeç metin olmalı: %v", err)
}

// TestQueryProviderFetchByIDsBatch kimlik kümesinin tek turda çözüldüğünü ve
// eksik kimliğin hata OLMADIĞINI doğrular (ADR 0004).
func TestQueryProviderFetchByIDsBatch(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	ilk := koleksiyonAc(t, svc, tutar)
	ikinci := koleksiyonAc(t, svc, tutar)
	p := service.NewQueryProvider(svc)

	records, err := p.FetchByIDs(ctx, []string{ilk.ID, ikinci.ID, "paycol_YOK"}, []string{service.FieldID})

	require.NoError(t, err)
	assert.Len(t, records, 2, "bulunamayan kimlik için kayıt DÖNMEZ")

	bos, err := p.FetchByIDs(ctx, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, bos)
}

// TestQueryProviderLimitTavanaKirpilir çekirdeğin "sınırsız" limitinin
// sağlayıcı tavanına indirildiğini doğrular.
//
// Sınırsız bir kök sorgu tüm koleksiyon tablosunu belleğe alırdı; kırpma
// sessizdir ve hata dönmez, çünkü buradaki limit istemci girdisi değil başka
// bir modülün sorgu tanımından gelir.
func TestQueryProviderLimitTavanaKirpilir(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	koleksiyonAc(t, svc, tutar)
	p := service.NewQueryProvider(svc)

	for _, limit := range []int{0, -5, int(service.MaxLimit) + 1} {
		_, err := p.List(ctx, query.ListOptions{Limit: limit})
		require.NoError(t, err, "limit %d hata vermemeli", limit)
	}

	// Kırpmanın gerçekten uygulandığı, servisin sayfalama doğrulamasını
	// geçmesinden anlaşılır: tavanı aşan bir limit doğrudan geçseydi
	// errors.Invalid dönerdi.
	_, count, err := store.ListPaymentCollections(ctx, models.CollectionFilter{Limit: service.MaxLimit})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

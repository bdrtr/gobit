package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// yeniSaglayici sahte depo üzerinde çalışan bir Query sağlayıcısı kurar.
func yeniSaglayici(t *testing.T) (*service.QueryProvider, *fakeStore) {
	t.Helper()

	svc, store := yeniServis(t)
	return service.NewQueryProvider(svc), store
}

// TestProviderEntity sağlayıcının container'a kaydedileceği adla tutarlı
// olduğunu doğrular; Query kayıt adının önekiyle Entity()'yi karşılaştırır.
func TestProviderEntity(t *testing.T) {
	provider, _ := yeniSaglayici(t)

	assert.Equal(t, "inventory_item", provider.Entity())
	assert.Equal(t, service.EntityName, provider.Entity())
}

// TestFetchByIDsSatilabilirAdetIcerir sağlayıcının kalemi TOPLAM satılabilir
// adediyle döndürdüğünü doğrular. product'ın mağaza listelemesi stoğu tek
// çağrıda bu alandan okur; alan eksik ya da yanlış olursa ürün stoksuz görünür.
func TestFetchByIDsSatilabilirAdetIcerir(t *testing.T) {
	provider, store := yeniSaglayici(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedLevel(itemID, locB, 5, 1)

	records, err := provider.FetchByIDs(context.Background(), []string{itemID}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, itemID, records[0][query.IDField])
	assert.Equal(t, "SKU-1", records[0][service.FieldSKU])
	assert.Equal(t, int64(10), records[0][service.FieldAvailableQuantity],
		"(10-4) + (5-1) = 10 olmalı")
	assert.Equal(t, true, records[0][service.FieldRequiresShipping])
}

// TestFetchByIDsSeviyesizKalemSifirDoner hiç stok seviyesi olmayan kalemin
// kayıttan DÜŞMEDİĞİNİ, satılabilir adedinin sıfır geldiğini doğrular.
// Düşseydi stoksuz ürünler mağaza listelemesinden tamamen kaybolurdu.
func TestFetchByIDsSeviyesizKalemSifirDoner(t *testing.T) {
	provider, store := yeniSaglayici(t)
	store.seedItem(itemID, "SKU-1")

	records, err := provider.FetchByIDs(context.Background(), []string{itemID}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, int64(0), records[0][service.FieldAvailableQuantity])
}

// TestFetchByIDsTekTurdaCalisir kaç kalem istenirse istensin satılabilirlik
// için TEK sorgu yapıldığını doğrular (ADR 0004: N+1 yapısal olarak yasak).
func TestFetchByIDsTekTurdaCalisir(t *testing.T) {
	provider, store := yeniSaglayici(t)
	ids := []string{}
	for _, id := range []string{"invitem_1", "invitem_2", "invitem_3"} {
		store.seedItem(id, "SKU-"+id)
		store.seedLevel(id, locA, 5, 1)
		ids = append(ids, id)
	}

	records, err := provider.FetchByIDs(context.Background(), ids, nil)

	require.NoError(t, err)
	assert.Len(t, records, 3)
	assert.Equal(t, 1, store.availableCalls, "kayıt başına değil, toplu tek çağrı yapılmalı")
}

// TestFetchByIDsBulunamayanKimlikHataDegil bulunamayan kimlik için kayıt
// dönmediğini ama hata da dönmediğini doğrular (ADR 0004).
func TestFetchByIDsBulunamayanKimlikHataDegil(t *testing.T) {
	provider, store := yeniSaglayici(t)
	store.seedItem(itemID, "SKU-1")

	records, err := provider.FetchByIDs(context.Background(), []string{itemID, unknown}, nil)

	require.NoError(t, err)
	assert.Len(t, records, 1)
}

// TestFetchByIDsBosKimlikListesi boş kimlik listesinin sağlayıcıya hiç
// gitmeden boş sonuç döndürdüğünü doğrular.
func TestFetchByIDsBosKimlikListesi(t *testing.T) {
	provider, store := yeniSaglayici(t)

	records, err := provider.FetchByIDs(context.Background(), nil, nil)

	require.NoError(t, err)
	assert.Empty(t, records)
	assert.Zero(t, store.availableCalls)
}

// TestFetchByIDsAlanSecimi yalnızca istenen alanların döndüğünü ve satılabilir
// adet istenmediğinde HİÇ HESAPLANMADIĞINI doğrular.
func TestFetchByIDsAlanSecimi(t *testing.T) {
	provider, store := yeniSaglayici(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)

	records, err := provider.FetchByIDs(context.Background(), []string{itemID},
		[]string{query.IDField, service.FieldSKU})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2)
	assert.Equal(t, "SKU-1", records[0][service.FieldSKU])
	assert.NotContains(t, records[0], service.FieldAvailableQuantity)
	assert.Zero(t, store.availableCalls, "istenmeyen alan için sorgu yapılmamalı")
}

// TestFetchByIDsBilinmeyenAlanInvalid sunulmayan bir alan istendiğinde
// errors.Invalid dönüldüğünü doğrular (ADR 0004).
func TestFetchByIDsBilinmeyenAlanInvalid(t *testing.T) {
	provider, store := yeniSaglayici(t)
	store.seedItem(itemID, "SKU-1")

	_, err := provider.FetchByIDs(context.Background(), []string{itemID}, []string{"fiyat"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "fiyat")
}

// TestListSKUFiltresi kök listelemede sku filtresinin uygulandığını doğrular.
func TestListSKUFiltresi(t *testing.T) {
	provider, store := yeniSaglayici(t)
	store.seedItem("invitem_1", "SKU-1")
	store.seedItem("invitem_2", "SKU-2")
	store.seedLevel("invitem_2", locA, 3, 0)

	records, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{service.FieldSKU: "SKU-2"},
	})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "invitem_2", records[0][query.IDField])
	assert.Equal(t, int64(3), records[0][service.FieldAvailableQuantity])
}

// TestListBilinmeyenFiltreInvalid desteklenmeyen filtrenin sessizce
// yok sayılmadığını doğrular; yok sayılsaydı çağıran süzülmemiş bir listeyi
// süzülmüş sanırdı.
func TestListBilinmeyenFiltreInvalid(t *testing.T) {
	provider, _ := yeniSaglayici(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"renk": "kirmizi"},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "renk")
}

// TestListFiltreTipiDogrulanir filtre değerinin tipinin denetlendiğini
// doğrular.
func TestListFiltreTipiDogrulanir(t *testing.T) {
	provider, _ := yeniSaglayici(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{service.FieldSKU: 42},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListSayfalar limit/offset'in kök listelemede uygulandığını doğrular.
func TestListSayfalar(t *testing.T) {
	provider, store := yeniSaglayici(t)
	for _, id := range []string{"invitem_1", "invitem_2", "invitem_3"} {
		store.seedItem(id, "SKU-"+id)
	}

	records, err := provider.List(context.Background(), query.ListOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, records, 2)

	records, err = provider.List(context.Background(), query.ListOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, records, 1)
}

// TestProviderQuerySozlesmesiniKarsilar sağlayıcının çekirdeğin beklediği
// arayüzü karşıladığını çalışma zamanında da doğrular; container'dan adla
// çözüm bu dönüşümü yapar.
func TestProviderQuerySozlesmesiniKarsilar(t *testing.T) {
	provider, _ := yeniSaglayici(t)

	var asProvider any = provider
	_, ok := asProvider.(query.Provider)

	assert.True(t, ok, "QueryProvider, query.Provider arayüzünü karşılamalı")
}

// TestListLimitTavanaKirpilir çekirdekten gelen limitin sağlayıcının sayfa
// tavanına kırpıldığını doğrular.
//
// Çekirdek sözleşmesinde Limit=0 "sınırsız" demektir. Bu sağlayıcı sınırsız
// listeleme sunmaz; sınırsız isteği varsayılan sayfa boyutuna indirseydi
// çağıran hata almadan EKSİK veri alır ve hepsini aldığını sanırdı. Tavanı
// aşan bir limit de reddedilmez: çekirdek yolunda hata dönmek, tek bir sayı
// yüzünden hiç veri döndürmemek olurdu.
func TestListLimitTavanaKirpilir(t *testing.T) {
	provider, store := yeniSaglayici(t)
	// Fikstür hem MaxLimit'in (100) hem DefaultLimit'in (50) üstündedir;
	// ikisi arasındaki farkı ancak böyle bir küme gösterir.
	const kalemSayisi = 120
	for i := range kalemSayisi {
		id := fmt.Sprintf("invitem_%03d", i)
		store.seedItem(id, "SKU-"+id)
	}
	tavan := int(service.MaxLimit)

	durumlar := []struct {
		ad       string
		limit    int
		beklenen int
	}{
		{"sınırsız istek tavana çıkar", 0, tavan},
		{"tavanı aşan limit kırpılır", tavan + 1, tavan},
		{"tavanın kendisi geçerlidir", tavan, tavan},
		{"tavanın altı aynen uygulanır", 7, 7},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			records, err := provider.List(context.Background(), query.ListOptions{Limit: durum.limit})

			require.NoError(t, err, "sağlayıcı limiti hata değil, kırpma sebebi saymalı")
			assert.Len(t, records, durum.beklenen)
		})
	}
}

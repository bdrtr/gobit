package query_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Testlerde kullanılan link tanımları. Adlar ve uçlar plan Bölüm 6'daki
// "önemli linkler" listesine sadıktır.
var (
	// productVariant bir ürünün varyantlarına bağlar: bir ürün, çok varyant.
	productVariant = link.LinkDefinition{
		Name:        "product_variant",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "variant", Field: "variant_id"},
		Cardinality: link.OneToMany,
	}
	// variantPrice bir varyantı tek bir fiyat kümesine bağlar.
	variantPrice = link.LinkDefinition{
		Name:        "variant_price",
		From:        link.LinkSide{Module: "variant", Field: "variant_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToOne,
	}
	// productChannel bir ürünü satış kanallarına bağlar: çoktan çoğa.
	productChannel = link.LinkDefinition{
		Name:        "product_channel",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "channel", Field: "channel_id"},
		Cardinality: link.ManyToMany,
	}
)

// --- kök çekme --------------------------------------------------------------

func TestGraphKokKayitlariCekerVeAlanSeciminiIletir(t *testing.T) {
	products := newProvider("product",
		query.Record{"id": "prod_1", "title": "Kırmızı Tişört", "gizli": "a"},
		query.Record{"id": "prod_2", "title": "Mavi Tişört", "gizli": "b"},
		query.Record{"id": "prod_3", "title": "Yeşil Tişört", "gizli": "c"},
	)
	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity:  "product",
		Fields:  []string{"title"},
		Filters: map[string]any{"status": "published"},
		Limit:   1,
		Offset:  1,
	})
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, query.Record{"title": "Mavi Tişört"}, got[0])

	opts := products.opts()
	assert.Equal(t, []string{"title"}, opts.Fields,
		"genişletme yokken kimlik alanı eklenmemeli")
	assert.Equal(t, map[string]any{"status": "published"}, opts.Filters)
	assert.Equal(t, 1, opts.Limit)
	assert.Equal(t, 1, opts.Offset)
	assert.Equal(t, providerCalls{list: 1}, products.calls())
}

func TestGraphKokKayitYoksaBosDilimDoner(t *testing.T) {
	products := newProvider("product")
	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.NoError(t, err)
	require.NotNil(t, got, "kök kayıt yokken nil değil boş dilim dönmeli")
	assert.Empty(t, got)
}

func TestGraphGenisletmeVarkenKimlikAlaniEklenir(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1", "title": "Tişört"})
	prices := newProvider("pricing", query.Record{"id": "pset_1", "amount": 1990})

	links := newLinks(variantPrice, link.LinkDefinition{
		Name:        "product_price",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToOne,
	})
	links.connect("product_price", "prod_1", "pset_1")

	q := query.New(links, newContainer(t, products, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Fields: []string{"title"},
		Expand: []query.Expansion{{Link: "product_price", Fields: []string{"amount"}}},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"title", "id"}, products.opts().Fields,
		"birleştirme için kimlik alanı kök alan listesine eklenmeli")

	_, fields := prices.fetchArgs()
	assert.Equal(t, []string{"amount", "id"}, fields,
		"birleştirme için kimlik alanı genişletme alan listesine de eklenmeli")

	require.Len(t, got, 1)
	assert.Equal(t, "prod_1", got[0]["id"])
	price, ok := got[0]["product_price"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, query.Record{"id": "pset_1", "amount": 1990}, price)
}

// --- kardinalite ve şekil ---------------------------------------------------

func TestGraphOneToOneGenisletmeTekKayitYazar(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1", "sku": "TS-M"})
	prices := newProvider("pricing", query.Record{"id": "pset_1", "amount": 1990})

	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")
	q := query.New(links, newContainer(t, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	price, ok := got[0]["variant_price"].(query.Record)
	require.Truef(t, ok, "OneToOne genişletmesi tek kayıt yazmalı; gelen tip: %T", got[0]["variant_price"])
	assert.Equal(t, "pset_1", price["id"])
}

func TestGraphOneToManyGenisletmeDilimYazar(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant",
		query.Record{"id": "var_1", "sku": "TS-S"},
		query.Record{"id": "var_2", "sku": "TS-M"},
	)

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1", "var_2")
	q := query.New(links, newContainer(t, products, variants), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	list, ok := got[0]["product_variant"].([]query.Record)
	require.Truef(t, ok, "OneToMany genişletmesi dilim yazmalı; gelen tip: %T", got[0]["product_variant"])
	require.Len(t, list, 2)
	assert.Equal(t, "var_1", list[0]["id"])
	assert.Equal(t, "var_2", list[1]["id"])
}

func TestGraphManyToManyGenisletmeDilimYazar(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	channels := newProvider("channel",
		query.Record{"id": "sc_web"},
		query.Record{"id": "sc_pos"},
	)

	links := newLinks(productChannel).connect("product_channel", "prod_1", "sc_web", "sc_pos")
	q := query.New(links, newContainer(t, products, channels), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_channel"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	list, ok := got[0]["product_channel"].([]query.Record)
	require.Truef(t, ok, "ManyToMany genişletmesi dilim yazmalı; gelen tip: %T", got[0]["product_channel"])
	assert.Len(t, list, 2)
}

func TestGraphEslesmeYokkenSekilKorunur(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant")
	prices := newProvider("pricing")

	links := newLinks(productVariant, link.LinkDefinition{
		Name:        "product_price",
		From:        link.LinkSide{Module: "product", Field: "product_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: link.OneToOne,
	})
	q := query.New(links, newContainer(t, products, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{
			{Link: "product_variant"},
			{Link: "product_price"},
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)

	list, ok := got[0]["product_variant"].([]query.Record)
	require.Truef(t, ok, "çok uçlu genişletme eşleşme yokken de dilim yazmalı; gelen tip: %T",
		got[0]["product_variant"])
	assert.Empty(t, list, "dilim boş olmalı ama nil olmamalı")
	assert.NotNil(t, list)

	require.Contains(t, got[0], "product_price")
	assert.Nil(t, got[0]["product_price"], "tek uçlu genişletme eşleşme yokken nil yazmalı")

	assert.Zero(t, variants.calls().fetch, "ilgili kimlik yokken sağlayıcıya hiç gidilmemeli")
	assert.Zero(t, prices.calls().fetch, "ilgili kimlik yokken sağlayıcıya hiç gidilmemeli")
}

// --- çıktı anahtarı ---------------------------------------------------------

func TestGraphAsBosIkenAnahtarLinkAdidir(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1"})
	prices := newProvider("pricing", query.Record{"id": "pset_1"})

	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")
	q := query.New(links, newContainer(t, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "variant_price", "As boşken anahtar link adı olmalı")
}

func TestGraphAsDoluykenAnahtarAsDegeridir(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1"})
	prices := newProvider("pricing", query.Record{"id": "pset_1"})

	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")
	q := query.New(links, newContainer(t, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price", As: "fiyat"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "fiyat")
	assert.NotContains(t, got[0], "variant_price", "As verildiğinde link adı anahtar olmamalı")
}

// --- iç içe genişletme ------------------------------------------------------

func TestGraphIcIceGenisletmeIkiSeviye(t *testing.T) {
	products := newProvider("product",
		query.Record{"id": "prod_1", "title": "Tişört"},
		query.Record{"id": "prod_2", "title": "Şapka"},
	)
	variants := newProvider("variant",
		query.Record{"id": "var_1", "sku": "TS-S"},
		query.Record{"id": "var_2", "sku": "TS-M"},
		query.Record{"id": "var_3", "sku": "SP-U"},
	)
	prices := newProvider("pricing",
		query.Record{"id": "pset_1", "amount": 1990},
		query.Record{"id": "pset_3", "amount": 2990},
	)

	links := newLinks(productVariant, variantPrice)
	links.connect("product_variant", "prod_1", "var_1", "var_2")
	links.connect("product_variant", "prod_2", "var_3")
	links.connect("variant_price", "var_1", "pset_1")
	links.connect("variant_price", "var_3", "pset_3")

	q := query.New(links, newContainer(t, products, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			As:     "varyantlar",
			Expand: []query.Expansion{{Link: "variant_price", As: "fiyat"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	first, ok := got[0]["varyantlar"].([]query.Record)
	require.True(t, ok)
	require.Len(t, first, 2)

	price, ok := first[0]["fiyat"].(query.Record)
	require.Truef(t, ok, "iç içe OneToOne tek kayıt yazmalı; gelen tip: %T", first[0]["fiyat"])
	assert.Equal(t, 1990, price["amount"])
	assert.Nil(t, first[1]["fiyat"], "bağı olmayan varyantın fiyatı nil olmalı")

	second, ok := got[1]["varyantlar"].([]query.Record)
	require.True(t, ok)
	require.Len(t, second, 1)
	price, ok = second[0]["fiyat"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, 2990, price["amount"])

	// Her seviye kendi içinde tek çağrıyla çözülmeli.
	assert.Equal(t, providerCalls{list: 1}, products.calls())
	assert.Equal(t, providerCalls{fetch: 1}, variants.calls())
	assert.Equal(t, providerCalls{fetch: 1}, prices.calls())
	assert.Equal(t, int64(2), links.listManyCalls.Load(), "genişletme başına tek link turu")
}

// --- N+1 yok ----------------------------------------------------------------

func TestGraphN1Yapmaz(t *testing.T) {
	const (
		kokSayisi     = 100
		varyantSayisi = 2
	)

	links := newLinks(productVariant, variantPrice)
	productRecords := make([]query.Record, 0, kokSayisi)
	variantRecords := make([]query.Record, 0, kokSayisi*varyantSayisi)
	priceRecords := make([]query.Record, 0, kokSayisi*varyantSayisi)

	for i := range kokSayisi {
		productID := fmt.Sprintf("prod_%03d", i)
		productRecords = append(productRecords, query.Record{"id": productID})

		for j := range varyantSayisi {
			variantID := fmt.Sprintf("var_%03d_%d", i, j)
			priceID := fmt.Sprintf("pset_%03d_%d", i, j)

			variantRecords = append(variantRecords, query.Record{"id": variantID})
			priceRecords = append(priceRecords, query.Record{"id": priceID, "amount": 100 * (i + 1)})

			links.connect("product_variant", productID, variantID)
			links.connect("variant_price", variantID, priceID)
		}
	}

	products := newProvider("product", productRecords...)
	variants := newProvider("variant", variantRecords...)
	prices := newProvider("pricing", priceRecords...)

	q := query.New(links, newContainer(t, products, variants, prices), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "variant_price"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, got, kokSayisi)

	// Asıl iddia: kayıt sayısından BAĞIMSIZ olarak genişletme başına tek çağrı.
	assert.Equal(t, providerCalls{list: 1}, products.calls(),
		"kök sağlayıcıya tek List çağrısı yapılmalı")
	assert.Equal(t, providerCalls{fetch: 1}, variants.calls(),
		"%d kök kayıt için variant sağlayıcısına tek FetchByIDs yapılmalı", kokSayisi)
	assert.Equal(t, providerCalls{fetch: 1}, prices.calls(),
		"%d varyant için pricing sağlayıcısına tek FetchByIDs yapılmalı", kokSayisi*varyantSayisi)
	assert.Equal(t, int64(2), links.listManyCalls.Load(),
		"link'ler genişletme başına tek turda çözülmeli")
	assert.Zero(t, links.listCalls.Load(), "kimlik başına List çağrılmamalı")

	// Tek çağrı gerçekten TÜM kimlikleri taşımalı; aksi hâlde sayaç yanıltır.
	variantIDs, _ := variants.fetchArgs()
	assert.Len(t, variantIDs, kokSayisi*varyantSayisi)
	priceIDs, _ := prices.fetchArgs()
	assert.Len(t, priceIDs, kokSayisi*varyantSayisi)

	// Veri de doğru birleşmiş olmalı.
	last, ok := got[kokSayisi-1]["product_variant"].([]query.Record)
	require.True(t, ok)
	require.Len(t, last, varyantSayisi)
	price, ok := last[0]["variant_price"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, 100*kokSayisi, price["amount"])
}

// --- yön --------------------------------------------------------------------

func TestGraphTersYonToplukCozumleTekKayitYazar(t *testing.T) {
	variants := newProvider("variant",
		query.Record{"id": "var_1"},
		query.Record{"id": "var_3"},
	)
	products := newProvider("product",
		query.Record{"id": "prod_1", "title": "Tişört"},
		query.Record{"id": "prod_2", "title": "Şapka"},
	)

	links := newReverseLinks(productVariant)
	links.connect("product_variant", "prod_1", "var_1", "var_2")
	links.connect("product_variant", "prod_2", "var_3")

	q := query.New(links, newContainer(t, variants, products), nil)

	// Kök entity link'in TO ucunda; çözüm ters yönde yapılmalı.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "product_variant", As: "urun"}},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	first, ok := got[0]["urun"].(query.Record)
	require.Truef(t, ok, "OneToMany ters yönde tek kayıt yazmalı; gelen tip: %T", got[0]["urun"])
	assert.Equal(t, "prod_1", first["id"])

	second, ok := got[1]["urun"].(query.Record)
	require.True(t, ok)
	assert.Equal(t, "prod_2", second["id"])

	assert.Equal(t, int64(1), links.listManyByToCalls.Load(), "ters yön de tek turda çözülmeli")
	assert.Equal(t, providerCalls{fetch: 1}, products.calls())
}

func TestGraphLinkKokEntityyeBaglanmiyorsaInvalidDoner(t *testing.T) {
	orders := newProvider("order", query.Record{"id": "order_1"})
	q := query.New(newLinks(productVariant), newContainer(t, orders), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "order",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
	assert.Contains(t, err.Error(), "order")
	assert.Contains(t, err.Error(), "product_variant")
}

// --- teşhis edilebilir hatalar ----------------------------------------------

func TestGraphKokSaglayiciYoksaNotFoundVeArananAdiYazar(t *testing.T) {
	q := query.New(newLinks(), container.New(nil), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)

	// Aranan ad, ALTTAKİ container hatasından değil query'nin KENDİ mesajından
	// okunmalı (ADR 0004); bu yüzden en dıştaki tipli hataya bakılır.
	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Contains(t, typed.Message, "product"+query.ProviderSuffix,
		"query'nin kendi mesajı container'da aranan adı içermeli")
	assert.Equal(t, "product"+query.ProviderSuffix, typed.Details["aranan_ad"])
}

func TestGraphGenisletmeSaglayicisiYoksaNotFoundVeArananAdiYazar(t *testing.T) {
	variants := newProvider("variant", query.Record{"id": "var_1"})
	links := newLinks(variantPrice).connect("variant_price", "var_1", "pset_1")

	// pricing.query bilerek kaydedilmiyor.
	q := query.New(links, newContainer(t, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "variant",
		Expand: []query.Expansion{{Link: "variant_price"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Contains(t, typed.Message, "pricing"+query.ProviderSuffix,
		"query'nin kendi mesajı container'da aranan adı içermeli")
	assert.Equal(t, "pricing"+query.ProviderSuffix, typed.Details["aranan_ad"])
}

func TestGraphSaglayiciEntitysiUyusmuyorsaInvalidDoner(t *testing.T) {
	c := container.New(nil)
	require.NoError(t, c.Provide("product"+query.ProviderSuffix, newProvider("urun")))

	q := query.New(newLinks(), c, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
	assert.Contains(t, err.Error(), "urun")
	assert.Contains(t, err.Error(), "product"+query.ProviderSuffix)
}

func TestGraphBilinmeyenLinkAdiNotFoundDoner(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "yok_boyle_link"}},
	})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)
	assert.Contains(t, err.Error(), "yok_boyle_link")
}

func TestGraphKokSaglayiciHatasiTumCagriyiDusurur(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	products.listErr = errors.Unavailable("product_down", "product modülü kapalı")

	q := query.New(newLinks(), newContainer(t, products), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"alttaki hatanın sınıfı korunmalı, gelen: %v", err)
}

func TestGraphGenisletmeSaglayiciHatasiKismiSonucDondurmez(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1", "title": "Tişört"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	variants.fetchErr = errors.Unavailable("variant_down", "variant modülü kapalı")

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.Nil(t, got, "kök kayıtlar çekilmiş olsa bile kısmi sonuç dönmemeli")
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"alttaki hatanın sınıfı korunmalı, gelen: %v", err)
	assert.Contains(t, err.Error(), "variant")
}

func TestGraphLinkHatasiTumCagriyiDusurur(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	links.listErr = errors.Unavailable("link_db_down", "link tablosuna erişilemiyor")

	q := query.New(links, newContainer(t, products, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "gelen: %v", err)
	assert.Zero(t, variants.calls().fetch, "link çözülemediyse sağlayıcıya gidilmemeli")
}

// --- bağlam -----------------------------------------------------------------

func TestGraphIptalEdilmisBaglamdaErkenCikar(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(), newContainer(t, products), nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := q.Graph(ctx, query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "gelen: %v", err)
	assert.Zero(t, products.calls().list, "iptal edilmiş bağlamda sağlayıcıya hiç gidilmemeli")
}

func TestGraphBaglamGenisletmedenOnceIptalEdilirse(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	// Kök listeleme bittikten hemen sonra bağlam iptal edilir.
	products.afterList = cancel

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	_, err := q.Graph(ctx, query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	// Tanım okuma PLANLAMA aşamasının parçasıdır ve plan bilinçli olarak kök
	// List'ten ÖNCE çalışır (bozuk spec, kök sorgusunun maliyetini ödemeden
	// hata versin diye). Bu yüzden tanım bir kez okunmuş olabilir; önemli olan
	// iptalden SONRA hiçbir bağ çözümü ve genişletme getirmesi yapılmamasıdır.
	assert.Zero(t, links.listManyCalls.Load(), "iptalden sonra link servisine gidilmemeli")
	assert.Zero(t, variants.calls().fetch, "iptalden sonra genişletme sağlayıcısına gidilmemeli")
}

// --- spec doğrulama ---------------------------------------------------------

func TestGraphGecersizSpecReddedilir(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(productVariant), newContainer(t, products), nil)

	tests := map[string]query.GraphSpec{
		"bos entity":     {Entity: ""},
		"negatif limit":  {Entity: "product", Limit: -1},
		"negatif offset": {Entity: "product", Offset: -1},
		"bos link adi": {
			Entity: "product",
			Expand: []query.Expansion{{Link: ""}},
		},
		"cakisan anahtar": {
			Entity: "product",
			Expand: []query.Expansion{
				{Link: "product_variant"},
				{Link: "product_channel", As: "product_variant"},
			},
		},
		"ic ice bos link adi": {
			Entity: "product",
			Expand: []query.Expansion{{
				Link:   "product_variant",
				Expand: []query.Expansion{{Link: ""}},
			}},
		},
	}

	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := q.Graph(t.Context(), spec)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
		})
	}

	assert.Zero(t, products.calls().list, "geçersiz spec sağlayıcıya hiç gitmemeli")
}

func TestGraphAsiriDerinGenisletmeReddedilir(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(newLinks(productVariant), newContainer(t, products), nil)

	// En içteki genişletmeden başlayıp dışa doğru 12 seviye kurulur.
	exp := query.Expansion{Link: "product_variant"}
	for i := range 11 {
		exp = query.Expansion{
			Link:   "product_variant",
			As:     fmt.Sprintf("seviye_%d", i),
			Expand: []query.Expansion{exp},
		}
	}

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{exp},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
	assert.Zero(t, products.calls().list)
}

// --- sağlayıcı sözleşme ihlali ----------------------------------------------

func TestGraphKimliksizKokKayitGenisletilemez(t *testing.T) {
	products := newProvider("product")
	products.order = []string{""}
	products.records = map[string]query.Record{"": {"title": "kimliksiz"}}

	links := newLinks(productVariant)
	q := query.New(links, newContainer(t, products, newProvider("variant")), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "gelen: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
}

func TestGraphKimliksizGenisletmeKaydiHataDondurur(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant")
	// Sağlayıcı kimliksiz bir kayıt döndürüyor; birleştirme yapılamaz.
	variants.order = []string{"var_1"}
	variants.records = map[string]query.Record{"var_1": {"sku": "TS-M"}}

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "gelen: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
}

// --- kurulum hataları -------------------------------------------------------

func TestGraphContainersizKurulumTipliHataDoner(t *testing.T) {
	q := query.New(newLinks(), nil, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "gelen: %v", err)
	assert.Contains(t, err.Error(), "product"+query.ProviderSuffix)
}

func TestGraphLinkServissizKurulumGenisletmedeHataDoner(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	q := query.New(nil, newContainer(t, products), nil)

	// Genişletme yoksa link servisine hiç ihtiyaç duyulmaz.
	got, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	_, err = q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "gelen: %v", err)
}

// --- birleştirme anahtarının korunması --------------------------------------

func TestGraphCiktiAnahtariKokKimliginiEzemez(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1", "title": "Tişört"})
	variants := newProvider("variant", query.Record{"id": "var_1"})

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	// As "id" olsaydı genişletme sonucu kök kaydın kimliğinin ÜZERİNE yazılır ve
	// çağıran kaydı tanıyamaz olurdu; ayrıca sonraki genişletmeler birleştirme
	// anahtarını okuyamazdı.
	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant", As: query.IDField}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
	assert.Zero(t, products.calls().list, "geçersiz spec sağlayıcıya hiç gitmemeli")
}

func TestGraphIcIceCiktiAnahtariKimligiEzemez(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	prices := newProvider("pricing", query.Record{"id": "pset_1"})

	links := newLinks(productVariant, variantPrice)
	q := query.New(links, newContainer(t, products, variants, prices), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "variant_price", As: query.IDField}},
		}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
	assert.Zero(t, products.calls().list)
}

func TestGraphKimligiOkunamayanKokKayitSessizceAtlanmaz(t *testing.T) {
	products := newProvider("product")
	products.order = []string{"prod_1", "kimliksiz"}
	products.records = map[string]query.Record{
		"prod_1":    {"id": "prod_1", "title": "kimlikli"},
		"kimliksiz": {"title": "kimliksiz"},
	}
	variants := newProvider("variant", query.Record{"id": "var_1"})

	links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
	q := query.New(links, newContainer(t, products, variants), nil)

	// Atlanan kayıt genişletme anahtarını HİÇ almaz; sonuç dilimi heterojen
	// kalır ve eksik veri doğru sonuç gibi görünür. Politika kısmi sonuç
	// dönmemektir.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err, "kimliği okunamayan kök kayıt sessizce genişletme dışı bırakılmamalı")
	assert.Nil(t, got, "kısmi sonuç dönmemeli")
	assert.True(t, errors.HasKind(err, errors.KindInternal), "gelen: %v", err)
	assert.Contains(t, err.Error(), query.IDField)
}

func TestGraphKimlikAlaniStringDegilseMesajTipiYazar(t *testing.T) {
	products := newProvider("product")
	products.order = []string{"uuid"}
	// pgx.RowToMap ile beslenen bir sağlayıcıda uuid kolonu böyle gelir.
	products.records = map[string]query.Record{"uuid": {"id": [16]byte{1}, "title": "uuid kimlik"}}
	variants := newProvider("variant", query.Record{"id": "var_1"})

	q := query.New(newLinks(productVariant), newContainer(t, products, variants), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%T", [16]byte{}),
		"alan VAR ama tipi yanlışken mesaj gelen tipi yazmalı; yoksa hata yanlış tarafı suçlar")
}

// --- kayıt sahipliği --------------------------------------------------------

func TestGraphSaglayicininKayitlariniKirletmez(t *testing.T) {
	products := newSharingProvider("product", query.Record{"id": "prod_1", "title": "Tişört"})
	variants := newSharingProvider("variant", query.Record{"id": "var_1", "sku": "TS-M"})
	prices := newSharingProvider("pricing", query.Record{"id": "pset_1", "amount": 1990})

	c := container.New(nil)
	for _, p := range []*sharingProvider{products, variants, prices} {
		require.NoError(t, c.Provide(p.Entity()+query.ProviderSuffix, p))
	}

	links := newLinks(productVariant, variantPrice)
	links.connect("product_variant", "prod_1", "var_1")
	links.connect("variant_price", "var_1", "pset_1")

	q := query.New(links, c, nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "variant_price"}},
		}},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Contains(t, got[0], "product_variant", "genişletme gerçekten yapılmış olmalı")

	// Sağlayıcı kayıtlarını kopyalamıyor; Query kendi kopyasına yazmalı ki
	// modülün durumu kirlenmesin (bayat alan sızması ve eşzamanlı çağrılarda
	// veri yarışı buradan doğar).
	assert.Equal(t, query.Record{"id": "prod_1", "title": "Tişört"}, products.records[0],
		"kök sağlayıcının kaydına genişletme anahtarı yazılmamalı")
	assert.Equal(t, query.Record{"id": "var_1", "sku": "TS-M"}, variants.records[0],
		"genişletme sağlayıcısının kaydına iç içe genişletme anahtarı yazılmamalı")
	assert.Equal(t, query.Record{"id": "pset_1", "amount": 1990}, prices.records[0])
}

// --- iptal sınıflandırması --------------------------------------------------

func TestGraphHamBaglamHatasiUnavailableDoner(t *testing.T) {
	// Sağlayıcı ve link servisi TİPSİZ context hatası dönebilir (pgx'in doğrudan
	// döndürdüğü hata budur). Bu hata KindInternal'a düşerse API sınırında 503
	// yerine mesajı bastırılmış bir 500 üretilir.
	t.Run("kok list", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		products.listErr = context.DeadlineExceeded
		q := query.New(newLinks(), newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"beklenen sınıf Unavailable, gelen: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("genisletme fetch", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		variants := newProvider("variant", query.Record{"id": "var_1"})
		variants.fetchErr = context.Canceled

		links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
		q := query.New(links, newContainer(t, products, variants), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "product_variant"}},
		})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"beklenen sınıf Unavailable, gelen: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("link servisi", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		variants := newProvider("variant", query.Record{"id": "var_1"})

		links := newLinks(productVariant).connect("product_variant", "prod_1", "var_1")
		links.listErr = context.DeadlineExceeded
		q := query.New(links, newContainer(t, products, variants), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "product_variant"}},
		})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"beklenen sınıf Unavailable, gelen: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("link tanimi", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		variants := newProvider("variant", query.Record{"id": "var_1"})

		links := newLinks(productVariant)
		links.defErr = context.Canceled
		q := query.New(links, newContainer(t, products, variants), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "product_variant"}},
		})
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindUnavailable),
			"beklenen sınıf Unavailable, gelen: %v (%v)", errors.KindOf(err), err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("tipli hata sinifi korunur", func(t *testing.T) {
		// İptal ayrımı, sağlayıcının BİLİNÇLİ olarak verdiği sınıfı ezmemeli.
		products := newProvider("product", query.Record{"id": "prod_1"})
		products.listErr = errors.Invalid("product_bad_filter", "bilinmeyen filtre")
		q := query.New(newLinks(), newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product"})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "gelen: %v", err)
	})
}

// --- veriden bağımsız doğrulama ---------------------------------------------

func TestGraphIcIceGenisletmeVeriYokkenDeDogrulanir(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})
	variants := newProvider("variant", query.Record{"id": "var_1"})
	channels := newProvider("channel", query.Record{"id": "sc_web"})

	// Hiç bağ yok: üst seviye genişletme boş dilim üretir. product_channel
	// link'i product <-> channel arasındadır, variant'a BAĞLANMAZ.
	links := newLinks(productVariant, productChannel)
	q := query.New(links, newContainer(t, products, variants, channels), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{
			Link:   "product_variant",
			Expand: []query.Expansion{{Link: "product_channel"}},
		}},
	})
	require.Error(t, err, "alt seviye spec hatası, üst seviye veri getirmese de raporlanmalı")
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
	assert.Contains(t, err.Error(), "product_channel")
	assert.Contains(t, err.Error(), "variant")
}

func TestGraphHedefSaglayiciKaydiVeriYokkenDeDogrulanir(t *testing.T) {
	products := newProvider("product", query.Record{"id": "prod_1"})

	// variant.query bilerek kaydedilmiyor ve hiç bağ yok: eski davranışta
	// unutulmuş kayıt veri boşken sessiz kalıyordu.
	links := newLinks(productVariant)
	q := query.New(links, newContainer(t, products), nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "product_variant"}},
	})
	require.Error(t, err, "kayıtlı olmayan hedef sağlayıcı veri boşken de bildirilmeli")
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, "variant"+query.ProviderSuffix, typed.Details["aranan_ad"])
}

func TestGraphKokKayitYokkenDeGenisletmeAgaciDogrulanir(t *testing.T) {
	products := newProvider("product")
	q := query.New(newLinks(productVariant), newContainer(t, products, newProvider("variant")), nil)

	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "product",
		Expand: []query.Expansion{{Link: "yok_boyle_link"}},
	})
	require.Error(t, err, "kök kayıt yokken de bozuk genişletme tanımı raporlanmalı")
	assert.Nil(t, got)
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)
	assert.Contains(t, err.Error(), "yok_boyle_link")
}

// --- genişlik sınırı --------------------------------------------------------

func TestGraphCokGenisGenisletmeReddedilir(t *testing.T) {
	// Derinlik sınırının ALTINDA kalan ama çok sayıda genişletme taşıyan bir
	// spec de tek istekte yüzlerce gidiş-dönüş açar; maliyet derinlikle değil
	// genişletme adediyle büyür.
	t.Run("tek seviye", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		q := query.New(newLinks(productVariant), newContainer(t, products), nil)

		exps := make([]query.Expansion, 0, 51)
		for i := range 51 {
			exps = append(exps, query.Expansion{Link: "product_variant", As: fmt.Sprintf("v_%d", i)})
		}

		_, err := q.Graph(t.Context(), query.GraphSpec{Entity: "product", Expand: exps})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
		assert.Zero(t, products.calls().list, "geçersiz spec sağlayıcıya hiç gitmemeli")
	})

	t.Run("ic ice", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		q := query.New(newLinks(productVariant), newContainer(t, products), nil)

		// 3 seviye, her seviyede 4 kardeş: 4 + 16 + 64 = 84 genişletme.
		// Derinlik sınırı (10) bu spec'i durdurmaz.
		var derinlestir func(kalan int) []query.Expansion
		derinlestir = func(kalan int) []query.Expansion {
			if kalan == 0 {
				return nil
			}
			out := make([]query.Expansion, 0, 4)
			for i := range 4 {
				out = append(out, query.Expansion{
					Link:   "product_variant",
					As:     fmt.Sprintf("s%d_%d", kalan, i),
					Expand: derinlestir(kalan - 1),
				})
			}
			return out
		}

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: derinlestir(3),
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
		assert.Zero(t, products.calls().list)
	})
}

// TestGraphBozukSpecKokSorgusunuOdetmez genişletme planının kök veriyi
// getirmeden ÖNCE çözüldüğünü doğrular.
//
// Regresyon: plan, kök List çağrısından SONRA çalışıyordu. İki sonucu vardı:
// (1) bilinmeyen bir link adı taşıyan sorgu, hata vermeden önce tam bir kök
// sorgusunun maliyetini ödetiyordu; (2) daha ciddisi, sağlayıcıdan gelen
// geçici bir hata deterministik spec hatasını MASKELİYORDU — "veritabanı
// erişilemez" hatası, aslında düzeltilmesi gereken bir yazım hatasını
// gizliyordu.
func TestGraphBozukSpecKokSorgusunuOdetmez(t *testing.T) {
	t.Run("bilinmeyen link kok sorgusu yapilmadan reddedilir", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		links := newLinks(productVariant)
		q := query.New(links, newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "yok_boyle_link"}},
		})
		require.Error(t, err)
		assert.Zero(t, products.calls().list,
			"bozuk spec, kök sorgusunun maliyetini ödetmemeli")
	})

	t.Run("saglayici hatasi spec hatasini maskelemez", func(t *testing.T) {
		products := newProvider("product", query.Record{"id": "prod_1"})
		// Kök sağlayıcı da bozuk: eskiden bu hata öne geçip link hatasını
		// gizliyordu.
		products.listErr = errors.Unavailable("db_down", "veritabanı erişilemez")

		links := newLinks(productVariant)
		q := query.New(links, newContainer(t, products), nil)

		_, err := q.Graph(t.Context(), query.GraphSpec{
			Entity: "product",
			Expand: []query.Expansion{{Link: "yok_boyle_link"}},
		})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "db_down",
			"deterministik spec hatası, geçici sağlayıcı hatasının arkasında gizlenmemeli")
		assert.Contains(t, err.Error(), "yok_boyle_link",
			"hata hangi link adının bulunamadığını yazmalı")
	})
}

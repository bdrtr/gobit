package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Testlerde kullanılan satış kanalı kimlikleri.
const (
	testChannelA = "sc_a"
	testChannelB = "sc_b"
)

// magazaContext verilen kanallara bağlı bir publishable anahtar kimliği taşıyan
// context üretir.
//
// Kimlik, üretimde corehttp.RequireStore tarafından konur; burada elle
// konmasının sebebi, sınanan şeyin akışın KİMLİKTEN okuması olmasıdır — kimliği
// nereye kimin koyduğu HTTP katmanının işidir ve uçtan uca testte kanıtlanır
// (bkz. internal/e2e/kanal_sepeti_test.go).
func magazaContext(channels []string) context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID:   "apk_test",
		Kind: "api_key",
		// Yetki listesi BOŞTUR: publishable anahtar yetki taşımaz ve kanal
		// kapsamı bir yetki denetimi DEĞİL, kimliğin kapsamıdır.
		SalesChannelIDs: channels,
	})
}

// katalogSorgusu sahte kataloğa giden TEK varyant sorgusunu döner.
func katalogSorgusu(t *testing.T, h *harness) query.GraphSpec {
	t.Helper()

	for _, spec := range h.catalog.specs {
		if spec.Entity == EntityVariant {
			return spec
		}
	}

	t.Fatalf("katalogdan hiç varyant sorgusu geçmedi; görülen sorgular: %v", h.catalog.specs)
	return query.GraphSpec{}
}

// satirEkle sepete satır ekler; yazılan satırı ve akışın hatasını döner.
//
// Yazma kaydı da dönüyor çünkü bu dosyadaki iddiaların yarısı satırın
// YAZILMADIĞI üzerinedir; hatayı görmek yetmez — hata dönerken satırı yazmış
// bir akış da testi geçerdi.
func satirEkle(t *testing.T, h *harness, ctx context.Context, variantID string) (*addedLine, error) {
	t.Helper()

	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: variantID, Quantity: 1}}, nil),
	)
	seen := recordAddLine(h.carts, testLineA)

	_, err := h.wf.AddLineItem(ctx, AddLineItemInput{
		CartID:    testCartID,
		VariantID: variantID,
		Quantity:  1,
	})
	return seen, err
}

// TestKatalogSorgusuKanallariKimliktenOkur üç kimlik durumunun katalog
// sorgusuna nasıl yansıdığını doğrular.
//
// Üç durum okuma yüzeyindekiyle AYNI olmak zorundadır
// (bkz. saleschannel.go ve product/graph.SalesChannelIDsFromContext); farklı
// davranan bir yazma yolu, kapsamı yüzeylerden birinde delik bırakır. Burada
// sınanan şey davranışın kendisi değil, sorguya KONAN değerdir: kuralı
// uygulayan taraf product modülüdür ve ona doğru soruyu sormak bu akışın tek
// sorumluluğudur.
func TestKatalogSorgusuKanallariKimliktenOkur(t *testing.T) {
	testler := map[string]struct {
		ctx       func() context.Context
		suzgecVar bool
		kanallar  []string
	}{
		"kimlik yok -> süzgeç YOK": {
			ctx:       context.Background,
			suzgecVar: false,
		},
		"kanalsız kimlik -> BOŞ KÜME": {
			ctx:       func() context.Context { return magazaContext(nil) },
			suzgecVar: true,
			kanallar:  []string{},
		},
		"kanallı kimlik -> o kanallar": {
			ctx:       func() context.Context { return magazaContext([]string{testChannelA, testChannelB}) },
			suzgecVar: true,
			kanallar:  []string{testChannelA, testChannelB},
		},
	}

	for ad, tt := range testler {
		t.Run(ad, func(t *testing.T) {
			h := newHarness(t)
			_, err := satirEkle(t, h, tt.ctx(), testVariantA)
			require.NoError(t, err)

			spec := katalogSorgusu(t, h)
			ham, sorguda := spec.Filters[FilterSalesChannelIDs]

			if !tt.suzgecVar {
				assert.False(t, sorguda,
					"kimliksiz istekte kanal süzgeci HİÇ konmamalı; konsaydı auth'suz "+
						"bir kurulumda sepet hiçbir varyantı bulamazdı")
				return
			}

			require.True(t, sorguda,
				"kimlikli istekte kanal süzgeci konmalı; konmazsa yazma yolu kapsamsız kalır")
			assert.Equal(t, tt.kanallar, ham,
				"süzgeç kimliğin kanallarını BİREBİR taşımalı")
			assert.NotNil(t, ham,
				"boş küme nil DEĞİLDİR: nil 'süzme yok' demektir ve kanalsız bir "+
					"anahtara tüm kanalların katalogunu açardı")
		})
	}
}

// TestKapsamDisiVaryantSepeteGiremez kapsam dışı bir varyant için satırın HİÇ
// yazılmadığını doğrular.
//
// Katalog kaydı döndürmediğinde akışın devam edip başlıksız bir satır yazması
// ya da hatayı yutması, kapsamı yalnızca teşhis mesajına indirgerdi.
func TestKapsamDisiVaryantSepeteGiremez(t *testing.T) {
	h := newHarness(t)
	h.catalog.scopedOut = map[string]bool{testVariantA: true}

	seen, err := satirEkle(t, h, magazaContext([]string{testChannelB}), testVariantA)

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err),
		"kapsam dışı varyant BULUNAMADI sınıfında olmalı; hata: %v", err)
	assert.Equal(t, 0, seen.calls,
		"kapsam dışı varyant için sepete satır YAZILMAMALI")
}

// TestKapsamDisiVaryantVarliginiEleVermez kapsam dışı varyantın hatasının, hiç
// var olmayan bir varyantınkinden AYIRT EDİLEMEDİĞİNİ doğrular.
//
// Ayırt edilebilseydi gizleme delinirdi: elindeki herhangi bir publishable
// anahtarla gelen bir rakip, varyant kimliklerini deneyerek hangilerinin BAŞKA
// bir kanalda satıldığını öğrenirdi. Okuma yüzeyi aynı kararı verir ve aynı
// iddia orada da vardır (bkz. e2e TestGizlenenUrunVarliginiHataKoduylaEleVermez).
//
// Karşılaştırma hem KODU hem MESAJI kapsar; burada ikisi de aynı olabilir,
// çünkü iki mesaj da yalnızca istenen kimliği yankılar.
func TestKapsamDisiVaryantVarliginiEleVermez(t *testing.T) {
	ctx := magazaContext([]string{testChannelB})

	gizlenen := newHarness(t)
	gizlenen.catalog.scopedOut = map[string]bool{testVariantA: true}
	_, gizliHata := satirEkle(t, gizlenen, ctx, testVariantA)
	require.Error(t, gizliHata)

	olmayan := newHarness(t)
	delete(olmayan.catalog.titles, testVariantA)
	_, olmayanHata := satirEkle(t, olmayan, ctx, testVariantA)
	require.Error(t, olmayanHata)

	assert.Equal(t, errors.CodeOf(olmayanHata), errors.CodeOf(gizliHata),
		"kapsam dışı varyant ile olmayan varyant AYNI hata kodunu dönmeli")
	assert.Equal(t, errors.KindOf(olmayanHata), errors.KindOf(gizliHata),
		"iki durumun hata SINIFI da aynı olmalı; sınıf istemcinin kararını değiştirir")
	assert.Equal(t, olmayanHata.Error(), gizliHata.Error(),
		"mesajlar da ayrışmamalı; ayrıştıkları gün fark bir sızıntı kanalı olur")
}

// TestKendiKanalindakiVaryantSepeteGirer kapsam denetiminin HER ŞEYİ reddeden
// bir kapı olmadığını doğrular.
//
// Bu iddia olmadan diğer testler değersizdir: katalog okumasını tümüyle bozan
// bir değişiklik de "kapsam dışı varyant eklenemiyor" testini geçirirdi.
func TestKendiKanalindakiVaryantSepeteGirer(t *testing.T) {
	h := newHarness(t)
	// Katalog bu varyantı isteğin kapsamında SAYAR; kapsam dışı senaryosuyla
	// aradaki tek fark budur.
	seen, err := satirEkle(t, h, magazaContext([]string{testChannelA}), testVariantA)

	require.NoError(t, err)
	assert.Equal(t, 1, seen.calls, "kendi kanalındaki varyant sepete girmeli")
	assert.Equal(t, "Kırmızı Tişört / M", seen.title,
		"başlık yine katalogdan kopyalanmalı; kapsam denetimi okumayı bozmamalı")
}

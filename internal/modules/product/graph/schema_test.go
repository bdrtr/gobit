package graph_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// sema derlenmiş şemayı döner.
//
// Şema DOSYADAN değil üretilen koddan okunur: sunucunun gerçekten konuştuğu
// şey budur. Dosyayı okumak, üretimi unutulmuş bir şema değişikliğini "geçer"
// gösterirdi.
func sema(t *testing.T) *ast.Schema {
	t.Helper()

	return graph.NewExecutableSchema(graph.Config{}).Schema()
}

// tip şemadaki bir tipin tanımını döner.
func tip(t *testing.T, ad string) *ast.Definition {
	t.Helper()

	tanim, ok := sema(t).Types[ad]
	require.True(t, ok, "%q tipi şemada olmalı", ad)

	return tanim
}

// esitAd gqlgen'in alan eşleştirme kuralını uygular: alt çizgiler atılır ve
// karşılaştırma büyük/küçük harf duyarsızdır ("collectionId" ↔ "CollectionID").
func esitAd(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "_", ""), strings.ReplaceAll(b, "_", ""))
}

// goAlani Go tipinde şema alanına karşılık gelen alanı arar.
func goAlani(gt reflect.Type, semaAlani string) (reflect.StructField, bool) {
	return gt.FieldByNameFunc(func(ad string) bool { return esitAd(ad, semaAlani) })
}

// baglama bir şema tipini karşılığı olan Go tipine ve o tipte BİLİNÇLİ olarak
// şemaya konmamış alanlara bağlar.
type baglama struct {
	semaTipi string
	goTipi   reflect.Type
	// disarida şemaya KASTEN konmamış Go alanlarıdır. Alan adı burada da
	// yoksa test düşer; yani servise eklenen her alan için bir KARAR
	// verilmesi zorunludur — "eklemeyi unuttuk" ile "eklememeye karar
	// verdik" ayrımı ancak böyle korunur.
	disarida map[string]string
}

// baglamalar şema tipleri ile modülün tipleri arasındaki eşlemedir.
//
// Eşleme gqlgen.yml'deki "models" bloğunun aynısıdır ve BURADA TEKRARLANIR.
// Tekrar sessiz bir ayrışma bırakmaz: yapılandırmadaki satır silinirse gqlgen
// o tip için KENDİ modelini üretir, resolver imzaları değişir ve paket hiç
// derlenmez — yani ayrışma bu testin çalışmasına bile fırsat vermeden çıkar.
func baglamalar() []baglama {
	// Zaman damgaları ve silinme bilgisi vitrinde ANLAMSIZDIR: silinmiş kayıt
	// hiç dönmez, taksonominin ne zaman yazıldığı da müşteriyi ilgilendirmez.
	// Ürün ve varyantta createdAt/updatedAt tutulur çünkü istemci önbelleğini
	// onlara göre tazeler.
	taksonomiDisi := map[string]string{
		"CreatedAt": "vitrin istemcisi taksonominin yazılma zamanını kullanmaz",
		"UpdatedAt": "vitrin istemcisi taksonominin güncellenme zamanını kullanmaz",
		"DeletedAt": "silinmiş kayıt vitrinden zaten hiç dönmez",
	}

	return []baglama{
		{
			semaTipi: "Product",
			goTipi:   reflect.TypeOf(service.StoreProduct{}),
			disarida: map[string]string{
				"Status":    "vitrin yalnızca yayındaki ürünü döner; alan her zaman \"published\" olurdu",
				"DeletedAt": "silinmiş ürün vitrinden zaten hiç dönmez",
			},
		},
		{
			semaTipi: "Variant",
			goTipi:   reflect.TypeOf(service.StoreVariant{}),
			disarida: map[string]string{
				"DeletedAt": "silinmiş varyant vitrinden zaten hiç dönmez",
			},
		},
		{semaTipi: "Option", goTipi: reflect.TypeOf(models.Option{}), disarida: taksonomiDisi},
		{semaTipi: "OptionValue", goTipi: reflect.TypeOf(models.OptionValue{}), disarida: taksonomiDisi},
		{semaTipi: "Image", goTipi: reflect.TypeOf(models.Image{}), disarida: taksonomiDisi},
		{semaTipi: "Tag", goTipi: reflect.TypeOf(models.Tag{}), disarida: taksonomiDisi},
		{semaTipi: "Category", goTipi: reflect.TypeOf(models.Category{}), disarida: taksonomiDisi},
	}
}

// TestSemaAlanlariServisTipinde şemadaki her alanın servis tipinde bir
// karşılığı olduğunu doğrular.
//
// Şemaya, servisin döndürmediği bir alan konabilseydi istemciye hiç dolmayacak
// bir özellik vaat edilmiş olurdu. Üretilen kod bu ihlali zaten DERLEME
// zamanında yakalar (bağlanamayan alan resolver ister); test, o resolver'ın
// elle yazılıp alanın uydurulmasını da kapatır.
func TestSemaAlanlariServisTipinde(t *testing.T) {
	t.Parallel()

	for _, b := range baglamalar() {
		t.Run(b.semaTipi, func(t *testing.T) {
			t.Parallel()

			for _, alan := range tip(t, b.semaTipi).Fields {
				if strings.HasPrefix(alan.Name, "__") {
					continue
				}

				_, ok := goAlani(b.goTipi, alan.Name)
				assert.True(t, ok, "%s.%s alanının %s tipinde karşılığı yok",
					b.semaTipi, alan.Name, b.goTipi)
			}
		})
	}
}

// TestServisAlanlariSemadaYaDaKararliDisarida servisin döndüğü her alanın ya
// şemada olduğunu ya da bilinçli olarak dışarıda bırakıldığını doğrular.
//
// Bu testin ASIL işi yarın yapılacaktır: servise bir alan eklendiğinde test
// düşer ve ekleyen kişi "vitrine de girsin mi" sorusunu yanıtlamak zorunda
// kalır. Aksi hâlde ikinci okuma yüzeyi zamanla birincisinin gerisinde kalır
// ve bunu kimse fark etmez.
func TestServisAlanlariSemadaYaDaKararliDisarida(t *testing.T) {
	t.Parallel()

	for _, b := range baglamalar() {
		t.Run(b.semaTipi, func(t *testing.T) {
			t.Parallel()

			tanim := tip(t, b.semaTipi)

			for _, alan := range reflect.VisibleFields(b.goTipi) {
				// Gömülü yapının kendisi bir veri alanı değildir; alanları
				// zaten düzleştirilmiş olarak listede görünür.
				if alan.Anonymous || !alan.IsExported() {
					continue
				}

				semada := false

				for _, semaAlani := range tanim.Fields {
					if esitAd(semaAlani.Name, alan.Name) {
						semada = true

						break
					}
				}

				if semada {
					continue
				}

				gerekce, kararli := b.disarida[alan.Name]
				assert.True(t, kararli,
					"%s.%s şemada yok. Vitrine girecekse şemaya, girmeyecekse gerekçesiyle "+
						"testteki 'disarida' listesine eklenmeli", b.goTipi, alan.Name)
				assert.NotEmpty(t, gerekce)
			}
		})
	}
}

// TestProductsArgumanlariServisinOkuduklari sorgu argümanlarının
// service.StoreListOptions ile BİREBİR örtüştüğünü doğrular.
//
// Servisin okumadığı bir argümanı şemaya koymak, istemciye çalışmayan bir
// özellik vaat etmektir: üreteç sorguya alan koyar, çağıran doldurur, sunucu
// sessizce yok sayar.
//
// Bir vitrin seçeneğinin değeri ÜÇ yerden gelebilir ve her biri ayrı bir
// karardır: sorgu argümanından, isteğin kimliğinden ya da SEÇİM KÜMESİNDEN.
// Üçüncüsü GraphQL'e özgüdür — istemcinin bir alanı seçip seçmemesi de bir
// girdidir — ve bu yüzden bir argüman aramak yanlış olurdu; şemaya "sayayım
// mı" diye bir argüman koymak, "count" alanının kendisiyle aynı soruyu ikinci
// kez sormak olurdu.
func TestProductsArgumanlariServisinOkuduklari(t *testing.T) {
	t.Parallel()

	// Ad eşlemesi ELLE yazılır çünkü biri örtüşmez: serbest metin araması
	// şemada "q"dur (REST'teki adıyla aynı tutuldu), servis alanı ise Search.
	karsilik := map[string]string{
		"CollectionID": "collectionId",
		"Search":       "q",
		"Limit":        "limit",
		"Offset":       "offset",
	}

	// İstemcinin VEREMEYECEĞİ alanlar: değerleri isteğin kimliğinden gelir.
	kimlikten := map[string]bool{"SalesChannelIDs": true}

	// Değeri SEÇİM KÜMESİNDEN gelen alanlar: karşılığı bir argüman değil,
	// ProductList üzerindeki bir ALANDIR. Alan seçilmemişse iş de yapılmaz.
	secimden := map[string]string{"SkipCount": "count"}

	urunListesi := sema(t).Types["ProductList"]
	require.NotNil(t, urunListesi, "ProductList tipi şemada olmalı")

	var beklenen []string

	for _, alan := range reflect.VisibleFields(reflect.TypeOf(service.StoreListOptions{})) {
		if kimlikten[alan.Name] {
			continue
		}

		if alanAdi, secimeBagli := secimden[alan.Name]; secimeBagli {
			require.NotNil(t, urunListesi.Fields.ForName(alanAdi),
				"StoreListOptions.%s kararını ProductList.%s alanının seçilmesine bağlıyor "+
					"(bkz. graph.seciliMi); o alan şemada yok", alan.Name, alanAdi)

			continue
		}

		ad, ok := karsilik[alan.Name]
		require.True(t, ok,
			"StoreListOptions.%s ne şema argümanı, ne kimlikten gelen, ne de seçim "+
				"kümesinden gelen bir alan olarak tanımlı; yeni bir seçenek eklendiyse "+
				"üçünden birine karar verilmeli", alan.Name)

		beklenen = append(beklenen, ad)
	}

	var bulunan []string

	for _, arg := range sema(t).Query.Fields.ForName("products").Arguments {
		bulunan = append(bulunan, arg.Name)
	}

	assert.ElementsMatch(t, beklenen, bulunan,
		"products argümanları servisin okuduğu seçeneklerle aynı olmalı")
}

// metinSkalarlari Go tarafında *string'e bağlanan şema skalarlarıdır.
//
// ID, String'den ayrı bir skalardır ama taşıyıcısı aynıdır ve boş dizge ikisi
// için de aynı anlama gelir; yalnızca String'e bakan bir test, süzgeçlerin
// yarısını (collectionId) sessizce atlardı.
var metinSkalarlari = map[string]bool{"String": true, "ID": true}

// TestBosMetinArgumaniSuzgecKurmaz boş verilen her metin argümanının servise
// nil geçtiğini doğrular.
//
// [TestProductsArgumanlariServisinOkuduklari] argümanların servisin
// seçenekleriyle örtüştüğünü söyler; bu test o örtüşmenin BOŞ DEĞERDE ne
// anlama geldiğini sabitler. Kural modülde zaten vardı — REST'te stringParam,
// tekil GraphQL ucunda tekilSecici boş dizgeyi "verilmedi" sayar — ve
// uygulanmadığı tek yer liste yoluydu. Bedeli iki yönde de SESSİZDİR:
// `collectionId: ""` boş bir kimlikle süzüp hiçbir şey döndürmez,
// `q: ""` ise her satırı eşleştiren, sonuca hiç dokunmayan bir ILIKE taraması
// ekler.
//
// Test tek tek argümanları değil ŞEMAYI gezer; asıl işi yarın yapılacaktır:
// products'a eklenecek yeni bir metin süzgeci de bu iddianın içindedir ve
// normalize etmeyi unutan ekleme burada düşer.
func TestBosMetinArgumaniSuzgecKurmaz(t *testing.T) {
	t.Parallel()

	// Şema argümanı → [service.StoreListOptions] alanı. Eşleme ELLE yazılır;
	// gerekçesi [TestProductsArgumanlariServisinOkuduklari] içindeki karşılık
	// tablosuyla aynıdır (adlar birebir örtüşmüyor).
	alanlar := map[string]string{
		"collectionId": "CollectionID",
		"q":            "Search",
	}

	for _, arg := range sema(t).Query.Fields.ForName("products").Arguments {
		if !metinSkalarlari[arg.Type.NamedType] {
			continue
		}

		t.Run(arg.Name, func(t *testing.T) {
			t.Parallel()

			ad, bilinen := alanlar[arg.Name]
			require.True(t, bilinen,
				"%q metin argümanının StoreListOptions karşılığı bilinmiyor; yeni bir "+
					"süzgeç eklendiyse eşlemeye de eklenmeli", arg.Name)

			svc := &sahteVitrin{}

			// Yalnızca boşluktan oluşan değer verilir: hem "boş" hem
			// "kırpıldıktan sonra boş" durumunu tek vakada sınar.
			yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc,
				fmt.Sprintf(`{ products(%s: "   ") { count } }`, arg.Name))

			require.Empty(t, yanit.Errors)

			deger := reflect.ValueOf(svc.sonListe(t)).FieldByName(ad)
			require.Equal(t, reflect.Pointer, deger.Kind(),
				"%s işaretçi olmalı; 'verilmedi' ayrımını taşıyan tek şey nil'dir", ad)
			assert.True(t, deger.IsNil(),
				"boş %q argümanı süzgeç kurmamalı; servise nil geçmeli", arg.Name)
		})
	}
}

// TestSemadaSatisKanaliArgumaniYok kanalın hiçbir yerden İSTENEMEYECEĞİNİ
// doğrular.
//
// Bu bir kolaylık değil GÜVENLİK iddiasıdır: kanal argümana dönüştüğü an
// süzgeç bir yetkilendirme olmaktan çıkıp görüntüleme tercihine döner ve
// elindeki herhangi bir publishable anahtarla gelen istemci başka bir vitrinin
// katalogunu okur. İddia tek tek sorgulara değil ŞEMANIN TAMAMINA bakar:
// yarın eklenecek bir sorgu da bu kuralın içindedir.
func TestSemadaSatisKanaliArgumaniYok(t *testing.T) {
	t.Parallel()

	for ad, tanim := range sema(t).Types {
		if strings.HasPrefix(ad, "__") {
			continue
		}

		for _, alan := range tanim.Fields {
			for _, arg := range alan.Arguments {
				assert.NotContains(t, strings.ToLower(arg.Name), "channel",
					"%s.%s(%s): satış kanalı istekten değil KİMLİKTEN okunur",
					ad, alan.Name, arg.Name)
			}
		}
	}
}

// TestSemadaYazmaYuzeyiYok yüzeyin OKUMA ile sınırlı kaldığını doğrular.
//
// Mutation'ın yokluğu bir eksiklik değil verilmiş bir karardır (bkz.
// schema.graphqls); kararın bir testi yoksa bir gün "eksik" diye tamamlanır.
func TestSemadaYazmaYuzeyiYok(t *testing.T) {
	t.Parallel()

	s := sema(t)

	assert.Nil(t, s.Mutation, "vitrin GraphQL yüzeyi yalnızca okumadır")
	assert.Nil(t, s.Subscription, "abonelik yüzeyi yoktur")
}

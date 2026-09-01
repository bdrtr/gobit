package arch_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Bu dosya SATIŞ KANALI KAPSAMININ iki yüzeyde de aynı kalmasını zorlar.
//
// Kapsam kuralı ("ataması olmayan ürün her kanalda görünür, ataması olan
// yalnızca atandığı kanallarda") product modülünün verisidir ve orada tek bir
// SQL şablonunda yaşar. Tehlike kuralın kendisinde değil, UYGULANMADIĞI
// yollardadır: kural bir zamanlar yalnızca yazılıyor hiç okunmuyordu, sonra
// yalnızca OKUMA yüzeyinde uygulanıyor YAZMA yolunda uygulanmıyordu. İkisi de
// aynı sınıftır — "bir yerde karar verilmiş, başka yerde uygulanmamış" — ve
// ikisi de testler yeşilken yaşadı.
//
// Buradaki üç değişmez o sınıfın üç ayrı yüzünü kapatır: türetmenin ANLAMI,
// sözleşme ADI ve okumanın KAPSAM KARARINDAN geçtiği.

// TestKanalTuretmesiIkiYuzeydeAyniAnlamda okuma ve yazma yüzeylerinin
// kimlikten aynı üç durumu çıkardığını doğrular.
//
// İki yüzey iki ayrı pakette yaşar ve birbirini import EDEMEZ: product modülü
// akışları göremez, akışlar da modülleri (ADR 0006). Bu yüzden türetme iki kez
// yazılmıştır ve derleyicinin kuramadığı bağı bu test kurar.
//
// Ayrışma SESSİZ olurdu ve yalnızca belirli bir kimlik biçiminde görülürdü:
// örneğin yazma yolu "kanalsız kimlik" durumunda nil dönmeye başlasa, o
// anahtar sahibi vitrinde hiçbir ürün göremezken sepete HER ürünü ekleyebilir
// olurdu — ve hiçbir uç hata vermezdi.
func TestKanalTuretmesiIkiYuzeydeAyniAnlamda(t *testing.T) {
	t.Parallel()

	kimlikli := func(channels []string) context.Context {
		return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
			ID:              "apk_test",
			Kind:            "api_key",
			SalesChannelIDs: channels,
		})
	}

	durumlar := map[string]context.Context{
		"kimlik yok":                context.Background(),
		"kanalsız kimlik (nil)":     kimlikli(nil),
		"kanalsız kimlik (boş)":     kimlikli([]string{}),
		"tek kanallı kimlik":        kimlikli([]string{"sc_a"}),
		"çok kanallı kimlik":        kimlikli([]string{"sc_a", "sc_b"}),
		"kanalı boş dizgeli kimlik": kimlikli([]string{""}),
	}

	for ad, ctx := range durumlar {
		okuma := graph.SalesChannelIDsFromContext(ctx)
		yazma := cartwf.SalesChannelIDsFromContext(ctx)

		assert.Equal(t, okuma, yazma,
			"%s: okuma ve yazma yüzeyleri kimlikten AYNI kanal kümesini çıkarmalı", ad)
		// Eşitlik iddiası nil ile boş dilimi zaten ayırır (reflect.DeepEqual),
		// ama ayrım bu kuralın TAM MERKEZİ olduğu için açıkça yazılır: nil
		// "süzme yok", boş dilim "kanalı olmayan kimlik" demektir ve ikisini
		// bir tutmak kanalsız bir anahtara tüm katalogu açar.
		assert.Equal(t, okuma == nil, yazma == nil,
			"%s: nil ile BOŞ KÜME ayrımı iki yüzeyde de aynı olmalı", ad)
	}
}

// TestKanalSozlesmeAdlariUyusuyor akışın product'a sorduğu soruyu ADLARINDAN
// doğrular.
//
// Akış product'ı import edemez (ADR 0006) ve Query katmanına gönderdiği entity
// adı ile filtre anahtarını DİZE olarak tekrarlar. Ayrışırlarsa sağlayıcı ya
// hiç bulunamaz ya da filtreyi tanımaz; ikisi de errors ile düşer, yani arıza
// sessiz DEĞİLDİR — ama üretimde, müşterinin sepetinde görülür. Bu test onu
// takıma taşır.
func TestKanalSozlesmeAdlariUyusuyor(t *testing.T) {
	t.Parallel()

	assert.Equal(t, productsvc.FilterSalesChannelIDs, cartwf.FilterSalesChannelIDs,
		"sepet akışının gönderdiği kanal süzgeç anahtarı, varyant sağlayıcısının "+
			"tanıdığı anahtarla aynı olmalı")
	assert.Equal(t, productsvc.EntityVariant, cartwf.EntityVariant,
		"akışın sorduğu entity adı product'ın sunduğu adla aynı olmalı; ayrışırsa "+
			"Query sağlayıcıyı hiç bulamaz")
}

// varyantOkumaMuafiyeti kanal kararını BİLEREK vermeyen bir varyant okumasıdır.
type varyantOkumaMuafiyeti struct {
	// dosya depo köküne göre yoldur.
	dosya string
	// fonksiyon okumayı yapan fonksiyonun (ya da metodun) adıdır.
	fonksiyon string
	// neden kararın gerekçesidir; gerekçesiz muafiyet, kuralın sessizce
	// aşınmasıdır.
	neden string
}

// varyantOkumaMuafiyetleri kanal kararı ARAMAYAN varyant okumalarıdır.
//
// Liste kapsanan yolların değil, VERİLMİŞ İSTİSNALARIN listesidir: tarama tüm
// ağacı gezer, buraya yazılmayan her yeni varyant okuması kararı vermek
// zorundadır. Kullanılmayan bir muafiyet de hata verir (bkz. testin sonu), yani
// istisna kalktığında liste kendiliğinden küçülür.
var varyantOkumaMuafiyetleri = []varyantOkumaMuafiyeti{
	{
		dosya:     "internal/workflows/checkout/plan.go",
		fonksiyon: "variantTitles",
		neden: "kapsam GİRİŞTE uygulanır: sepete varyant sokabilen tek yol satır " +
			"eklemedir ve orası kapsanmıştır. Bu okuma, sepete ÇOKTAN girmiş bir " +
			"satırın adını sipariş satırına kopyalar; yeniden süzmek, ürünü başka " +
			"bir kanala taşıyan bir yönetici düzenlemesinin müşterinin dolu " +
			"sepetini ödenemez kılması demek olurdu ve product modülünün yazılı " +
			"kararıyla çelişirdi (bkz. productsvc.productProvider.List).",
	},
	{
		dosya:     "internal/modules/product/service/store.go",
		fonksiyon: "enrichVariants",
		neden: "kapsam bir ÜST adımda uygulanmıştır: bu okuma, vitrin listesinin " +
			"zaten süzülmüş ürünlerinin varyantlarını fiyat/stok bağlarıyla " +
			"zenginleştirir. İkinci kez süzmek aynı kuralı aynı istekte iki kez " +
			"uygulamak olurdu; kapsam dışı bir ürün buraya hiç gelmez.",
	},
}

// kanalKarariVerenCagrilar bir varyant okumasının kapsam kararını verdiğini
// gösteren fonksiyon adlarıdır.
//
// Ada bakılır, tipe değil: tarama derleyici değil ayrıştırıcıdır ve paketler
// arası bir tip çözümü, testi go/types'a ve tüm derleme grafiğine bağlardı.
// Adın yanlış pozitif üretmesi de zararsızdır — kararı gerçekten veren tek şey
// süzgecin sorguya konmasıdır ve onu davranış testleri kanıtlar.
var kanalKarariVerenCagrilar = map[string]bool{
	"SalesChannelIDsFromContext": true,
	"salesChannelFilter":         true,
}

// TestVaryantOkumalariKanalKararindanGecer varyant okuyan her fonksiyonun
// satış kanalı kapsamı hakkında GÖRÜNÜR bir karar verdiğini doğrular.
//
// # Neden yapıyı geziyor
//
// Elle tutulan bir "kapsanmış yollar" listesi kuralı yalnızca BUGÜN için
// uygular: yarın eklenen bir yazma yolu listede olmaz ve sessizce kapsamsız
// kalır — bu depodaki hataların tamamı tam olarak o sınıftandı. Bu test bunun
// yerine internal/workflows ve internal/modules ağaçlarını gezer, Query'ye
// giden her `variant` okumasını bulur ve şunu sorar: bu okumayı yapan
// fonksiyon, kapsam kararını veren yardımcıyı çağırıyor mu?
//
// # Ne KANITLAR, ne kanıtlamaz
//
// Değişmez bir PROXY'dir ve bunu saklamamak gerekir: kararın VERİLDİĞİNİ
// zorlar, DOĞRU verildiğini değil. Süzgeci sorguya koymayan ama yardımcıyı
// çağıran bir fonksiyon buradan geçer. Kararın doğruluğu davranış testlerinin
// işidir ve üç katmanda birden vardır: akışın birim testleri (sorguya konan
// değer), product'ın entegrasyon testleri (gerçek SQL) ve uçtan uca test
// (iki publishable anahtar, gerçek koruma yığını).
//
// Daha güçlü bir değişmez — "kanal süzgecini taşımayan bir varyant okuması
// DERLENMESİN" — ancak Query filtrelerinin map[string]any değil tipli bir
// yapı olmasıyla mümkün olurdu; o da çekirdeğin modülleri tanımama kuralına
// (Prensip 2.4) çarpar, çünkü filtre adları modüllerin sözleşmesidir. Bu
// yüzden yazılabilecek en güçlü yapısal denetim budur.
func TestVaryantOkumalariKanalKararindanGecer(t *testing.T) {
	t.Parallel()

	kullanilan := make([]bool, len(varyantOkumaMuafiyetleri))
	taranan := 0

	for _, kok := range []string{"internal/workflows", modulesDir} {
		for _, dosya := range uretimDosyalari(t, filepath.Join(repoRoot, kok)) {
			taranan += varyantOkumalariniDenetle(t, dosya, kullanilan)
		}
	}

	require.Positive(t, taranan,
		"hiç varyant okuması bulunamadı; tarama artık hiçbir şeyi denetlemiyor olabilir "+
			"(GraphSpec alan adı ya da entity sabiti değişmiş olabilir)")

	for i, muaf := range varyantOkumaMuafiyetleri {
		assert.True(t, kullanilan[i],
			"kullanılmayan muafiyet: %s içindeki %q artık varyant okumuyor.\n"+
				"Gerekçesi (%q) bir şeyi savunmuyor: ya okuma kaldırıldı ve muafiyet de "+
				"silinmeli, ya da taşındı ve muafiyet onu artık görmüyor.",
			muaf.dosya, muaf.fonksiyon, muaf.neden)
	}
}

// varyantOkumalariniDenetle bir dosyadaki varyant okumalarını denetler ve
// bulunan okuma sayısını döner.
func varyantOkumalariniDenetle(t *testing.T, dosya string, kullanilan []bool) int {
	t.Helper()

	fset := token.NewFileSet()
	agac, err := parser.ParseFile(fset, dosya, nil, 0)
	if err != nil {
		t.Fatalf("%s ayrıştırılamadı: %v", dosya, err)
	}

	yol := filepath.ToSlash(depoYolu(dosya))
	bulunan := 0

	for _, decl := range agac.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !varyantOkuyor(fn) {
			continue
		}
		bulunan++

		if muaf := muafiyetiIsaretle(yol, fn.Name.Name, kullanilan); muaf {
			continue
		}
		if kanalKarariVeriyor(fn) {
			continue
		}

		t.Errorf("%s:%d: %q varyant okuyor ama satış kanalı kapsamı hakkında hiçbir "+
			"karar vermiyor.\n"+
			"Okuma isteğin kimliğinden gelen kanalları süzgeç olarak taşımalı "+
			"(bkz. workflows/cart/saleschannel.go) ya da neden taşımadığı "+
			"varyantOkumaMuafiyetleri'ne GEREKÇESİYLE yazılmalıdır. Kapsamsız bir "+
			"varyant okuması, başka bir vitrinin ürününün bu yoldan geçmesi demektir.",
			yol, fset.Position(fn.Pos()).Line, fn.Name.Name)
	}

	return bulunan
}

// varyantOkuyor fonksiyonun gövdesinde `variant` entity'sine giden bir
// query.GraphSpec kurulup kurulmadığını söyler.
func varyantOkuyor(fn *ast.FuncDecl) bool {
	bulundu := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if bulundu {
			return false
		}
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !graphSpecMi(lit.Type) {
			return true
		}
		if varyantEntitysi(lit) {
			bulundu = true
		}

		return true
	})

	return bulundu
}

// graphSpecMi bir bileşik değişmez tipinin query.GraphSpec olduğunu söyler.
func graphSpecMi(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "GraphSpec"
}

// varyantEntitysi GraphSpec'in Entity alanının varyantları gösterdiğini söyler.
//
// Hem sabit adı ("EntityVariant") hem de düz dizge ("variant") kabul edilir:
// sabiti atlayıp dizgeyi elle yazmak, denetimden kaçmanın en kolay ve en
// masumca görünen yolu olurdu.
func varyantEntitysi(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		anahtar, ok := kv.Key.(*ast.Ident)
		if !ok || anahtar.Name != "Entity" {
			continue
		}

		switch deger := kv.Value.(type) {
		case *ast.Ident:
			return deger.Name == "EntityVariant"
		case *ast.SelectorExpr:
			return deger.Sel.Name == "EntityVariant"
		case *ast.BasicLit:
			return deger.Value == `"`+productsvc.EntityVariant+`"`
		}
	}

	return false
}

// kanalKarariVeriyor fonksiyonun kapsam kararını veren bir yardımcıyı çağırıp
// çağırmadığını söyler.
func kanalKarariVeriyor(fn *ast.FuncDecl) bool {
	bulundu := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if bulundu {
			return false
		}
		cagri, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		switch hedef := cagri.Fun.(type) {
		case *ast.Ident:
			bulundu = kanalKarariVerenCagrilar[hedef.Name]
		case *ast.SelectorExpr:
			bulundu = kanalKarariVerenCagrilar[hedef.Sel.Name]
		}

		return true
	})

	return bulundu
}

// muafiyetiIsaretle okumanın muaf olup olmadığını söyler ve muafiyeti
// KULLANILDI olarak işaretler.
func muafiyetiIsaretle(dosya, fonksiyon string, kullanilan []bool) bool {
	for i, muaf := range varyantOkumaMuafiyetleri {
		if muaf.dosya == dosya && muaf.fonksiyon == fonksiyon {
			kullanilan[i] = true
			return true
		}
	}

	return false
}

package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Bu dosya TEK bir değişmezi üç yüzeyde zorlar: ÜRETİLEN HER YETENEĞİN BİR
// TÜKETİCİSİ VARDIR.
//
// Depodaki en pahalı hataların bir sınıfı, kuralın ihlali değil YOKLUĞUDUR:
// yetenek yazılır, kablolanır, belgelenir — ve kimse kullanmaz. Faz 8/9'un
// tamamı mount edilmeden yazılmıştı; b2b bileşim köküne kaydedilmemişti;
// RequireScope ölü koddu; "order.placed" uzun süre abonesiz yayımlandı. Hiçbiri
// hata vermez: kod derlenir, testler geçer, özellik yokmuş gibi davranır.
//
// Üç yüzey ayrı ayrı denetlenir ve AYRI testlerdir. Tek testte toplansalardı
// ilk t.Fatal'dan sonrası koşmaz ve birinin mutasyonu diğerinin bulgusunu
// maskelerdi.
//
// Ortak yöntem: kaynak go/parser ile GEZİLİR, liste TUTULMAZ. Adlar sabitlerde
// yaşadığı ve modüller birbirini import edemediği için (ADR 0001) sabitin
// DEĞERİ paketler arası çözülür; bir tüketici adı elle tekrarladığında
// (order → "b2b.interop", searchpg → "product.interop") bağ ancak değer
// düzeyinde görünür.

// uretimKokleri üretim (test olmayan) Go kaynağının yaşadığı ağaçlardır.
//
// Depoda Go kaynağı yalnızca bu üç kökte bulunur. Liste bir SABİTTİR ve
// dördüncü bir kök eklendiğinde burada güncellenmelidir; unutulursa bu
// dosyadaki üç test de daralır. Daralmanın yönü GÜRÜLTÜLÜDÜR: taranmayan bir
// ağaçtaki tüketici "yok" sayılır ve üretilmiş bir yetenek haksız yere ölü
// ilan edilir — sessizce geçen bir test değil, açıklaması yanlış bir hata
// alınır. Ters yön (bildirimi kaçırmak) sessiz olurdu ve bu yüzden bildirim
// tarafı hiçbir zaman kök listesine bakmaz: bildirimler de aynı taramadan
// gelir.
var uretimKokleri = []string{"cmd", "internal", "plugins"}

// azamiCozumDerinligi sabit değeri çözerken izlenecek en fazla adım sayısıdır.
//
// Çözüm özyinelemelidir (sabit → sabit → parametre → çağıran) ve döngüsel bir
// bildirim ya da birbirini çağıran iki fonksiyon sonsuz iniş üretebilirdi.
// Bugünkü en derin zincir dört adımdır; sekiz, kural değişmeden büyüyecek
// zincirlere yer bırakır.
const azamiCozumDerinligi = 8

// sabitTanimi bir sabitin değer ifadesini ve tanımlandığı dosyayı tutar.
//
// Dosya da saklanır çünkü değerin içindeki nitelikli adlar (örn.
// query.ProviderSuffix) yalnızca TANIMIN dosyasındaki import tablosuyla
// çözülebilir.
type sabitTanimi struct {
	ifade ast.Expr
	dosya *kaynakDosyasi
}

// kaynakDosyasi ayrıştırılmış tek bir üretim dosyasıdır.
type kaynakDosyasi struct {
	// yol depo köküne göre yoldur; hata mesajlarında bu görünür.
	yol string
	// importYolu dosyanın paketinin tam import yoludur.
	importYolu string
	agac       *ast.File
	// importlar dosyadaki YEREL paket adından import yoluna eşlemedir.
	importlar map[string]string
}

// cagriYeri bir fonksiyon çağrısını ve içinde bulunduğu bağlamı tutar.
type cagriYeri struct {
	dosya *kaynakDosyasi
	// fn çağrıyı içeren fonksiyondur; paket düzeyi bildirimlerde nil olur.
	fn    *ast.FuncDecl
	cagri *ast.CallExpr
}

// bilesikYeri bir bileşik değeri (composite literal) ve bağlamını tutar.
type bilesikYeri struct {
	dosya *kaynakDosyasi
	fn    *ast.FuncDecl
	deger *ast.CompositeLit
}

// kaynakAgaci deponun üretim kaynağının taranmış hâlidir.
//
// Üç testin de tek girdisidir: bildirimler de tüketimler de buradan okunur,
// hiçbiri elle yazılmış bir listeden gelmez.
type kaynakAgaci struct {
	fset     *token.FileSet
	dosyalar []*kaynakDosyasi
	// sabitler import yolundan sabit adına, oradan tanıma gider.
	sabitler map[string]map[string]sabitTanimi
	// paketAdi import yolundan paketin BİLDİRİLEN adına gider; takma adsız
	// import'un yerel adı budur ve dizin adından farklı olabilir.
	paketAdi map[string]string
	// cagrilar çağrılan fonksiyonun NİTELİKSİZ adından çağrı yerlerine gider.
	cagrilar map[string][]cagriYeri
	// bilesikler taranan tüm bileşik değerlerdir.
	bilesikler []bilesikYeri
}

// kaynagiTara üretim kaynağını ayrıştırır ve indeksleri kurar.
func kaynagiTara(t *testing.T) *kaynakAgaci {
	t.Helper()

	agac := &kaynakAgaci{
		fset:     token.NewFileSet(),
		sabitler: map[string]map[string]sabitTanimi{},
		paketAdi: map[string]string{},
		cagrilar: map[string][]cagriYeri{},
	}

	for _, kok := range uretimKokleri {
		mutlak := filepath.Join(repoRoot, kok)
		if _, err := os.Stat(mutlak); err != nil {
			t.Fatalf("%q kökü bulunamadı: %v", kok, err)
		}
		for _, yol := range goFiles(t, mutlak) {
			if strings.HasSuffix(yol, "_test.go") {
				continue
			}
			ayrisik, err := parser.ParseFile(agac.fset, yol, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s ayrıştırılamadı: %v", yol, err)
			}
			bagil, err := filepath.Rel(repoRoot, yol)
			if err != nil {
				t.Fatalf("%s göreli yola çevrilemedi: %v", yol, err)
			}
			bagil = filepath.ToSlash(bagil)
			dosya := &kaynakDosyasi{
				yol:        bagil,
				importYolu: modulePath + "/" + filepath.ToSlash(filepath.Dir(bagil)),
				agac:       ayrisik,
				importlar:  map[string]string{},
			}
			agac.dosyalar = append(agac.dosyalar, dosya)
			agac.paketAdi[dosya.importYolu] = ayrisik.Name.Name
		}
	}

	for _, dosya := range agac.dosyalar {
		agac.importlariTopla(dosya)
		agac.sabitleriTopla(dosya)
	}
	for _, dosya := range agac.dosyalar {
		agac.bildirimleriTara(dosya)
	}

	return agac
}

// importlariTopla dosyanın yerel paket adı → import yolu tablosunu kurar.
//
// Takma adı olmayan bir import'un yerel adı, hedef paketin BİLDİRDİĞİ addır;
// dizin adının son parçası yalnızca bir tahmindir ve bu depoda tutmadığı yerler
// vardır. Bu yüzden önce gerçekten ayrıştırılmış paket adına bakılır ve tahmin
// yalnızca depo dışı (stdlib, üçüncü taraf) paketler için kullanılır — onların
// sabitleri zaten çözülmez.
func (a *kaynakAgaci) importlariTopla(dosya *kaynakDosyasi) {
	for _, imp := range dosya.agac.Imports {
		yol := strings.Trim(imp.Path.Value, `"`)
		yerel := ""
		switch {
		case imp.Name != nil:
			yerel = imp.Name.Name
		case a.paketAdi[yol] != "":
			yerel = a.paketAdi[yol]
		default:
			parcalar := strings.Split(yol, "/")
			yerel = parcalar[len(parcalar)-1]
		}
		if yerel == "" || yerel == "_" || yerel == "." {
			continue
		}
		dosya.importlar[yerel] = yol
	}
}

// sabitleriTopla dosyadaki paket düzeyi sabitleri indeksler.
//
// Yalnızca DEĞERİ olan sabitler alınır: iota ile üretilenlerin ve tip
// bildirimlerinin bu testlerde karşılığı yoktur, container adları ile olay ve
// link adlarının hepsi dizedir.
func (a *kaynakAgaci) sabitleriTopla(dosya *kaynakDosyasi) {
	for _, decl := range dosya.agac.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			deger, ok := spec.(*ast.ValueSpec)
			if !ok || len(deger.Values) != len(deger.Names) {
				continue
			}
			for i, ad := range deger.Names {
				if a.sabitler[dosya.importYolu] == nil {
					a.sabitler[dosya.importYolu] = map[string]sabitTanimi{}
				}
				a.sabitler[dosya.importYolu][ad.Name] = sabitTanimi{ifade: deger.Values[i], dosya: dosya}
			}
		}
	}
}

// bildirimleriTara dosyadaki çağrıları ve bileşik değerleri indeksler.
func (a *kaynakAgaci) bildirimleriTara(dosya *kaynakDosyasi) {
	for _, decl := range dosya.agac.Decls {
		fn, _ := decl.(*ast.FuncDecl)
		ast.Inspect(decl, func(n ast.Node) bool {
			switch dugum := n.(type) {
			case *ast.CallExpr:
				if ad := cagriAdi(dugum.Fun); ad != "" {
					a.cagrilar[ad] = append(a.cagrilar[ad], cagriYeri{dosya: dosya, fn: fn, cagri: dugum})
				}
			case *ast.CompositeLit:
				a.bilesikler = append(a.bilesikler, bilesikYeri{dosya: dosya, fn: fn, deger: dugum})
			}
			return true
		})
	}
}

// cagriAdi çağrılan fonksiyonun NİTELİKSİZ adını döner.
//
// Jenerik çağrılar (container.Resolve[T](...)) sarmalayıcı bir IndexExpr
// üretir; soyulmazsa "Resolve" adı hiç görülmezdi.
func cagriAdi(ifade ast.Expr) string {
	switch x := ifade.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.IndexExpr:
		return cagriAdi(x.X)
	case *ast.IndexListExpr:
		return cagriAdi(x.X)
	case *ast.ParenExpr:
		return cagriAdi(x.X)
	}
	return ""
}

// yer bir konumu depo köküne göre "dosya:satır" olarak yazar.
func (a *kaynakAgaci) yer(dosya *kaynakDosyasi, pos token.Pos) string {
	return fmt.Sprintf("%s:%d", dosya.yol, a.fset.Position(pos).Line)
}

// dizeDegerleri bir ifadenin alabileceği DİZE değerlerini çözer.
//
// Birden çok değer dönebilir çünkü bir ad tek bir değere bağlı olmayabilir:
// olay adı bir fonksiyon PARAMETRESİ olarak taşınıyorsa (product'ın
// publishProductEvent'i böyledir) değer çağıranlardan gelir ve üç farklı olay
// adı aynı yayım satırından geçer. Bu yüzden çözüm, tek bir sabite değil
// OLASI DEĞERLER KÜMESİNE gider.
//
// Desteklenen biçimler bu depoda GERÇEKTEN kullanılanlardır: dize sabiti,
// birleştirme (ModuleName + ".interop"), aynı paketteki sabit, başka paketteki
// nitelikli sabit, fonksiyon parametresi ve dilim sabiti üzerinde dönen range
// değişkeni. Çözülemeyen bir ifade sessizce atlanmaz; çağıran taraf boş kümeyi
// GÖRÜR ve testine göre hata verir.
func (a *kaynakAgaci) dizeDegerleri(dosya *kaynakDosyasi, fn *ast.FuncDecl, ifade ast.Expr, derinlik int) []string {
	if ifade == nil || derinlik > azamiCozumDerinligi {
		return nil
	}

	switch x := ifade.(type) {
	case *ast.ParenExpr:
		return a.dizeDegerleri(dosya, fn, x.X, derinlik+1)

	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return nil
		}
		deger, err := strconv.Unquote(x.Value)
		if err != nil {
			return nil
		}
		return []string{deger}

	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return nil
		}
		var out []string
		for _, sol := range a.dizeDegerleri(dosya, fn, x.X, derinlik+1) {
			for _, sag := range a.dizeDegerleri(dosya, fn, x.Y, derinlik+1) {
				out = append(out, sol+sag)
			}
		}
		return out

	case *ast.Ident:
		return a.identDegerleri(dosya, fn, x, derinlik)

	case *ast.SelectorExpr:
		paket, ok := x.X.(*ast.Ident)
		if !ok {
			return nil
		}
		yol, ok := dosya.importlar[paket.Name]
		if !ok {
			return nil
		}
		tanim, ok := a.sabitler[yol][x.Sel.Name]
		if !ok {
			return nil
		}
		return a.dizeDegerleri(tanim.dosya, nil, tanim.ifade, derinlik+1)
	}

	return nil
}

// identDegerleri niteliksiz bir adın olası dize değerlerini çözer.
//
// Sıra önemlidir: yerel bağlam (parametre, range değişkeni) paket düzeyi
// sabitten ÖNCE denenir, çünkü gölgeleyen yerel ad kazanır.
func (a *kaynakAgaci) identDegerleri(dosya *kaynakDosyasi, fn *ast.FuncDecl, id *ast.Ident, derinlik int) []string {
	if fn != nil {
		if indeks, ok := parametreIndeksi(fn, id.Name); ok {
			return a.cagriArgumanlari(fn, indeks, derinlik)
		}
		if degerler := a.rangeDegerleri(dosya, fn, id.Name, derinlik); len(degerler) > 0 {
			return degerler
		}
	}
	if tanim, ok := a.sabitler[dosya.importYolu][id.Name]; ok {
		return a.dizeDegerleri(tanim.dosya, nil, tanim.ifade, derinlik+1)
	}
	return nil
}

// parametreIndeksi adın fonksiyonun kaçıncı DİZE parametresi olduğunu döner.
//
// Alıcı (receiver) sayılmaz: metot çağrısının argüman listesinde de yoktur.
//
// Yalnızca string parametreler izlenir. Bu bir hız önlemi DEĞİL, doğruluk
// önlemidir: dize olmayan bir parametrenin (container, logger, ctx) çağıran
// zincirine inmek hiçbir zaman bir ad üretmez, ama her çağıranı gezerek çözümü
// üstel biçimde büyütür ve testi dakikalar sürer hâle getirir.
func parametreIndeksi(fn *ast.FuncDecl, ad string) (int, bool) {
	if fn.Type == nil || fn.Type.Params == nil {
		return 0, false
	}
	indeks := 0
	for _, alan := range fn.Type.Params.List {
		if len(alan.Names) == 0 {
			indeks++
			continue
		}
		tip, dize := alan.Type.(*ast.Ident)
		dize = dize && tip.Name == "string"
		for _, isim := range alan.Names {
			if isim.Name == ad {
				return indeks, dize
			}
			indeks++
		}
	}
	return 0, false
}

// cagriArgumanlari fonksiyonun çağıranlarındaki verilen argümanı çözer.
//
// Eşleşme fonksiyon ADIYLA yapılır; tip bilgisi olmadan alıcı tipi
// bilinemez. Yanlış eşleşme testi YANLIŞ TARAFA kaydırmaz: aynı ada sahip
// başka bir fonksiyonun argümanı, aranan ad kümesinde bulunmayan bir dizeye
// çözülür ve hiçbir iddiaya girmez.
func (a *kaynakAgaci) cagriArgumanlari(fn *ast.FuncDecl, indeks, derinlik int) []string {
	var out []string
	for _, yer := range a.cagrilar[fn.Name.Name] {
		if yer.fn == fn || len(yer.cagri.Args) <= indeks {
			continue
		}
		out = append(out, a.dizeDegerleri(yer.dosya, yer.fn, yer.cagri.Args[indeks], derinlik+1)...)
	}
	return out
}

// rangeDegerleri dilim sabiti üzerinde dönen bir değişkenin değerlerini çözer.
//
// Bu depoda temizlik ve telafi yolları adları tek tek yazmak yerine
// "for _, name := range []string{LinkVariantPriceSet, LinkVariantInventory}"
// biçiminde dönebilir. Bu biçim çözülmeseydi o çağrılar TANINMAZ olurdu ve bir
// bağ, yalnızca silinmek üzere okunduğu hâlde "okunuyor" sayılırdı.
func (a *kaynakAgaci) rangeDegerleri(dosya *kaynakDosyasi, fn *ast.FuncDecl, ad string, derinlik int) []string {
	var out []string
	ast.Inspect(fn, func(n ast.Node) bool {
		dongu, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		id, ok := dongu.Value.(*ast.Ident)
		if !ok || id.Name != ad {
			return true
		}
		dilim, ok := dongu.X.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, eleman := range dilim.Elts {
			out = append(out, a.dizeDegerleri(dosya, fn, eleman, derinlik+1)...)
		}
		return true
	})
	return out
}

// nitelikliTip bir tip ifadesinin verilen paketteki verilen tip olup
// olmadığını söyler.
//
// Karşılaştırma yerel takma ada değil IMPORT YOLUNA yapılır: aynı tip bir
// dosyada "link.LinkDefinition", diğerinde "corelink.LinkDefinition" olarak
// yazılabilir.
func nitelikliTip(dosya *kaynakDosyasi, ifade ast.Expr, importYolu, ad string) bool {
	sel, ok := ifade.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != ad {
		return false
	}
	paket, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return dosya.importlar[paket.Name] == importYolu
}

// alanIfadesi bileşik değerdeki adlandırılmış alanın ifadesini döner.
func alanIfadesi(deger *ast.CompositeLit, alan string) ast.Expr {
	for _, eleman := range deger.Elts {
		kv, ok := eleman.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		anahtar, ok := kv.Key.(*ast.Ident)
		if !ok || anahtar.Name != alan {
			continue
		}
		return kv.Value
	}
	return nil
}

// ---------------------------------------------------------------------------
// 1. YÜZEY — interop kayıtları
// ---------------------------------------------------------------------------

// Container kayıt adlarının AİLELERİ.
//
// Bir modülün container'a bıraktığı her ad aynı sözleşmeyi taşımaz ve bu test
// yalnızca [interopAilesi] ile bitenleri denetler. Gerekçeler tek tek:
//
//   - [interopAilesi]: modüller arası İLKEL yüzey (ADR 0001/0006). Tek amacı
//     BAŞKA bir modülün ya da workflow'un onu çözmesidir; yalnızca ilkel ve
//     stdlib tipleri kullanır, tam da import edilemediği için. Tüketicisi
//     olmayan bir interop, hiç kimsenin konuşmadığı bir dildir: kayıt maliyeti
//     ödenir, karşılığı alınmaz.
//   - [servisAilesi]: modülün KENDİ servisi. Eklentilere ve gömülü kullanıma
//     açılan genişletme yüzeyidir; çekirdek onu çözmez, çözmesi de beklenmez.
//     Kutudan çıkan tüketicisinin OLMAMASI meşrudur ve bu yüzden kapsam
//     dışıdır — aksi hâlde test, her modülü kendi servisini kullanacak yapay
//     bir tüketici yazmaya zorlardı.
//   - [sorguAilesi]: Query katmanının sağlayıcısı. Tüketicisi çekirdektedir ve
//     adı ÇALIŞMA ZAMANINDA hesaplar (core/query, link'in karşı ucundaki
//     entity adına ".query" ekleyip container'da arar). Statik olarak
//     izlenemez; bu ailenin bağı [TestSatisKanaliEntityAdiUyusuyor] ile ayrıca
//     sabitlenmiştir.
//   - [saglayiciAilesi]: eklenti sağlayıcılarının kayıt noktası. Bağı
//     [TestSaglayiciKayitAdlariUyusuyor] zaten iddia eder.
//   - [cekirdekAilesi]: çekirdek altyapısı (veritabanı, link, query, saga,
//     olay veri yolu). Modül üretimi değildir.
//   - [raporlamaAilesi]: çekirdeğin sahip olduğu TEK bir yuva — hata
//     raporlayıcı (ADR 0014). Ötekilerden iki yönden ayrılır: modül üretimi
//     DEĞİLDİR (eklenti doldurur) ve TEKİLDİR (kayıt noktası bir defter değil,
//     adın kendisidir; container yinelenen adı zaten reddeder). Tüketicisi
//     çekirdektedir ama KOŞULLUDUR — kurulum bir toplayıcı tanımlamamışsa ad
//     hiç kaydedilmez ve cmd/server önce Has ile sorar — bu yüzden "tüketicisi
//     var mı" denetiminin kapsamı dışındadır.
//   - [yonetimAilesi]: modülün YÖNETİM YAZMA yüzeyi (ADR 0013). Tek tüketicisi
//     yönetim panelidir ve o tüketici KOŞULLUDUR: panel bu adı ancak
//     kayıtlıysa çözer, çünkü modülü kurulmamış bir kurulumda da açılabilmesi
//     gerekir. Bu yüzden "tüketicisi var mı" denetiminin KAPSAMI DIŞINDADIR —
//     kapsama alınsaydı koşullu çözümü zorunlu bir bağa çevirirdi. Ailenin
//     asıl kısıtı başka: bu adı KİMİN anabileceği
//     [TestAdminSurfaceHasOneAudience] ile sınırlıdır, panelin adı modülün
//     sabitiyle tutuyor mu sorusu [TestPanelKatalogAdlariUyusuyor] ile.
const (
	interopAilesi   = ".interop"
	servisAilesi    = ".service"
	sorguAilesi     = ".query"
	saglayiciAilesi = ".providers"
	yonetimAilesi   = ".admin"
	raporlamaAilesi = "error.reporter"
	cekirdekAilesi  = "core."
)

// cozumCagrisiParcasi container'dan çözüm yapan fonksiyonların ad parçasıdır.
//
// Çözüm her zaman doğrudan container.Resolve ile yapılmaz: workflow'lar hatayı
// sınıfını koruyarak sarmak için resolve/resolveOptional sarmalayıcıları,
// eklenti host'u ise cozSink'i kullanır. Ad parçasıyla eşleşmek, bu
// sarmalayıcıların hepsini tek kuralla yakalar ve yeni bir sarmalayıcı
// eklendiğinde testin güncellenmesini gerektirmez.
const cozumCagrisiParcasi = "resolve"

// TestInteropYuzeylerininTuketicisiVar container'a bırakılan her modüller arası
// yüzeyin BAŞKA bir yerden çözüldüğünü doğrular.
//
// Kayıt edilip hiç çözülmeyen bir interop ölü sözleşmedir ve ölü olduğunu
// söylemez: modül açılır, adı loglar, yüzeyi kurar. Faz 8/9'un mount
// edilmemesi ve b2b'nin bileşim köküne kaydedilmemesi tam olarak bu şekilde
// gözden kaçtı.
//
// İki ayrım bilinçlidir:
//
//   - TESTLERDE çözülen bir yüzey tüketilmiş SAYILMAZ. Testler bu taramanın
//     dışındadır ([kaynagiTara] _test.go dosyalarını atlar). Bir yüzeyin tek
//     çağıranı kendi entegrasyon testiyse, o yüzey ürüne hiçbir şey katmıyor
//     demektir; test yalnızca yüzeyin çalıştığını kanıtlar, gerektiğini değil.
//   - KENDİ modülü içinden çözülen bir yüzey tüketilmiş SAYILMAZ. interop'un
//     varlık nedeni modüller arası erişimdir; kendi paketinden erişim için
//     zaten somut tip vardır.
func TestInteropYuzeylerininTuketicisiVar(t *testing.T) {
	t.Parallel()

	agac := kaynagiTara(t)
	saglanan := agac.saglananAdlar(t)

	tuketen := map[string][]string{}
	for ad, yerler := range agac.cagrilar {
		if !strings.Contains(strings.ToLower(ad), cozumCagrisiParcasi) {
			continue
		}
		for _, yer := range yerler {
			for _, arg := range yer.cagri.Args {
				for _, deger := range agac.dizeDegerleri(yer.dosya, yer.fn, arg, 0) {
					tuketen[deger] = append(tuketen[deger], yer.dosya.yol)
				}
			}
		}
	}

	sayac := 0
	for _, ad := range slices.Sorted(maps.Keys(saglanan)) {
		if !strings.HasSuffix(ad, interopAilesi) {
			continue
		}
		sayac++

		kaynakDosya := saglanan[ad]
		sahipOnek := modulOneki(kaynakDosya)
		var disaridan []string
		for _, yol := range tuketen[ad] {
			if sahipOnek != "" && strings.HasPrefix(yol, sahipOnek) {
				continue
			}
			disaridan = append(disaridan, yol)
		}

		if len(disaridan) == 0 {
			t.Errorf("%s: %q yüzeyi container'a kaydediliyor ama HİÇBİR ÜRETİM DOSYASI çözmüyor.\n"+
				"Modüller arası ilkel yüzeyin (ADR 0001/0006) tek amacı başka bir modülün ya da "+
				"workflow'un onu çözmesidir; tüketicisi olmayan bir interop ölü sözleşmedir.\n"+
				"Ya yüzeyi tüketen kablolamayı ekleyin ya da kaydı kaldırın. "+
				"(Yalnızca testlerde çözülmesi TÜKETİM SAYILMAZ.)",
				kaynakDosya.yol, ad)
		}
	}

	if sayac == 0 {
		t.Fatal("hiç interop kaydı bulunamadı; tarama yüzeyi kaçırıyor olmalı " +
			"(kayıt biçimi değiştiyse bu test de değişmeli)")
	}
}

// saglananAdlar container'a Provide ile bırakılan tüm adları döner.
//
// Ad çözülemezse hata verilir: çözülemeyen bir kayıt, denetlenmeyen bir
// kayıttır ve sessizce atlanması bu testin kapsamını görünmez biçimde
// daraltırdı.
//
// Ad AİLESİ de burada denetlenir. Tanınmayan bir soneke sahip yeni bir kayıt,
// testi düşürür: yeni bir aile ortaya çıktığında birinin "bunun tüketicisi
// kim?" sorusunu yanıtlaması ve kararı yukarıdaki sabit bloğuna yazması
// gerekir. Sessizce kapsam dışında kalması, bu dosyanın önlemeye çalıştığı
// hatanın ta kendisidir.
func (a *kaynakAgaci) saglananAdlar(t *testing.T) map[string]*kaynakDosyasi {
	t.Helper()

	saglanan := map[string]*kaynakDosyasi{}
	for _, yer := range a.cagrilar["Provide"] {
		if len(yer.cagri.Args) == 0 {
			continue
		}
		adlar := a.dizeDegerleri(yer.dosya, yer.fn, yer.cagri.Args[0], 0)
		if len(adlar) == 0 {
			t.Errorf("%s: Provide çağrısının KAYIT ADI statik olarak çözülemedi.\n"+
				"Bu testler adları kaynaktan gezerek toplar; çözülemeyen bir ad denetlenemez. "+
				"Adı bir dize sabitine bağlayın.", a.yer(yer.dosya, yer.cagri.Pos()))
			continue
		}
		for _, ad := range adlar {
			if !bilinenAile(ad) {
				t.Errorf("%s: %q kaydı TANINMAYAN bir ad ailesine ait.\n"+
					"Her aile için \"tüketicisi kim ve nerede aranır\" sorusu yanıtlanmıştır "+
					"(bkz. interopAilesi/servisAilesi/... sabitleri). Yeni aileyi oraya "+
					"gerekçesiyle ekleyin; kapsam dışı kalması BİLİNÇLİ bir karar olmalı.",
					a.yer(yer.dosya, yer.cagri.Pos()), ad)
				continue
			}
			if _, varsa := saglanan[ad]; !varsa {
				saglanan[ad] = yer.dosya
			}
		}
	}
	return saglanan
}

// bilinenAile adın tanınan kayıt ailelerinden birine ait olup olmadığını söyler.
func bilinenAile(ad string) bool {
	if strings.HasPrefix(ad, cekirdekAilesi) {
		return true
	}
	if ad == raporlamaAilesi {
		return true
	}
	for _, sonek := range []string{interopAilesi, servisAilesi, sorguAilesi, saglayiciAilesi, yonetimAilesi} {
		if strings.HasSuffix(ad, sonek) {
			return true
		}
	}
	return false
}

// modulOneki dosyanın ait olduğu modülün depo yolu önekini döner.
//
// Modül dışındaki dosyalar (cmd, workflows, plugins, çekirdek) için boş döner:
// onların her çözümü tanım gereği "dışarıdan"dır.
func modulOneki(dosya *kaynakDosyasi) string {
	onek := modulesDir + "/"
	if !strings.HasPrefix(dosya.yol, onek) {
		return ""
	}
	kalan := strings.TrimPrefix(dosya.yol, onek)
	modul, _, bulundu := strings.Cut(kalan, "/")
	if !bulundu {
		return ""
	}
	return onek + modul + "/"
}

// ---------------------------------------------------------------------------
// 2. YÜZEY — olay konuları
// ---------------------------------------------------------------------------

// abonesizYayinlar dışarıya açık olduğu için abonesi ARANMAYAN olaylardır.
//
// Bir konunun depo içinde abonesi olmaması BİLİNÇLİ olabilir: gobit bir
// çatıdır ve kurulumu yapan uygulama kendi handler'ını bağlayabilir. O karar
// burada, konu adı ve GEREKÇESİYLE yazılır; yazılmadığı sürece abonesiz konu
// hatadır.
//
// Harita bugün BOŞTUR ve bu bir eksiklik değil bulgudur: yayımlanan dört
// konunun dördünün de depo içinde abonesi vardır (üç katalog olayını
// plugins/searchpg, "order.placed"ı notification modülü dinler). Muafiyetin
// bedeli yüksektir — muaf bir konu, "order.placed"ın aylarca hiçbir şey
// yapmadığı duruma geri dönüş demektir — bu yüzden buraya bir satır eklerken
// gerekçenin "kim, hangi kurulumda dinliyor" sorusunu yanıtlaması beklenir.
var abonesizYayinlar = map[string]string{}

// TestOlayKonularininAbonesiVar yayımlanan her olay adının bir abonesi
// olduğunu doğrular.
//
// "order.placed" uzun süre abonesiz yayımlandı: sipariş verildiğinde olay
// üretiliyor, veri yoluna yazılıyor ve hiçbir şey olmuyordu. Kimse hata
// görmedi; olayın var olması, bir işin yapıldığı izlenimini verdi. Bildirim
// modülü yazılana kadar özellik YOKTU.
//
// Yayım tarafı GEZİLİR: eventbus.Event değerinin Name alanı çözülür, ad bir
// fonksiyon parametresinden geliyorsa çağıranlara inilir (product'ın üç
// katalog olayı tek bir yayım satırından geçer). Çözülemeyen bir yayım hata
// verir; sessizce atlanması, denetlenmeyen bir konu bırakırdı.
//
// Abonelik tarafında çözülemeyen bir ad ise SESSİZCE atlanır ve bu asimetri
// bilinçlidir: çekirdeğin eklenti host'u aboneliği ileten bir ara katmandır ve
// adı parametre olarak taşır. Kaçırılan bir abonelik yalnızca YANLIŞ ALARM
// üretebilir (var olan aboneyi görmemek), sessiz geçiş üretemez.
func TestOlayKonularininAbonesiVar(t *testing.T) {
	t.Parallel()

	agac := kaynagiTara(t)
	const eventbusYolu = modulePath + "/internal/core/eventbus"

	yayimlanan := map[string]string{}
	for _, yer := range agac.cagrilar["Publish"] {
		if len(yer.cagri.Args) < 2 {
			continue
		}
		deger := agac.olayDegeri(yer, yer.cagri.Args[1], eventbusYolu)
		if deger == nil {
			continue
		}
		adIfadesi := alanIfadesi(deger, "Name")
		adlar := agac.dizeDegerleri(yer.dosya, yer.fn, adIfadesi, 0)
		if len(adlar) == 0 {
			t.Errorf("%s: yayımlanan olayın ADI statik olarak çözülemedi.\n"+
				"Adı çözülemeyen bir konunun abonesi de aranamaz; olay adını bir dize "+
				"sabitine bağlayın (bkz. service.EventOrderPlaced).",
				agac.yer(yer.dosya, yer.cagri.Pos()))
			continue
		}
		for _, ad := range adlar {
			yayimlanan[ad] = agac.yer(yer.dosya, yer.cagri.Pos())
		}
	}

	abone := map[string][]string{}
	for _, yer := range agac.cagrilar["Subscribe"] {
		if len(yer.cagri.Args) == 0 {
			continue
		}
		for _, ad := range agac.dizeDegerleri(yer.dosya, yer.fn, yer.cagri.Args[0], 0) {
			abone[ad] = append(abone[ad], yer.dosya.yol)
		}
	}

	if len(yayimlanan) == 0 {
		t.Fatal("hiç olay yayımı bulunamadı; tarama yayım yüzeyini kaçırıyor olmalı " +
			"(eventbus.Event'in kullanımı değiştiyse bu test de değişmeli)")
	}

	for _, ad := range slices.Sorted(maps.Keys(yayimlanan)) {
		if gerekce, muaf := abonesizYayinlar[ad]; muaf {
			if len(abone[ad]) > 0 {
				t.Errorf("%q konusu abonesiz sayılmış ama abonesi VAR (%s).\n"+
					"Muafiyet gerekçesi artık geçerli değil (%q); abonesizYayinlar'dan silin — "+
					"ölü bir muafiyet, bir sonraki gerçek ihlali örter.",
					ad, strings.Join(abone[ad], ", "), gerekce)
			}
			continue
		}
		if len(abone[ad]) == 0 {
			t.Errorf("%s: %q olayı yayımlanıyor ama HİÇBİR ÜRETİM DOSYASI abone olmuyor.\n"+
				"Abonesiz bir konu, yapıldığı sanılan ama yapılmayan bir iştir: yayım başarılı "+
				"döner, kimse hata görmez, özellik yoktur (\"order.placed\" aylarca böyleydi).\n"+
				"Ya aboneyi ekleyin ya da yayını dış gözlemciler için BİLİNÇLİ tutuyorsanız "+
				"abonesizYayinlar haritasına gerekçesiyle yazın.", yayimlanan[ad], ad)
		}
	}

	for _, ad := range slices.Sorted(maps.Keys(abonesizYayinlar)) {
		if _, varsa := yayimlanan[ad]; !varsa {
			t.Errorf("%q konusu abonesizYayinlar'da muaf ama artık YAYIMLANMIYOR.\n"+
				"Muafiyetler de bakımsız kalır; girdiyi silin.", ad)
		}
	}
}

// olayDegeri Publish çağrısına verilen eventbus.Event bileşik değerini bulur.
//
// İki biçim desteklenir çünkü depoda ikisi de vardır: değer çağrının içinde
// yazılabilir (product) ya da önce bir yerel değişkene kurulup sonra
// verilebilir (order). Bu paketin Event'i olmayan bir Publish çağrısı — başka
// bir kütüphanenin aynı adlı metodu — nil döner ve denetime girmez.
func (a *kaynakAgaci) olayDegeri(yer cagriYeri, arg ast.Expr, eventbusYolu string) *ast.CompositeLit {
	if deger, ok := arg.(*ast.CompositeLit); ok {
		if nitelikliTip(yer.dosya, deger.Type, eventbusYolu, "Event") {
			return deger
		}
		return nil
	}

	id, ok := arg.(*ast.Ident)
	if !ok || yer.fn == nil {
		return nil
	}

	var bulunan *ast.CompositeLit
	ast.Inspect(yer.fn, func(n ast.Node) bool {
		atama, ok := n.(*ast.AssignStmt)
		if !ok || len(atama.Lhs) != 1 || len(atama.Rhs) != 1 {
			return true
		}
		hedef, ok := atama.Lhs[0].(*ast.Ident)
		if !ok || hedef.Name != id.Name {
			return true
		}
		deger, ok := atama.Rhs[0].(*ast.CompositeLit)
		if !ok || !nitelikliTip(yer.dosya, deger.Type, eventbusYolu, "Event") {
			return true
		}
		bulunan = deger
		return false
	})
	return bulunan
}

// ---------------------------------------------------------------------------
// 3. YÜZEY — link tanımları
// ---------------------------------------------------------------------------

// linkOkumaMetotlari link servisinin bir bağı GEZEN metotlarıdır.
//
// Ayrım bu testin çekirdeğidir: bir bağı kurmak (Create) ve kaldırmak (Delete)
// YAZMA yoludur, ilişkiyi kimseye göstermez. Bir bağın var olmasının tek
// gözlemlenebilir sonucu, birinin onu OKUMASIDIR.
var linkOkumaMetotlari = []string{"List", "ListMany", "ListManyByTo"}

// Link kullanımlarının YAZMA tarafını ve Query genişletmesini adlandıran
// etiketler.
//
// [genisletmeMetodu] bir metot adı değildir: Query katmanında bağ, çağrıyla
// değil query.Expansion değeriyle gezilir ve o da bir okuma yoludur. Aynı
// listede durması, "okuma nedir" sorusunun tek yerde yanıtlanmasını sağlar.
const (
	linkSilmeMetodu  = "Delete"
	linkYazmaMetodu  = "Create"
	genisletmeMetodu = "Expansion"
)

// linkKullanimi bir link adının tek bir kullanım yeridir.
type linkKullanimi struct {
	dosya *kaynakDosyasi
	fn    *ast.FuncDecl
	metot string
	pos   token.Pos
}

// TestLinkTanimlariGeziliyor bildirilen her link adının bir OKUMA yolunda
// kullanıldığını doğrular.
//
// Bu, satış kanalı arızasının testidir. Ürün ↔ satış kanalı bağı yazılıyor,
// yönetim API'sinden atanıyor, veritabanında duruyordu — ve hiçbir okuma onu
// gezmiyordu. Vitrin, kanala atanmamış ürünleri de gösteriyordu; özellik
// "yapıldı" sayıldı, davranış hiç değişmedi. Bağın yazılması, bağın
// ÇALIŞTIĞININ kanıtı değildir.
//
// # Temizlik okuması tüketim SAYILMAZ
//
// Silme telafisi kendi bağlarını önce okur, sonra siler (bugünkü örnek:
// product servisindeki clearVariantLink). Bu okuma, bağı yok etmek içindir
// ve ilişkiyi kimseye göstermez — yazma yolunun bir parçasıdır. Sayılsaydı
// kural boşalırdı: bağı silen her modül, bağını "okuyor" sayılırdı ve test
// hiçbir zaman hiçbir şey bulamazdı. Bu yüzden bir okuma, AYNI FONKSİYONDA
// aynı bağın silinmesi varsa temizlik kabul edilir.
//
// # Muafiyet mekanizması YOKTUR
//
// Olay konularının aksine burada bilinçli boşluk kabul edilmez. Bir bağ
// gerçekten dışarısı için bildiriliyorsa doğru cevap muafiyet değil, o bağı
// gezen bir okuma yolu (Query genişletmesi ya da modülün kendi API'si)
// eklemektir: okunmayan bir bağ, veriyi yazar, maliyeti öder ve karşılığında
// hiçbir davranış üretmez.
func TestLinkTanimlariGeziliyor(t *testing.T) {
	t.Parallel()

	agac := kaynagiTara(t)
	const linkYolu = modulePath + "/internal/core/link"
	const queryYolu = modulePath + "/internal/core/query"

	bildirilen := map[string]string{}
	for _, bilesik := range agac.bilesikler {
		for _, tanim := range nitelikliDegerler(bilesik, linkYolu, "LinkDefinition") {
			// Alansız değer bir BİLDİRİM değil, sıfır değerdir: çekirdeğin
			// query motoru hata dönerken "link.LinkDefinition{}" yazar ve onun
			// çözülecek bir adı yoktur.
			if len(tanim.Elts) == 0 {
				continue
			}
			adIfadesi := alanIfadesi(tanim, "Name")
			adlar := agac.dizeDegerleri(bilesik.dosya, bilesik.fn, adIfadesi, 0)
			if len(adlar) == 0 {
				t.Errorf("%s: link tanımının ADI statik olarak çözülemedi.\n"+
					"Adı çözülemeyen bir bağın gezilip gezilmediği de denetlenemez; "+
					"adı bir dize sabitine bağlayın (bkz. service.LinkProductSalesChannel).",
					agac.yer(bilesik.dosya, tanim.Pos()))
				continue
			}
			for _, ad := range adlar {
				bildirilen[ad] = agac.yer(bilesik.dosya, tanim.Pos())
			}
		}
	}

	if len(bildirilen) == 0 {
		t.Fatal("hiç link tanımı bulunamadı; tarama bildirim yüzeyini kaçırıyor olmalı " +
			"(link.LinkDefinition'ın kullanımı değiştiyse bu test de değişmeli)")
	}

	kullanimlar := agac.linkKullanimlari(bildirilen, queryYolu)

	for _, ad := range slices.Sorted(maps.Keys(bildirilen)) {
		siliciler := map[*ast.FuncDecl]bool{}
		for _, k := range kullanimlar[ad] {
			if k.metot == linkSilmeMetodu && k.fn != nil {
				siliciler[k.fn] = true
			}
		}

		var okumalar, yazmalar []string
		for _, k := range kullanimlar[ad] {
			if k.metot == linkYazmaMetodu {
				yazmalar = append(yazmalar, agac.yer(k.dosya, k.pos))
				continue
			}
			if !slices.Contains(linkOkumaMetotlari, k.metot) && k.metot != genisletmeMetodu {
				continue
			}
			if k.fn != nil && siliciler[k.fn] {
				continue // temizlik: bağı silmek için okuyor
			}
			okumalar = append(okumalar, agac.yer(k.dosya, k.pos))
		}

		if len(okumalar) == 0 {
			t.Errorf("%s: %q bağı bildiriliyor ve yazılıyor (%s) ama HİÇ GEZİLMİYOR.\n"+
				"Bağın tek gözlemlenebilir sonucu okunmasıdır; okunmayan bir bağ veriyi yazar, "+
				"maliyetini öder ve hiçbir davranış üretmez — satış kanalı arızası tam olarak "+
				"buydu.\n"+
				"Okuma yolu: link servisinin %s metotlarından biri ya da query.Expansion. "+
				"(Silme telafisinin kendi bağını okuması TÜKETİM SAYILMAZ.)",
				bildirilen[ad], ad, yazmaOzeti(yazmalar), strings.Join(linkOkumaMetotlari, "/"))
		}
	}
}

// nitelikliDegerler bileşik değerin içindeki verilen tipteki değerleri döner.
//
// İki biçim de karşılanır: tekil "link.LinkDefinition{...}" ve modüllerin
// kullandığı "[]link.LinkDefinition{ {...}, {...} }" — ikincide eleman
// değerlerinin tipi YAZILMAZ ve yalnızca dilim tipinden bilinir. Aynı ikilik
// query.Expansion için de geçerlidir; ortak yardımcı, ikisinden birinin dilim
// biçiminin gözden kaçmasını engeller.
func nitelikliDegerler(bilesik bilesikYeri, importYolu, tipAdi string) []*ast.CompositeLit {
	if nitelikliTip(bilesik.dosya, bilesik.deger.Type, importYolu, tipAdi) {
		return []*ast.CompositeLit{bilesik.deger}
	}

	dizi, ok := bilesik.deger.Type.(*ast.ArrayType)
	if !ok || !nitelikliTip(bilesik.dosya, dizi.Elt, importYolu, tipAdi) {
		return nil
	}

	var out []*ast.CompositeLit
	for _, eleman := range bilesik.deger.Elts {
		if deger, ok := eleman.(*ast.CompositeLit); ok {
			out = append(out, deger)
		}
	}
	return out
}

// yazmaOzeti bağın yazıldığı yerleri hata mesajı için özetler.
//
// Yazma yeri hiç yoksa bulgunun anlamı DEĞİŞİR: okunmayan ve yazılmayan bir
// bağ, unutulmuş bir bildirimdir; okunmayan ama yazılan bir bağ, her istekte
// bedeli ödenen ölü veridir. Mesaj ikisini karıştırmamalıdır.
func yazmaOzeti(yazmalar []string) string {
	if len(yazmalar) == 0 {
		return "hiç yazılmıyor da"
	}
	return "yazma: " + strings.Join(yazmalar, ", ")
}

// linkKullanimlari bildirilen link adlarının tüm kullanım yerlerini toplar.
//
// Tarama ÇAĞRIDAN değil DEĞERDEN gider: metot adı aday listesini daraltır, ama
// kaydı yapan şey argümanın bildirilen bir link adına çözülmesidir. Böylece
// aynı ada sahip alakasız metotlar (repository.Delete gibi) kendiliğinden
// elenir ve çekirdeğin kendi jenerik gezicisi (core/query, adı çalışma
// zamanında alır) hiçbir bağı "okunmuş" göstermez.
func (a *kaynakAgaci) linkKullanimlari(bildirilen map[string]string, queryYolu string) map[string][]linkKullanimi {
	out := map[string][]linkKullanimi{}

	metotlar := append(slices.Clone(linkOkumaMetotlari), linkSilmeMetodu, linkYazmaMetodu)
	for _, metot := range metotlar {
		for _, yer := range a.cagrilar[metot] {
			if len(yer.cagri.Args) < 2 {
				continue
			}
			for _, deger := range a.dizeDegerleri(yer.dosya, yer.fn, yer.cagri.Args[1], 0) {
				if _, bildirilmis := bildirilen[deger]; !bildirilmis {
					continue
				}
				out[deger] = append(out[deger], linkKullanimi{
					dosya: yer.dosya, fn: yer.fn, metot: metot, pos: yer.cagri.Pos(),
				})
			}
		}
	}

	for _, bilesik := range a.bilesikler {
		for _, genisletme := range nitelikliDegerler(bilesik, queryYolu, "Expansion") {
			for _, deger := range a.dizeDegerleri(bilesik.dosya, bilesik.fn, alanIfadesi(genisletme, "Link"), 0) {
				if _, bildirilmis := bildirilen[deger]; !bildirilmis {
					continue
				}
				out[deger] = append(out[deger], linkKullanimi{
					dosya: bilesik.dosya, fn: bilesik.fn, metot: genisletmeMetodu, pos: genisletme.Pos(),
				})
			}
		}
	}

	return out
}

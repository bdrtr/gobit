package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Bileşim köklerinin ve çekirdek sözleşmesinin yolları.
const (
	// bilesimKoku üretim ikilisinin modül kaydını kurduğu pakettir. Kaynağı
	// AYRIŞTIRILIR, import EDİLMEZ: main paketi import edilemez.
	bilesimKoku = "cmd/server"
	// e2eZemini uçtan uca testlerin modülleri gerçek migration'larla ayağa
	// kaldırdığı pakettir. O paket "integration" derleme etiketinin
	// arkasındadır; ayrıştırma etiketi umursamaz, bu yüzden bu denetim
	// entegrasyon koşusu olmadan da çalışır.
	e2eZemini = "internal/e2e"
	// cekirdekModulPaketi [module.Module] sözleşmesinin ve [module.Registry]
	// kaydının yaşadığı çekirdek paketidir.
	cekirdekModulPaketi = modulePath + "/internal/core/module"
)

// modulSozlesmesi [module.Module] arayüzünün metot kümesidir: metot adından
// beklenen parametre ve sonuç tiplerine.
//
// İmzalar KAYNAK METİN olarak karşılaştırılır (go/types.ExprString), çünkü bu
// denetimin tip denetleyicisi çalıştırması gerekmez ve gerekseydi go.mod'a yeni
// bir bağımlılık girerdi. Bunun bedeli, paketin farklı bir takma adla import
// edilmesi hâlinde eşleşmenin kaçmasıdır; bedel [modulUygulayanPaketler]
// içinde ödenir: dört metot ADI da bulunup imza tutmazsa test SESSİZ
// GEÇMEZ, hata verir.
var modulSozlesmesi = map[string]struct {
	parametreler []string
	sonuclar     []string
}{
	"Name": {sonuclar: []string{"string"}},
	"Register": {
		parametreler: []string{"context.Context", "*container.Container"},
		sonuclar:     []string{"error"},
	},
	"Migrations": {sonuclar: []string{"fs.FS"}},
	"Routes":     {parametreler: []string{"chi.Router"}},
}

// kayitDisiModuller bileşim kökünde BİLİNÇLİ olarak kayıt dışı bırakılan modül
// paketlerinin gerekçeleridir; anahtar paketin import yoludur.
//
// # Muafiyet neden var
//
// Yazılmış ama henüz kablolanmamış bir modül gerçek bir durumdur (yarım kalmış
// bir faz, yalnızca gömülü kullanım için düşünülmüş bir modül). Böyle bir
// modülü kayıt DIŞI bırakmayı imkânsız kılmak, geliştiriciyi testi susturmanın
// başka bir yolunu aramaya iterdi ve o yol her zaman daha sessiz olurdu.
//
// # Muafiyet neden BURADA
//
// İşaret, dizinde duran ".gitkeep" gibi sessiz bir dosya DEĞİL, gerekçesi
// yazılmış bir kod satırıdır: gerekçe kod incelemesinde görünür, git blame'de
// bir tarihi ve sahibi olur, gerekçesiz eklenemez (değer zorunludur) ve
// aşağıdaki BAYAT MUAFİYET denetimi sayesinde çürüyemez — muaf tutulan modül
// bir gün kaydedilirse ya da paket silinirse test yine düşer. Yani muafiyet
// borçtur ve borç görünür durur.
//
// Bugün boştur: deponun on beş modülünün on beşi de kayıtlıdır.
var kayitDisiModuller = map[string]string{}

// e2eZemininDisiModuller e2e zemininde BİLİNÇLİ olarak kurulmayan modül
// paketlerinin gerekçeleridir; anahtar paketin import yoludur.
//
// Ayrı bir harita tutulmasının sebebi, iki muafiyetin FARKLI şeyleri
// anlatmasıdır: [kayitDisiModuller] "bu modül üretimde yok" der,
// bu harita "bu modül üretimde var ama uçtan uca zeminde koşturulamıyor" der
// (örneğin dış bir servise zorunlu bağımlılığı olan bir modül). İkincisi
// birincisinden çok daha hafif bir borçtur ve aynı listede durmaları, ağır
// olanı hafif gösterirdi.
//
// Bugün boştur: zemin üretimin kaydettiği her modülü kurar.
var e2eZemininDisiModuller = map[string]string{}

// ayristirilmisDosya bir Go dosyasının ayrıştırılmış hâlini ve import takma
// adlarını taşır.
type ayristirilmisDosya struct {
	yol       string
	agac      *ast.File
	importlar map[string]string
}

// TestHerModulBilesimKokundeKayitli "yazılan her modül BİLEŞİM KÖKÜNDE
// kayıtlı" değişmezini denetler.
//
// # Hangi arıza sınıfı
//
// Bu deponun en pahalı hata sınıfı, TÜKETİCİSİ OLMAYAN YETENEKTİR: Faz 8 ve
// Faz 9'un tamamı yazılmış, testleri yeşilken /admin/v1/** uçlarının HİÇBİRİ
// mount edilmemişti — yönetim yüzeyi bir kurulumda hiç var olmadı. Aynısı b2b
// modülünde tekrarlandı: modül, migration'ı, uçları ve testleri hazırdı ama
// cmd/server'da kaydı yoktu, yani harcama limiti HİÇBİR kurulumda
// uygulanmıyordu ve order her müşteriyi sınırsız sayıyordu.
//
// İki arızanın da ortak yanı, hiçbir testin düşmemesidir: modülün kendi
// testleri modülü kendisi kurar, bu yüzden yeşildir. Eksik olan şey modülde
// değil, modül ile bileşim kökü ARASINDADIR ve orayı denetleyen kimse yoktu.
//
// # Neden liste tutmuyor
//
// Denetim modül dizinlerini GEZER ve [module.Module] sözleşmesini kimin
// karşıladığını metot kümesinden çıkarır. Elle yazılmış bir modül listesi,
// kuralı yalnızca BUGÜN için uygulardı: on altıncı modülü yazan kişi listeyi
// güncellemeyi unuttuğunda test yine yeşil kalırdı — yani tam olarak
// yakalaması gereken hatayı kaçırırdı.
//
// # Neden ayrıştırıyor, import etmiyor
//
// Bileşim kökü main paketidir ve import EDİLEMEZ. Kayıt bu yüzden kaynaktan
// okunur: [module.Registry] değişkeni bulunur, üzerindeki Add çağrıları
// toplanır ve her çağrının argümanının hangi pakete gittiği dosyanın import
// listesinden çözülür.
func TestHerModulBilesimKokundeKayitli(t *testing.T) {
	t.Parallel()

	moduller := modulUygulayanPaketler(t)
	require.NotEmpty(t, moduller,
		"internal/modules altında [module.Module] uygulayan hiçbir paket bulunamadı; "+
			"denetim KÖR kalmış olmalı (sözleşme mi değişti?)")

	kayitli := kayitliModulPaketleri(t, bilesimKoku, false)

	for _, yol := range slices.Sorted(maps.Keys(moduller)) {
		if _, kayitliMi := kayitli[yol]; kayitliMi {
			continue
		}
		if gerekce, muaf := kayitDisiModuller[yol]; muaf {
			t.Logf("%s bileşim kökünde bilinçli olarak KAYITLI DEĞİL: %s", yol, gerekce)
			continue
		}
		t.Errorf("%s paketi [module.Module]'ü uyguluyor (%s) ama %s/'daki kayda EKLENMİYOR.\n"+
			"Kaydedilmeyen bir modül hiçbir kurulumda yoktur: migration'ı uygulanmaz, "+
			"servisi container'a girmez, uçları mount edilmez ve modülün kendi testleri "+
			"yeşil kaldığı için bu hiçbir yerde görünmez.\n"+
			"Ya cmd/server/main.go'da registry.Add(...) satırını ekleyin, ya da modülü "+
			"gerekçesiyle birlikte kayitDisiModuller haritasına yazın.",
			yol, strings.Join(moduller[yol], ", "), bilesimKoku)
	}

	// Ters yön denetimin KENDİ kör noktasını kapatır: bileşim kökünün
	// kaydettiği bir internal/modules paketini denetim modül olarak GÖRMÜYORSA,
	// sözleşme okuması kaymış demektir. O andan sonra bu test "her modül
	// kayıtlı" değil "gördüğüm modüller kayıtlı" derdi — yani yarın yazılan
	// modülü sessizce kapsam dışı bırakır ve yeşil kalırdı.
	for _, yol := range slices.Sorted(maps.Keys(modulPaketlerineSuz(kayitli))) {
		if _, gorulduMu := moduller[yol]; !gorulduMu {
			t.Errorf("%s paketi %s/'da kayıtlı ama denetim onu [module.Module] "+
				"uygulayan bir paket olarak GÖRMÜYOR.\n"+
				"Kayıt bir modül olduğunu kanıtlar; denetimin görmemesi, sözleşme "+
				"okumasının (modulSozlesmesi) gerçekle ayrıştığı anlamına gelir ve bu "+
				"testi bundan sonra kör bırakır.", yol, bilesimKoku)
		}
	}

	bayatMuafiyetleriDenetle(t, kayitDisiModuller, moduller,
		"[module.Module] uygulayan bir paket", kayitli, bilesimKoku)
}

// TestKayitliHerModulE2EZemindeKurulu bileşim kökünde kayıtlı her modülün
// uçtan uca zeminde de kurulduğunu denetler.
//
// # Değişmezin ikinci yarısı neden gerekli
//
// Birinci yarı ([TestHerModulBilesimKokundeKayitli]) "yazılan modül üretimde
// kayıtlı mı" diye sorar. Tek başına yetmez, çünkü KAYIT ile ÇALIŞMA aynı şey
// değildir: registry.Add satırı derlenir, açılışta koşar ve yine de o modülün
// gerçek migration'ı, gerçek route'ları ve modüller arası bağı hiçbir testte
// bir arada denenmemiş olabilir. b2b tam olarak böyleydi — kaydın kendisi bir
// satırdı ve o satırın order modülünün davranışını değiştirdiğini gösteren tek
// yer e2e zeminidir.
//
// # Neden e2e zemini, neden başka bir zemin değil
//
// Zemin, kurulumun üretimdeki hâlinin tek kopyasıdır: aynı çekirdek servis
// adları, aynı [module.Registry], gerçek PostgreSQL, gerçek migration'lar ve
// yetki denetimini router AĞACINI gezerek yapan testler (bkz.
// internal/e2e/yetki_test.go, 196 yönetim ucu). Bir modül o zemine girdiği anda
// mevcut ağaç gezen testlerin kapsamına da girer; girmediği sürece ne kadar
// test yazılırsa yazılsın kendi kabarcığında kalır.
//
// # Bu test Faz 8/9 arızasını görür müydü
//
// Doğrudan değil, birinci yarı görürdü. Ama zemin ile üretimin AYNI kümeyi
// tutması şartı, arızanın oluşmasını engellerdi: Faz 8/9 modülleri zemine
// eklendiği anda bu test üretimde eksik olan kaydı isterdi; zemine de
// eklenmeselerdi birinci yarı zaten düşerdi. İki yarı birlikte, "modül var ama
// hiçbir bileşim kökünde yok" durumunu kapatır.
//
// # Neden internal/e2e'ye dokunulmuyor
//
// e2e_test.go zaten "Modül kümesi ve sırası cmd/server/main.go'dakinin
// aynısıdır" diye YAZIYOR. Bu bir yorum vaadidir ve bu depodaki üçüncü hata
// sınıfı tam olarak budur: godoc'un vaadi ile kodun davranışının ayrışması.
// Vaadi zorlayan şey vaadin yanına yazılacak bir satır değil, onu dışarıdan
// denetleyen bir testtir; test o dosyayı DEĞİŞTİRMEZ, yalnızca okur.
func TestKayitliHerModulE2EZemindeKurulu(t *testing.T) {
	t.Parallel()

	uretim := modulPaketlerineSuz(kayitliModulPaketleri(t, bilesimKoku, false))
	zemin := modulPaketlerineSuz(kayitliModulPaketleri(t, e2eZemini, true))
	require.NotEmpty(t, zemin,
		"e2e zemininde hiçbir modül kaydı bulunamadı; denetim KÖR kalmış olmalı")

	for _, yol := range slices.Sorted(maps.Keys(uretim)) {
		if _, kuruluMu := zemin[yol]; kuruluMu {
			continue
		}
		if gerekce, muaf := e2eZemininDisiModuller[yol]; muaf {
			t.Logf("%s e2e zemininde bilinçli olarak KURULMUYOR: %s", yol, gerekce)
			continue
		}
		t.Errorf("%s modülü %s/'da kayıtlı (%s) ama %s/ zemininde KURULMUYOR.\n"+
			"Zemine girmeyen bir modülün üretim kablolaması hiçbir yerde uçtan uca "+
			"denenmez: migration'ı gerçek veritabanında koşmaz, uçları router ağacını "+
			"gezen yetki denetiminin kapsamına girmez ve modüller arası bağı yalnızca "+
			"TAKLİT karşılıklarla sınanır.\n"+
			"Ya zemine ekleyin, ya da modülü gerekçesiyle e2eZemininDisiModuller "+
			"haritasına yazın.",
			yol, bilesimKoku, uretim[yol], e2eZemini)
	}

	// Ters yön BURADA denetlenmez ve bu bir eksiklik değil: zeminde kurulu olup
	// üretimde kayıtlı OLMAYAN bir modül, tam olarak Faz 8/9 arızasıdır ve
	// [TestHerModulBilesimKokundeKayitli] onu zaten düşürür. İki testin aynı
	// şeyi iki kez söylemesi, biri değiştiğinde ötekinin sessizce gereksizleşmesi
	// demek olurdu.
	bayatMuafiyetleriDenetle(t, e2eZemininDisiModuller, uretim,
		"bileşim kökünde kayıtlı bir modül", zemin, e2eZemini)
}

// bayatMuafiyetleriDenetle muafiyet haritasının çürümesini yakalar.
//
// Bir muafiyet iki şekilde bayatlar: muaf tutulan paket ARTIK YOKTUR (silinmiş
// ya da adı değişmiş bir modül) ya da paket ARTIK KAYITLIDIR. İkisi de sessiz
// kalırsa harita, kimsenin okumadığı ve kimsenin doğrulamadığı bir yorum
// yığınına döner — muafiyeti kod içinde tutmanın bütün gerekçesi de o anda
// düşer.
func bayatMuafiyetleriDenetle[T any](
	t *testing.T,
	muafiyetler map[string]string,
	aday map[string]T,
	adayAciklamasi string,
	kayitli map[string]token.Position,
	kok string,
) {
	t.Helper()

	for _, yol := range slices.Sorted(maps.Keys(muafiyetler)) {
		if _, adayMi := aday[yol]; !adayMi {
			t.Errorf("muafiyet BAYAT: %q artık %s değil.\n"+
				"Paket silindiyse ya da adı değiştiyse muafiyet satırı da gitmelidir; "+
				"kalırsa bir gün aynı adla yazılan yeni bir modülü sessizce muaf tutar.",
				yol, adayAciklamasi)
			continue
		}
		if konum, kayitliMi := kayitli[yol]; kayitliMi {
			t.Errorf("muafiyet BAYAT: %q %s/'da artık kayıtlı (%s) ama hâlâ muaf tutuluyor.\n"+
				"Muafiyet borçtur; borç ödendiğinde satır silinmelidir.", yol, kok, konum)
		}
	}
}

// modulUygulayanPaketler internal/modules altında [module.Module] sözleşmesini
// karşılayan paketleri döner: anahtar paketin import yolu, değer sözleşmeyi
// karşılayan tiplerin adlarıdır.
//
// Modül olmayan yardımcı paketler (api, service, repository, models…) elenir
// çünkü sözleşme METOT KÜMESİNDEN okunur, dizin adından değil: bir modülün alt
// paketi dördünü birden taşımaz.
func modulUygulayanPaketler(t *testing.T) map[string][]string {
	t.Helper()

	bulunan := map[string][]string{}
	for _, dizin := range slices.Sorted(maps.Keys(uretimPaketleri(t, filepath.Join(repoRoot, modulesDir)))) {
		fset := token.NewFileSet()
		dosyalar := ayristir(t, fset, dizin, false)
		aliciMetotlari := map[string]map[string]*ast.FuncDecl{}

		for _, d := range dosyalar {
			for _, decl := range d.agac.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
					continue
				}
				tip := aliciTipAdi(fn.Recv.List[0].Type)
				if tip == "" {
					continue
				}
				if aliciMetotlari[tip] == nil {
					aliciMetotlari[tip] = map[string]*ast.FuncDecl{}
				}
				aliciMetotlari[tip][fn.Name.Name] = fn
			}
		}

		for _, tip := range slices.Sorted(maps.Keys(aliciMetotlari)) {
			if sozlesmeyiKarsiliyor(t, fset, dizin, tip, aliciMetotlari[tip]) {
				yol := paketImportYolu(t, dizin)
				bulunan[yol] = append(bulunan[yol], tip)
			}
		}
	}

	return bulunan
}

// sozlesmeyiKarsiliyor bir tipin metot kümesinin [module.Module]'ü karşılayıp
// karşılamadığını söyler.
//
// Dört metot ADININ tamamı varken imzalardan biri tutmuyorsa sonuç "hayır"
// değil, HATADIR: bu ya sözleşmenin değişip denetimin geride kalması ya da
// paketin farklı bir takma adla import edilmesi demektir. İkisinde de doğru
// davranış sessizce elemek değil, sesini çıkarmaktır — sessiz eleme, modülü
// denetimin kapsamından çıkarır ve testi tam da işe yarayacağı yerde kör
// bırakır.
func sozlesmeyiKarsiliyor(
	t *testing.T,
	fset *token.FileSet,
	dizin, tip string,
	metotlar map[string]*ast.FuncDecl,
) bool {
	t.Helper()

	tam := true
	for ad := range modulSozlesmesi {
		if _, varMi := metotlar[ad]; !varMi {
			tam = false
			break
		}
	}
	if !tam {
		return false
	}

	uyumlu := true
	for _, ad := range slices.Sorted(maps.Keys(modulSozlesmesi)) {
		beklenen := modulSozlesmesi[ad]
		fn := metotlar[ad]
		parametreler := alanTipleri(fn.Type.Params)
		sonuclar := alanTipleri(fn.Type.Results)
		if slices.Equal(parametreler, beklenen.parametreler) && slices.Equal(sonuclar, beklenen.sonuclar) {
			continue
		}
		uyumlu = false
		t.Errorf("%s: %s.%s imzası [module.Module] sözleşmesine uymuyor.\n"+
			"beklenen: (%s) (%s) — bulunan: (%s) (%s)\n"+
			"Tip dört metodun DÖRDÜNÜ de taşıyor, yani büyük olasılıkla bir modül. "+
			"Sözleşme değiştiyse modulSozlesmesi haritası da güncellenmelidir; "+
			"aksi hâlde bu paket kayıt denetiminden sessizce düşer.",
			fset.Position(fn.Pos()), tip, ad,
			strings.Join(beklenen.parametreler, ", "), strings.Join(beklenen.sonuclar, ", "),
			strings.Join(parametreler, ", "), strings.Join(sonuclar, ", "))
	}

	if !uyumlu {
		t.Logf("%s: %s tipi sözleşmeyi karşılamış SAYILIYOR; denetim kör kalmasın diye "+
			"kayıt şartı yine uygulanır", dizin, tip)
	}

	return true
}

// kayitliModulPaketleri verilen paketteki [module.Registry] kaydına eklenen
// modüllerin import yollarını döner; değer, Add çağrısının konumudur.
//
// testDosyalariDahil, e2e zemini içindir: orada kurulum TestMain akışında
// yapılır ve üretim dosyası yoktur.
func kayitliModulPaketleri(t *testing.T, kok string, testDosyalariDahil bool) map[string]token.Position {
	t.Helper()

	dizin := filepath.Join(repoRoot, kok)
	fset := token.NewFileSet()
	dosyalar := ayristir(t, fset, dizin, testDosyalariDahil)
	require.NotEmpty(t, dosyalar, "%s içinde ayrıştırılacak Go dosyası yok", kok)

	// Önce kaydın DEĞİŞKEN adları toplanır: "Add" adında bir metot her tipte
	// olabilir (atomik sayaç, eklenti kaydı, slice sarmalayıcı) ve alıcının
	// gerçekten modül kaydı olduğunu ancak bu ad kümesi söyler.
	degiskenler := kayitDegiskenAdlari(dosyalar)
	require.NotEmpty(t, degiskenler,
		"%s içinde bir [module.Registry] değişkeni bulunamadı.\n"+
			"Kayıt başka bir biçimde kuruluyorsa (yardımcı işlev, tip gömme) bu denetim "+
			"HİÇBİR ŞEY doğrulamıyor demektir; kayitDegiskenAdlari güncellenmelidir.", kok)

	kayitli := map[string]token.Position{}
	for _, d := range dosyalar {
		ast.Inspect(d.agac, func(n ast.Node) bool {
			cagri, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sec, ok := cagri.Fun.(*ast.SelectorExpr)
			if !ok || sec.Sel.Name != "Add" {
				return true
			}
			alici, ok := sec.X.(*ast.Ident)
			if !ok || !degiskenler[alici.Name] {
				return true
			}

			konum := fset.Position(cagri.Lparen)
			paketAdi, cozuldu := argumaninPaketi(cagri.Args)
			if !cozuldu {
				t.Errorf("%s: %s.Add(...) argümanı tanınmayan biçimde.\n"+
					"Denetim yalnızca \"paket.Yapıcı(...)\" biçimini okuyabilir; başka bir "+
					"biçim, kaydın hangi modüle gittiğini denetimden GİZLER. Kayıt bu biçime "+
					"getirilmeli ya da argumaninPaketi genişletilmelidir.", konum, alici.Name)
				return true
			}
			yol, bilinen := d.importlar[paketAdi]
			if !bilinen {
				t.Errorf("%s: %q paketi %s dosyasının import listesinde çözülemedi",
					konum, paketAdi, filepath.Base(d.yol))
				return true
			}
			kayitli[yol] = konum

			return true
		})
	}

	return kayitli
}

// kayitDegiskenAdlari paketteki [module.Registry] değerlerini tutan
// tanımlayıcıların adlarını döner.
//
// İki kaynak taranır: module.NewRegistry çağrısının atandığı değişkenler ve
// tipi *module.Registry olan bildirimler (işlev parametreleri dâhil — e2e
// zemini kaydı yardımcı bir işleve PARAMETRE olarak geçirir).
func kayitDegiskenAdlari(dosyalar []ayristirilmisDosya) map[string]bool {
	adlar := map[string]bool{}

	for _, d := range dosyalar {
		ast.Inspect(d.agac, func(n ast.Node) bool {
			switch dugum := n.(type) {
			case *ast.AssignStmt:
				for i, sag := range dugum.Rhs {
					cagri, ok := sag.(*ast.CallExpr)
					if !ok || !cekirdeginModulTipi(cagri.Fun, "NewRegistry", d.importlar) {
						continue
					}
					if i < len(dugum.Lhs) {
						if ident, ok := dugum.Lhs[i].(*ast.Ident); ok {
							adlar[ident.Name] = true
						}
					}
				}
			case *ast.ValueSpec:
				if cekirdeginModulTipi(dugum.Type, "Registry", d.importlar) {
					for _, ident := range dugum.Names {
						adlar[ident.Name] = true
					}
					break
				}
				for i, deger := range dugum.Values {
					cagri, ok := deger.(*ast.CallExpr)
					if !ok || !cekirdeginModulTipi(cagri.Fun, "NewRegistry", d.importlar) {
						continue
					}
					if i < len(dugum.Names) {
						adlar[dugum.Names[i].Name] = true
					}
				}
			case *ast.Field:
				if cekirdeginModulTipi(dugum.Type, "Registry", d.importlar) {
					for _, ident := range dugum.Names {
						adlar[ident.Name] = true
					}
				}
			}

			return true
		})
	}

	return adlar
}

// cekirdeginModulTipi ifadenin çekirdeğin module paketindeki verilen ada
// karşılık gelip gelmediğini söyler; işaretçi yıldızı yok sayılır.
//
// Takma ad haritası üzerinden çözülür, tanımlayıcı adına bakılmaz: paketi
// "coremodule" diye import eden bir dosya da doğru tanınmalıdır.
func cekirdeginModulTipi(ifade ast.Expr, ad string, importlar map[string]string) bool {
	if ifade == nil {
		return false
	}
	if yildiz, ok := ifade.(*ast.StarExpr); ok {
		ifade = yildiz.X
	}
	sec, ok := ifade.(*ast.SelectorExpr)
	if !ok || sec.Sel.Name != ad {
		return false
	}
	paket, ok := sec.X.(*ast.Ident)

	return ok && importlar[paket.Name] == cekirdekModulPaketi
}

// argumaninPaketi Add argümanındaki modül yapıcısının paket adını döner.
//
// Beklenen biçim "paket.Yapıcı(...)"dır; başka biçimler çözülemedi olarak
// döner ve çağıran tarafta HATA olur (bkz. [kayitliModulPaketleri]).
func argumaninPaketi(argumanlar []ast.Expr) (string, bool) {
	if len(argumanlar) != 1 {
		return "", false
	}
	cagri, ok := argumanlar[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sec, ok := cagri.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	paket, ok := sec.X.(*ast.Ident)
	if !ok {
		return "", false
	}

	return paket.Name, true
}

// modulPaketlerineSuz kayıt kümesini internal/modules altındaki paketlere
// indirger.
//
// Eklentilerin getirdiği modüller (bkz. plugins/searchpg) kayda çekirdeğin
// eklenti host'u üzerinden girer, bileşim kökünde Add satırı yoktur ve bu
// denetimin konusu değildir: değişmez "internal/modules altında yazılan her
// modül" der.
func modulPaketlerineSuz(kayitli map[string]token.Position) map[string]token.Position {
	onek := modulePath + "/" + modulesDir + "/"
	suzulmus := make(map[string]token.Position, len(kayitli))
	for yol, konum := range kayitli {
		if strings.HasPrefix(yol, onek) {
			suzulmus[yol] = konum
		}
	}

	return suzulmus
}

// uretimPaketleri kök altındaki, en az bir üretim (_test.go olmayan) dosyası
// bulunan dizinleri döner.
func uretimPaketleri(t *testing.T, kok string) map[string]struct{} {
	t.Helper()

	dizinler := map[string]struct{}{}
	for _, dosya := range goFiles(t, kok) {
		if strings.HasSuffix(dosya, "_test.go") {
			continue
		}
		dizinler[filepath.Dir(dosya)] = struct{}{}
	}

	return dizinler
}

// ayristir bir dizindeki Go dosyalarını (alt dizinlere İNMEDEN) ayrıştırır ve
// import takma adlarını çözer.
func ayristir(t *testing.T, fset *token.FileSet, dizin string, testDosyalariDahil bool) []ayristirilmisDosya {
	t.Helper()

	girdiler, err := os.ReadDir(dizin)
	if err != nil {
		t.Fatalf("%s okunamadı: %v", dizin, err)
	}

	var dosyalar []ayristirilmisDosya
	for _, girdi := range girdiler {
		ad := girdi.Name()
		if girdi.IsDir() || !strings.HasSuffix(ad, ".go") {
			continue
		}
		if !testDosyalariDahil && strings.HasSuffix(ad, "_test.go") {
			continue
		}

		yol := filepath.Join(dizin, ad)
		agac, parseErr := parser.ParseFile(fset, yol, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", yol, parseErr)
		}
		dosyalar = append(dosyalar, ayristirilmisDosya{yol: yol, agac: agac, importlar: importTakmaAdlari(agac)})
	}

	return dosyalar
}

// importTakmaAdlari dosyadaki her import'un yerel adını import yoluna eşler.
//
// Takma ad verilmemişse yol son parçası kullanılır. Bu, paket adının dizin
// adından farklı olduğu durumda yanılır; denetimin baktığı paketlerin hepsinde
// ikisi aynıdır ve yanılma yalnızca bir kaydı GÖRMEMEK olur — o da çağıran
// tarafta sessiz kalmaz, çünkü çözülemeyen paket adı hata verir.
func importTakmaAdlari(agac *ast.File) map[string]string {
	adlar := make(map[string]string, len(agac.Imports))
	for _, imp := range agac.Imports {
		yol := strings.Trim(imp.Path.Value, `"`)
		ad := yol[strings.LastIndex(yol, "/")+1:]
		if imp.Name != nil {
			ad = imp.Name.Name
		}
		adlar[ad] = yol
	}

	return adlar
}

// aliciTipAdi metot alıcısının taban tip adını döner; çözülemezse boş dize.
func aliciTipAdi(ifade ast.Expr) string {
	if yildiz, ok := ifade.(*ast.StarExpr); ok {
		ifade = yildiz.X
	}
	// Genel (generic) alıcılar "T[P]" biçimindedir; taban ad soldadır.
	switch tipli := ifade.(type) {
	case *ast.IndexExpr:
		ifade = tipli.X
	case *ast.IndexListExpr:
		ifade = tipli.X
	}
	if ident, ok := ifade.(*ast.Ident); ok {
		return ident.Name
	}

	return ""
}

// alanTipleri bir parametre ya da sonuç listesinin tiplerini kaynak biçiminde
// döner; "a, b int" gibi paylaşılan bildirimler tipi TEKRARLAYARAK açılır.
func alanTipleri(liste *ast.FieldList) []string {
	if liste == nil {
		return nil
	}

	var tipler []string
	for _, alan := range liste.List {
		for range max(len(alan.Names), 1) {
			tipler = append(tipler, types.ExprString(alan.Type))
		}
	}

	return tipler
}

// paketImportYolu dosya sistemi yolundan Go import yolunu üretir.
func paketImportYolu(t *testing.T, dizin string) string {
	t.Helper()

	rel, err := filepath.Rel(repoRoot, dizin)
	if err != nil {
		t.Fatalf("%s için göreli yol hesaplanamadı: %v", dizin, err)
	}

	return modulePath + "/" + filepath.ToSlash(rel)
}

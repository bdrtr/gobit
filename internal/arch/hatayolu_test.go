package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// coreHTTPPath çekirdeğin HTTP paketinin import yoludur.
const coreHTTPPath = modulePath + "/internal/core/http"

// netHTTPPath standart kütüphanenin HTTP paketinin import yoludur.
const netHTTPPath = "net/http"

// hataYazanAd çekirdekte hata gövdesini yazan TEK fonksiyonun adıdır.
//
// Politikanın tamamı oradadır: sınıflandırılmamış hata KindInternal sayılır,
// mesajı MASKELENİR ve gerçek metin loglanır (bkz. corehttp.WriteError).
const hataYazanAd = "WriteError"

// basariYazanAd çekirdekte BAŞARI gövdesini yazan yardımcının adıdır.
const basariYazanAd = "WriteJSON"

// yaziciTipAdi ResponseWriter parametrelerini tanımak için aranan tip adıdır.
const yaziciTipAdi = "ResponseWriter"

// basariDurumlari net/http'nin 2xx sabitlerinin adlarıdır.
//
// Küme KAPALIDIR: 2xx aralığı HTTP'de sabittir ve net/http'de bu on addan
// başkası yoktur. Bu yüzden burada tutulan liste "bugünün uçları" gibi
// büyümez; yeni bir uç eklendiğinde güncellenmesi gerekmez.
//
// Neden ada bakılıyor: [basariYazanAd] gövdeyi hiç yorumlamadan yazar, yani
// ona 4xx/5xx bir durum verilirse hata yanıtı ÇEKİRDEĞİN POLİTİKASINDAN
// GEÇMEDEN istemciye gider — maskeleme de loglama da atlanır. Sonuç,
// WriteError'ı hiç çağırmamakla aynıdır.
var basariDurumlari = map[string]bool{
	"StatusOK":                   true,
	"StatusCreated":              true,
	"StatusAccepted":             true,
	"StatusNonAuthoritativeInfo": true,
	"StatusNoContent":            true,
	"StatusResetContent":         true,
	"StatusPartialContent":       true,
	"StatusMultiStatus":          true,
	"StatusAlreadyReported":      true,
	"StatusIMUsed":               true,
}

// yaziciyaGuvenliMetotlar ResponseWriter üzerinde gövde ya da durum YAZMAYAN
// metotlardır.
//
// Header() yalnızca haritayı döner; yazma WriteHeader/Write ile başlar ve
// ikisi de bu kümenin dışındadır.
var yaziciyaGuvenliMetotlar = map[string]bool{
	"Header": true,
}

// yaziciAlanGuvenliCagrilar yazıcıyı ALAN ama yanıt YAZMAYAN dış çağrılardır.
//
// Anahtar "importYolu.Ad" biçimindedir; değer gerekçedir.
var yaziciAlanGuvenliCagrilar = map[string]string{
	netHTTPPath + ".MaxBytesReader": "yazıcıyı yalnızca sınır aşılınca bağlantıyı kapatmak için alır; " +
		"gövdeye tek bayt yazmaz",
}

// hataYoluMuafiyeti yanıt gövdesini çekirdek dışından yazan, gerekçesi
// tartışılmış tek bir çağrıdır.
//
// Muafiyet SESSİZ ATLAMA DEĞİLDİR: kullanılmayan bir muafiyet testi düşürür
// (bkz. [TestHataYanitlariTekYerdenYazilir]), yani gerekçe kodla birlikte
// yaşamak zorundadır.
type hataYoluMuafiyeti struct {
	// dosya depo köküne göre yoldur.
	dosya string
	// cagri kaynakta yazıldığı hâliyle çağrı adıdır (örn. "http.ServeContent").
	cagri string
	// neden bu çağrının neden ikinci bir hata tanımı ÜRETMEDİĞİDİR.
	neden string
}

// hataYoluMuafiyetleri gövdeyi çekirdek dışından yazan meşru çağrılardır.
var hataYoluMuafiyetleri = []hataYoluMuafiyeti{
	{
		dosya: "internal/modules/file/api/serve.go",
		cagri: "http.ServeContent",
		// Bu çağrı bir dosyanın İÇERİĞİNİ sunar; gövdesi JSON zarfı değildir,
		// yani çekirdeğin yardımcılarından geçemez. Hata yolu yine de
		// çekirdektedir: sunulacak dosya bulunamazsa ya da açılamazsa handler
		// ServeContent'e HİÇ ULAŞMADAN corehttp.WriteError ile döner (bkz.
		// serveFile). ServeContent'in kendi ürettiği tek hata sınıfı
		// (416 Range Not Satisfiable) istek başlığından hesaplanır ve sunucu
		// içinden hiçbir ayrıntı taşımaz — maskelenecek bir metni yoktur.
		neden: "dosya içeriğini sunar; JSON zarfı değildir ve hata yolu ServeContent'e " +
			"girmeden önce corehttp.WriteError'dan geçer",
	},
}

// httpYuzeyMuafiyeti api paketleri DIŞINDA HTTP yanıtı yazan bir pakettir.
type httpYuzeyMuafiyeti struct {
	// paket depo köküne göre paket dizinidir.
	paket string
	// neden bu yüzeyin neden ayrı bir kapıdan çıkabildiğidir.
	neden string
}

// httpYuzeyMuafiyetleri api dışındaki meşru HTTP yüzeyleridir.
//
// Muaf tutulan paket, hata gövdesini kendi zarfında yazsa bile kuralı
// ÇEKİRDEKTEN GEÇİREREK uygulamak zorundadır; test bunu paketin gerçekten
// corehttp.WriteError çağırdığını doğrulayarak arar (bkz.
// [TestHTTPYuzeyleriYalnizcaApiPaketlerinde]).
var httpYuzeyMuafiyetleri = []httpYuzeyMuafiyeti{
	{
		paket: "internal/modules/product/graph",
		// GraphQL'in yanıt zarfı HTTP'ninki değildir: durum kodu her zaman
		// 200'dür ve hata "errors" dizisinde döner. Bu yüzey gövdeyi
		// kaçınılmaz olarak kendisi kurar. Ama kuralı TEKRAR ETMEZ:
		// servisHatasi, hatayı bellek içi bir yazıcıya corehttp.WriteError ile
		// yazdırır ve çekirdeğin ürettiği zarfı GERİ OKUR. Maskeleme kararı da
		// loglama da o çağrının içinde kalır. Bu satırın var olma sebebi zaten
		// bir kez ayrışmış olmasıdır: koşul bir zamanlar "tipli olmayanı
		// geçir" idi ve pq'nun bağlantı dizesini istemciye veriyordu.
		neden: "GraphQL zarfı HTTP durum kodu taşımaz; gövdeyi kendi kurar ama kararı " +
			"corehttp.WriteError'a yazdırıp zarfını geri okuyarak alır",
	},
}

// TestHataYanitlariTekYerdenYazilir modül API'lerinde hata gövdesini
// çekirdeğin dışında yazan bir yol OLMADIĞINI doğrular.
//
// # Hangi hata sınıfı
//
// Kural TEK yerde tanımlıdır (corehttp.WriteError: sınıflandırılmamış hata
// KindInternal sayılır, mesajı maskelenir, gerçek metin loglanır) ama İKİNCİ
// bir yüzey onu tekrar etmeye kalkıp ayrışabilir. Ölçülmüş bedeli şudur:
// sarmalanmamış bir depo hatası döndüğünde "pq: SSL connection error
// host=db.internal user=gobit password=…" metni istemciye ulaşır ve hiçbir
// yere yazılmaz. Ayrışma sessizdir — testler geçer, uçlar çalışır, yalnızca
// yanlış kişi yanlış metni görür.
//
// # Neden yapıyı GEZİYOR
//
// Elle tutulan bir uç listesi kuralı yalnızca BUGÜN için uygular: yarın
// eklenen handler listede olmaz ve sessizce muaf kalır. Bu test bunun yerine
// api paketlerinin AST'sini gezer ve şunu sorar: bu fonksiyonda
// http.ResponseWriter'a dokunan her yol, çekirdeğin yardımcılarından birine mi
// gidiyor?
//
// Meşru sayılan yollar:
//
//   - corehttp.WriteError — hatanın TEK kapısı.
//   - corehttp.WriteJSON — başarının kapısı; durumu 2xx OLMAK ZORUNDA, çünkü
//     4xx/5xx bir durumla çağrılırsa hata yanıtı politikadan geçmemiş olur.
//     Durum bir parametreyle aktarılıyorsa (writeItem gibi yardımcılar)
//     çağrının kendisine kadar İZLENİR.
//   - Paket içindeki başka bir fonksiyon/metot — o da bu tarama içindedir,
//     yani kural tümevarımla korunur.
//   - [yaziciAlanGuvenliCagrilar] — yazıcıyı alıp gövdeye yazmayanlar.
//
// Geri kalan her şey (w.Write, w.WriteHeader, http.Error, json.NewEncoder(w),
// yazıcının başka bir değere kaçırılması) ihlaldir.
func TestHataYanitlariTekYerdenYazilir(t *testing.T) {
	t.Parallel()

	kullanilan := make([]bool, len(hataYoluMuafiyetleri))
	paketler := apiPaketleri(t)
	if len(paketler) == 0 {
		t.Fatal("hiç api paketi bulunamadı; tarama kökü yanlış olabilir")
	}

	for _, dizin := range paketler {
		paketiDenetle(t, dizin, kullanilan)
	}

	for i, muaf := range hataYoluMuafiyetleri {
		if !kullanilan[i] {
			t.Errorf("kullanılmayan muafiyet: %s içinde %q çağrısı yok.\n"+
				"Gerekçesi (%q) artık bir şeyi savunmuyor: ya çağrı kaldırıldı ve "+
				"muafiyet de silinmeli, ya da başka bir dosyaya taşındı ve muafiyet "+
				"onu artık görmüyor.",
				muaf.dosya, muaf.cagri, muaf.neden)
		}
	}
}

// TestHTTPYuzeyleriYalnizcaApiPaketlerinde HTTP yanıtı yazan her modül
// paketinin TARANDIĞINI doğrular.
//
// [TestHataYanitlariTekYerdenYazilir] yalnızca internal/modules/*/api altını
// gezer. Tek başına bu, kuralı DİZİN ADINA bağlar: yarın eklenen
// modules/x/webhook ya da modules/x/graph paketi hiç taranmaz ve invaryant
// sessizce kapsam dışında kalır — tam olarak "tüketicisi olmayan yetenek"
// sınıfı, ama tersinden: denetleyicisi olmayan yüzey.
//
// Bu test kapsamın kendisini denetler: modüllerde http.ResponseWriter geçen
// her paket ya api altındadır (yani taranır) ya da [httpYuzeyMuafiyetleri]
// içinde gerekçelidir. Muaf paketin gerekçesi de HAVADA KALMAZ: kuralı
// çekirdekten geçirdiğini iddia ettiği için corehttp.WriteError'ı gerçekten
// çağırdığı aranır.
func TestHTTPYuzeyleriYalnizcaApiPaketlerinde(t *testing.T) {
	t.Parallel()

	muafPaketler := map[string]httpYuzeyMuafiyeti{}
	for _, muaf := range httpYuzeyMuafiyetleri {
		muafPaketler[filepath.FromSlash(muaf.paket)] = muaf
	}
	gorulen := map[string]bool{}

	for _, mod := range modulNames(t) {
		kok := filepath.Join(repoRoot, modulesDir, mod)
		for _, dosya := range uretimDosyalari(t, kok) {
			if !yaziciGeciyor(t, dosya) {
				continue
			}
			paket := depoYolu(filepath.Dir(dosya))
			if filepath.Base(paket) == "api" {
				continue
			}
			gorulen[paket] = true
			muaf, ok := muafPaketler[paket]
			if !ok {
				t.Errorf("%s: api DIŞINDA bir HTTP yüzeyi var ve hata yolu denetlenmiyor.\n"+
					"Ya paket internal/modules/<mod>/api altına taşınmalı, ya da "+
					"httpYuzeyMuafiyetleri'ne GEREKÇESİYLE eklenmeli. Gerekçesiz bırakılan "+
					"her yüzey, çekirdeğin hata politikasının ikinci bir kopyasını yazma "+
					"fırsatıdır.", paket)

				continue
			}
			if !cekirdekHataYolunuKullaniyor(t, filepath.Join(repoRoot, paket)) {
				t.Errorf("%s: muafiyetin gerekçesi (%q) tutmuyor — pakette hiç "+
					"corehttp.%s çağrısı yok.\nMuaf yüzey gövdesini kendi kurabilir ama "+
					"hangi hatanın istemciye verilebileceği kararını çekirdeğe "+
					"SORMAK zorundadır.", paket, muaf.neden, hataYazanAd)
			}
		}
	}

	for paket := range muafPaketler {
		if !gorulen[paket] {
			t.Errorf("kullanılmayan yüzey muafiyeti: %s artık http.ResponseWriter kullanmıyor "+
				"(ya da paket kaldırıldı). Muafiyet silinmeli.", paket)
		}
	}
}

// paketiDenetle tek bir api paketini gezer ve ihlalleri raporlar.
//
// Denetim PAKET düzeyindedir çünkü yardımcılar (writeItem gibi) bir dosyada
// tanımlanıp başka dosyalardan çağrılır; durum kodunun izi ancak paketin
// tamamı elde tutulunca sürülebilir.
func paketiDenetle(t *testing.T, dizin string, muafiyetKullanildi []bool) {
	t.Helper()

	fset := token.NewFileSet()
	dosyalar := uretimDosyalari(t, dizin)
	agaclar := make(map[string]*ast.File, len(dosyalar))
	yerelAdlar := map[string]bool{}

	for _, dosya := range dosyalar {
		agac, err := parser.ParseFile(fset, dosya, nil, 0)
		if err != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", dosya, err)
		}
		agaclar[dosya] = agac
		for _, decl := range agac.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				yerelAdlar[fn.Name.Name] = true
			}
		}
	}

	aktaranlar := durumAktaranlariBul(agaclar, yerelAdlar)

	for dosya, agac := range agaclar {
		yollar := importYollari(agac)
		for _, decl := range agac.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			d := &denetim{
				t:          t,
				fset:       fset,
				dosya:      dosya,
				yollar:     yollar,
				yerelAdlar: yerelAdlar,
				aktaranlar: aktaranlar,
				kullanildi: muafiyetKullanildi,
			}
			d.fonksiyonuDenetle(fn)
		}
	}
}

// denetim tek bir fonksiyonun denetimi için gereken bağlamı taşır.
type denetim struct {
	t          *testing.T
	fset       *token.FileSet
	dosya      string
	yollar     map[string]string
	yerelAdlar map[string]bool
	aktaranlar map[string]map[int]bool
	kullanildi []bool
}

// fonksiyonuDenetle bir fonksiyon gövdesindeki yazıcı kullanımlarını denetler.
func (d *denetim) fonksiyonuDenetle(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	sirasi := parametreSirasi(fn)
	kendiAktardigi := d.aktaranlar[fn.Name.Name]

	// Durum kodu denetimi yazıcıdan BAĞIMSIZ yürür: durumu çekirdeğe aktaran
	// bir ara yardımcı, yazıcıyı hiç almadan da çağrılabilir ve o çağrı yine
	// gövdenin hangi durumla yazıldığını belirler.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if cagri, ok := n.(*ast.CallExpr); ok {
			d.durumlariDenetle(cagri, sirasi, kendiAktardigi)
		}

		return true
	})

	yazicilar, bildirimler := yaziciParametreleri(fn, d.yollar)
	if len(yazicilar) == 0 {
		return
	}

	// hesabiVerilen: yazıcının MEŞRU kullanım yerleri. Geriye kalan her
	// kullanım, yazıcının bir çağrının dışına — bir yapıya, bir alana, bir
	// değişkene — kaçırıldığı anlamına gelir ve oradan sonra izi sürülemez.
	hesabiVerilen := map[token.Pos]bool{}
	for _, bildirim := range bildirimler {
		hesabiVerilen[bildirim] = true
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); ok && yazicilar[x.Name] {
				hesabiVerilen[x.Pos()] = true
			}
		case *ast.CallExpr:
			for _, arg := range node.Args {
				if id, ok := arg.(*ast.Ident); ok && yazicilar[id.Name] {
					hesabiVerilen[id.Pos()] = true
				}
			}
			d.cagriyiDenetle(node, yazicilar)
		}

		return true
	})

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || !yazicilar[id.Name] || hesabiVerilen[id.Pos()] {
			return true
		}
		d.hata(id.Pos(), "yazıcı %q bir çağrının dışında kullanılıyor.\n"+
			"Yazıcı başka bir değere kaçırıldığında hangi gövdeyi yazdığı bu "+
			"taramayla izlenemez; hata yanıtı çekirdeği atlayabilir.", id.Name)

		return true
	})
}

// cagriyiDenetle yazıcıya dokunan tek bir çağrıyı kurallara vurur.
func (d *denetim) cagriyiDenetle(cagri *ast.CallExpr, yazicilar map[string]bool) {
	hedef := cagriHedefi(cagri, d.yollar)

	// Yazıcının ÜZERİNDEKİ metot çağrısı: Header() dışındakiler doğrudan
	// gövde/durum yazar.
	if hedef.alici != "" && yazicilar[hedef.alici] {
		if yaziciyaGuvenliMetotlar[hedef.ad] {
			return
		}
		if d.muafMi(hedef.kaynak) {
			return
		}
		d.hata(cagri.Pos(), "%s ile yanıt DOĞRUDAN yazılıyor.\n"+
			"Hata gövdesinin tek kapısı corehttp.%s, başarınınki corehttp.%s'dır; "+
			"ikinci bir yazma yolu, maskeleme ve loglama kararlarının kopyalanması "+
			"demektir.", hedef.kaynak, hataYazanAd, basariYazanAd)

		return
	}

	if !yaziciAlanCagri(cagri, yazicilar) {
		return
	}

	switch {
	case hedef.paket == coreHTTPPath && (hedef.ad == hataYazanAd || hedef.ad == basariYazanAd):
		// Durumun 2xx olup olmadığı ayrıca denetlenir (bkz. durumlariDenetle).
		return
	case hedef.paket != "" && yaziciAlanGuvenliCagrilar[hedef.paket+"."+hedef.ad] != "":
		return
	case hedef.yerel && d.yerelAdlar[hedef.ad]:
		// Çağrılan fonksiyon da bu taramanın içindedir; kural tümevarımla korunur.
		return
	case d.muafMi(hedef.kaynak):
		return
	}

	if hedef.kaynak == "" {
		d.hata(cagri.Pos(), "yazıcı, adı çözülemeyen bir çağrıya veriliyor "+
			"(fonksiyon değeri ya da alan üzerinden).\nYazıcıyı alan her yol "+
			"taranabilir olmalı; aksi hâlde hata gövdesini kimin yazdığı bilinemez.")

		return
	}
	d.hata(cagri.Pos(), "yazıcı %s çağrısına veriliyor.\n"+
		"api paketleri yanıtı yalnızca corehttp.%s / corehttp.%s üzerinden "+
		"yazabilir. Gerçekten meşruysa hataYoluMuafiyetleri'ne GEREKÇESİYLE "+
		"eklenmeli.", hedef.kaynak, hataYazanAd, basariYazanAd)
}

// durumlariDenetle çağrının taşıdığı durum kodlarının 2xx olduğunu doğrular.
//
// İki yol denetlenir. Birincisi çekirdeğe DOĞRUDAN yapılan
// corehttp.WriteJSON çağrısıdır. İkincisi, durumu olduğu gibi çekirdeğe
// geçiren paket içi yardımcılardır (writeItem gibi): denetim yalnızca
// çekirdek çağrısına bakarsa o kapı açık kalır ve writeItem(w, r, 500, gövde)
// hiç görülmez.
func (d *denetim) durumlariDenetle(cagri *ast.CallExpr, sirasi map[string]int, kendiAktardigi map[int]bool) {
	hedef := cagriHedefi(cagri, d.yollar)

	if hedef.paket == coreHTTPPath && hedef.ad == basariYazanAd {
		const durumSirasi = 2
		if len(cagri.Args) > durumSirasi {
			d.durumIfadesiniDenetle(cagri.Args[durumSirasi], sirasi, kendiAktardigi,
				"corehttp."+basariYazanAd)
		}

		return
	}

	if !hedef.yerel || !d.yerelAdlar[hedef.ad] {
		return
	}
	// Sıra numaraları sıralanır: harita gezinme sırası rastgeledir ve hata
	// çıktısının koşudan koşuya değişmesi, aynı ihlali iki farklı bulgu gibi
	// gösterirdi.
	siralar := make([]int, 0, len(d.aktaranlar[hedef.ad]))
	for i := range d.aktaranlar[hedef.ad] {
		siralar = append(siralar, i)
	}
	sort.Ints(siralar)
	for _, i := range siralar {
		if i < len(cagri.Args) {
			d.durumIfadesiniDenetle(cagri.Args[i], sirasi, kendiAktardigi, hedef.ad)
		}
	}
}

// durumIfadesiniDenetle bir durum kodu ifadesinin 2xx olduğunu doğrular.
func (d *denetim) durumIfadesiniDenetle(
	ifade ast.Expr,
	sirasi map[string]int,
	kendiAktardigi map[int]bool,
	hedef string,
) {
	switch v := ifade.(type) {
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		if ok && d.yollar[x.Name] == netHTTPPath {
			if basariDurumlari[v.Sel.Name] {
				return
			}
			d.hata(ifade.Pos(), "%s çağrısına 2xx OLMAYAN durum (http.%s) veriliyor.\n"+
				"Hata yanıtı bu yoldan çıkarsa corehttp.%s'ın maskeleme ve loglama "+
				"kararları HİÇ çalışmaz: gövde ne olursa istemciye o gider.",
				hedef, v.Sel.Name, hataYazanAd)

			return
		}
	case *ast.BasicLit:
		if v.Kind == token.INT {
			if kod, err := strconv.Atoi(v.Value); err == nil && kod >= 200 && kod <= 299 {
				return
			}
		}
	case *ast.Ident:
		// Durum çağıranın parametresinden geliyorsa iz, bu fonksiyonun kendi
		// çağrı yerlerinde sürülür (bkz. durumAktaranlariBul).
		if i, varsa := sirasi[v.Name]; varsa && kendiAktardigi[i] {
			return
		}
	}

	d.hata(ifade.Pos(), "%s çağrısının durum kodu bu taramayla ÇÖZÜLEMİYOR.\n"+
		"Durum ya doğrudan bir 2xx sabiti olmalı ya da çağıranın parametresinden "+
		"aktarılmalı; çözülemeyen bir durum, hata yanıtının çekirdeği atlayıp "+
		"atlamadığını bilinemez kılar.", hedef)
}

// muafMi çağrının bu dosya için gerekçelendirilmiş olup olmadığını söyler.
func (d *denetim) muafMi(kaynak string) bool {
	if kaynak == "" {
		return false
	}
	yol := depoYolu(d.dosya)
	for i, muaf := range hataYoluMuafiyetleri {
		if filepath.FromSlash(muaf.dosya) == yol && muaf.cagri == kaynak {
			d.kullanildi[i] = true

			return true
		}
	}

	return false
}

// hata konumuyla birlikte bir ihlal raporlar.
func (d *denetim) hata(pos token.Pos, bicim string, args ...any) {
	d.t.Helper()
	konum := d.fset.Position(pos)
	d.t.Errorf("%s:%d: "+bicim, append([]any{depoYolu(d.dosya), konum.Line}, args...)...)
}

// hedefBilgisi bir çağrının nereye gittiğini anlatır.
type hedefBilgisi struct {
	// paket dış paketse import yoludur, değilse boştur.
	paket string
	// ad çağrılan fonksiyonun/metodun adıdır.
	ad string
	// alici metot çağrısında alıcı ifadenin tanımlayıcısıdır.
	alici string
	// yerel çağrının aynı pakette çözüldüğünü söyler.
	yerel bool
	// kaynak çağrının kaynaktaki yazılışıdır ("http.ServeContent" gibi);
	// çözülemediyse boştur.
	kaynak string
}

// cagriHedefi çağrının hedefini SÖZDİZİMSEL olarak çözer.
//
// Tip denetleyici çalıştırılmaz: arch paketi depoyu ayrıştırarak gezer ve
// go/types'a bağlanmak taramayı derlenebilirliğe bağımlı kılardı. Ayrım
// import adlarıyla yapılır — dosyanın import ettiği bir ad ise dış paket,
// değilse paket içi bir değer (alıcı) sayılır.
func cagriHedefi(cagri *ast.CallExpr, yollar map[string]string) hedefBilgisi {
	fun := cagri.Fun
	for {
		switch v := fun.(type) {
		case *ast.ParenExpr:
			fun = v.X

			continue
		case *ast.IndexExpr: // generic çağrının açık tip argümanı
			fun = v.X

			continue
		case *ast.IndexListExpr:
			fun = v.X

			continue
		}

		break
	}

	switch v := fun.(type) {
	case *ast.Ident:
		return hedefBilgisi{ad: v.Name, yerel: true, kaynak: v.Name}
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		if !ok {
			return hedefBilgisi{ad: v.Sel.Name}
		}
		if yol, varsa := yollar[x.Name]; varsa {
			return hedefBilgisi{paket: yol, ad: v.Sel.Name, kaynak: x.Name + "." + v.Sel.Name}
		}

		return hedefBilgisi{ad: v.Sel.Name, alici: x.Name, yerel: true, kaynak: x.Name + "." + v.Sel.Name}
	}

	return hedefBilgisi{}
}

// yaziciAlanCagri çağrının argümanları arasında bir yazıcı olup olmadığını
// söyler.
func yaziciAlanCagri(cagri *ast.CallExpr, yazicilar map[string]bool) bool {
	for _, arg := range cagri.Args {
		if id, ok := arg.(*ast.Ident); ok && yazicilar[id.Name] {
			return true
		}
	}

	return false
}

// durumAktaranlariBul durum kodunu çağıranından alıp çekirdeğe geçiren
// yardımcıları ve o parametrenin sırasını bulur.
//
// Sabit noktaya kadar tekrarlanır: bir yardımcıyı saran ikinci bir yardımcı da
// aktaran sayılır, yoksa araya bir katman koymak denetimi atlatırdı.
func durumAktaranlariBul(agaclar map[string]*ast.File, yerelAdlar map[string]bool) map[string]map[int]bool {
	aktaranlar := map[string]map[int]bool{}

	isaretle := func(ad string, sira int) bool {
		if aktaranlar[ad] == nil {
			aktaranlar[ad] = map[int]bool{}
		}
		if aktaranlar[ad][sira] {
			return false
		}
		aktaranlar[ad][sira] = true

		return true
	}

	for degisti := true; degisti; {
		degisti = false
		for _, agac := range agaclar {
			yollar := importYollari(agac)
			for _, decl := range agac.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				sirasi := parametreSirasi(fn)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					cagri, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					hedef := cagriHedefi(cagri, yollar)
					var siralar []int
					switch {
					case hedef.paket == coreHTTPPath && hedef.ad == basariYazanAd:
						siralar = []int{2}
					case hedef.yerel && yerelAdlar[hedef.ad]:
						for i := range aktaranlar[hedef.ad] {
							siralar = append(siralar, i)
						}
					}
					for _, sira := range siralar {
						if sira >= len(cagri.Args) {
							continue
						}
						id, ok := cagri.Args[sira].(*ast.Ident)
						if !ok {
							continue
						}
						if i, varsa := sirasi[id.Name]; varsa && isaretle(fn.Name.Name, i) {
							degisti = true
						}
					}

					return true
				})
			}
		}
	}

	return aktaranlar
}

// parametreSirasi fonksiyonun parametre adlarını sıra numaralarına eşler.
func parametreSirasi(fn *ast.FuncDecl) map[string]int {
	sirasi := map[string]int{}
	if fn.Type.Params == nil {
		return sirasi
	}
	i := 0
	for _, alan := range fn.Type.Params.List {
		if len(alan.Names) == 0 {
			i++

			continue
		}
		for _, ad := range alan.Names {
			sirasi[ad.Name] = i
			i++
		}
	}

	return sirasi
}

// yaziciParametreleri fonksiyondaki http.ResponseWriter parametrelerinin
// adlarını ve BİLDİRİM konumlarını döner.
//
// İç fonksiyon değişmezleri de gezilir: bir handler'ı saran closure da yazıcı
// alır ve gövdeyi oradan yazmak aynı ihlaldir.
//
// Bildirim konumları ayrıca dönülür çünkü bildirimin kendisi bir "kullanım"
// değildir; kaçış taraması onu görmezden gelebilmelidir.
func yaziciParametreleri(fn ast.Node, yollar map[string]string) (map[string]bool, []token.Pos) {
	adlar := map[string]bool{}
	var konumlar []token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		tip, ok := n.(*ast.FuncType)
		if !ok || tip.Params == nil {
			return true
		}
		for _, alan := range tip.Params.List {
			if !yaziciTipiMi(alan.Type, yollar) {
				continue
			}
			for _, ad := range alan.Names {
				adlar[ad.Name] = true
				konumlar = append(konumlar, ad.Pos())
			}
		}

		return true
	})

	return adlar, konumlar
}

// yaziciTipiMi ifadenin http.ResponseWriter tipi olup olmadığını söyler.
func yaziciTipiMi(tip ast.Expr, yollar map[string]string) bool {
	sel, ok := tip.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != yaziciTipAdi {
		return false
	}
	x, ok := sel.X.(*ast.Ident)

	return ok && yollar[x.Name] == netHTTPPath
}

// importYollari dosyadaki import adlarını yollarına eşler.
func importYollari(agac *ast.File) map[string]string {
	yollar := map[string]string{}
	for _, imp := range agac.Imports {
		yol, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		ad := yol[strings.LastIndex(yol, "/")+1:]
		if imp.Name != nil {
			ad = imp.Name.Name
		}
		yollar[ad] = yol
	}

	return yollar
}

// apiPaketleri modüllerin api paket dizinlerini döner.
func apiPaketleri(t *testing.T) []string {
	t.Helper()
	var dizinler []string
	for _, mod := range modulNames(t) {
		dizin := filepath.Join(repoRoot, modulesDir, mod, "api")
		if info, err := os.Stat(dizin); err == nil && info.IsDir() {
			dizinler = append(dizinler, dizin)
		}
	}

	return dizinler
}

// uretimDosyalari kök altındaki test OLMAYAN .go dosyalarını döner.
func uretimDosyalari(t *testing.T, kok string) []string {
	t.Helper()
	var out []string
	for _, dosya := range goFiles(t, kok) {
		if !strings.HasSuffix(dosya, "_test.go") {
			out = append(out, dosya)
		}
	}

	return out
}

// yaziciGeciyor dosyada http.ResponseWriter kullanılıp kullanılmadığını
// söyler.
func yaziciGeciyor(t *testing.T, dosya string) bool {
	t.Helper()
	agac, err := parser.ParseFile(token.NewFileSet(), dosya, nil, 0)
	if err != nil {
		t.Fatalf("%s ayrıştırılamadı: %v", dosya, err)
	}
	yollar := importYollari(agac)
	bulundu := false
	ast.Inspect(agac, func(n ast.Node) bool {
		if bulundu {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && yaziciTipiMi(sel, yollar) {
			bulundu = true
		}

		return true
	})

	return bulundu
}

// cekirdekHataYolunuKullaniyor pakette corehttp.WriteError çağrısı arar.
func cekirdekHataYolunuKullaniyor(t *testing.T, dizin string) bool {
	t.Helper()
	for _, dosya := range uretimDosyalari(t, dizin) {
		agac, err := parser.ParseFile(token.NewFileSet(), dosya, nil, 0)
		if err != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", dosya, err)
		}
		yollar := importYollari(agac)
		bulundu := false
		ast.Inspect(agac, func(n ast.Node) bool {
			if bulundu {
				return false
			}
			cagri, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			hedef := cagriHedefi(cagri, yollar)
			if hedef.paket == coreHTTPPath && hedef.ad == hataYazanAd {
				bulundu = true
			}

			return true
		})
		if bulundu {
			return true
		}
	}

	return false
}

// depoYolu mutlak ya da göreli bir yolu depo köküne göre normalleştirir.
func depoYolu(yol string) string {
	temiz := filepath.Clean(yol)
	kok := filepath.Clean(repoRoot) + string(filepath.Separator)

	return strings.TrimPrefix(temiz, kok)
}

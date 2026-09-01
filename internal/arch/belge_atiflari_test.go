package arch_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bu dosya TEK bir değişmezi zorlar: BELGEDEKİ HER ATIF ÇÖZÜLÜR.
//
// Bu deponun en inatçı hata sınıfı, kuralın ihlali değil BELGENİN ÇÜRÜMESİDİR:
// godoc bir sembole yollar, sembol silinir ya da adı değişir, godoc yerinde
// kalır. Hiçbir araç ses çıkarmaz — Go, çözülemeyen bir bağı hata saymaz, düz
// metin gibi basar. Ölçülmüş örnekler bu turda da çıktı: bir godoc, adı bir kez
// değişmiş bir Config metoduna yolluyordu; bir başkası hiç var olmamış bir
// üreteç adına; bir üçüncüsü, sonradan adı uzatılmış bir repository metoduna.
// Üçü de derlenen, testleri geçen, incelemeden geçmiş kodun içindeydi.
//
// Çürümenin bedeli, yanlış bir yorumun bedelinden BÜYÜKTÜR: yanlış yorum
// okuyanı yanıltır, çürümüş atıf okuyanı ARAMAYA yollar. Aranan şey yoktur ve
// arayan kişi bunu ancak depo genelinde grep ettikten sonra öğrenir.
//
// # Neden burada ve neden GEZEREK
//
// Denetim liste TUTMAZ: hangi bağın nereye çözüleceği kaynaktan çıkarılır.
// Elle yazılmış bir "geçerli semboller" listesi, tam da korumaya çalıştığı şeyi
// (bir sembolün silinmesini) kaçırırdı — liste de onunla birlikte
// güncellenmezdi.
//
// # Bu denetimin NE GARANTİ ETMEDİĞİ
//
// Kapsamı dar tutmak, verdiği sözü de dar tutmak demektir:
//
//   - Atfın DOĞRU yere gittiğini söylemez, ÇÖZÜLDÜĞÜNÜ söyler. Bir sepetin
//     toplamı ile ara toplamı karışmışsa iki bağ da çözülür, denetim susar.
//   - Köşeli ayraç İÇİNDE olmayan anmaları görmez. "bkz. service.Foo" gibi düz
//     metin bir anma denetlenmez; ayraç bir SÖZDÜR, düz metin değildir.
//   - Yalnızca YORUMLARA bakar, dizelere değil. Bu bir eksiklik olurdu — bu
//     deponun ölçülmüş çürüklerinden biri testin DÜŞTÜĞÜNDE bastığı mesajın
//     içindeydi — ama ölçüm tersini söylüyor: depodaki tüm dize sabitlerinde
//     ayraç biçiminde toplam sekiz metin var ve sekizi de bağ değil, geliştirici
//     mesajındaki VURGU. Dizeleri kapsama almak sekiz yanlış pozitifle başlar
//     ve karşılığında bugün hiçbir şey yakalamaz.
//   - Üçüncü taraf paketlerin sembollerini doğrulamaz (bkz. [paketteAtifAra]).
//   - Aynı paketin test dosyalarındaki bildirimler de sayılır. Üretim
//     godoc'unun yalnızca testte var olan bir ada bağlanması bu yüzden
//     yakalanmaz; ayırmak, kendi yardımcılarına bağ veren test godoc'larını
//     bozardı ve kazanç bu bedele değmezdi.
//   - Alıcısız üye adları (bkz. [atifPaketi]) paketteki HERHANGİ bir tipin
//     üyesine çözülür. Silinmiş bir alanı anan böyle bir atıf, aynı adı taşıyan
//     başka bir tip varsa denetimden sessizce geçer.
//
// Yukarıdaki maddeler GO YORUMLARI içindir. Markdown belgeleri de denetlenir
// ama kapsamı ayrı çizilmiştir: orada çıpa köşeli ayraç değil, yolun kendi
// şekli ile ters tırnaktır. Sınırlar [markdownAtiflari] ve
// [TestBelgelerdekiAtiflarCozuluyor] godoc'larında yazılıdır.

// atifDosyasi taranmış tek bir Go dosyasıdır.
type atifDosyasi struct {
	// yol depo köküne göredir ve hata mesajlarında bu görünür.
	yol   string
	dizin string
	paket *atifPaketi
	agac  *ast.File
}

// atifPaketi bir dizin + paket adı ikilisinin ad ve import tablosudur.
//
// Birim DİZİN DEĞİL paket adıdır: aynı dizinde "foo" ve "foo_test" iki ayrı
// paket yaşayabilir ve harici test paketi, test edilen paketin unexported
// adlarını GÖREMEZ. İkisini bir tutmak, çözülemeyecek bir bağı çözülmüş
// gösterirdi.
type atifPaketi struct {
	dizin string
	ad    string
	// adlar üst düzey bildirimleri, "Alıcı.Metot" ve "Tip.Alan" çiftlerini
	// tutar.
	adlar map[string]bool
	// uyeler alıcısı yazılmadan anılabilecek üye adlarıdır (metot ve alan).
	//
	// Go'nun kendi kuralı bunu tanımaz; bu depo tanır, çünkü aynı struct'ın
	// godoc'u kardeş alanına yalnızca ADIYLA yollar ve okur onu bir satır
	// aşağıda bulur. Bedeli, kümenin GENİŞ olmasıdır ve dosya başındaki
	// "garanti etmez" listesinde yazılıdır.
	uyeler map[string]bool
	// gomulu bir tipin gömdüğü tipleri tutar; "T.Metot" araması gömülüyü
	// izleyerek yapılır (testing.T.TempDir gerçekte common üzerindedir).
	gomulu map[string][]string
	// importlar yerel paket adından import yoluna gider; boş değer, aynı adın
	// iki farklı yola bağlandığını (BELİRSİZ) söyler.
	importlar map[string]string
}

// atifTaramasi deponun Go kaynağının atıf denetimi için taranmış hâlidir.
type atifTaramasi struct {
	fset     *token.FileSet
	dosyalar []*atifDosyasi
	paketler map[string]*atifPaketi
	// uretimAdi import yolundan paketin ÜRETİM adına gider; takma adsız bir
	// import'un yerel adı budur ve dizin adından farklı olabilir.
	uretimAdi map[string]string
	// testAdlari depodaki TÜM test fonksiyonlarının adlarıdır.
	//
	// Test paketleri import EDİLEMEZ, yani bir teste yapılan atıf hiçbir zaman
	// nitelikli yazılamaz. Depo testleri adıyla anar (yapilandirma_test.go
	// aynı kümeyi README için kurar) ve "go test -run" da adı adresler.
	testAdlari map[string]bool
	// stdKovasi çözülmüş stdlib paketlerini önbelleğe alır; nil değer "stdlib
	// değil" demektir.
	stdKovasi map[string]*atifPaketi
}

// belgeAtiflariniTara üretim köklerindeki Go kaynağını ayrıştırır.
//
// Test dosyaları DAHİLDİR: bu deponun en yoğun godoc'ları mimari testlerinin
// içindedir ve çürümüş bir atıf orada da aynı zararı verir — üstelik bazıları
// testin DÜŞTÜĞÜNDE bastığı mesajın içine kadar sızar.
func belgeAtiflariniTara(t *testing.T) *atifTaramasi {
	t.Helper()

	tarama := &atifTaramasi{
		fset:       token.NewFileSet(),
		paketler:   map[string]*atifPaketi{},
		uretimAdi:  map[string]string{},
		testAdlari: map[string]bool{},
		stdKovasi:  map[string]*atifPaketi{},
	}

	for _, kok := range uretimKokleri {
		mutlak := filepath.Join(repoRoot, kok)
		if _, err := os.Stat(mutlak); err != nil {
			t.Fatalf("%q kökü bulunamadı: %v", kok, err)
		}
		for _, yol := range goFiles(t, mutlak) {
			agac, err := parser.ParseFile(tarama.fset, yol, nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s ayrıştırılamadı: %v", yol, err)
			}
			bagil, err := filepath.Rel(repoRoot, yol)
			if err != nil {
				t.Fatalf("%s göreli yola çevrilemedi: %v", yol, err)
			}
			bagil = filepath.ToSlash(bagil)
			dosya := &atifDosyasi{
				yol:   bagil,
				dizin: filepath.ToSlash(filepath.Dir(bagil)),
				agac:  agac,
			}
			tarama.dosyalar = append(tarama.dosyalar, dosya)
			if !strings.HasSuffix(agac.Name.Name, "_test") {
				tarama.uretimAdi[modulePath+"/"+dosya.dizin] = agac.Name.Name
			}
		}
	}

	// İkinci geçiş: paket adları toplandıktan SONRA import tabloları kurulur.
	// Takma adsız bir import'un yerel adı hedef paketin BİLDİRDİĞİ addır ve o
	// ad ancak hedef ayrıştırıldıktan sonra bilinir.
	for _, dosya := range tarama.dosyalar {
		dosya.paket = tarama.paketiAl(dosya.dizin, dosya.agac.Name.Name)
		tarama.importlariTopla(dosya)
		bildirimAdlariniTopla(dosya.agac, dosya.paket)
		for _, decl := range dosya.agac.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				tarama.testAdlari[fn.Name.Name] = true
			}
		}
	}

	return tarama
}

// paketiAl dizin + paket adı birimini döner, yoksa kurar.
func (a *atifTaramasi) paketiAl(dizin, ad string) *atifPaketi {
	anahtar := dizin + "\x00" + ad
	if p, ok := a.paketler[anahtar]; ok {
		return p
	}
	p := &atifPaketi{
		dizin:     dizin,
		ad:        ad,
		adlar:     map[string]bool{},
		uyeler:    map[string]bool{},
		gomulu:    map[string][]string{},
		importlar: map[string]string{},
	}
	a.paketler[anahtar] = p
	return p
}

// importlariTopla dosyanın import'larını PAKETİN tablosuna ekler.
//
// Tablo dosya değil PAKET düzeyindedir çünkü go/doc da öyle yapar: nitelikli
// bir bağ, niteleyen paketi PAKETİN herhangi bir dosyası import etmişse
// çözülür. Kural olmasaydı doc.go gibi hiçbir şey import etmeyen belge
// dosyalarındaki bağların tamamı kırık sayılırdı.
func (a *atifTaramasi) importlariTopla(dosya *atifDosyasi) {
	for _, imp := range dosya.agac.Imports {
		yol := strings.Trim(imp.Path.Value, `"`)
		yerel := ""
		switch {
		case imp.Name != nil:
			yerel = imp.Name.Name
		case a.uretimAdi[yol] != "":
			yerel = a.uretimAdi[yol]
		default:
			yerel = varsayilanPaketAdi(yol)
		}
		if yerel == "" || yerel == "_" || yerel == "." {
			continue
		}
		if eski, ok := dosya.paket.importlar[yerel]; ok && eski != yol {
			dosya.paket.importlar[yerel] = ""
			continue
		}
		dosya.paket.importlar[yerel] = yol
	}
}

// varsayilanPaketAdi takma adı olmayan bir import'un yerel adını tahmin eder.
//
// go/doc'un assumedPackageName'inin aynısıdır ve aynısı olmak ZORUNDADIR:
// "github.com/go-chi/chi/v5" yolunun son parçası "v5"tir, yerel adı ise
// "chi". Tahminin kayması, o paketin TÜM bağlarını "import yok" diye kırık
// gösterirdi — yani yanlış suçlama yığını üretirdi.
func varsayilanPaketAdi(importYolu string) string {
	kimlikDisi := func(ch rune) bool {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_':
			return false
		case ch >= utf8.RuneSelf && (unicode.IsLetter(ch) || unicode.IsDigit(ch)):
			return false
		default:
			return true
		}
	}
	taban := path.Base(importYolu)
	if strings.HasPrefix(taban, "v") {
		if _, err := strconv.Atoi(taban[1:]); err == nil {
			if dizin := path.Dir(importYolu); dizin != "." {
				taban = path.Base(dizin)
			}
		}
	}
	taban = strings.TrimPrefix(taban, "go-")
	if i := strings.IndexFunc(taban, kimlikDisi); i >= 0 {
		taban = taban[:i]
	}
	return taban
}

// bildirimAdlariniTopla dosyanın üst düzey bildirimlerini pakete ekler.
func bildirimAdlariniTopla(agac *ast.File, paket *atifPaketi) {
	for _, decl := range agac.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				paket.adlar[d.Name.Name] = true
				continue
			}
			paket.adlar[atifAliciAdi(d.Recv.List[0].Type)+"."+d.Name.Name] = true
			paket.uyeler[d.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					paket.adlar[s.Name.Name] = true
					uyeAdlariniTopla(s.Name.Name, s.Type, paket)
				case *ast.ValueSpec:
					for _, ad := range s.Names {
						paket.adlar[ad.Name] = true
					}
				}
			}
		}
	}
}

// uyeAdlariniTopla bir tipin alanlarını, arayüz metotlarını ve gömdüklerini
// kaydeder.
func uyeAdlariniTopla(tip string, ifade ast.Expr, paket *atifPaketi) {
	var liste *ast.FieldList
	switch t := ifade.(type) {
	case *ast.StructType:
		liste = t.Fields
	case *ast.InterfaceType:
		liste = t.Methods
	default:
		return
	}
	if liste == nil {
		return
	}
	for _, alan := range liste.List {
		if len(alan.Names) == 0 {
			// Gömülü alan: adı gömülen TİPİN adıdır ve üyeleri o tipten gelir.
			if ad := atifAliciAdi(alan.Type); ad != "" {
				paket.gomulu[tip] = append(paket.gomulu[tip], ad)
				paket.adlar[tip+"."+ad] = true
				paket.uyeler[ad] = true
			}
			continue
		}
		for _, ad := range alan.Names {
			paket.adlar[tip+"."+ad.Name] = true
			paket.uyeler[ad.Name] = true
		}
	}
}

// atifAliciAdi bir tip ifadesinin niteliksiz adını döner.
func atifAliciAdi(ifade ast.Expr) string {
	switch t := ifade.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return atifAliciAdi(t.X)
	case *ast.IndexExpr:
		return atifAliciAdi(t.X)
	case *ast.IndexListExpr:
		return atifAliciAdi(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// bagBicimi bir doc bağı adayının SÖZDİZİM sınıfıdır.
//
// Sınıf, körleşme denetiminin ölçüsüdür: tarayıcı bozulduğunda genellikle TÜM
// bağları değil bir SINIFI kaybeder (ayraç okuması, nokta ayrıştırması, yol
// biçimi ayrı kod yollarıdır). Sınıf başına sayaç, o kaybı görünür kılar.
type bagBicimi int

const (
	// bagYerelAd ayraç içinde tek parçalı bir addır.
	bagYerelAd bagBicimi = iota
	// bagNitelikli iki parçalıdır: paket.Ad ya da Tip.Üye.
	bagNitelikli
	// bagUcParcali paket + alıcı + addır: [testing.T.TempDir].
	bagUcParcali
	// bagTamYol import yolu taşır: [github.com/bdrtr/gobit/internal/core/module.Module].
	bagTamYol
	bagBicimSayisi
)

// bagBicimAdlari hata mesajlarında biçimin adıdır.
var bagBicimAdlari = [bagBicimSayisi]string{
	bagYerelAd:   "yerel ad ([Ad])",
	bagNitelikli: "nitelikli ad ([paket.Ad] ya da [Tip.Üye])",
	bagUcParcali: "paket + alıcı ([paket.Tip.Üye])",
	bagTamYol:    "tam import yolu ([github.com/…/paket.Ad])",
}

// bagAdayi bir yorumda geçen, doc bağı BİÇİMİNDE bir metindir.
type bagAdayi struct {
	icerik string
	bicim  bagBicimi
	dosya  *atifDosyasi
	satir  int
}

// bagAdaylari bir yorum metnindeki doc bağı adaylarını çıkarır.
//
// # Yanlış pozitif KURALA bağlıdır, listeye değil
//
// Köşeli ayraç her zaman bir bağ değildir: yorumlarda JSON dizileri
// (["a","b"]), matematiksel aralıklar ([0, %100]), dilim sözdizimi ([]string)
// ve Türkçe metinde parantez yerine kullanılmış ayraçlar da vardır. Ayıklama
// üç kuralla yapılır ve hiçbiri "şu metinleri yok say" listesi değildir:
//
//  1. Go'nun KENDİ bağlam kuralı: ayracın öncesi ve sonrası boşluk ya da
//     noktalama olmalıdır (kural go/doc/comment'ten birebir alınmıştır).
//  2. İçerik, isteğe bağlı bir "*" soyulduktan sonra NOKTAYLA ayrılmış Go
//     tanımlayıcılarına (ya da bir import yolu + tanımlayıcılara) bölünmelidir.
//     Boşluk, tırnak, süslü ayraç, yüzde işareti, Türkçe harf — hepsi elenir.
//  3. Parça sayısı üçü aşamaz; Go'nun kendisi daha derinini ifade edemez.
//
// Kuralın Go'dan SAPTIĞI tek yer, adın büyük harfle başlama zorunluluğudur:
// go/doc yalnızca dışa açık adları bağ sayar, bu depo ise küçük harfli yerel
// adlara da bağ verir (paket içinde geçerlidirler ve godoc'ların yarısı
// unexported tanımların üstündedir). Sapma bilinçlidir; bedeli, tek kelimelik
// bir ASCII sözcüğün ayraç içinde yazılmasının bağ SAYILMASIDIR.
func bagAdaylari(metin string, dosya *atifDosyasi, satir int) []bagAdayi {
	var adaylar []bagAdayi
	for bas := 0; bas < len(metin); bas++ {
		if metin[bas] != '[' {
			continue
		}
		uzaklik := strings.IndexByte(metin[bas+1:], ']')
		if uzaklik < 0 {
			break
		}
		son := bas + 1 + uzaklik
		icerik := metin[bas+1 : son]
		once, sonra := metin[:bas], metin[son+1:]
		bas = son

		if !bagBaglamiUygun(once, sonra) {
			continue
		}
		bicim, ok := bagBicimini(icerik)
		if !ok {
			continue
		}
		adaylar = append(adaylar, bagAdayi{icerik: icerik, bicim: bicim, dosya: dosya, satir: satir})
	}
	return adaylar
}

// bagBaglamiUygun go/doc/comment'in bağ bağlam kuralını uygular.
//
// Kural şudur: ayracın hemen öncesindeki ve sonrasındaki karakter boşluk ya da
// noktalama olmalıdır. Bir kelimeye yapışık ayraç ("dizi[i]") bağ değildir.
func bagBaglamiUygun(once, sonra string) bool {
	if once != "" {
		r, _ := utf8.DecodeLastRuneInString(once)
		if !unicode.IsPunct(r) && r != ' ' && r != '\t' && r != '\n' {
			return false
		}
	}
	if sonra != "" {
		r, _ := utf8.DecodeRuneInString(sonra)
		if !unicode.IsPunct(r) && r != ' ' && r != '\t' && r != '\n' {
			return false
		}
	}
	return true
}

// bagKimligiMi bir parçanın Go tanımlayıcısı olup olmadığını söyler.
//
// ASCII ile sınırlıdır ve bu, yanlış pozitifleri eleyen kuralın kendisidir:
// Türkçe metinde ayraç içine alınmış bir sözcük (ör. [Ölçüldü]) neredeyse her
// zaman ASCII dışı bir harf taşır ve bu yüzden bağ sayılmaz. Go'nun izin
// verdiği ASCII dışı tanımlayıcılar bu depoda kullanılmaz.
func bagKimligiMi(parca string) bool {
	if parca == "" {
		return false
	}
	for i := 0; i < len(parca); i++ {
		switch c := parca[i]; {
		case c == '_', c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return parca[0] < '0' || parca[0] > '9'
}

// bagParcala bağ içeriğini import yolu ve nokta ile ayrılmış parçalara böler.
func bagParcala(icerik string) (importYolu string, parcalar []string, ok bool) {
	icerik = strings.TrimPrefix(icerik, "*")
	if icerik == "" {
		return "", nil, false
	}
	if !strings.Contains(icerik, "/") {
		parcalar = strings.Split(icerik, ".")
		for _, parca := range parcalar {
			if !bagKimligiMi(parca) {
				return "", nil, false
			}
		}
		return "", parcalar, true
	}
	egik := strings.LastIndexByte(icerik, '/')
	kuyruk := strings.Split(icerik[egik+1:], ".")
	for _, parca := range kuyruk {
		if !bagKimligiMi(parca) {
			return "", nil, false
		}
	}
	yol := icerik[:egik+1] + kuyruk[0]
	if !bagYoluGecerli(yol) {
		return "", nil, false
	}
	return yol, kuyruk[1:], true
}

// bagYoluGecerli bir dizenin import yolu biçiminde olup olmadığını söyler.
func bagYoluGecerli(yol string) bool {
	if yol == "" || strings.Contains(yol, "//") || strings.HasSuffix(yol, "/") {
		return false
	}
	for _, parca := range strings.Split(yol, "/") {
		if parca == "" || strings.HasPrefix(parca, ".") || strings.HasSuffix(parca, ".") {
			return false
		}
		for i := 0; i < len(parca); i++ {
			c := parca[i]
			uygun := c == '_' || c == '-' || c == '.' || c == '~' ||
				c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !uygun {
				return false
			}
		}
	}
	return true
}

// bagBicimini bir içeriğin bağ biçimini döner; bağ biçiminde değilse false.
func bagBicimini(icerik string) (bagBicimi, bool) {
	yol, parcalar, ok := bagParcala(icerik)
	if !ok {
		return 0, false
	}
	if yol != "" {
		if len(parcalar) > 2 {
			return 0, false
		}
		return bagTamYol, true
	}
	switch len(parcalar) {
	case 1:
		return bagYerelAd, true
	case 2:
		return bagNitelikli, true
	case 3:
		return bagUcParcali, true
	}
	return 0, false
}

// bagiCoz bir bağın çözülüp çözülmediğini söyler; boş dize ÇÖZÜLDÜ demektir.
//
// Dönen değer bir NEDENDİR, bir bayrak değil: "çözülmedi" bilgisi tek başına
// düzeltmeyi kolaylaştırmaz — arayanın öğrenmesi gereken şey, adın nerede
// arandığıdır.
func (a *atifTaramasi) bagiCoz(aday bagAdayi) string {
	paket := aday.dosya.paket
	yol, parcalar, ok := bagParcala(aday.icerik)
	if !ok {
		return "biçim tanınmadı"
	}
	if yol != "" {
		return a.paketteAtifAra(yol, parcalar)
	}

	switch len(parcalar) {
	case 1:
		ad := parcalar[0]
		switch {
		case a.yerelAdVar(paket, ad):
			return ""
		case paket.importlar[ad] != "":
			// [fmt] gibi paketin kendisine yapılan atıf.
			return ""
		case ad == paket.ad || ad+"_test" == paket.ad:
			// Paket kendi adını anabilir (go/doc da izin verir).
			return ""
		case a.testAdlari[ad]:
			return ""
		}
		return fmt.Sprintf("%s (paket %s) içinde böyle bir bildirim yok ve depoda böyle "+
			"bir test de yok.%s", paket.dizin, paket.ad, vurguIpucu(ad))

	case 2:
		// Sıra ÖNEMLİDİR: nitelikli ad önce PAKET olarak denenir, sonra yerel
		// tipin üyesi olarak. Go bu ayrımı büyük/küçük harfle yapar ([Tip.Üye]
		// büyük harfliyse alıcıdır), bu depo ise küçük harfli tip adları da
		// kullanır (uploadDTO.URL) ve harfe bakan bir kural onları kaçırırdı.
		if hedef, ok := paket.importlar[parcalar[0]]; ok && hedef != "" {
			return a.paketteAtifAra(hedef, parcalar[1:])
		}
		if a.yerelAdVar(paket, strings.Join(parcalar, ".")) ||
			a.yerelUyeVar(paket, parcalar[0], parcalar[1]) {
			return ""
		}
		if !a.yerelAdVar(paket, parcalar[0]) {
			return fmt.Sprintf("%q ne %s paketinin import ettiği bir paket ne de %s "+
				"içinde tanımlı bir tip", parcalar[0], paket.ad, paket.dizin)
		}
		return fmt.Sprintf("%s tipinin %s diye bir üyesi yok", parcalar[0], parcalar[1])

	case 3:
		hedef, ok := paket.importlar[parcalar[0]]
		if !ok || hedef == "" {
			return fmt.Sprintf("%s paketi %q adlı bir paket import etmiyor", paket.ad, parcalar[0])
		}
		return a.paketteAtifAra(hedef, parcalar[1:])
	}
	return "biçim tanınmadı"
}

// yerelAdVar bir adın paketin kendisinde bildirilip bildirilmediğini söyler.
//
// Harici test paketi (foo_test) ÜRETİM paketinin adlarına da bakar: o dosyalar
// test edilen paketin yanında yaşar ve godoc'ları onun dışa açık adlarını
// anar. Ters yön yoktur; üretim, testin adlarını göremez.
func (a *atifTaramasi) yerelAdVar(paket *atifPaketi, ad string) bool {
	if paket.adlar[ad] || (!strings.Contains(ad, ".") && paket.uyeler[ad]) {
		return true
	}
	if !strings.HasSuffix(paket.ad, "_test") {
		return false
	}
	uretim, ok := a.paketler[paket.dizin+"\x00"+strings.TrimSuffix(paket.ad, "_test")]
	if !ok {
		return false
	}
	return uretim.adlar[ad] || (!strings.Contains(ad, ".") && uretim.uyeler[ad])
}

// yerelUyeVar "Alıcı.Ad" ikilisini gömülü tipleri İZLEYEREK arar.
func (a *atifTaramasi) yerelUyeVar(paket *atifPaketi, alici, ad string) bool {
	return uyeAtfiVar(paket, alici, ad, 0)
}

// uyeAtfiVar bir tipin (ve gömdüklerinin) verilen üyeyi taşıyıp taşımadığını
// söyler.
//
// Derinlik sınırı, birbirini gömen iki tipin sonsuz inişe yol açmasını
// engeller; gerçek gömme zincirleri bu derinliğin çok altındadır.
func uyeAtfiVar(paket *atifPaketi, alici, ad string, derinlik int) bool {
	if derinlik > 4 {
		return false
	}
	if paket.adlar[alici+"."+ad] {
		return true
	}
	for _, gomulu := range paket.gomulu[alici] {
		if uyeAtfiVar(paket, gomulu, ad, derinlik+1) {
			return true
		}
	}
	return false
}

// atifHedefi bir import yolunun taranmış paketini ve deponun içinde olup
// olmadığını döner.
//
// Depo içi bir yol için nil hedef "böyle bir paket YOK" demektir; depo dışı bir
// yol için ise yalnızca "kaynağına bakılamadı" demektir (stdlib değilse üçüncü
// taraftır). İki durumu ayırt eden şey dönen ikinci değerdir; ayırt edilmezse
// var olmayan bir depo paketi, doğrulanamayan bir üçüncü taraf paketi gibi
// sessizce onaylanırdı.
func (a *atifTaramasi) atifHedefi(importYolu string) (hedef *atifPaketi, depoIci bool) {
	depoIci = importYolu == modulePath || strings.HasPrefix(importYolu, modulePath+"/")
	if !depoIci {
		return a.stdPaketi(importYolu), false
	}
	ad, ok := a.uretimAdi[importYolu]
	if !ok {
		return nil, true
	}
	dizin := strings.TrimPrefix(importYolu, modulePath+"/")
	return a.paketler[dizin+"\x00"+ad], true
}

// paketteAtifAra bir import yolundaki paketde adı arar.
//
// Üç sınıf paket vardır ve üçüne verilen söz FARKLIDIR:
//
//   - Depo paketleri: hem paket hem sembol doğrulanır.
//   - stdlib: GOROOT'taki kaynaktan doğrulanır. Bedava değildir ama ucuzdur ve
//     go test zaten bir Go kurulumunun içinde koşar.
//   - Üçüncü taraf: YALNIZCA paketin import edildiği doğrulanır, sembol
//     DOĞRULANMAZ. Doğrulamak modül önbelleğini çözmeyi gerektirirdi; kazanç
//     küçüktür (bu bağlar ancak bağımlılık yükseltmesinde çürür), maliyet ise
//     denetimi ağa ve modül düzenine bağlamaktır.
func (a *atifTaramasi) paketteAtifAra(importYolu string, parcalar []string) string {
	hedef, depoIci := a.atifHedefi(importYolu)
	if depoIci && hedef == nil {
		return "depoda böyle bir paket yok: " + importYolu
	}

	switch {
	case len(parcalar) == 0:
		return ""
	case hedef == nil:
		return "" // üçüncü taraf: sembol doğrulanmaz
	case len(parcalar) == 1:
		if hedef.adlar[parcalar[0]] {
			return ""
		}
		return fmt.Sprintf("%s paketinde %s diye bir dışa açık bildirim yok",
			importYolu, parcalar[0])
	default:
		if uyeAtfiVar(hedef, parcalar[0], parcalar[1], 0) {
			return ""
		}
		return fmt.Sprintf("%s paketinde %s.%s diye bir üye yok",
			importYolu, parcalar[0], parcalar[1])
	}
}

// stdPaketi bir stdlib paketini GOROOT kaynağından okur; stdlib değilse nil.
func (a *atifTaramasi) stdPaketi(importYolu string) *atifPaketi {
	if paket, ok := a.stdKovasi[importYolu]; ok {
		return paket
	}
	dizin := filepath.Join(build.Default.GOROOT, "src", filepath.FromSlash(importYolu))
	bilgi, err := os.Stat(dizin)
	if err != nil || !bilgi.IsDir() {
		a.stdKovasi[importYolu] = nil
		return nil
	}
	paket := &atifPaketi{
		dizin:     dizin,
		ad:        path.Base(importYolu),
		adlar:     map[string]bool{},
		uyeler:    map[string]bool{},
		gomulu:    map[string][]string{},
		importlar: map[string]string{},
	}
	girdiler, err := os.ReadDir(dizin)
	if err != nil {
		a.stdKovasi[importYolu] = nil
		return nil
	}
	for _, girdi := range girdiler {
		ad := girdi.Name()
		if girdi.IsDir() || !strings.HasSuffix(ad, ".go") || strings.HasSuffix(ad, "_test.go") {
			continue
		}
		agac, err := parser.ParseFile(a.fset, filepath.Join(dizin, ad), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		bildirimAdlariniTopla(agac, paket)
	}
	a.stdKovasi[importYolu] = paket
	return paket
}

// tumBagAdaylari taranan her yorumdaki bağ adaylarını döner.
func (a *atifTaramasi) tumBagAdaylari() []bagAdayi {
	var adaylar []bagAdayi
	for _, dosya := range a.dosyalar {
		for _, grup := range dosya.agac.Comments {
			satir := a.fset.Position(grup.Pos()).Line
			adaylar = append(adaylar, bagAdaylari(grup.Text(), dosya, satir)...)
		}
	}
	return adaylar
}

// TestGodocBaglariCozuluyor yorumlardaki her doc bağının GERÇEK bir bildirime
// çözüldüğünü doğrular.
//
// Çözülmeyen bir bağ derlemeyi kırmaz, lint'e takılmaz ve gözle de zor
// görülür: Go onu düz metin olarak basar, yani okuyan kişi "burada bir bağ
// vardı" bile diyemez. Tek belirtisi, o adı aramaya çıkan kişinin hiçbir şey
// bulamamasıdır.
func TestGodocBaglariCozuluyor(t *testing.T) {
	t.Parallel()

	tarama := belgeAtiflariniTara(t)
	for _, aday := range tarama.tumBagAdaylari() {
		if neden := tarama.bagiCoz(aday); neden != "" {
			t.Errorf("%s:%d: [%s] bağı çözülmüyor — %s.\n"+
				"Ayraç bir SÖZDÜR: okuyanı o adı aramaya yollar. Sembol adını "+
				"değiştirdiyse bağ da değişmeli; başka bir pakette yaşıyorsa ve bu "+
				"paket onu import etmiyorsa TAM YOL yazılmalı "+
				"([github.com/bdrtr/gobit/…/paket.Ad]); artık hiçbir yerde yoksa "+
				"atıf silinmeli. Ayracın kaldırılıp düz metne çevrilmesi de bir "+
				"cevaptır ama BİLİNÇLİ olmalıdır: düz metin bu denetimin dışındadır.",
				aday.dosya.yol, aday.satir, aday.icerik, neden)
		}
	}
}

// TestBagTarayicisiKorlesmemis bağ tarayıcısının HÂLÂ bağ gördüğünü doğrular.
//
// [TestGodocBaglariCozuluyor] hiçbir aday bulamadığında SESSİZCE geçer: bulgu
// üretmeyen bir tarayıcı ile ihlali olmayan bir depo, çıktı olarak birbirinin
// aynısıdır. Bu test ikisini ayırır ve ayrı bir test olmasının nedeni budur —
// aynı testin içinde olsalardı, çözülmeyen ilk bağın hatası körleşme
// mesajıyla karışırdı.
//
// Sayaç biçim başına tutulur çünkü tarayıcı genellikle TÜMDEN değil PARÇA
// PARÇA körleşir: ayraç okuması, nokta ayrıştırması ve import yolu biçimi ayrı
// kod yollarıdır ve biri bozulduğunda toplam sayı hâlâ binlerde kalır.
func TestBagTarayicisiKorlesmemis(t *testing.T) {
	t.Parallel()

	tarama := belgeAtiflariniTara(t)
	require.NotEmpty(t, tarama.dosyalar, "üretim köklerinde hiç Go dosyası bulunamadı")

	sayac := [bagBicimSayisi]int{}
	kokler := map[string]int{}
	for _, aday := range tarama.tumBagAdaylari() {
		sayac[aday.bicim]++
		kokler[strings.SplitN(aday.dosya.yol, "/", 2)[0]]++
	}

	for bicim, adet := range sayac {
		require.Positive(t, adet,
			"%s biçiminde TEK BİR doc bağı bile bulunamadı; tarayıcı bu sınıfta KÖR "+
				"kalmış olmalı.\nBağ çözümlemesinin o dalı artık hiçbir şeyi "+
				"denetlemiyor: sınıf gerçekten depodan kalktıysa hem bu iddia hem de "+
				"karşılık gelen çözümleme dalı silinmelidir; sessizce yeşil kalması, "+
				"o sınıfın hâlâ korunduğu izlenimini verir.",
			bagBicimAdlari[bicim])
	}

	for _, kok := range uretimKokleri {
		require.Positive(t, kokler[kok],
			"%s/ ağacında hiç doc bağı görülmedi; tarama o kökü hiç okumamış olabilir "+
				"(goFiles yürüyüşü ya da yorum ayrıştırması). Okunmayan bir ağaçtaki "+
				"her çürük atıf onaylanır.", kok)
	}

	// Olumlu kontroller. Çözümleme "bulamadım" ile "bakamadım" arasındaki farkı
	// sessizce yutabilir: bir paketin kaynağına hiç ulaşılamadığında denetim
	// sembolü DOĞRULAMAZ ve bağı onaylar (üçüncü taraf paketlerde verilen söz
	// budur). Aynı sessizlik depo ve stdlib paketlerinde bir ARIZA olurdu, bu
	// yüzden ikisinin de var olmayan bir sembolü REDDETTİĞİ ayrıca sınanır.
	require.NotEmpty(t, tarama.paketteAtifAra("testing", []string{"BoyleBirSembolYok"}),
		"stdlib paketinde olmayan bir sembol onaylandı; GOROOT kaynağı okunamamış "+
			"olmalı (build.Default.GOROOT boş ya da kaynak ağacı yok). O durumda her "+
			"stdlib bağı, sembolü hiç aranmadan geçer.")
	require.NotEmpty(t, tarama.paketteAtifAra(modulePath+"/internal/core/module", []string{"BoyleBirSembolYok"}),
		"depo paketinde olmayan bir sembol onaylandı; paket dizini kurulmamış olmalı. "+
			"O durumda depo içi her nitelikli bağ doğrulanmadan geçer.")
}

// bolumAtfiDeseni godoc içinde ANILAN bir belge bölümünü yakalar.
//
// Desen tırnak içindeki başlığın hemen ardından "bölüm" sözcüğünü arar
// ("… bölümündedir", "… bölümüne"). Tırnaksız anmalar (ör. "güven sınırı
// bölümü") KAPSAM DIŞIDIR ve olmak zorundadır: başlığın nerede bittiğini
// söyleyen bir sınır yoktur, yani ya cümlenin yarısı başlık sanılır ya da
// hiçbir şey bulunmaz.
var bolumAtfiDeseni = regexp.MustCompile(`"([^"]{3,80})"\s*bölüm`)

// TestGodocBolumAtiflariCozuluyor godoc'un adıyla andığı belge bölümünün
// GERÇEKTEN var olduğunu doğrular.
//
// Bu, bağ çürümesinin ikinci yüzüdür ve ölçülmüş bir örneği vardır: bir DTO
// alanının godoc'u, sınırının "paket belgesindeki şu bölümde" yazdığını
// söylüyordu; o bölüm bir yeniden yazımda adını değiştirmişti ve okuyan kişi
// paket belgesinde olmayan bir başlık arıyordu.
//
// Başlık AYNI PAKETTE aranır. Bir godoc başka bir paketin bölümüne yolluyorsa
// hangi paket olduğunu yazmalıdır; "paket belgesindeki" ifadesi ancak kendi
// paketi için kesindir.
func TestGodocBolumAtiflariCozuluyor(t *testing.T) {
	t.Parallel()

	tarama := belgeAtiflariniTara(t)

	// Başlık dizini: paket → başlık kümesi, ve depo geneli.
	paketBasliklari := map[*atifPaketi]map[string]bool{}
	depoBasliklari := map[string]string{}
	baslikSayisi := 0
	for _, dosya := range tarama.dosyalar {
		for _, grup := range dosya.agac.Comments {
			for _, satir := range strings.Split(grup.Text(), "\n") {
				baslik, ok := strings.CutPrefix(satir, "# ")
				if !ok {
					continue
				}
				baslik = strings.TrimSpace(baslik)
				baslikSayisi++
				if paketBasliklari[dosya.paket] == nil {
					paketBasliklari[dosya.paket] = map[string]bool{}
				}
				paketBasliklari[dosya.paket][baslik] = true
				if _, varsa := depoBasliklari[baslik]; !varsa {
					depoBasliklari[baslik] = dosya.yol
				}
			}
		}
	}

	atifSayisi := 0
	for _, dosya := range tarama.dosyalar {
		for _, grup := range dosya.agac.Comments {
			metin := strings.ReplaceAll(grup.Text(), "\n", " ")
			for _, eslesme := range bolumAtfiDeseni.FindAllStringSubmatch(metin, -1) {
				baslik := strings.TrimSpace(eslesme[1])
				atifSayisi++
				if paketBasliklari[dosya.paket][baslik] {
					continue
				}
				nerede := "hiçbir godoc'ta böyle bir başlık yok"
				if yol, varsa := depoBasliklari[baslik]; varsa {
					nerede = fmt.Sprintf("başlık %s içinde var ama BU pakette yok", yol)
				}
				t.Errorf("%s:%d: godoc %q bölümüne yolluyor ama %s (aranan paket: %s).\n"+
					"Adıyla anılan bir bölüm, adı değiştiğinde sessizce okunamaz hâle "+
					"gelir: okuyan kişi belgede olmayan bir başlık arar.",
					dosya.yol, tarama.fset.Position(grup.Pos()).Line, baslik, nerede, dosya.paket.dizin)
			}
		}
	}

	require.Positive(t, baslikSayisi,
		"hiçbir godoc'ta \"# Başlık\" satırı bulunamadı; başlık dizini KÖR kalmış "+
			"olmalı.\nBoş bir dizin, her bölüm atfını ihlal sayar ve bir yanlış "+
			"suçlama yığını üretir; yığının sebebi budur.")
	require.Positive(t, atifSayisi,
		"hiçbir godoc'ta tırnaklı bölüm atfı bulunamadı; desen artık depodaki "+
			"yazımla eşleşmiyor olabilir.\nDepo bu anlatım biçimini gerçekten "+
			"bıraktıysa bu denetim de bilinçli olarak kaldırılmalıdır; eşleşmeyen bir "+
			"desenle yeşil kalması, bölüm atıflarının hâlâ denetlendiği izlenimini "+
			"verir.")
}

// adrNumaraDeseni metinde "ADR 0004" biçimindeki atıfları yakalar.
var adrNumaraDeseni = regexp.MustCompile(`\bADR ?(\d{4})\b`)

// adrYolDeseni bir karar kaydına DOSYA YOLUYLA yapılan atıfları yakalar.
var adrYolDeseni = regexp.MustCompile(`docs/adr/(\d{4})-[a-z0-9-]+\.md`)

// TestADRAtiflariCozuluyor koddaki ve belgelerdeki her ADR atfının GERÇEK bir
// karar kaydına gittiğini doğrular.
//
// ADR'ler bu depoda kararın TEK gerekçesidir ve kod yorumları onlara numarayla
// yollar (ADR 0001 tek başına 174 yerde anılır). Numaranın kayması ya da bir
// kaydın yeniden numaralanması, o 174 atfın hepsini sessizce yanlış yere
// yollardı.
//
// Numaranın DOĞRU karara gittiği doğrulanamaz — "ADR 0004" yazıp 0006'yı
// kastetmek denetimin göremediği bir hatadır. Doğrulanan, numaranın karşılığı
// olan bir dosyanın VAR OLDUĞUDUR.
func TestADRAtiflariCozuluyor(t *testing.T) {
	t.Parallel()

	adrDizini := filepath.Join(repoRoot, "docs", "adr")
	girdiler, err := os.ReadDir(adrDizini)
	require.NoError(t, err, "ADR dizini okunamadı")

	kayitlar := map[string]string{}
	for _, girdi := range girdiler {
		ad := girdi.Name()
		if girdi.IsDir() || !strings.HasSuffix(ad, ".md") || len(ad) < 4 {
			continue
		}
		kayitlar[ad[:4]] = ad
	}
	require.NotEmpty(t, kayitlar,
		"docs/adr altında NNNN- ile başlayan hiçbir kayıt bulunamadı; ADR dizini KÖR "+
			"kalmış olmalı.\nBoş bir dizin, her ADR atfını ihlal sayar.")

	numaraAtfi, yolAtfi := 0, 0
	denetle := func(kaynak string, satirNo int, metin string) {
		for _, eslesme := range adrNumaraDeseni.FindAllStringSubmatch(metin, -1) {
			numaraAtfi++
			if _, varsa := kayitlar[eslesme[1]]; !varsa {
				t.Errorf("%s:%d: %q diye bir karar kaydı yok (docs/adr altında %s- ile "+
					"başlayan dosya bulunamadı).\nNumarayla yapılan bir atıf, kayıt "+
					"yeniden numaralandığında sessizce başka bir kararı gösterir.",
					kaynak, satirNo, eslesme[0], eslesme[1])
			}
		}
		for _, eslesme := range adrYolDeseni.FindAllStringSubmatch(metin, -1) {
			yolAtfi++
			if kayitlar[eslesme[1]] != filepath.Base(eslesme[0]) {
				t.Errorf("%s:%d: %q dosyası yok; %s numaralı kayıt bugün %q adını taşıyor.\n"+
					"Kaydın başlığı değiştiğinde dosya adı da değişir ve ona yol ile "+
					"yapılan her atıf 404 olur.",
					kaynak, satirNo, eslesme[0], eslesme[1], kayitlar[eslesme[1]])
			}
		}
	}

	tarama := belgeAtiflariniTara(t)
	for _, dosya := range tarama.dosyalar {
		for _, grup := range dosya.agac.Comments {
			denetle(dosya.yol, tarama.fset.Position(grup.Pos()).Line, grup.Text())
		}
	}
	for _, belge := range markdownBelgeleri(t) {
		for i, satir := range belge.satirlar {
			denetle(belge.yol, i+1, satir)
		}
	}

	require.Positive(t, numaraAtfi,
		"hiçbir yerde \"ADR NNNN\" biçiminde atıf bulunamadı; desen KÖR kalmış olmalı.\n"+
			"Depo kararlarını gerekçeleriyle anmayı bıraktıysa bu denetimin kapsamı "+
			"yeniden yazılmalıdır.")
	require.Positive(t, yolAtfi,
		"hiçbir yerde docs/adr/NNNN-… yolu bulunamadı; yol deseni KÖR kalmış olmalı "+
			"(kayıtlar başka bir dizine taşınmış ya da adlandırma değişmiş olabilir).")
}

// markdownBelgesi okunmuş bir markdown dosyasıdır.
type markdownBelgesi struct {
	yol      string
	satirlar []string
}

// markdownBelgeleri depodaki markdown dosyalarını okur.
func markdownBelgeleri(t *testing.T) []markdownBelgesi {
	t.Helper()

	var belgeler []markdownBelgesi
	err := filepath.WalkDir(repoRoot, func(yol string, girdi os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if girdi.IsDir() {
			if girdi.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(yol, ".md") {
			return nil
		}
		icerik, err := os.ReadFile(yol)
		if err != nil {
			return err
		}
		bagil, err := filepath.Rel(repoRoot, yol)
		if err != nil {
			return err
		}
		belgeler = append(belgeler, markdownBelgesi{
			yol:      filepath.ToSlash(bagil),
			satirlar: strings.Split(string(icerik), "\n"),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("markdown belgeleri taranamadı: %v", err)
	}
	require.NotEmpty(t, belgeler, "depoda hiç markdown dosyası bulunamadı")
	return belgeler
}

// yolAtfiDeseni yorumlarda geçen DEPO KÖKÜNDEN yazılmış yolları yakalar.
//
// Yalnızca kökten yazılmış yollar denetlenir. Göreli anmalar ("bkz. interop.go",
// "service/provider.go") KAPSAM DIŞIDIR ve bu ölçülmüş bir karardır: aynı ada
// sahip dosya on altı modülde birden vardır (her modülde bir interop.go), sqlc
// üretimi başlıklar sorgu dosyalarını kardeş bir dizinden anar ve üçüncü taraf
// dosya adları (transport/http_post.go) da aynı biçimdedir. Göreli adları
// çözmeye çalışan bir denetim ya bunların hepsini ihlal sayar ya da hepsini
// affeder; ikisi de denetimden beklenen şey değildir.
var yolAtfiDeseni = regexp.MustCompile(
	`(?:^|[^\w/.-])((?:cmd|internal|plugins|docs|config|deploy|migrations)/[A-Za-z0-9_][A-Za-z0-9_./-]*)`)

// yolAtifMuafiyeti bugün ÇÖZÜLMEYEN bir yol atfının gerekçesidir.
type yolAtifMuafiyeti struct {
	dosya   string
	yol     string
	gerekce string
}

// yolAtifMuafiyetleri kasıtlı olarak var olmayan yolları anan atıflardır.
//
// Muafiyetin bedeli tam olarak ölçülür: yol BİR GÜN var olduğunda test düşer
// ve muafiyetin kaldırılmasını ister. Borç ödendiğinde defterde kalmaz.
var yolAtifMuafiyetleri = []yolAtifMuafiyeti{
	{
		dosya: "internal/modules/tax/service/service.go",
		yol:   "internal/core/provider/tax.go",
		gerekce: "Vergi sağlayıcı sözleşmesi bilinçli olarak modülde yaşıyor; godoc, " +
			"ikinci bir gerçek sağlayıcı yazıldığında TAŞINACAĞI yeri adıyla söylüyor. " +
			"Atıf bir taşınma KARARIDIR, bugünkü bir konum iddiası değil.",
	},
	{
		dosya: "internal/modules/tax/service/taxprovider.go",
		yol:   "internal/core/provider/tax.go",
		gerekce: "Aynı taşınma kararının sözleşmenin tanımlandığı dosyadaki hâli; " +
			"gerekçesi service.go'daki muafiyetle aynıdır.",
	},
}

// TestYorumlardakiYolAtiflariCozuluyor yorumlarda anılan her depo yolunun
// GERÇEKTEN var olduğunu doğrular.
//
// Yol atıfları sembol atıflarından daha sessiz çürür: bir paket taşındığında
// derleyici import'ları düzeltir ama yorumdaki yolu kimse görmez.
//
// Yol önce depo kökünde, sonra atfın YAPILDIĞI dosyanın üst dizinlerinde
// aranır. İkinci kural bir üsluptan değil ölçümden gelir: bir modülün içinden
// yazılan migration atıfları modülün köküne GÖRELİDİR (aynı ada sahip bir
// dizin depo kökünde de vardır) ve yalnızca kökten arayan bir denetim onları
// haksız yere kırık sayardı.
//
// # Yol atfı ile sembol atfı ayrımı
//
// Yorumlarda "internal/core/http" gibi bir dizinin ardından bir sembol adı da
// yazılır. Ayrım kuraldan çıkar, listeden değil: son parça BÜYÜK harfle
// başlıyorsa o bir Go sembolüdür (dışa açık adlar büyük, dosya uzantıları
// küçüktür) ve denetim yalnızca PAKET dizinini doğrular. Sembolün kendisi
// ayraçla yazılmışsa zaten [TestGodocBaglariCozuluyor] denetler; ayraçsızsa
// düz metindir ve bu dosyanın başındaki kapsam kuralı gereği denetlenmez.
func TestYorumlardakiYolAtiflariCozuluyor(t *testing.T) {
	t.Parallel()

	tarama := belgeAtiflariniTara(t)
	kullanilan := make([]bool, len(yolAtifMuafiyetleri))
	gorulen := 0

	for _, dosya := range tarama.dosyalar {
		for _, grup := range dosya.agac.Comments {
			// Doc bağları önce ÇIKARILIR: [github.com/…/paket.Ad] biçimindeki
			// bir bağ yol gibi görünür ama dosya sistemine değil pakete gider
			// ve onu burada da denetlemek, aynı atfa iki farklı sözlükten
			// bakmak olurdu.
			metin := bagAyraclariniSil(grup.Text())
			for _, eslesme := range yolAtfiDeseni.FindAllStringSubmatch(metin, -1) {
				yol := yolAtfiniBudakla(eslesme[1])
				if yol == "" {
					continue
				}
				yol = yolAtfindanSembolAyikla(yol)
				gorulen++
				if yolAtfiCozuluyor(dosya.dizin, yol) {
					continue
				}
				if i := yolMuafiyetiniBul(dosya.yol, yol); i >= 0 {
					kullanilan[i] = true
					continue
				}
				t.Errorf("%s:%d: yorumda anılan %q yolu yok.\n"+
					"Yol depo kökünde ve dosyanın üst dizinlerinde arandı. Dosya "+
					"taşındıysa atıf da taşınmalı; henüz yazılmamış bir yeri "+
					"anlatıyorsa gerekçesi yolAtifMuafiyetleri'ne yazılmalıdır — "+
					"gerekçesiz bir 'ileride olacak' atfı, çürümüş bir atıftan "+
					"ayırt edilemez.",
					dosya.yol, tarama.fset.Position(grup.Pos()).Line, yol)
			}
		}
	}

	for i, muafiyet := range yolAtifMuafiyetleri {
		assert.True(t, kullanilan[i],
			"muafiyet BAYAT: %s içindeki %q atfı artık kırık değil (ya atıf silindi ya "+
				"da yol gerçekten oluştu).\nGerekçe: %s\nBorç ödendiyse muafiyet de "+
				"defterden düşmelidir; kalan bir muafiyet, bir sonraki kırık atfı "+
				"sessizce affeder.",
			muafiyet.dosya, muafiyet.yol, muafiyet.gerekce)
	}

	require.Positive(t, gorulen,
		"yorumlarda TEK BİR depo yolu atfı bile bulunamadı; desen KÖR kalmış olmalı.\n"+
			"Desen üst düzey dizin adlarına (cmd, internal, plugins, docs, config, "+
			"deploy, migrations) baştan bağlıdır; ağaç yeniden düzenlendiğinde hiçbir "+
			"yolu görmez ve taşınmış her dosya atfı sessizce geçer.")
}

// bagAyraclariniSil metindeki [ … ] bloklarını boşlukla değiştirir.
func bagAyraclariniSil(metin string) string {
	var b strings.Builder
	for i := 0; i < len(metin); i++ {
		if metin[i] != '[' {
			b.WriteByte(metin[i])
			continue
		}
		uzaklik := strings.IndexByte(metin[i+1:], ']')
		if uzaklik < 0 {
			b.WriteByte(metin[i])
			continue
		}
		b.WriteByte(' ')
		i += uzaklik + 1
	}
	return b.String()
}

// yolAtfiniBudakla bir yol atfının sonundaki cümle noktalamasını atar.
//
// Türkçe ek kesme işaretiyle yazılır ("openapi.go'da") ve yol orada biter;
// cümle sonu noktalaması da yola dâhil değildir. Budama olmasaydı doğru
// yazılmış her atıf kırık görünürdü.
func yolAtfiniBudakla(ham string) string {
	if kesme := strings.IndexByte(ham, '\''); kesme >= 0 {
		ham = ham[:kesme]
	}
	return strings.TrimRight(ham, ".,;:)\"")
}

// yolAtfindanSembolAyikla "paket/yolu.Sembol" biçimindeki bir atıftan sembolü
// atar.
//
// Ayrımın ölçütü BÜYÜK HARFTİR: Go'da dışa açık adlar büyük harfle başlar,
// dosya uzantıları ise küçüktür. Ölçüt olmasaydı "internal/core/http.WriteError"
// gibi doğru yazılmış her sembol atfı "böyle bir dosya yok" diye kırık
// görünürdü — ve tersine, uzantıyı sembol sanan bir kural "x.go" atıflarının
// dosya adını hiç denetlemezdi.
func yolAtfindanSembolAyikla(yol string) string {
	paketYolu, parcalar, ok := bagParcala(yol)
	if !ok || paketYolu == "" || len(parcalar) == 0 {
		return yol
	}
	ilk, _ := utf8.DecodeRuneInString(parcalar[0])
	if !unicode.IsUpper(ilk) {
		return yol
	}
	return paketYolu
}

// yolAtfiCozuluyor yolu depo kökünde ve atfın yapıldığı dizinin üstlerinde
// arar.
func yolAtfiCozuluyor(atfinDizini, yol string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(yol))); err == nil {
		return true
	}
	for dizin := atfinDizini; dizin != "." && dizin != "/" && dizin != ""; dizin = path.Dir(dizin) {
		aday := filepath.Join(repoRoot, filepath.FromSlash(dizin), filepath.FromSlash(yol))
		if _, err := os.Stat(aday); err == nil {
			return true
		}
	}
	return false
}

// yolMuafiyetiniBul verilen atfa uyan muafiyetin indeksini döner, yoksa -1.
func yolMuafiyetiniBul(dosya, yol string) int {
	return slices.IndexFunc(yolAtifMuafiyetleri, func(m yolAtifMuafiyeti) bool {
		return m.dosya == dosya && m.yol == yol
	})
}

// markdownAtifSinifi bir markdown atfının SÖZDİZİM sınıfıdır.
//
// Sınıf, körleşme denetiminin ölçüsüdür: her sınıf ayrı bir kod yolundan geçer
// (yol deseni, sembol ayıklaması, niteleyici tablosu, üye dizini) ve biri
// bozulduğunda toplam sayı hâlâ yüzlerde kalır. Sınıf başına sayaç, o kaybı
// görünür kılar.
type markdownAtifSinifi int

const (
	// mdYol depo kökünden yazılmış bir dizin ya da dosya yoludur.
	mdYol markdownAtifSinifi = iota
	// mdYolluSembol paket YOLU + sembol taşır: internal/core/http.Scoped.
	mdYolluSembol
	// mdNitelikliSembol paket ADIYLA niteler: corehttp.PrincipalKey.
	mdNitelikliSembol
	// mdUyeSembol alıcı + üye ikilisidir: Config.FileRoot.
	mdUyeSembol
	mdSinifSayisi
)

// mdSinifAdlari hata mesajlarında sınıfın adıdır.
var mdSinifAdlari = [mdSinifSayisi]string{
	mdYol:             "depo yolu (internal/…)",
	mdYolluSembol:     "yollu sembol (internal/…/paket.Ad)",
	mdNitelikliSembol: "nitelikli sembol (paket.Ad)",
	mdUyeSembol:       "üye ikilisi (Tip.Üye)",
}

// markdownAtfi bir belgede geçen, çözülmesi beklenen tek bir atıftır.
type markdownAtfi struct {
	belge string
	satir int
	ham   string
	sinif markdownAtifSinifi
	// yol mdYol sınıfında depo köküne göre dosya/dizin yoludur.
	yol string
	// importYolu sembol sınıflarında adın arandığı pakettir.
	importYolu string
	// parcalar sembolün nokta ile ayrılmış parçalarıdır (Ad ya da Tip.Üye).
	parcalar []string
}

// mdNitelikliDeseni ters tırnak içindeki "paket.Ad" atıflarını yakalar.
//
// Desen üç şeyi ZORLAR ve üçü de yanlış pozitifi eleyen kuralın kendisidir:
//
//  1. Niteleyici küçük harfle başlar. Go'da paket adları küçüktür; büyük harfle
//     başlayan bir niteleyici TİP adıdır ("Principal.Kind") ve o ayrı bir sınıfa,
//     [mdUyeDeseni]'ne aittir — orada paket değil ALICI aranır.
//  2. Sembol BÜYÜK harfle başlar. Paket dışından anılabilen tek ad dışa açık
//     olandır; bu Go'nun kendi kuralıdır ve bu depoda tam olarak ihtiyaç duyulan
//     ayrımı verir: "product.created" bir olay adı, "cart.interop" bir container
//     kaydı, "region.currency_code" bir sütundur — hiçbiri sembol değildir.
//  3. Atıf ters tırnağın TAMAMINI kaplar. "eventbus.Publish/Subscribe" ya da
//     "db.Pool.Pool()" gibi bir kabuk değil bir ANLATIM olan yazımlar elenir.
var mdNitelikliDeseni = regexp.MustCompile(`^([a-z][a-z0-9]*)\.([A-Z][A-Za-z0-9_]*)(?:\.([A-Za-z_][A-Za-z0-9_]*))?$`)

// mdUyeDeseni ters tırnak içindeki "Tip.Üye" atıflarını yakalar.
//
// Alıcı adı BÜYÜK harfle başlar ve en az bir küçük harf taşır. İkinci koşul
// dosya adlarını eler ("CHANGELOG.md", "README.md" tam olarak bu şekildedir);
// Go tip adları CamelCase'tir, tümü büyük harfli bir belirteç bu depoda tip
// değil dosya ya da kısaltmadır.
var mdUyeDeseni = regexp.MustCompile(`^([A-Z][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)$`)

// mdSatirIciKod bir satırdaki ters tırnaklı parçaları yakalar.
var mdSatirIciKod = regexp.MustCompile("`([^`]+)`")

// mdSecenekBasligiSozcukleri bir ADR bölümünü "seçenek tartışması" yapan
// başlık sözcükleridir.
//
// Sözcükler bir muafiyet listesi DEĞİLDİR: hangi yolun affedileceğini değil,
// belgenin hangi BÖLÜMÜNÜN bugünkü depo hakkında iddia taşımadığını söylerler.
// Bkz. [markdownAtiflari].
var mdSecenekBasligiSozcukleri = []string{"seçenek", "değerlendirme"}

// markdownAtiflari tek bir belgedeki atıf adaylarını çıkarır.
//
// # Kapsam KURALLA çizilir, listeyle değil
//
// Dört karar, dördü de ölçümle verildi:
//
//  1. Kod blokları (üç ters tırnak) KAPSAM İÇİNDEDİR. Ölçüm: bloklardaki her
//     depo yolu bugün çözülüyor ve bloklar en çok okunan haritayı taşıyor
//     (README'nin dizin ağacı, plan belgesinin dosya başlıkları). Bir atfın
//     doğruluğu yazıldığı yerin dizgisine bağlı olsaydı, ağaç taşındığında
//     sessizce yanlışa dönen bir harita elde ederdik.
//  2. Ters tırnak içi ile düz metin AYNIDIR — yol atıfları için. Yol deseni
//     kendi çıpasını taşır (üst düzey dizin adı + eğik çizgi) ve bu şekil Türkçe
//     düz metinde kazara oluşmaz. Ölçüm: README yollarının bir kısmı tırnaksız
//     yazılmıştır (bağ hedefleri, cümle içi anmalar) ve tırnağı şart koşan bir
//     kural onları kaçırırdı.
//  3. SEMBOL atıfları ("paket.Ad" ve "Tip.Üye") yalnızca ters tırnak içinde
//     aranır. Bunun gerekçesi (2)'nin tersidir: noktalı şekil kendi çıpasını
//     TAŞIMAZ — cümle sonu noktalaması, sürüm numaraları ve tablo hücreleri
//     aynı şekli üretir. Burada ters tırnak, yazarın "bu bir addır" demesidir.
//  4. ADR'lerin seçenek bölümleri KAPSAM DIŞIDIR. Kural yolun kendisine değil
//     belgenin YAPISINA bakar: bir ADR'nin seçenek bölümü, tanımı gereği
//     KURULMAMIŞ bir dünyayı anlatır. Ölçülmüş örneği ADR 0001'dedir ("nötr bir
//     paket" olarak anılan, hiç yazılmamış bir sözleşme paketi). Muafiyet
//     yazmak yerine kural bulundu çünkü muafiyet her yeni ADR'de yeniden
//     yazılırdı; bölümün rolü ise ADR biçiminin bir parçasıdır.
//
// # Bu ayıklamanın NE GARANTİ ETMEDİĞİ
//
//   - Göreli anmalar ("core/link", "cart/api/store.go") görülmez. Gerekçe
//     [yolAtfiDeseni] ile aynıdır ve aynı ölçüme dayanır: aynı ada sahip dosya
//     on altı modülde birden vardır.
//   - TEK PARÇALI çıplak adlar ("salesChannelVisibleTemplate",
//     "ModuleRegistry") görülmez. Bkz. [TestBelgelerdekiAtiflarCozuluyor].
//   - Üçüncü taraf paketlerin sembolleri doğrulanmaz (bkz. [paketteAtifAra]).
func markdownAtiflari(belge markdownBelgesi, niteleyiciler map[string]string) []markdownAtfi {
	var atiflar []markdownAtfi
	bolum := ""
	kodBlogu := false

	for i, satir := range belge.satirlar {
		if strings.HasPrefix(strings.TrimSpace(satir), "```") {
			kodBlogu = !kodBlogu
			continue
		}
		if !kodBlogu {
			if baslik, ok := strings.CutPrefix(satir, "## "); ok {
				bolum = strings.TrimSpace(baslik)
			} else if strings.HasPrefix(satir, "# ") {
				bolum = ""
			}
		}
		if adrSecenekBolumu(belge.yol, bolum) {
			continue
		}

		for _, eslesme := range yolAtfiDeseni.FindAllStringSubmatch(satir, -1) {
			ham := yolAtfiniBudakla(eslesme[1])
			if ham == "" {
				continue
			}
			atif := markdownAtfi{belge: belge.yol, satir: i + 1, ham: ham}
			if paketYolu, parcalar := markdownYolluSembol(ham); paketYolu != "" {
				atif.sinif = mdYolluSembol
				atif.importYolu = modulePath + "/" + paketYolu
				atif.parcalar = parcalar
			} else {
				atif.sinif = mdYol
				atif.yol = ham
			}
			atiflar = append(atiflar, atif)
		}

		for _, eslesme := range mdSatirIciKod.FindAllStringSubmatch(satir, -1) {
			uye := mdUyeDeseni.FindStringSubmatch(eslesme[1])
			if len(uye) == 3 && strings.ToUpper(uye[1]) != uye[1] {
				atiflar = append(atiflar, markdownAtfi{
					belge:    belge.yol,
					satir:    i + 1,
					ham:      eslesme[1],
					sinif:    mdUyeSembol,
					parcalar: uye[1:],
				})
				continue
			}
			parcalar := mdNitelikliDeseni.FindStringSubmatch(eslesme[1])
			if parcalar == nil {
				continue
			}
			hedef, ok := niteleyiciler[parcalar[1]]
			if !ok {
				continue
			}
			ad := []string{parcalar[2]}
			if parcalar[3] != "" {
				ad = append(ad, parcalar[3])
			}
			atiflar = append(atiflar, markdownAtfi{
				belge:      belge.yol,
				satir:      i + 1,
				ham:        eslesme[1],
				sinif:      mdNitelikliSembol,
				importYolu: hedef,
				parcalar:   ad,
			})
		}
	}
	return atiflar
}

// adrSecenekBolumu bir bölümün, ADR'nin seçenek tartışması olup olmadığını
// söyler.
//
// Kural yalnızca docs/adr altında geçerlidir: seçenek bölümü ADR BİÇİMİNİN bir
// parçasıdır. README ya da CHANGELOG'da aynı başlık bugünkü depo hakkında bir
// iddia olurdu ve affedilmesi için bir sebep yoktur.
func adrSecenekBolumu(belgeYolu, bolum string) bool {
	if !strings.HasPrefix(belgeYolu, "docs/adr/") || bolum == "" {
		return false
	}
	kucuk := strings.ToLower(bolum)
	return slices.ContainsFunc(mdSecenekBasligiSozcukleri, func(s string) bool {
		return strings.Contains(kucuk, s)
	})
}

// markdownYolluSembol "paket/yolu.Sembol" atfını paket yolu ve sembol
// parçalarına böler; atıf bu biçimde değilse boş paket yolu döner.
//
// Ayrımın ölçütü [yolAtfindanSembolAyikla] ile AYNIDIR (son parça büyük harfle
// başlıyorsa semboldür) ama sonucu farklıdır: yorumlarda sembol ATILIR ve
// yalnızca dizin doğrulanır, markdown'da ise sembol de aranır. Fark bilinçlidir
// — yorumdaki bir sembol ayraçla yazıldığında zaten
// [TestGodocBaglariCozuluyor] tarafından denetlenir, markdown'da ise ayraç
// yoktur ve denetlenecek başka bir yer kalmaz.
func markdownYolluSembol(atif string) (paketYolu string, parcalar []string) {
	yol, kuyruk, ok := bagParcala(atif)
	if !ok || yol == "" || len(kuyruk) == 0 {
		return "", nil
	}
	ilk, _ := utf8.DecodeRuneInString(kuyruk[0])
	if !unicode.IsUpper(ilk) {
		return "", nil
	}
	return yol, kuyruk
}

// markdownNiteleyicileri paket adından import yoluna giden depo geneli tabloyu
// kurar.
//
// # Tablo neden KAYNAKTAN çıkarılıyor
//
// Bir belge "corehttp.Principal" yazdığında "corehttp"nin hangi paket olduğunu
// yalnızca deponun kendi import'ları bilir; elle yazılmış bir eşleme, bir paket
// taşındığında güncellenmeyecek ikinci bir gerçek olurdu.
//
// # BELİRSİZ niteleyiciler tablodan DÜŞÜLÜR
//
// İki kural eliyor:
//
//   - Aynı ad iki farklı yola bağlanıyorsa ("api", "service", "models",
//     "repository", "errors") niteleyici hiçbir paketi ADRESLEMİYOR demektir.
//   - Ad bir MODÜL adıysa düşülür. Bu deponun belgelerinde modül adı Go
//     paketini değil MODÜLÜ anar ve modül api/service/repository üçlüsüne
//     yayılır: "order.CreateOrder" gerçekte order/service içindedir, modülün
//     kök paketinde değil. Kural olmasaydı doğru yazılmış her modül anması
//     kırık görünürdü.
func markdownNiteleyicileri(t *testing.T, tarama *atifTaramasi) map[string]string {
	t.Helper()

	tablo := map[string]string{}
	for _, paket := range tarama.paketler {
		for yerel, yol := range paket.importlar {
			eski, varsa := tablo[yerel]
			if yol == "" || (varsa && eski != yol) {
				tablo[yerel] = ""
				continue
			}
			tablo[yerel] = yol
		}
	}
	for _, mod := range modulNames(t) {
		delete(tablo, mod)
	}
	for ad, yol := range tablo {
		if yol == "" {
			delete(tablo, ad)
		}
	}

	require.NotEmpty(t, tablo,
		"depo import'larından TEK BİR niteleyici bile çıkarılamadı; tablo KÖR kalmış "+
			"olmalı.\nBoş bir tabloyla nitelikli sembol atıflarının hepsi sessizce "+
			"atlanır ve denetim o sınıfta hiçbir şey söylemez.")
	return tablo
}

// markdownSembolCoz markdown'daki bir sembol atfını çözer; boş dize ÇÖZÜLDÜ
// demektir.
//
// [paketteAtifAra]'dan tek farkı, tek parçalı bir adın paketin ÜYE adlarında da
// aranmasıdır. Genişletme ölçülmüştür: belgeler bir metodu paketiyle anar
// ("config.Validate"), alıcı tipini yazmaz — Go bunu tanımaz ama okuyan kişi
// için atıf çözülür. Bedeli, bu dosyanın başındaki "garanti etmez" listesindeki
// alıcısız üye kuralının markdown'a da yayılmasıdır: aynı adı taşıyan başka bir
// tip varsa silinmiş bir üyeye yapılan atıf sessizce geçer.
func (a *atifTaramasi) markdownSembolCoz(importYolu string, parcalar []string) string {
	neden := a.paketteAtifAra(importYolu, parcalar)
	if neden == "" || len(parcalar) != 1 {
		return neden
	}
	if hedef, _ := a.atifHedefi(importYolu); hedef != nil && hedef.uyeler[parcalar[0]] {
		return ""
	}
	return neden
}

// markdownAtfiniCoz bir atfın çözülüp çözülmediğini söyler; boş dize ÇÖZÜLDÜ
// demektir.
func (a *atifTaramasi) markdownAtfiniCoz(atif markdownAtfi) string {
	switch atif.sinif {
	case mdYol:
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(atif.yol))); err == nil {
			return ""
		}
		return "depo kökünde böyle bir dosya ya da dizin yok"
	case mdUyeSembol:
		if a.uyeCiftiVar(atif.parcalar[0], atif.parcalar[1]) {
			return ""
		}
		return fmt.Sprintf("depoda %s tipinin %s diye bir üyesi yok",
			atif.parcalar[0], atif.parcalar[1])
	default:
		return a.markdownSembolCoz(atif.importYolu, atif.parcalar)
	}
}

// uyeCiftiVar "Tip.Üye" ikilisini DEPO GENELİNDE arar.
//
// Arama paketsizdir çünkü atıf da paketsizdir; bu, ikilinin niteliksiz
// yazılabilmesinin bedelidir ve bu dosyanın başındaki "garanti etmez"
// listesindeki alıcısız üye kuralının markdown'daki karşılığıdır: aynı adı
// taşıyan başka bir tip varsa, silinmiş bir üyeye yapılan atıf sessizce geçer.
func (a *atifTaramasi) uyeCiftiVar(tip, uye string) bool {
	for _, paket := range a.paketler {
		if uyeAtfiVar(paket, tip, uye, 0) {
			return true
		}
	}
	return false
}

// TestBelgelerdekiAtiflarCozuluyor markdown belgelerindeki her yol ve sembol
// atfının GERÇEK bir dosyaya, dizine ya da bildirime çözüldüğünü doğrular.
//
// # Neden markdown ayrı bir denetim
//
// Go yorumlarındaki atıflar bu dosyanın öteki denetimlerine takılır; markdown
// TAKILMIYORDU ve bu bir ÖLÇÜM ile biliniyordu: bağımsız bir doğrulama bir
// ADR'de hem sembolü hem yolu bozdu, internal/arch yeşil kaldı. Boşluk tam da
// [TestBelgelerdeSatirNumarasiAtfiYok] satır numarasını yasakladığı ve mevcut
// atıfları sembole çevirdiği turda önemliydi: belgeler denetlenmeyen bir biçime
// yönlendiriliyordu.
//
// Belgelerde bedel yorumlardakinden BÜYÜKTÜR. ADR'ler uzun ömürlü karar
// kayıtlarıdır: kimse onları derlemez, kimse yeniden okumaz, ve kanıt blokları
// tam da çürüyen yerdir. Deponun kendi provası var — ADR 0009'un dört satır
// numarası atfının dördü de kaymıştı.
//
// # TEK PARÇALI çıplak adlar neden kapsam dışı
//
// "salesChannelVisibleTemplate" ya da "ModuleRegistry" gibi tek parçalı bir ad
// kapsam DIŞIDIR ve bu bir eksiklik değil, sınırın dürüst çizilmesidir: çıplak
// bir ad hangi pakette aranacağını SÖYLEMEZ, yani denetim hangi göğe bakacağını
// bilemez. Ölçüm bunu doğruluyor — depodaki markdown'da ters tırnak içinde
// yazılmış yüzlerce tek parçalı ad var ve aralarında golang-migrate'in
// "SetVersion"ı, net/http'nin "ServeContent"ı, gqlgen'in "SetParserTokenLimit"i
// gibi ÜÇÜNCÜ TARAF adları duruyor. (Kesin sayı YAZILMIYOR: "tek parçalı aday"
// tanımı ters tırnak, büyük harf ve dışa açıklık ölçütlerine göre değişiyor ve
// yeniden üretilemeyen bir sayı, doğrulanamayan bir iddiadır.) Depo kaynağında aranan bir kural onları kırık ilan ederdi; aramayan
// bir kural ise yalnızca "bu ad depoda bir yerde geçiyor mu" derdi ve o soru,
// adın hangi pakette olduğunu doğrulamadığı için bir atfı DEĞİL bir grep'i
// onaylar. Aynı gerekçe [paketteAtifAra]'nın üçüncü taraf sembollerini
// doğrulamama kararının ta kendisidir; orada paket biliniyor ve denetim
// susuyor, burada paket hiç bilinmiyor.
//
// "Tip.Üye" İKİLİSİ ise kapsam İÇİNDEDİR (bkz. [mdUyeSembol]) ve ayrımın
// dayanağı ölçümdür, sezgi değil: ikili, tek parçalı adın taşımadığı bir
// bağlamı taşır (alıcı tipi) ve depodaki 18 anmanın 18'i de bu deponun
// tipleridir — üçüncü taraf bir ikili yoktur. Bir gün olursa çıkış yolu
// açıktır ve daha iyi bir yazımdır: ikiliyi paketiyle nitelemek
// ("http.Server.ReadTimeout"), ki o biçim [paketteAtifAra] üzerinden çözülür.
//
// Kapsam dışı kalmanın bedeli ölçüldü ve kabul edildi: bu turda tek parçalı
// yazıldığı için yakalanamayan iki çürük atıf vardı ("ModuleRegistry" ve
// "regionCurrency"); ikisi de denetlenebilir biçime çevrildi, yani bugünden
// sonra kapsam İÇİNDELER. Çürük bir atfı düzeltmenin doğru yolu denetimi
// genişletmek değil, atfı denetlenebilir biçimde yazmaktır.
func TestBelgelerdekiAtiflarCozuluyor(t *testing.T) {
	t.Parallel()

	tarama := belgeAtiflariniTara(t)
	niteleyiciler := markdownNiteleyicileri(t, tarama)

	for _, belge := range markdownBelgeleri(t) {
		for _, atif := range markdownAtiflari(belge, niteleyiciler) {
			neden := tarama.markdownAtfiniCoz(atif)
			if neden == "" {
				continue
			}
			t.Errorf("%s:%d: %q atfı çözülmüyor — %s.\n"+
				"Belgedeki bir atıf okuyanı ARAMAYA yollar; aranan şey yoksa okuyan "+
				"bunu ancak depo genelinde grep ettikten sonra öğrenir. Ad ya da yol "+
				"değiştiyse atıf da değişmeli; anlatılan şey hiç kurulmadıysa (bir "+
				"ADR'nin reddettiği seçenek gibi) o anlatım belgenin SEÇENEK bölümüne "+
				"aittir.",
				atif.belge, atif.satir, atif.ham, neden)
		}
	}
}

// markdownCozucuOrnegi çözücünün HER sınıfta gerçekten "çözülmedi" diyebildiğini
// sınayan, BİLEREK BOZUK atıflardan oluşan örnektir.
//
// # Neden tarayıcı örneği yetmiyor
//
// [markdownTarayiciOrnegi] tarayıcıyı korur: hangi yazımın atıf SAYILDIĞINI
// sabitler. Ama tarayıcı sağlamken çözücü körleşebilir ve o körleşme SESSİZDİR:
// aday sayısı düşmez, sayaç mutlu kalır, her atıf "çözüldü" sayılır. Ölçüldü —
// markdownAtfiniCoz'un yol dalını "her zaman çözüldü" yapmak, denetlenen 223
// atfın 142'sini (dörtte üçe yakınını) sessizce öldürüyor ve arch paketinin
// TAMAMI yeşil kalıyordu.
//
// Bu örnek o boşluğu kapatır: dört sınıfın dördünde de var olmayan bir hedefe
// atıf yapar ve DÖRDÜNÜN DE bildirildiğini şart koşar. Çözücü hangi sınıfta
// körleşirse o satır eksilir.
var markdownCozucuOrnegi = markdownBelgesi{
	yol: "docs/adr/9997-cozucu-ornegi.md",
	satirlar: []string{
		"# ADR 9997 — çözücü örneği",
		"",
		"## Bağlam",
		"",
		"Yol: `internal/core/boyle-bir-paket-yok`.",
		"Yollu sembol: `internal/core/http.BoyleBirSembolYok`.",
		"Nitelikli sembol: `corehttp.BoyleBirSembolYok`.",
		"Üye ikilisi: `Config.BoyleBirUyeYok`.",
	},
}

// TestMarkdownCozucusuKorlesmemis çözücünün dört sınıfta da ısırdığını doğrular.
//
// Olumlu kontroldür: denetimin BULABİLDİĞİNİ sınar, bulmadığını değil. Bir
// sınıfın çözümü "her zaman tamam" hâline gelirse o satır listeden eksilir ve
// bu test düşer — [TestBelgelerdekiAtiflarCozuluyor] ise sessizce yeşil kalırdı.
func TestMarkdownCozucusuKorlesmemis(t *testing.T) {
	t.Parallel()

	tarama := belgeAtiflariniTara(t)
	niteleyiciler := markdownNiteleyicileri(t, tarama)

	atiflar := markdownAtiflari(markdownCozucuOrnegi, niteleyiciler)
	require.Len(t, atiflar, 4,
		"çözücü örneğinin dört sınıfı da tarayıcıdan geçmeli; geçmiyorsa sınanan "+
			"şey çözücü değil tarayıcıdır (bkz. markdownTarayiciOrnegi)")

	var bildirilen []string
	for _, atif := range atiflar {
		if neden := tarama.markdownAtfiniCoz(atif); neden != "" {
			bildirilen = append(bildirilen, mdSinifAdlari[atif.sinif])
		}
	}

	require.ElementsMatch(t,
		[]string{
			mdSinifAdlari[mdYol],
			mdSinifAdlari[mdYolluSembol],
			mdSinifAdlari[mdNitelikliSembol],
			mdSinifAdlari[mdUyeSembol],
		},
		bildirilen,
		"çözücü, var olmayan bir hedefe yapılan atfı DÖRT sınIFTA da bildirmeli.\n"+
			"Eksik kalan sınıfın çözümü körleşmiş demektir: aday sayısı düşmez, "+
			"sayaç mutlu kalır ve o sınıftaki her atıf sessizce ÇÖZÜLDÜ sayılır.")
}

// markdownTarayiciOrnegi tarayıcının HER kuralını tek bir belgede sınayan
// örnektir.
//
// Örnek bir fikstür değil, kuralların YÜRÜTÜLEBİLİR yazımıdır: hangi yazımın
// atıf sayıldığı ve hangisinin sayılmadığı burada koddan okunur. Kurallar
// gevşerse örnek düşer.
var markdownTarayiciOrnegi = markdownBelgesi{
	yol: "docs/adr/9999-tarayici-ornegi.md",
	satirlar: []string{
		"# ADR 9999 — tarayıcı örneği",
		"",
		"## Bağlam",
		"",
		"Ters tırnaklı yol: `internal/core/db/migrate.go`.",
		"Düz metin yol: internal/arch içinde yaşar.",
		"Yollu sembol: `internal/core/http.Scoped`.",
		"Nitelikli sembol: `corehttp.Principal`.",
		"",
		"```",
		"internal/core/config/config.go",
		"```",
		"",
		"Üye ikilisi: `Principal.Kind`.",
		"",
		"Olay adı `product.created`, container kaydı `cart.interop`, sütun",
		"`region.currency_code` ve dosya adı `CHANGELOG.md` sembol DEĞİLDİR.",
		"Modül adıyla anma `order.CreateOrder` da kapsam dışıdır.",
		"Göreli anma `core/link` ve kabuk yazımı `db.Pool.Pool()` de öyle.",
		"",
		"## Reddedilen seçenekler",
		"",
		"`internal/contracts/product` gibi nötr bir paket ve `db.Conn` arayüzü.",
	},
}

// TestMarkdownAtifTarayicisiKorlesmemis markdown tarayıcısının HÂLÂ atıf
// gördüğünü ve gördüklerini DOĞRU sınıflandırdığını doğrular.
//
// [TestBelgelerdekiAtiflarCozuluyor] hiçbir aday bulamadığında SESSİZCE geçer:
// deseni bozulmuş bir tarayıcı ile çürüksüz bir depo, çıktı olarak birbirinin
// aynısıdır. Vakumda yeşil kalan bir denetim, olmayan bir denetimden kötüdür.
func TestMarkdownAtifTarayicisiKorlesmemis(t *testing.T) {
	t.Parallel()

	tarama := belgeAtiflariniTara(t)
	niteleyiciler := markdownNiteleyicileri(t, tarama)

	// 1. Kurallar: örnek belgeden ne çıktığı TAM olarak sınanır.
	var ornek []string
	for _, atif := range markdownAtiflari(markdownTarayiciOrnegi, niteleyiciler) {
		ornek = append(ornek, fmt.Sprintf("%s|%s", mdSinifAdlari[atif.sinif], atif.ham))
	}
	require.Equal(t, []string{
		mdSinifAdlari[mdYol] + "|internal/core/db/migrate.go",
		mdSinifAdlari[mdYol] + "|internal/arch",
		mdSinifAdlari[mdYolluSembol] + "|internal/core/http.Scoped",
		mdSinifAdlari[mdNitelikliSembol] + "|corehttp.Principal",
		mdSinifAdlari[mdYol] + "|internal/core/config/config.go",
		mdSinifAdlari[mdUyeSembol] + "|Principal.Kind",
	}, ornek,
		"tarayıcı örnek belgeyi beklenenden farklı okudu.\nÖrnek, kapsam "+
			"kurallarının yürütülebilir yazımıdır: fazladan bir satır YANLIŞ POZİTİF "+
			"kuralının (olay adı, container kaydı, sütun, dosya adı, modül anması, "+
			"göreli yol, seçenek bölümü) gevşediğini; eksik bir satır ise tarayıcının "+
			"o sınıfta KÖRLEŞTİĞİNİ söyler.")

	// 2. Gerçek belgeler: her sınıf depoda hâlâ görülüyor mu?
	sayac := [mdSinifSayisi]int{}
	secenekBolumu := 0
	for _, belge := range markdownBelgeleri(t) {
		for _, atif := range markdownAtiflari(belge, niteleyiciler) {
			sayac[atif.sinif]++
		}
		for _, satir := range belge.satirlar {
			if baslik, ok := strings.CutPrefix(satir, "## "); ok &&
				adrSecenekBolumu(belge.yol, strings.TrimSpace(baslik)) {
				secenekBolumu++
			}
		}
	}
	for sinif, adet := range sayac {
		require.Positive(t, adet,
			"belgelerde %s sınıfından TEK BİR atıf bile bulunamadı; tarayıcı bu sınıfta "+
				"KÖR kalmış olmalı.\nSınıf gerçekten belgelerden kalktıysa hem bu iddia "+
				"hem de karşılık gelen ayıklama dalı silinmelidir; sessizce yeşil kalması, "+
				"o sınıfın hâlâ denetlendiği izlenimini verir.",
			mdSinifAdlari[sinif])
	}
	require.Positive(t, secenekBolumu,
		"hiçbir ADR'de seçenek bölümü bulunamadı; varsayımsal yolları ayıran kural KÖR "+
			"kalmış olmalı.\nBaşlıklar değiştiyse mdSecenekBasligiSozcukleri de "+
			"değişmelidir: eşleşmeyen bir kural, reddedilmiş seçeneklerin yollarını "+
			"gerçek atıf sanıp bir yanlış suçlama yığını üretir.")

	// 3. Olumlu kontroller: çözümleme "bulamadım" ile "bakamadım" arasındaki
	// farkı sessizce yutabilir. Var olmayan bir sembolün REDDEDİLDİĞİ hem depo
	// hem stdlib paketinde ayrıca sınanır.
	require.NotEmpty(t, tarama.markdownSembolCoz(modulePath+"/internal/core/http", []string{"BoyleBirSembolYok"}),
		"depo paketinde olmayan bir sembol onaylandı; üye genişletmesi her adı kabul "+
			"ediyor olmalı. O durumda markdown'daki her nitelikli atıf doğrulanmadan geçer.")
	require.NotEmpty(t, tarama.markdownSembolCoz("net/http", []string{"BoyleBirSembolYok"}),
		"stdlib paketinde olmayan bir sembol onaylandı; GOROOT kaynağı okunamamış olmalı.")
	require.True(t, tarama.uyeCiftiVar("Principal", "Kind"),
		"depoda var olan bir üye ikilisi bulunamadı; üye dizini KÖR kalmış olmalı ve o "+
			"durumda her Tip.Üye atfı haksız yere kırık sayılır.")
	require.False(t, tarama.uyeCiftiVar("Principal", "BoyleBirUyeYok"),
		"olmayan bir üye ikilisi onaylandı; arama her adı kabul ediyor olmalı.")
}

// satirNumarasiAtfi "dosya.go:satır" biçimindeki atıfları yakalar.
var satirNumarasiAtfi = regexp.MustCompile(`[A-Za-z0-9_./-]+\.go:\d+`)

// TestBelgelerdeSatirNumarasiAtfiYok satır numarasıyla yapılan atıfları
// YASAKLAR.
//
// Yasak, bir üslup tercihi değil ÖLÇÜLMÜŞ bir sonuçtur. Depoda böyle dört atıf
// vardı (hepsi bir ADR'nin kanıt bloğunda) ve dördü de çürümüştü: işaret
// ettikleri satırlarda artık kanıt olarak gösterilen kod değil, komşu bir
// yorumun ortası ya da başka bir fonksiyon duruyordu. Hiçbiri bir hata
// üretmemişti, çünkü satır numarasını hiçbir şey doğrulamaz.
//
// Bu, çürümeye YAPISAL olarak mahkûm tek atıf biçimidir: üstteki bir satırın
// eklenmesi bile onu kaydırır ve kaydırma hiçbir iz bırakmaz.
//
// # Yerine konan biçim ARTIK denetleniyor
//
// Bu godoc bir zamanlar tersini yazıyordu: ADR ve README'deki
// "paket/yolu.Sembol" atıflarını hiçbir şeyin doğrulamadığını, markdown'ı
// denetlemenin ayrı ve henüz yapılmamış bir iş olduğunu söylüyordu. Boşluk
// [TestBelgelerdekiAtiflarCozuluyor] ile kapatıldı ve kapanış ölçümle
// doğrulandı: bir ADR'de sembolü bozmak da yolu bozmak da artık testi DÜŞÜRÜR.
// Yasağın yönlendirdiği biçim, yasağın kendisiyle aynı turda denetime girdi.
//
// Kapanmamış İKİ nokta var ve ikisi de ÖLÇÜLDÜ:
//
//   - ÇIPLAK sembol adları; gerekçesi [TestBelgelerdekiAtiflarCozuluyor]
//     godoc'unda yazılıdır: paketi anılmayan bir ad, hangi pakette aranacağını
//     söylemez.
//   - KOD BLOĞU içindeki nitelikli semboller. Yol atıfları kod bloklarında da
//     denetlenir, semboller denetlenmez: bir Go kod bloğu üçüncü taraf ya da
//     varsayımsal çağrı taşıyabilir ve onları kırık ilan eden bir kural,
//     belgelerdeki her örnek kod parçasını denetime sokardı.
//
// Yasak yine de doğrudur ve gerekçesi bu boşlukla birlikte okunmalıdır: çürümüş
// bir satır numarası SESSİZDİR — okuyan yanlış koda bakar ve baktığını sanır.
// Çürümüş bir sembol adı ise en azından ARANABİLİR: adı grep'leyen okuyucu
// sonuç bulamaz ve atfın bayatladığını anlar. Yasak, sessiz çürümeyi gürültülü
// çürümeye çevirir.
//
// Yasağın kapsamı Go yorumları ile markdown belgeleridir. Kaynak KODDA geçen
// "dosya.go:satır" dizeleri (hata mesajları, konum biçimleri) kapsam dışıdır:
// onlar bir atıf değil, çalışma anında ÜRETİLEN bir konumdur.
func TestBelgelerdeSatirNumarasiAtfiYok(t *testing.T) {
	t.Parallel()

	// Olumlu kontrol: yasağın kendisi de körleşebilir. Deseni bozan bir
	// değişiklik, hiçbir şey bulamayan ve bu yüzden hep yeşil kalan bir testi
	// geride bırakırdı — üstelik "yasak" adını taşımaya devam ederek.
	require.Regexp(t, satirNumarasiAtfi, "bkz. internal/core/http/guard.go:16",
		"desen kendi örneğini bile yakalamıyor; yasak KÖR kalmış olmalı")

	bildir := func(kaynak string, satirNo int, metin string) {
		for _, eslesme := range satirNumarasiAtfi.FindAllString(metin, -1) {
			t.Errorf("%s:%d: %q — satır numarasıyla atıf yapılmaz.\n"+
				"Satır numarası, üstüne bir satır eklendiği anda başka bir yeri "+
				"gösterir ve bunu hiçbir şey bildirmez. Yerine SEMBOL yazın "+
				"(paket.Fonksiyon, Tip.Metot) ya da godoc'un başlığını anın: "+
				"ad değiştiğinde bu dosyadaki denetimler onu yakalar.",
				kaynak, satirNo, eslesme)
		}
	}

	tarama := belgeAtiflariniTara(t)
	for _, dosya := range tarama.dosyalar {
		for _, grup := range dosya.agac.Comments {
			bildir(dosya.yol, tarama.fset.Position(grup.Pos()).Line, grup.Text())
		}
	}
	for _, belge := range markdownBelgeleri(t) {
		for i, satir := range belge.satirlar {
			bildir(belge.yol, i+1, satir)
		}
	}
}

// vurguIpucu ayraç içine VURGU için yazılmış bir sözcük gibi görünen atıflara
// ek teşhis cümlesi üretir; başka durumda boş dize döner.
//
// # Neden gerekli
//
// Bağ tanıma kuralı bilinçli olarak Go'dan sapıyor ve küçük harfli yerel adları
// da bağ sayıyor (godoc'ların yarısı unexported tanımların üstünde). Bunun
// ölçülmüş bedeli, ayraç içine alınmış tek bir ASCII Türkçe sözcüğün — "zorunlu",
// "sonuc", "tanim" gibi — bağ sanılmasıdır. Türkçe ASCII sözcük bakımından zengindir,
// yani bu bir teorik değil BEKLENEN durumdur.
//
// Ham hata mesajı o yazara "paket import edilmemiş" der ve yazar bir bağ
// yazmadığını bildiği için mesajı anlamsız bulur. Anlamsız bulunan bir denetimin
// sonu susturulmaktır; bu yüzden mesaj tahmini SÖYLER.
//
// # Neden kural gevşetilmiyor
//
// "Büyük harf içermeyen tek sözcüğü bağ sayma" demek, "kirp" gibi gerçek bir
// yerel yardımcıya yapılan atfı denetimin dışına çıkarırdı. Arıza yönü de
// güvenli taraftadır: test DÜŞER, sessizce onaylamaz. Bedel bir cümlelik
// teşhisle ödenebiliyorken kapsamı daraltmak, yakalanan gerçek kırıkları
// kaybetmek olurdu.
func vurguIpucu(ad string) string {
	if strings.ContainsAny(ad, "._*") || ad != strings.ToLower(ad) {
		return ""
	}
	return "\nVURGU mu demek istediniz? Köşeli ayraç godoc'ta bağ için AYRILMIŞTIR " +
		"(pkg.go.dev onu kırık bağ olarak gösterir). Vurgu için tırnak kullanın " +
		"ya da sözcüğü BÜYÜK yazın"
}

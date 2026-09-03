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

	"github.com/stretchr/testify/require"
)

// cekirdekHTTPDizini çekirdeğin HTTP paketinin depo içindeki dizinidir.
const cekirdekHTTPDizini = "internal/core/http"

// coreHTTPPath çekirdeğin HTTP paketinin import yoludur.
const coreHTTPPath = modulePath + "/" + cekirdekHTTPDizini

// cekirdekYaziciTanimi hata ve başarı yazıcılarının TANIMLANDIĞI dosyadır.
//
// Bu dosya taramanın dışındadır ve dışında olmak ZORUNDADIR: gövdeyi ve durum
// kodunu yazan yer tam olarak burasıdır, yani kuralı kendisine uygulamak
// "politikanın tek kopyası politikayı ihlal ediyor" demek olurdu. Adın sabit
// tutulması yerine yazıcıları GERÇEKTEN tanımladığı doğrulanır
// (bkz. [TestModulDisiHTTPYuzeyleriCekirdektenYazar]); yazıcılar başka bir
// dosyaya taşınırsa muafiyet onlarla birlikte taşınmalıdır, yoksa bu dosya
// sessizce "her şeyi yazabilen" bir ada dönüşürdü.
const cekirdekYaziciTanimi = cekirdekHTTPDizini + "/response.go"

// netHTTPPath standart kütüphanenin HTTP paketinin import yoludur.
const netHTTPPath = "net/http"

// hataYazanAd çekirdekte hata gövdesini yazan TEK fonksiyonun adıdır.
//
// Politikanın tamamı oradadır: sınıflandırılmamış hata KindInternal sayılır,
// mesajı MASKELENİR ve gerçek metin loglanır (bkz. corehttp.WriteError).
const hataYazanAd = "WriteError"

// basariYazanAd çekirdekte BAŞARI gövdesini yazan yardımcının adıdır.
const basariYazanAd = "WriteJSON"

// htmlYazanAd HTML gövdesinin tek kapısıdır (ADR 0011).
//
// [basariYazanAd]'ın aksine 2xx zorunluluğu YOKTUR: kimliksiz bir tarayıcıya
// giriş sayfasını 401 ile döndürmek, onu başka bir yere yollamaktan daha
// dürüsttür. Denetim bu yüzden HTML yazıcısına durum kısıtı uygulamaz.
const htmlYazanAd = "WriteHTML"

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
	yaziciTasiyanDosyalariDogrula(t, paketler,
		"internal/modules/*/api", "handler'lar http.ResponseWriter almayı bıraktıysa")

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
	// Bu testin girdisi modül listesi değil, modüllerde YAZICI taşıyan
	// dosyalardır: modüller yerinde dururken de o küme boşalabilir ve o an test
	// "api dışında yüzey yok" demiş olur — oysa hiç bakmamıştır.
	yaziciTasiyanDosya := 0

	for _, mod := range moduleNames(t) {
		kok := filepath.Join(repoRoot, modulesDir, mod)
		for _, dosya := range productionFiles(t, kok) {
			if !yaziciGeciyor(t, dosya) {
				continue
			}
			yaziciTasiyanDosya++
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

	require.Positive(t, yaziciTasiyanDosya,
		"modüllerde http.%s taşıyan TEK BİR üretim dosyası bile yok; kapsam denetimi "+
			"KÖR kalmış olmalı.\n"+
			"Bu test \"api dışında denetlenmeyen HTTP yüzeyi var mı\" diye sorar; yazıcıyı "+
			"tanıyamadığında cevabı her zaman \"yok\" olur ve yarın eklenen bir "+
			"modules/x/webhook paketi sessizce kapsam dışında kalır. Yazıcının tanınma "+
			"ölçütü (yaziciTipiMi) gerçekle birlikte güncellenmelidir.", yaziciTipAdi)
}

// govdeYazanNetHTTPCagrilari net/http'nin yanıt GÖVDESİ yazan yardımcılarıdır.
//
// Eşleşme yalnızca ADA bakar, çağrının argümanlarına değil: bu fonksiyonların
// TEK işi yanıt yazmaktır. Argümana bakan bir kontrol, yazıcı bir sarmalayıcıya
// konduğunda ("http.Error(rw, ...)") kaçırırdı — oysa gövdenin çekirdeğin
// dışında üretildiği gerçeği değişmez.
var govdeYazanNetHTTPCagrilari = map[string]bool{
	"Error":           true,
	"NotFound":        true,
	"NotFoundHandler": true,
	"Redirect":        true,
	"ServeContent":    true,
	"ServeFile":       true,
	"ServeFileFS":     true,
}

// yaziciyaGovdeYazanDisCagrilar yazıcıyı ALIP gövdesine yazan dış çağrılardır.
//
// Anahtar "importYolu.Ad" biçimindedir. Bunlar [yaziciAlanGuvenliCagrilar]'ın
// tersidir: yazıcıyı alırlar VE gövdeye yazarlar, yani zarf kararı çekirdeğin
// dışında verilmiş olur.
var yaziciyaGovdeYazanDisCagrilar = map[string]bool{
	"encoding/json.NewEncoder": true,
	"fmt.Fprint":               true,
	"fmt.Fprintf":              true,
	"fmt.Fprintln":             true,
	"io.Copy":                  true,
	"io.WriteString":           true,
}

// sablonYazanMetotlar bir şablon kümesini yazıcıya AKITAN metot adlarıdır.
//
// # Neden ADA bakıyor
//
// Bu denetimin geri kalanı çağrının hedefini import yoluyla çözer; şablon
// çalıştırma bunun DIŞINDA kalır çünkü alıcı bir paket adı değil, bir değerdir
// ("sablonlar.Execute(w, …)"). Hedef çözülemediği için [yaziciyaGovdeYazanDisCagrilar]
// dalı hiç koşmaz ve çağrı SESSİZCE geçer — ölçüldü.
//
// Bu bir izin değil, taramanın ölçme biçiminin negatifiydi: kural kalkmıyor,
// KÖRLEŞİYORDU. Ve körleşeceği yer rastgele değil — yönetim paneli (ADR 0011)
// tam olarak bu biçimi kullanan ilk ve en büyük yüzey.
//
// Ada bakmanın yanlış pozitif riski burada düşüktür: dal ancak yazıcı ARGÜMAN
// olarak verildiğinde koşar ve bir http.ResponseWriter'ı "Execute" adlı bir
// çağrıya vermek, şablonu doğrudan yanıta akıtmaktan başka bir şey değildir.
//
// Doğru yol şablonu ÖNCE belleğe üretip corehttp.WriteHTML'e vermektir; o zaman
// ortada doğan bir hata hâlâ 500'e çevrilebilir, oysa akıtılan şablonda 200
// durum kodlu YARIM bir sayfa kalır.
var sablonYazanMetotlar = map[string]bool{
	"Execute":         true,
	"ExecuteTemplate": true,
}

// cekirdekYaziciMuafiyeti modül DIŞINDAKİ bir yüzeyde yanıtı çekirdek
// yazıcılarının dışında yazan, gerekçesi tartışılmış bir fonksiyondur.
//
// Muafiyet fonksiyonun ADIYLA değil, orada geçmesine izin verilen ÇAĞRILARLA
// tanımlanır. Fonksiyon düzeyinde bir blanket muafiyet denendi ve HOLE
// olduğu görüldü: "gövdeyi kendi yazıyor" diye muaf tutulan bir fonksiyona
// sonradan eklenen bir http.Error da sessizce muaf kalıyordu — yani muafiyet,
// tam olarak kapatmak için var olduğu sınıfı içeriden açıyordu.
type cekirdekYaziciMuafiyeti struct {
	// dosya depo köküne göre yoldur.
	dosya string
	// fonksiyon muaf tutulan fonksiyonun (ya da metodun) adıdır.
	fonksiyon string
	// cagrilar bu fonksiyonda geçmesine izin verilen çağrılardır; kaynakta
	// yazıldığı hâliyle ("w.Write", "WriteJSON").
	//
	// Listede olmayan her çağrı, fonksiyon muaf olsa bile ihlaldir; listede
	// olup KULLANILMAYAN her çağrı testi düşürür.
	cagrilar []string
	// neden bu çağrıların neden ikinci bir hata tanımı ÜRETMEDİĞİDİR.
	neden string
}

// cekirdekYaziciMuafiyetleri modül dışındaki meşru yazma yollarıdır.
//
// Her muafiyet İKİ kapıdan geçer: bayatlayan bir muafiyet testi düşürür
// (gerekçe kodla birlikte yaşamak zorundadır) ve muaf dosyanın çekirdeğin
// yazıcılarını GERÇEKTEN çağırdığı aranır — muaf bir yüzey gövdesini kendi
// yazabilir ama hangi hatanın istemciye verilebileceği kararını çekirdeğe
// SORMAK zorundadır.
var cekirdekYaziciMuafiyetleri = []cekirdekYaziciMuafiyeti{
	{
		dosya:     cekirdekHTTPDizini + "/idempotency.go",
		fonksiyon: "replay",
		cagrilar:  []string{"w.WriteHeader", "w.Write"},
		// Çalınan yanıt bu sunucunun DAHA ÖNCE ürettiği yanıttır: gövdesi de
		// durumu da o zaman çekirdeğin yazıcılarından geçmiştir. Yeniden
		// zarflamak, kaydedilen yanıtın üstüne İKİNCİ bir gövde yazmak olurdu
		// ve idempotency'nin tek vaadi — "aynı anahtar aynı yanıtı verir" —
		// tam olarak burada bozulurdu. Hata yolu yine çekirdektedir: boş kayıt
		// ve parmak izi uyuşmazlığı corehttp.WriteError ile döner.
		neden: "kaydedilmiş yanıtı olduğu gibi çalar; gövde de durum da daha önce " +
			"çekirdek yazıcılarından geçmiştir",
	},
	{
		dosya:     cekirdekHTTPDizini + "/middleware.go",
		fonksiyon: "Recoverer",
		cagrilar:  []string{basariYazanAd},
		// Panik değeri bir error DEĞİLDİR (recover() any döner), yani
		// WriteError'a verilecek bir hata yoktur. Politika buna rağmen
		// kopyalanmaz: yanıt çekirdeğin KENDİ zarfıyla (newErrorResponse) ve
		// KENDİ maskelenmiş metniyle (genericInternalMessage) yazılır, yığın
		// izi yalnızca loga gider.
		neden: "panik değeri error değildir; yanıt yine çekirdeğin zarfı ve maskelenmiş " +
			"metniyle yazılır",
	},
	{
		dosya:     cekirdekHTTPDizini + "/router.go",
		fonksiyon: "readyHandler",
		cagrilar:  []string{basariYazanAd},
		// 503 burada bir HATA yanıtı değil, orkestratörün AYRIŞTIRDIĞI hazırlık
		// raporudur: hangi kontrolün düştüğü gövdenin anlamıdır ve maskelenirse
		// uç işlevini kaybeder. Durum kodu da çağrının kendisinde değil kontrol
		// sonuçlarında hesaplanır, yani bu taramayla çözülemez.
		neden: "/ready gövdesi hata zarfı değil, kontrol sonuçlarını taşıyan hazırlık " +
			"raporudur",
	},
	{
		dosya:     "internal/core/openapi/openapi.go",
		fonksiyon: "Handler",
		cagrilar:  []string{"w.Write"},
		// Gövde, ZATEN KODLANMIŞ ve önbelleklenmiş OpenAPI belgesidir; JSON
		// zarfı değildir ve çekirdeğin yazıcısına verilseydi her istekte bir
		// kez daha taranıp kopyalanırdı — önbelleğin varlık sebebi buydu.
		// Başlık ve durum kodu yine çekirdekten yazılır (WriteJSON, nil gövde),
		// hata yolu ise tümüyle corehttp.WriteError'dadır.
		neden: "önceden kodlanmış OpenAPI belgesini yazar; başlık, durum ve hata yolu " +
			"yine çekirdekten geçer",
	},
}

// TestModulDisiHTTPYuzeyleriCekirdektenYazar modüller DIŞINDA kalan HTTP
// yüzeylerinin de çekirdeğin yazıcılarından geçtiğini doğrular.
//
// # Neden bu test var
//
// [TestHataYanitlariTekYerdenYazilir] yalnızca internal/modules/*/api altını
// gezer ve [TestHTTPYuzeyleriYalnizcaApiPaketlerinde] o kapsamı MODÜLLER
// içinde denetler. İkisi birlikte bile depoda yanıt yazan yerlerin tamamını
// görmez: çekirdeğin kendi uçları (/health, /ready, /openapi.json),
// middleware'leri ve eklentilerin getirdiği uçlar hiçbir taramanın içinde
// değildi. Bedeli ölçüldü — /openapi.json belge üretilemediğinde
// "http.Error(w, ...)" ile DÜZ METİN bir 500 dönüyordu: JSON bekleyen istemci
// gövdeyi ayrıştıramıyor, istek kimliği yanıtta hiç geçmiyor ve hatanın metni
// (çakışan tiplerin paket yolları) maskelenmeden dışarı gidiyordu. Kural
// vardı, kapsam onu görmüyordu.
//
// # Ne sınanır
//
// Modüller dışındaki her üretim paketinde, yanıt yazan her yol çekirdeğin
// yazıcılarından birine gitmelidir:
//
//   - corehttp.WriteError — hatanın TEK kapısı.
//   - corehttp.WriteJSON — başarının kapısı; durumu 2xx OLMAK ZORUNDA, yoksa
//     hata yanıtı maskeleme ve loglama kararlarını atlamış olur.
//
// İhlal sayılanlar: net/http'nin gövde yazan yardımcıları
// ([govdeYazanNetHTTPCagrilari]), yazıcının kendi üzerindeki yazma metotları
// (Write/WriteHeader), yazıcıyı alıp gövdesine yazan dış çağrılar
// ([yaziciyaGovdeYazanDisCagrilar]) ve 2xx olmayan bir WriteJSON.
//
// # Bilinen sınır
//
// Yazıcı olarak tanınanlar: http.ResponseWriter tipli PARAMETRELER ve pakette
// tanımlı bir SARMALAYICI tipten (gömülü http.ResponseWriter taşıyan struct)
// kurulan yerel değişkenler. Sarmalayıcı tipin KENDİ metotları taranmaz; onlar
// yazıcıyı üretmez, aktarır — sarmalayıcının işi tam olarak budur. Sınırın
// yazılması bilinçlidir: eksik olduğunu bilmek, eksik olduğunu sanmamaktan
// iyidir. net/http yardımcıları bu sınırın dışındadır, çünkü onlar ADLA
// yakalanır.
func TestModulDisiHTTPYuzeyleriCekirdektenYazar(t *testing.T) {
	t.Parallel()

	yaziciTaniminiDogrula(t)

	kullanilan := make([]map[string]bool, len(cekirdekYaziciMuafiyetleri))
	for i := range kullanilan {
		kullanilan[i] = map[string]bool{}
	}

	paketler := modulDisiPaketler(t)
	if len(paketler) == 0 {
		t.Fatal("hiç paket bulunamadı; tarama kökü yanlış olabilir")
	}
	// Paket sayısı bu testin GÖRÜŞ ALANINI ölçmez: modül dışı paketlerin
	// çoğunda yanıt yazan bir yol yoktur ve liste her hâlükârda doludur.
	// Ölçülmesi gereken şey yazıcının hâlâ TANINDIĞIDIR.
	yaziciTasiyanDosyalariDogrula(t, paketler,
		"modül dışı üretim paketleri", "yazıcı bir sarmalayıcı arayüzün arkasına geçtiyse")

	for _, dizin := range paketler {
		cekirdekPaketiniDenetle(t, dizin, kullanilan)
	}

	for i, muaf := range cekirdekYaziciMuafiyetleri {
		bayat := false

		for _, cagri := range muaf.cagrilar {
			if kullanilan[i][cagri] {
				continue
			}

			bayat = true

			t.Errorf("kullanılmayan muafiyet: %s içindeki %q artık %s çağırmıyor.\n"+
				"Gerekçesi (%q) bu çağrı için bir şeyi savunmuyor: ya yol düzeltildi ve "+
				"satır silinmeli, ya da çağrı başka bir fonksiyona taşındı ve muafiyet "+
				"onu artık görmüyor.", muaf.dosya, muaf.fonksiyon, cagri, muaf.neden)
		}

		if bayat {
			continue
		}

		if !cekirdekYazicisiGeciyorMu(t, filepath.Join(repoRoot, muaf.dosya)) {
			t.Errorf("%s: muafiyetin gerekçesi (%q) tutmuyor — dosyada hiç corehttp.%s "+
				"ya da corehttp.%s çağrısı yok.\nMuaf yüzey gövdesini kendi yazabilir ama "+
				"hangi hatanın istemciye verilebileceği kararını çekirdeğe SORMAK "+
				"zorundadır.", muaf.dosya, muaf.neden, hataYazanAd, basariYazanAd)
		}
	}
}

// yaziciTaniminiDogrula taramadan muaf tutulan dosyanın gerçekten yazıcıları
// tanımladığını doğrular.
//
// Muafiyet bir DOSYA ADINA değil, o dosyanın İŞİNE verilmiştir. Yazıcılar
// başka bir dosyaya taşınırsa bu ad, hiçbir gerekçesi kalmadan "her şeyi
// yazabilen" bir muafiyete dönüşürdü — ve taşıma sırasında kimse buraya
// bakmazdı.
func yaziciTaniminiDogrula(t *testing.T) {
	t.Helper()

	yol := filepath.Join(repoRoot, cekirdekYaziciTanimi)

	agac, err := parser.ParseFile(token.NewFileSet(), yol, nil, 0)
	if err != nil {
		t.Fatalf("%s ayrıştırılamadı: %v", cekirdekYaziciTanimi, err)
	}

	bulunan := map[string]bool{}
	for _, decl := range agac.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			bulunan[fn.Name.Name] = true
		}
	}

	for _, ad := range []string{hataYazanAd, basariYazanAd} {
		if !bulunan[ad] {
			t.Fatalf("%s artık %q tanımlamıyor.\nBu dosya taramadan MUAFTIR çünkü "+
				"yazıcıların tanımlandığı yerdir; yazıcı taşındıysa cekirdekYaziciTanimi "+
				"de onunla taşınmalı, yoksa muafiyet gerekçesiz kalır.",
				cekirdekYaziciTanimi, ad)
		}
	}
}

// modulDisiPaketler modüller dışındaki üretim paketlerinin dizinlerini döner.
//
// Modüller BİLİNÇLİ olarak dışarıdadır: onları [TestHataYanitlariTekYerdenYazilir]
// çok daha ayrıntılı bir taramayla (yazıcının kaçırılması, durum kodunun
// izlenmesi) gezer. Geri kalan her şey — çekirdek, workflow'lar, eklentiler ve
// bileşim kökü — buraya düşer; yani depoda taranmayan bir dizin adı KALMAZ.
func modulDisiPaketler(t *testing.T) []string {
	t.Helper()

	kume := map[string]bool{}

	for _, dosya := range goFiles(t, repoRoot) {
		if strings.HasSuffix(dosya, "_test.go") {
			continue
		}

		yol := depoYolu(dosya)
		if strings.HasPrefix(yol, modulesDir+string(filepath.Separator)) || yol == cekirdekYaziciTanimi {
			continue
		}

		kume[filepath.Dir(dosya)] = true
	}

	dizinler := make([]string, 0, len(kume))
	for dizin := range kume {
		dizinler = append(dizinler, dizin)
	}
	sort.Strings(dizinler)

	return dizinler
}

// cekirdekDenetimi modül dışı tek bir dosyanın denetim bağlamıdır.
type cekirdekDenetimi struct {
	t     *testing.T
	fset  *token.FileSet
	dosya string
	// yollar dosyanın import adlarını yollarına eşler.
	yollar map[string]string
	// sarmalayicilar pakette tanımlı, gömülü http.ResponseWriter taşıyan
	// struct tiplerinin adlarıdır.
	sarmalayicilar map[string]bool
	// cekirdekHTTPPaketi dosyanın çekirdeğin HTTP paketinde olduğunu söyler;
	// orada yazıcı çağrıları NİTELİKSİZDİR (WriteError, corehttp.WriteError değil).
	cekirdekHTTPPaketi bool
	// kullanildi muafiyet başına, kullanılan çağrıların kümesidir.
	kullanildi []map[string]bool
}

// cekirdekPaketiniDenetle tek bir paketi gezer ve ihlalleri raporlar.
//
// Denetim PAKET düzeyindedir çünkü sarmalayıcı tipler bir dosyada tanımlanıp
// başka dosyalarda kullanılır (responseWriter middleware.go'da tanımlıdır,
// telemetry.go'da kurulur); dosya dosya bakan bir tarama onları göremezdi.
func cekirdekPaketiniDenetle(t *testing.T, dizin string, kullanilan []map[string]bool) {
	t.Helper()

	fset := token.NewFileSet()
	dosyalar := productionFiles(t, dizin)
	agaclar := make(map[string]*ast.File, len(dosyalar))

	for _, dosya := range dosyalar {
		if depoYolu(dosya) == cekirdekYaziciTanimi {
			continue
		}

		agac, err := parser.ParseFile(fset, dosya, nil, 0)
		if err != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", dosya, err)
		}

		agaclar[dosya] = agac
	}

	sarmalayicilar := sarmalayiciTipler(t, dizin)

	for dosya, agac := range agaclar {
		d := &cekirdekDenetimi{
			t:                  t,
			fset:               fset,
			dosya:              dosya,
			yollar:             importYollari(agac),
			sarmalayicilar:     sarmalayicilar,
			cekirdekHTTPPaketi: depoYolu(dizin) == cekirdekHTTPDizini,
			kullanildi:         kullanilan,
		}

		for _, decl := range agac.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				d.fonksiyonuDenetle(fn)
			}
		}
	}
}

// sarmalayiciTipler paketteki, gömülü http.ResponseWriter taşıyan struct
// adlarını döner.
//
// Yazıcının sarmalayıcıya konması bu depoda İSTİSNA değil kuraldır (durum
// sayacı, idempotency kaydı, telemetri); sarmalayıcıyı tanımayan bir tarama,
// gövdenin oradan yazıldığı her yolu kaçırırdı.
func sarmalayiciTipler(t *testing.T, dizin string) map[string]bool {
	t.Helper()

	adlar := map[string]bool{}

	for _, dosya := range productionFiles(t, dizin) {
		agac, err := parser.ParseFile(token.NewFileSet(), dosya, nil, 0)
		if err != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", dosya, err)
		}

		yollar := importYollari(agac)

		ast.Inspect(agac, func(n ast.Node) bool {
			tip, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}

			yapi, ok := tip.Type.(*ast.StructType)
			if !ok || yapi.Fields == nil {
				return true
			}

			for _, alan := range yapi.Fields.List {
				if len(alan.Names) == 0 && yaziciTipiMi(alan.Type, yollar) {
					adlar[tip.Name.Name] = true
				}
			}

			return true
		})
	}

	return adlar
}

// fonksiyonuDenetle bir fonksiyondaki yanıt yazma yollarını denetler.
func (d *cekirdekDenetimi) fonksiyonuDenetle(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}

	yazicilar := yaziciDegiskenleri(fn, d.yollar, d.sarmalayicilar)

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if cagri, ok := n.(*ast.CallExpr); ok {
			d.cagriyiDenetle(fn.Name.Name, cagri, yazicilar)
		}

		return true
	})
}

// cagriyiDenetle tek bir çağrıyı kurallara vurur.
func (d *cekirdekDenetimi) cagriyiDenetle(fonksiyon string, cagri *ast.CallExpr, yazicilar map[string]bool) {
	hedef := cagriHedefi(cagri, d.yollar)

	if hedef.paket == netHTTPPath && govdeYazanNetHTTPCagrilari[hedef.ad] {
		d.ihlal(fonksiyon, hedef.kaynak, cagri.Pos(),
			"%s yanıt gövdesini çekirdeğin DIŞINDA yazıyor.\n"+
				"Gövde ortak JSON zarfı olmaz: istemci hatayı ayrıştıramaz, istek kimliği "+
				"yanıta hiç girmez ve hatanın metni maskelenmeden dışarı çıkar. Hatanın tek "+
				"kapısı corehttp.%s'dır.", hedef.kaynak, hataYazanAd)

		return
	}

	if d.cekirdekYazicisiMi(hedef) {
		if hedef.ad == basariYazanAd {
			d.durumuDenetle(fonksiyon, hedef.kaynak, cagri)
		}

		return
	}

	if hedef.alici != "" && yazicilar[hedef.alici] {
		if yaziciyaGuvenliMetotlar[hedef.ad] {
			return
		}

		d.ihlal(fonksiyon, hedef.kaynak, cagri.Pos(),
			"%s ile yanıt DOĞRUDAN yazılıyor.\n"+
				"Hata gövdesinin tek kapısı corehttp.%s, başarınınki corehttp.%s'dır; "+
				"ikinci bir yazma yolu, maskeleme ve loglama kararlarının kopyalanması "+
				"demektir.", hedef.kaynak, hataYazanAd, basariYazanAd)

		return
	}

	if !yaziciAlanCagri(cagri, yazicilar) {
		return
	}

	if hedef.paket != "" && yaziciyaGovdeYazanDisCagrilar[hedef.paket+"."+hedef.ad] {
		d.ihlal(fonksiyon, hedef.kaynak, cagri.Pos(),
			"yazıcı %s çağrısına veriliyor ve gövde oradan "+
				"yazılıyor.\nZarfın biçimi ve maskeleme kararı çekirdekte durmalı; elle "+
				"kodlanan bir gövde, o kararların ikinci bir kopyasıdır.", hedef.kaynak)

		return
	}

	if sablonYazanMetotlar[hedef.ad] {
		d.ihlal(fonksiyon, hedef.kaynak, cagri.Pos(),
			"yazıcı %s çağrısına veriliyor: şablon doğrudan yanıta AKITILIYOR.\n"+
				"Şablon önce belleğe üretilmeli, hata olursa corehttp.%s çağrılmalı, ancak "+
				"başarılıysa corehttp.%s'e verilmelidir. Akıtılan bir şablonda ortada doğan "+
				"hata, 200 durum kodlu YARIM bir sayfa bırakır: başlık gönderilmiş olduğu "+
				"için ne panik yakalayıcı ne hata yazıcısı bir şey yapabilir.",
			hedef.kaynak, hataYazanAd, htmlYazanAd)
	}
}

// durumuDenetle WriteJSON çağrısının durum kodunun 2xx olduğunu doğrular.
//
// 4xx/5xx bir durumla çağrılan WriteJSON, hata yanıtını corehttp.WriteError'ın
// maskeleme ve loglama kararlarını ATLAYARAK yazar; sonuç, çekirdeği hiç
// çağırmamakla aynıdır.
func (d *cekirdekDenetimi) durumuDenetle(fonksiyon, kaynak string, cagri *ast.CallExpr) {
	const durumSirasi = 2

	if len(cagri.Args) <= durumSirasi {
		return
	}

	switch v := cagri.Args[durumSirasi].(type) {
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		if ok && d.yollar[x.Name] == netHTTPPath && basariDurumlari[v.Sel.Name] {
			return
		}
	case *ast.BasicLit:
		if v.Kind == token.INT {
			if kod, err := strconv.Atoi(v.Value); err == nil && kod >= 200 && kod <= 299 {
				return
			}
		}
	}

	d.ihlal(fonksiyon, kaynak, cagri.Args[durumSirasi].Pos(),
		"corehttp.%s çağrısının durum kodu 2xx olarak ÇÖZÜLEMİYOR.\n"+
			"2xx olmayan bir gövde corehttp.%s'ın maskeleme ve loglama kararlarını "+
			"atlar: gövde ne yazılırsa istemciye o gider.", basariYazanAd, hataYazanAd)
}

// cekirdekYazicisiMi çağrının çekirdeğin yazıcılarına gidip gitmediğini söyler.
//
// Çekirdeğin KENDİ paketinde çağrı niteliksizdir (WriteError), dışarıda ise
// import adıyla nitelenir (corehttp.WriteError); ikisi de aynı fonksiyondur ve
// tarama ikisini de tanımak zorundadır.
func (d *cekirdekDenetimi) cekirdekYazicisiMi(hedef hedefBilgisi) bool {
	if hedef.ad != hataYazanAd && hedef.ad != basariYazanAd {
		return false
	}

	if hedef.paket == coreHTTPPath {
		return true
	}

	return d.cekirdekHTTPPaketi && hedef.yerel && hedef.alici == ""
}

// ihlal muaf değilse bir ihlal raporlar.
func (d *cekirdekDenetimi) ihlal(fonksiyon, kaynak string, pos token.Pos, bicim string, args ...any) {
	d.t.Helper()

	if d.muafMi(fonksiyon, kaynak) {
		return
	}

	konum := d.fset.Position(pos)
	d.t.Errorf("%s:%d: %s içinde "+bicim,
		append([]any{depoYolu(d.dosya), konum.Line, fonksiyon}, args...)...)
}

// muafMi çağrının bu dosya ve fonksiyon için gerekçelendirilip
// gerekçelendirilmediğini söyler.
func (d *cekirdekDenetimi) muafMi(fonksiyon, kaynak string) bool {
	yol := depoYolu(d.dosya)

	for i, muaf := range cekirdekYaziciMuafiyetleri {
		if filepath.FromSlash(muaf.dosya) != yol || muaf.fonksiyon != fonksiyon {
			continue
		}

		for _, cagri := range muaf.cagrilar {
			if cagri != kaynak {
				continue
			}

			d.kullanildi[i][cagri] = true

			return true
		}
	}

	return false
}

// yaziciDegiskenleri fonksiyondaki yazıcı adlarını toplar.
//
// İki kaynak vardır: http.ResponseWriter tipli PARAMETRELER (iç fonksiyon
// değişmezlerininki dâhil) ve pakette tanımlı bir sarmalayıcı tipten kurulan
// yerel değişkenler. İkincisi olmasaydı, yazıcıyı bir sarmalayıcıya koyup
// gövdeyi oradan yazmak taramayı sessizce atlatırdı.
func yaziciDegiskenleri(fn ast.Node, yollar map[string]string, sarmalayicilar map[string]bool) map[string]bool {
	adlar, _ := yaziciParametreleri(fn, yollar)

	ast.Inspect(fn, func(n ast.Node) bool {
		atama, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for i, sag := range atama.Rhs {
			if i >= len(atama.Lhs) || !sarmalayiciKurulumu(sag, sarmalayicilar) {
				continue
			}

			if hedef, ok := atama.Lhs[i].(*ast.Ident); ok {
				adlar[hedef.Name] = true
			}
		}

		return true
	})

	return adlar
}

// sarmalayiciKurulumu ifadenin bir sarmalayıcı tipin örneğini kurup kurmadığını
// söyler.
func sarmalayiciKurulumu(ifade ast.Expr, sarmalayicilar map[string]bool) bool {
	if birli, ok := ifade.(*ast.UnaryExpr); ok && birli.Op == token.AND {
		ifade = birli.X
	}

	yapi, ok := ifade.(*ast.CompositeLit)
	if !ok {
		return false
	}

	ad, ok := yapi.Type.(*ast.Ident)

	return ok && sarmalayicilar[ad.Name]
}

// cekirdekYazicisiGeciyorMu dosyada çekirdeğin yazıcılarına yapılmış bir çağrı
// arar.
func cekirdekYazicisiGeciyorMu(t *testing.T, dosya string) bool {
	t.Helper()

	agac, err := parser.ParseFile(token.NewFileSet(), dosya, nil, 0)
	if err != nil {
		t.Fatalf("%s ayrıştırılamadı: %v", dosya, err)
	}

	d := &cekirdekDenetimi{
		yollar:             importYollari(agac),
		cekirdekHTTPPaketi: depoYolu(filepath.Dir(dosya)) == cekirdekHTTPDizini,
	}

	bulundu := false

	ast.Inspect(agac, func(n ast.Node) bool {
		if bulundu {
			return false
		}

		if cagri, ok := n.(*ast.CallExpr); ok && d.cekirdekYazicisiMi(cagriHedefi(cagri, d.yollar)) {
			bulundu = true
		}

		return true
	})

	return bulundu
}

// paketiDenetle tek bir api paketini gezer ve ihlalleri raporlar.
//
// Denetim PAKET düzeyindedir çünkü yardımcılar (writeItem gibi) bir dosyada
// tanımlanıp başka dosyalardan çağrılır; durum kodunun izi ancak paketin
// tamamı elde tutulunca sürülebilir.
func paketiDenetle(t *testing.T, dizin string, muafiyetKullanildi []bool) {
	t.Helper()

	fset := token.NewFileSet()
	dosyalar := productionFiles(t, dizin)
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
	for _, mod := range moduleNames(t) {
		dizin := filepath.Join(repoRoot, modulesDir, mod, "api")
		if info, err := os.Stat(dizin); err == nil && info.IsDir() {
			dizinler = append(dizinler, dizin)
		}
	}

	return dizinler
}

// productionFiles kök altındaki test OLMAYAN .go dosyalarını döner.
func productionFiles(t *testing.T, kok string) []string {
	t.Helper()
	var out []string
	for _, dosya := range goFiles(t, kok) {
		if !strings.HasSuffix(dosya, "_test.go") {
			out = append(out, dosya)
		}
	}

	return out
}

// yaziciTasiyanDosyalariDogrula verilen paketlerde en az bir dosyanın
// http.ResponseWriter taşıdığını doğrular.
//
// Üç hata yolu denetiminin de girdisi PAKET listesi değil, o paketlerdeki
// YAZICI kullanımlarıdır: paket listesi doluyken de tarama boş kalabilir,
// çünkü ihlal ancak yazıcıya dokunan bir yolda aranır. Yazıcı tanınmaz hâle
// geldiği gün (tip bir arayüzün arkasına geçtiğinde, handler imzası bir çatı
// tipine döndüğünde) denetimlerin üçü de hiçbir çağrıya bakmaz ve yeşil kalır.
//
// Bugün bu durumu MUAFİYETLER de dolaylı olarak yakalar: kullanılmayan bir
// muafiyet testi düşürür ve muafiyetlerin işaretlenmesi taramanın yazıcıyı
// görmesine bağlıdır. Ama o koruma tesadüfidir — borç ödenip son muafiyet
// silindiği gün kendisi de yok olur. Bu kapı, o güne bağlı değildir.
func yaziciTasiyanDosyalariDogrula(t *testing.T, paketler []string, kapsam, ipucu string) {
	t.Helper()

	bulunan := 0
	for _, dizin := range paketler {
		for _, dosya := range productionFiles(t, dizin) {
			if yaziciGeciyor(t, dosya) {
				bulunan++
			}
		}
	}

	require.Positive(t, bulunan,
		"%s içinde http.%s taşıyan TEK BİR üretim dosyası bile yok; hata yolu denetimi "+
			"KÖR kalmış olmalı (%s).\n"+
			"Yazıcıyı tanımayan bir tarama hiçbir yazma yoluna bakmaz: çekirdeğin "+
			"dışında yazılan bir hata gövdesi — maskelenmemiş metin, zarfsız yanıt — "+
			"hiçbir yerde görünmez. Yazıcının tanınma ölçütü (yaziciTipiMi) gerçekle "+
			"birlikte güncellenmelidir.", kapsam, yaziciTipAdi, ipucu)
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
	for _, dosya := range productionFiles(t, dizin) {
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

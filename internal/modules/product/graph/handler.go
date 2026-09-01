package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// onbellekGirdiSayisi ayrıştırılmış sorgu belgelerinin önbellekteki en fazla
// girdi sayısıdır.
//
// Vitrin istemcileri AYNI belgeyi (yalnızca değişkenleri farklı) tekrar tekrar
// gönderir; her istekte yeniden ayrıştırıp doğrulamak, aynı işi her seferinde
// yapmaktır. Sayı, bir vitrinin bakımını yaptığı belge çeşidinden (sayfa
// başına birkaç sorgu) fazlasıyla büyüktür.
//
// GİRDİ SAYISI TEK BAŞINA BİR SINIR DEĞİLDİR ve bir zamanlar öyle sanılıyordu:
// 100 girdi, girdi başına bir üst sınır yoksa 100 × belge boyutu kadar yer
// demektir. Ölçüldü: 65 KB'lık 100 belge — hepsi karmaşıklık sınırında
// reddedilmiş, servise hiç ulaşmamış — runtime.GC sonrası 171,8 MiB KALICI
// yığın bırakıyordu, yani 6,5 MB'lık yüklemeden 26 kat fazla. Sayının yanına
// [maxOnbellekBelgeBayt] bu yüzden eklendi.
const onbellekGirdiSayisi = 100

// maxOnbellekBelgeBayt önbelleğe girebilecek bir belgenin en fazla kaç bayt
// olabileceğidir.
//
// Ölçü olarak belgenin METNİ kullanılır çünkü elde ölçülebilir tek şey odur:
// önbelleğin anahtarı zaten ham sorgu metnidir ve saklanan ağacın gerçek yeri
// metnin bir katıdır, ama o katsayıyı öğrenmek ağacı gezmek demektir — yani
// önbelleğin kazandırdığı işi her eklemede geri yapmak.
//
// 8 KiB, vitrinin gerçek belgelerinin KAT KAT üstündedir: ölçülen en ağır
// meşru sorgu (varsayılan sayfa × şemanın tüm alanları) 655 bayttır, fragment
// ağırlıklı bir vitrin belgesi 6,3 KB. Gövde sınırının (64 KiB) sekizde biri
// olması bilinçlidir: gövde sınırı "ayrıştırmaya değer mi" sorusunu, bu sınır
// "SAKLAMAYA değer mi" sorusunu yanıtlar ve ikincisinin eşiği çok daha
// düşüktür — saklamanın bedeli isteğin ömrü kadar değil, önbellekten düşene
// kadar sürer.
//
// Sınırın altında kalan belge de otomatik saklanmaz; ayrıca sınırlardan
// GEÇMİŞ olması gerekir (bkz. [sorguOnbellegi]).
const maxOnbellekBelgeBayt = 8 << 10

// maxSorguBayt tek bir GraphQL istek gövdesinin üst sınırıdır.
//
// Bu sınır, derinlik ve karmaşıklık sınırlarının YAPAMADIĞI işi yapar: ikisi
// de belge AYRIŞTIRILDIKTAN sonra ölçülebilir, yani onlara ulaşana kadar
// sunucu 10 MiB'lık bir "{a{a{a…" metnini zaten okumuş ve ayrıştırmıştır.
// Ayrıştırma maliyetini yalnızca gövde sınırı bağlar.
//
// Değer REST tarafındakinden (1 MiB) küçüktür ve bu bilinçlidir: oradaki gövde
// bir KAYIT taşır (varyantları, görselleriyle bir ürün), buradaki gövde ise bir
// SORGU METNİDİR ve okuma yüzeyinin değişkenleri (kimlik, handle, sayfa) birkaç
// yüz bayttır. 64 KiB, fragment'lara bölünmüş büyük bir vitrin sorgusunun kat
// kat üstündedir.
//
// Sınır İKİ ayrı yerde uygulanır ve ikisi de gereklidir; gerekçe
// [govdeSiniri]'ndedir.
const maxSorguBayt = 64 << 10

// maxSorguJeton tek bir belgenin ayrıştırılabileceği en fazla jeton sayısıdır.
//
// [maxSorguBayt]'ın kardeşidir ve onun bırakmak zorunda kaldığı boşluğu
// kapatır: bayt sınırı ancak gövde okunurken uygulanabilir, jeton sınırı ise
// AYRIŞTIRMANIN İÇİNDE çalışır ve sınır aşıldığı anda ayrıştırıcıyı durdurur.
// Yani en ucuz kapı budur — belgeyi sonuna kadar ayrıştırmadan reddeder.
//
// Değer ölçüldü. 64 KiB'lık bir gövde en ucuz jetonlarla ("a a a …") 32.000
// jeton taşıyabilir; oysa vitrinin gerçek belgeleri 95 jetondur (varsayılan
// sayfa × tüm alanlar) ve fragment ağırlıklı, on kök sorgulu bir belge 922.
// 8.192 hem meşru kullanımın yaklaşık dokuz katı hem de bayt sınırının tek
// başına izin verdiğinin dörtte biridir.
//
// Kapının tek başına yakaladığı ölçülmüş belgeler var: 302 takma adlı __schema
// 9.364 jeton, 448 takma adlı __type 14.786 jetondur ve ikisi de gövde
// sınırının (45.796 ve 59.924 bayt) altında kaldığı için oradan geçiyordu.
//
// [maxSorguBayt] gibi SABİTTİR, ayara açılmadı: ikisi de belgenin
// ayrıştırılmasını bağlar ve bu ailenin gevşetilmesi bir kapasite tercihi
// değil, ayrıştırıcıyı istemciye açmaktır.
const maxSorguJeton = 8192

// NewHandler GraphQL vitrin ucunun HTTP handler'ını verilen sınırlarla kurar.
//
// Sınırların gerekçeleri ve sıfır değerin anlamı için bkz. [Options]; bu uçta
// maliyeti isteği YAZAN belirlediği için sınırlar bir ayar değil, yüzeyin
// çalışma koşuludur.
//
// # YALNIZCA POST
//
// GET taşıması BİLİNÇLİ olarak eklenmedi. GET'in tek gerçek getirisi ara
// önbelleklerdir ve o getiri burada YOKTUR: yanıt isteğin publishable
// anahtarına, yani satış kanalına göre değişir. Paylaşılan bir önbellek ya
// anahtar başlığına göre ayrışmak zorunda kalır (yani hemen hemen hiçbir şeyi
// önbelleklemez) ya da bir vitrinin kataloğunu başkasına servis eder — kanal
// süzgecinin var olma sebebi tam olarak budur.
//
// Karşılığında iki somut bedel ödenirdi: sorgunun tamamı URL'ye girer ve
// erişim loglarına, proxy loglarına, tarayıcı geçmişine düşer; uzun sorgular
// da yaygın proxy sınırlarında (~8 KiB) istemcinin teşhis edemeyeceği bir 414
// ile ölür.
//
// Uç chi'ye yalnızca POST ile kaydedilir (bkz. api/routes.go), böylece GET
// isteği gqlgen'in "transport not supported" 400'ü yerine dürüst bir 405 alır.
//
// # Kapıların SIRASI
//
// Eklentiler kayıt sırasıyla işletilir ve sıra bir tercih değil, iki
// zorunluluğun sonucudur.
//
// [secimButcesi] EN BAŞTADIR çünkü kendisinden sonraki her kapı belge ağacını
// gezer ve fragment'lar üssel açılabilir (bkz. [DefaultMaxSelections]); ağacın
// büyüklüğü bağlanmadan hiçbir yürüyüş güvenli değildir. Onun ardından sıra
// ucuzdan pahalıya dizilir: derinlik ve iç gözlem kökü ağacı bir kez gezer,
// alan tekrarı seviye başına bir harita kurar, karmaşıklık ise her alan için
// şemaya bakar.
//
// [onbellekKapisi] EN SONDADIR ve bu da [sorguOnbellegi]'nin çalışma
// koşuludur: ondan önce koşan her kapı, reddettiği belgenin önbelleğe
// girmesini engeller.
//
// # Koruma
//
// Kimlik doğrulama, hız sınırı ve idempotency BURADA KURULMAZ; /store/v1
// önekine bağlı koruma yığınından gelir (bkz. corehttp.APIGuards). Yığını
// burada tekrarlamak, aynı kuralın ikinci bir tanımını yaratırdı.
func NewHandler(svc Storefront, opts Options) http.Handler {
	srv, _ := yeniSunucu(svc, opts)

	return govdeSiniri(yanitSiniri(srv, opts.maxResponseBytes()))
}

// yeniSunucu gqlgen sunucusunu ve onun sorgu önbelleğini kurar.
//
// Önbellek AYRICA dönülür çünkü içeriği bir DAVRANIŞ iddiasıdır — reddedilen
// belge saklanmamalıdır (bkz. [sorguOnbellegi]) — ve bu iddia handler'ın
// dışından gözlenemez. Test için ikinci bir kurulum yazmak, testin gerçek
// kayıt sırasını değil kendi kopyasını doğrulaması olurdu; kapıların sırası
// ise düzeltmenin ta kendisidir. Sıranın gerekçesi [NewHandler]'dadır.
func yeniSunucu(svc Storefront, opts Options) (*handler.Server, *sorguOnbellegi) {
	cfg := Config{Resolvers: NewResolver(svc)}
	karmasiklikMaliyetleri(&cfg.Complexity)

	srv := handler.New(NewExecutableSchema(cfg))

	srv.AddTransport(transport.POST{})
	srv.SetParserTokenLimit(maxSorguJeton)

	onbellek := yeniSorguOnbellegi(onbellekGirdiSayisi, maxOnbellekBelgeBayt)
	srv.SetQueryCache(onbellek)

	srv.Use(secimButcesi{sinir: opts.maxSelections()})
	srv.Use(derinlikSiniri{
		sinir:          opts.maxDepth(),
		icGozlemSiniri: opts.maxIntrospectionDepth(),
	})
	srv.Use(icGozlemKokSiniri{
		sinir:  opts.maxIntrospectionRoots(),
		kapali: opts.IntrospectionDisabled,
	})
	srv.Use(alanTekrariSiniri{sinir: opts.maxFieldRepetition()})
	srv.Use(extension.FixedComplexityLimit(opts.maxComplexity()))

	// İç gözlem varsayılan olarak AÇIKTIR ve kurulum kapatabilir; kararın
	// gerekçesi [Options.IntrospectionDisabled] alanındadır. gqlgen'de iç
	// gözlem eklentisi KURULMADIĞINDA kapalıdır (OperationContext'in
	// DisableIntrospection alanı true doğar), bu yüzden kapatmanın yolu
	// eklentiyi hiç eklememektir. Belgeyi asıl reddeden kapı yukarıdaki
	// [icGozlemKokSiniri]'dir; buradaki eksiklik onun arkasındaki emniyettir.
	//
	// Öneriler AYNI anahtara bağlanır ve bu bir tercih değil, anahtarın
	// vaadinin tamamlanmasıdır: doğrulayıcının "Did you mean …?" cümleleri
	// şemanın adlarını perakende dağıtır (bkz. [Options.IntrospectionDisabled]).
	// gqlgen'in anahtarı bu iki kuralın önerisini hiç HESAPLAMAZ; ulaşamadığı
	// kuralların cümlesi [protokolHatasi]'nda kesilir.
	if opts.IntrospectionDisabled {
		srv.SetDisableSuggestion(true)
	} else {
		srv.Use(extension.Introspection{})
	}

	srv.Use(onbellekKapisi{onbellek: onbellek})

	srv.SetErrorPresenter(hataSunucusu(opts))
	srv.SetRecoverFunc(panikYakala)

	return srv, onbellek
}

// govdeSiniri handler'ı istek gövdesi sınırıyla sarar.
//
// Sınır gqlgen'in İÇİNDE kurulamaz: sunucu gövdeyi kendi taşımasında okur ve
// okuyucuyu değiştirmek için bir kanca sunmaz.
//
// # İki kapı
//
// Bildirilen boyut (Content-Length) BURADA reddedilir ve yanıt, çekirdeğin
// hata zarfıdır — /store/v1 altındaki her uçla aynı biçim, aynı kod, aynı
// istek kimliği. Kural şudur: GraphQL zarfı (data/errors) yalnızca
// ÇALIŞTIRICIYA ULAŞMIŞ belgelere aittir; ondan öncesi — yetkisiz istek,
// desteklenmeyen metot, sığmayan gövde — bu yüzeyde de sıradan bir HTTP
// hatasıdır. Uç zaten böyle davranıyor: publishable anahtarı olmayan istek
// 401'i çekirdek zarfıyla, GET isteği chi'den 405 alıyor.
//
// Ama Content-Length bir İDDİADIR: parçalı (chunked) gövdede hiç yoktur ve
// yanlış de olabilir. Asıl sınırı bu yüzden [net/http.MaxBytesReader] uygular;
// o yola düşen istek GraphQL zarfını alır (200 + errors), yani ikinci biçim
// yalnızca boyutunu SAKLAYAN istemciye görünür. Alternatif, gövdeyi burada
// tümüyle okuyup saymaktı — tam da kaçınmak istediğimiz şeyi yapmak.
//
// Değişen yalnızca ZARFTIR, cümle değil: sınırın kesildiği an [govdeAsimi]'ne
// kaydedilir ve [tasimaHatasi] aynı sebebi aynı sayıyla söyler. Kayıt olmadan
// istemci yalnızca gqlgen'in kendi cümlesini görürdü ve o cümle, taşımanın
// mesajını ayrıştırmadan bizim tarafımızdan tanınamaz.
func govdeSiniri(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxSorguBayt {
			corehttp.WriteError(r.Context(), w, coreerrors.Invalid(codeGovdeCokBuyuk,
				"GraphQL belgesi çok büyük (en fazla %d bayt)", maxSorguBayt))

			return
		}

		durum := &govdeAsimi{}
		r = r.WithContext(context.WithValue(r.Context(), govdeAsimiAnahtari{}, durum))

		// Yanıt yazıcısı da verilir: sınır aşıldığında sunucu isteği düzgün
		// sonlandırabilsin diye. Aksi hâlde bağlantı yarım okunmuş bir gövdeyle
		// asılı kalırdı. Sayan sarmalayıcı DEĞİL ham yazıcı verilir; stdlib
		// yazıcıyı kendi iç arayüzüne çevirebildiğinde bağlantıyı işaretler ve
		// araya giren bir tip o davranışı sessizce kaldırırdı.
		r.Body = govdeOkuyucu{
			ReadCloser: http.MaxBytesReader(w, r.Body, maxSorguBayt),
			durum:      durum,
		}

		next.ServeHTTP(w, r)
	})
}

// govdeAsimiAnahtari gövde aşımı kaydının context içindeki anahtarıdır.
//
// Kendi tipi vardır: anahtar olarak dize kullanmak, başka bir paketin aynı
// dizeyle yazdığı değeri okumaya açık kapı bırakırdı.
type govdeAsimiAnahtari struct{}

// govdeAsimi isteğin gövde sınırını aşıp aşmadığını taşır.
//
// Bilgi context'te taşınır çünkü onu ÜRETEN yer ([govdeSiniri]) ile ona
// İHTİYAÇ DUYAN yer ([tasimaHatasi]) arasında gqlgen'in taşıması durur:
// taşıma, okuma hatasını kendi cümlesine gömer ("could not read request
// body: %+v") ve sebebi oradan geri çıkarmak, kütüphanenin metnini ikinci kez
// tanımlamak olurdu. Kayıt bir metin değil bir ÖLÇÜMDÜR.
//
// Alan atomiktir çünkü gövdeyi okuyan gorutin ile hatayı sunan gorutinin aynı
// olacağı hiçbir sözleşmede yazmaz.
type govdeAsimi struct{ asildi atomic.Bool }

// govdeOkuyucu sınırın kestiği ANI kaydeden istek gövdesi okuyucusudur.
type govdeOkuyucu struct {
	io.ReadCloser

	durum *govdeAsimi
}

// Read okumayı geçirir ve sınır aşımını işaretler.
func (g govdeOkuyucu) Read(p []byte) (int, error) {
	n, err := g.ReadCloser.Read(p)

	// Tip denetlenir, metin DEĞİL: stdlib aşımı kendi hata tipiyle bildirir ve
	// cümlesini bir gün değiştirebilir.
	var asim *http.MaxBytesError
	if errors.As(err, &asim) {
		g.durum.asildi.Store(true)
	}

	return n, err
}

// govdeAsildi isteğin gövde sınırını aşıp aşmadığını döner.
//
// Kayıt yoksa false döner: [govdeSiniri] devrede değilse (paket içinden
// kurulan bir sunucu) aşım da yoktur.
func govdeAsildi(ctx context.Context) bool {
	durum, ok := ctx.Value(govdeAsimiAnahtari{}).(*govdeAsimi)

	return ok && durum.asildi.Load()
}

// yanitSiniri handler'ı yanıt bayt sınırıyla sarar.
//
// Bu kapı ötekilerden farklı bir soru sorar ve tam olarak bu yüzden gereklidir:
// derinlik, tekrar ve karmaşıklık belgeye bakıp maliyeti TAHMİN eder; burada
// sayılan şey GERÇEKLEŞEN bayttır. Tahmin ne kadar iyi olursa olsun bir alanın
// içeriğini bilemez, bu yüzden son söz ölçüme aittir (bkz.
// [DefaultMaxResponseBytes]).
//
// Sarmalayıcı gqlgen'in DIŞINDA kurulur çünkü içeride durduramaz: gqlgen'in
// sunucusu kendi ServeHTTP'sinde her paniği yakalayıp 500 yazar, yani
// bağlantıyı bırakma kararı orada verilemez. Dışarıda verilir ve
// corehttp.Recoverer http.ErrAbortHandler'ı yeniden fırlatarak stdlib'e
// ulaştırır.
func yanitSiniri(next http.Handler, sinir int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sayac := &yanitSayaci{ResponseWriter: w, sinir: sinir, kalan: sinir}

		next.ServeHTTP(sayac, r)

		if sayac.kesildi {
			panic(http.ErrAbortHandler)
		}
	})
}

// errYanitCokBuyuk yanıt sınırı aşıldığında yazana dönen hatadır.
//
// Sıradan bir yazma hatası gibi görünür ve bu doğrudur: çağıran için sonuç
// aynıdır — gövdenin geri kalanı istemciye gitmeyecektir.
var errYanitCokBuyuk = errors.New("graph: yanıt sınırı aşıldı")

// yanitSayaci istemciye yazılan baytları sayan http.ResponseWriter'dır.
//
// # Sınıra çarpınca ne olur
//
// Yanıt AKIŞ HÂLİNDE yazılır, yani sarmalayıcı sınırı aştığını ancak gövdenin
// bir kısmı çoktan gitmişken öğrenebilir. İki durum ayrılır ve ikisinde de
// YARIM JSON GÖNDERİLMEZ:
//
//   - Henüz hiçbir bayt gitmediyse — bugün gqlgen'in POST taşımasında her
//     zaman böyledir, yanıtı tek seferde kodlayıp tek Write ile yazar — aşan
//     gövde ATILIR ve yerine tam, geçerli bir hata zarfı yazılır. İstemci
//     kırık bir belge değil, sebebini söyleyen bir yanıt alır.
//   - Bir kısmı gitmişse tam bir belge artık imkânsızdır. O hâlde bağlantı
//     BIRAKILIR ([yanitSiniri] http.ErrAbortHandler ile panikler). Yarım JSON
//     göndermek istemciyi bozar: ya ayrıştırma hatası alır ve sebebini
//     bilemez, ya da — daha kötüsü — kırpılmış gövdeyi kısa bir sonuç sanır.
//     Bağlantıyı düşürmek dürüsttür; istemci bir aktarım hatası görür, ki
//     olan tam olarak budur.
//
// İkinci dal bugün ULAŞILMAZDIR ve bilerek duruyor: http.ResponseWriter
// sözleşmesi parçalı yazmaya izin verir ve bu uca bir gün akış yapan bir
// taşıma (SSE, @defer) eklendiğinde karar burada verilmiş olacaktır.
//
// # Neyi bağlar, neyi bağlamaz
//
// Sarmalayıcı EGZOZU bağlar, BELLEĞİ bağlamaz: gqlgen yanıtı önce belleğe
// kodlar (json.Marshal), yani 200 MiB'lık bir yanıt buraya gelmeden önce
// zaten ayrılmıştır. Belleği bağlayan kapı [alanTekrariSiniri]'dir ve
// çalıştırmadan ÖNCE reddeder. İkisi bu yüzden birbirinin yerine geçmez:
// biri işin yapılmasını engeller, öteki tahminin kaçırdığını yakalar.
type yanitSayaci struct {
	http.ResponseWriter

	sinir   int
	kalan   int
	yazildi bool
	asildi  bool
	kesildi bool
}

// Write sınırı aşmayan baytları geçirir, aşanı reddeder.
func (y *yanitSayaci) Write(p []byte) (int, error) {
	if y.asildi {
		return 0, errYanitCokBuyuk
	}

	if len(p) <= y.kalan {
		n, err := y.ResponseWriter.Write(p)
		y.kalan -= n

		if n > 0 {
			y.yazildi = true
		}

		return n, err
	}

	y.asildi = true

	if y.yazildi {
		y.kesildi = true

		return 0, errYanitCokBuyuk
	}

	// Hata zarfı SAYILMAZ: sayaç istemcinin istediği gövdeyi sınırlamak
	// içindir, sınırın kendi hata mesajını değil.
	if _, err := y.ResponseWriter.Write(asimZarfi(y.sinir)); err != nil {
		return 0, err
	}

	return 0, errYanitCokBuyuk
}

// asimZarfi yanıt sınırı aşımının GraphQL hata zarfını üretir.
//
// Zarf elle değil graphql.Response üzerinden kodlanır: alan adları ve
// sıralaması gqlgen'in ürettiğiyle aynı kalsın diye. İkinci bir zarf biçimi,
// istemciye iki ayrı hata sınıfı olduklarını düşündürürdü.
func asimZarfi(sinir int) []byte {
	hata := gqlerror.Errorf("response exceeds the limit of %d bytes", sinir)
	errcode.Set(hata, kodYanitAsimi)

	// Kodlama hatası MÜMKÜN DEĞİLDİR: zarf yalnızca dize ve sayı taşır.
	// Yine de dönen hata yutulmaz, boş gövde yerine sabit bir zarf yazılır —
	// istemcinin eline hiçbir şey geçmemesindense eksik bir şey geçsin.
	govde, err := json.Marshal(&graphql.Response{Errors: gqlerror.List{hata}})
	if err != nil {
		return []byte(`{"errors":[{"message":"response too large"}],"data":null}`)
	}

	return govde
}

// sorguOnbellegi ayrıştırılmış belgelerin önbelleğidir.
//
// gqlgen'in lru.LRU'su doğrudan kullanılmaz çünkü onun sayabildiği tek şey
// GİRDİ SAYISIDIR ve ölçülen sorun boyuttaydı (bkz. [onbellekGirdiSayisi]).
// Buraya iki kural eklenir:
//
//  1. BOYUT — metni [maxOnbellekBelgeBayt]'tan uzun belge saklanmaz. Girdi
//     sayısıyla çarpıldığında önbelleğin tavanı artık bilinen bir sayıdır.
//  2. KABUL — belge önce ADAY olur, önbelleğe ancak tüm sınır kapılarından
//     geçerse girer.
//
// İkincisinin sebebi gqlgen'in sırasıdır: executor belgeyi ayrıştırıp
// doğruladıktan HEMEN SONRA önbelleğe ekler, oysa derinlik/tekrar/karmaşıklık
// eklentileri ONDAN SONRA koşar. Yani reddedilen — servise hiç ulaşmayan —
// belge de önbellekte yer tutuyordu ve saldırgan, tek bir kotayla vitrinin
// GERÇEK belgelerini önbellekten atabiliyordu. Ölçüldü: 100 × 65 KB'lık
// reddedilmiş belge, runtime.GC sonrası 171,8 MiB kalıcı yığın.
//
// Aday, isteğin kendi OperationContext'inde taşınır: Add'e gelen ctx ile
// eklentilerin gördüğü opCtx aynı isteğe aittir, yani araya paylaşılan bir
// durum konmadan bağ kurulabilir.
type sorguOnbellegi struct {
	girdiler *lru.LRU[*ast.QueryDocument]
	maxBayt  int
}

// gqlgen'in önbellek sözleşmesi derleme zamanında sabitlenir: imza kayarsa
// SetQueryCache'e verilemez ve uç sessizce önbelleksiz kalmaz, derlenmez.
var _ graphql.Cache[*ast.QueryDocument] = (*sorguOnbellegi)(nil)

// onbellekAdayAnahtari adayın OperationContext içindeki adıdır.
//
// gqlgen'in Stats.SetExtension haritası paylaşılan bir alandır; ad, başka bir
// eklentininkiyle çakışmasın diye paket yolunu taşır.
const onbellekAdayAnahtari = "product/graph.queryCacheCandidate"

// onbellekAdayi sınırlardan geçmeyi bekleyen belgedir.
type onbellekAdayi struct {
	anahtar string
	belge   *ast.QueryDocument
}

// yeniSorguOnbellegi verilen girdi ve bayt sınırlarıyla önbellek kurar.
func yeniSorguOnbellegi(girdi, maxBayt int) *sorguOnbellegi {
	return &sorguOnbellegi{
		girdiler: lru.New[*ast.QueryDocument](girdi),
		maxBayt:  maxBayt,
	}
}

// Get belgeyi önbellekten okur.
func (o *sorguOnbellegi) Get(ctx context.Context, anahtar string) (*ast.QueryDocument, bool) {
	return o.girdiler.Get(ctx, anahtar)
}

// Add belgeyi ADAY olarak alır; önbelleğe yazmaz.
//
// Yazma [sorguOnbellegi.kabulEt]'e bırakılır. İsteğe ait bir bağlam yoksa
// (gqlgen'in dışından, örneğin bir testten çağrı) belge doğrudan saklanır:
// kabul edecek bir kapı olmadığında adayı bekletmek, önbelleği tümüyle
// kapatmak olurdu.
func (o *sorguOnbellegi) Add(ctx context.Context, anahtar string, belge *ast.QueryDocument) {
	if len(anahtar) > o.maxBayt {
		return
	}

	if !graphql.HasOperationContext(ctx) {
		o.girdiler.Add(ctx, anahtar, belge)

		return
	}

	graphql.GetOperationContext(ctx).Stats.SetExtension(onbellekAdayAnahtari,
		onbellekAdayi{anahtar: anahtar, belge: belge})
}

// kabulEt bekleyen adayı önbelleğe yazar.
func (o *sorguOnbellegi) kabulEt(ctx context.Context, opCtx *graphql.OperationContext) {
	aday, ok := opCtx.Stats.GetExtension(onbellekAdayAnahtari).(onbellekAdayi)
	if !ok {
		return
	}

	opCtx.Stats.SetExtension(onbellekAdayAnahtari, nil)
	o.girdiler.Add(ctx, aday.anahtar, aday.belge)
}

// onbellekKapisi sınırlardan geçmiş belgeyi önbelleğe alan gqlgen eklentisidir.
//
// Hiçbir belgeyi REDDETMEZ; yaptığı tek şey, kendisinden önce koşan kapıların
// hepsinden geçmiş olmayı önbelleğe girmenin koşulu hâline getirmektir. Bu
// yüzden EN SON kaydedilmelidir (bkz. [NewHandler]) — daha önce kaydedilirse
// arkasındaki kapıların reddettiği belgeler yine saklanır ve düzeltme sessizce
// etkisizleşir.
type onbellekKapisi struct{ onbellek *sorguOnbellegi }

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = onbellekKapisi{}

// ExtensionName eklentinin adını döner.
func (onbellekKapisi) ExtensionName() string { return "QueryCacheAdmission" }

// Validate eklentinin bir önbellekle kurulduğunu doğrular.
func (o onbellekKapisi) Validate(graphql.ExecutableSchema) error {
	if o.onbellek == nil {
		return errors.New("graph: önbellek kapısı önbelleksiz kurulamaz")
	}

	return nil
}

// MutateOperationContext belgeyi önbelleğe kabul eder.
func (o onbellekKapisi) MutateOperationContext(
	ctx context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	o.onbellek.kabulEt(ctx, opCtx)

	return nil
}

// Taşımanın ürettiği hataların kodları.
//
// gqlgen ayrıştırmadan itibaren her protokol hatasına bir kod koyar
// (GRAPHQL_PARSE_FAILED, GRAPHQL_VALIDATION_FAILED) ve bu paketin kapıları da
// koyar; kodsuz kalan tek sınıf, belge daha OKUNAMADAN başarısız olan
// taşımadır. Bu iki kod o boşluğu doldurur, böylece istemci "belge hiç
// okunamadı" durumunu da öteki protokol hataları gibi extensions.code'dan
// ayırt eder.
//
// Biçim BÜYÜK HARFTİR ve gerekçesi sınır kodlarınınkiyle aynıdır (bkz.
// limits.go): belge çalıştırıcıya ulaşmamıştır, yani bunlar servis hatası
// değil protokol hatasıdır.
const (
	// kodGovdeAsimi gövde sınırını aşan isteğin GraphQL zarfındaki kodudur.
	//
	// AYNI koşul, boyutunu Content-Length ile BİLDİREN istemciye çekirdeğin
	// zarfıyla ve product_graphql_body_too_large koduyla döner; iki kod tek bir
	// koşulun iki zarftaki adıdır. Zarfların neden ayrıldığı
	// [govdeSiniri]'ndedir.
	kodGovdeAsimi = "REQUEST_BODY_TOO_LARGE"

	// kodGovdeCozulemedi JSON olarak çözülemeyen istek gövdesinin kodudur.
	kodGovdeCozulemedi = "REQUEST_DECODE_FAILED"
)

// oneriBaslangici gqlparser'ın öneri cümlesinin başladığı yerdir.
//
// Doğrulayıcının bütün öneri yardımcıları (SuggestListQuoted,
// SuggestListUnquoted, Suggestf ve fields_on_correct_type'ın satır içi
// fragment önerisi) mesajın SONUNA tek bir cümle ekler ve hepsi bu dizeyle
// başlar. Kesim noktasının tek olmasının sebebi budur: teşhis cümlesi (hangi
// alan, hangi tip) yerinde kalır, yalnızca ADLARI SAYAN kısım düşer.
const oneriBaslangici = " Did you mean"

// hataSunucusu hataları KAYNAĞINA göre iki politikaya ayırır.
//
// Ayrım hatanın TİPİNE değil KAYNAĞINA bakar ve bu bir düzeltmedir: koşul bir
// zamanlar "*coreerrors.Error mi" idi ve tipsiz her hata — pq'nun bağlantı
// dizesini, parolayı ve SQL metnini taşıyan hatası dâhil — istemciye OLDUĞU
// GİBİ gidiyor, üstelik hiç loglanmıyordu. Oysa çekirdeğin kuralı tam
// tersidir: tipsiz hata KindInternal sayılır, mesajı maskelenir ve gerçek hata
// kaydedilir. "Tipli olmayanı geçir" satırı, kaçınmak istediği İKİNCİ TANIMIN
// ta kendisiydi — hem de çekirdekle ters düşen bir tanım.
//
// # Kaynak, "gqlerror mi" sorusuyla ANLAŞILMAZ
//
// Buraya gelen hemen her hata zaten bir *gqlerror.Error'dur: gqlgen resolver
// hatasını presenter'a vermeden önce graphql.ErrorOnPath ile SARAR
// (graphql.AddError'ın ilk işi budur). Yani tipe bakan bir ayrım, pq'nun
// hatasını da protokol hatası sayardı — ölçüldü.
//
// Ayrım gqlerror'ın NE TAŞIDIĞINA bakar: sarmalayıcı olarak üretilenin içinde
// yabancı bir hata durur (gqlerror.WrapPath onu Err alanına koyar), belgenin
// kendi hataları ise gqlerror.Errorf ile SIFIRDAN kurulur ve hiçbir şey
// sarmaz. "Sardığı bir şey var mı" sorusu bu yüzden tam olarak "bu hatayı
// GraphQL boru hattı üretti mi, yoksa yalnızca giydirdi mi" sorusudur.
//
// İki dal:
//
//   - PROTOKOL — hiçbir şey sarmayan gqlerror'ı ayrıştırma, doğrulama, sınır
//     kapıları ya da taşıma üretmiştir; hepsi istemcinin YAZDIĞI istekle
//     ilgilidir ve maskelenirlerse yüzey hata ayıklanamaz hâle gelir. İki
//     istisnayla olduğu gibi bırakılır (bkz. [protokolHatasi]).
//   - SERVİS — geri kalan her şey, TİPLİ OLSUN OLMASIN,
//     corehttp.WriteError'a yazdırılır ve yazdığı zarf geri okunur. Hangi
//     hatanın istemciye olduğu gibi verilebileceği kuralı burada İKİNCİ KEZ
//     yazılmaz; ayrıştıkları gün ikinci okuma yüzeyi, birincisinin gizlediği
//     ayrıntıyı (DSN, sorgu metni, dosya yolu) sızdırırdı.
//
// Yan kazanç: kod, mesaj, ayrıntılar ve istek kimliği iki yüzeyde AYNI olur;
// istemci hata kodlarını tek bir sözlükten okur.
//
// [Options] ALINIR çünkü öneri kesimi iç gözlem anahtarına bağlıdır (bkz.
// [Options.IntrospectionDisabled]); sunucu kurulurken bağlanır, her istekte
// yeniden okunmaz.
func hataSunucusu(opts Options) graphql.ErrorPresenterFunc {
	return func(ctx context.Context, err error) *gqlerror.Error {
		// Yol ve konum bilgisi gqlgen'in kendi sunucusundan alınır; hangi alanın
		// başarısız olduğunu yalnızca o bilir.
		sunulan := graphql.DefaultErrorPresenter(ctx, err)

		var protokol *gqlerror.Error
		if errors.As(err, &protokol) && protokol.Unwrap() == nil {
			return protokolHatasi(ctx, sunulan, opts.IntrospectionDisabled)
		}

		return servisHatasi(ctx, sunulan, err)
	}
}

// protokolHatasi belgeye ait hatayı istemciye sunar.
//
// Bu hatalar MASKELENMEZ: istemcinin yazdığı isteği anlatırlar ve "Cannot
// query field x" yerine "sunucu hatası" gören istemci sorgusunu düzeltemez.
// İki istisna vardır ve ikisi de mesajın, istemcinin ZATEN BİLMEDİĞİ bir şeyi
// taşıdığı yerlerdir:
//
//  1. KODSUZ hata taşımadan gelir ve metnini biz yazmayız. Ölçüldü: bugünkü
//     POST taşıması JSON'u çözemediğinde HAM GÖVDEYİ mesaja ekliyor
//     (transport/http_post.go), yani 64 KiB'a kadar saldırgan denetimindeki
//     metin yanıta ve yanıtı kaydeden ara katmanların loglarına giriyordu.
//     Metin [tasimaHatasi] ile değiştirilir.
//  2. İç gözlem KAPALIYSA öneri cümlesi kesilir; gerekçe
//     [Options.IntrospectionDisabled]'dadır.
func protokolHatasi(
	ctx context.Context,
	sunulan *gqlerror.Error,
	icGozlemKapali bool,
) *gqlerror.Error {
	if kod, _ := sunulan.Extensions["code"].(string); kod == "" {
		tasimaHatasi(ctx, sunulan)

		return sunulan
	}

	if icGozlemKapali {
		sunulan.Message = oneriyiKes(sunulan.Message)
	}

	return sunulan
}

// tasimaHatasi taşımanın yazdığı mesajı bizim metnimizle değiştirir.
//
// İki sebep AYRILIR çünkü istemcinin yapacağı şey farklıdır: gövdeyi
// küçültmek ile geçerli JSON göndermek aynı düzeltme değildir. Ayrım bir metin
// eşleşmesi DEĞİL, [govdeSiniri]'nin kaydettiği ölçümdür.
func tasimaHatasi(ctx context.Context, sunulan *gqlerror.Error) {
	if govdeAsildi(ctx) {
		sunulan.Message = fmt.Sprintf(
			"request body exceeds the limit of %d bytes", maxSorguBayt)
		errcode.Set(sunulan, kodGovdeAsimi)

		return
	}

	sunulan.Message = "request body is not valid JSON"
	errcode.Set(sunulan, kodGovdeCozulemedi)
}

// oneriyiKes doğrulama mesajından ad SAYAN cümleyi atar.
func oneriyiKes(mesaj string) string {
	if i := strings.Index(mesaj, oneriBaslangici); i >= 0 {
		return mesaj[:i]
	}

	return mesaj
}

// servisHatasi hatayı çekirdeğin hata politikasıyla sunar.
//
// Gövde burada YENİDEN KURULMAZ: corehttp.WriteError'a yazdırılır ve yazdığı
// zarf geri okunur. Maskeleme de loglama da o çağrının içinde olur, yani
// tipsiz hata bu yüzeyde de KindInternal sayılır ve gerçek metin yalnızca loga
// gider.
func servisHatasi(ctx context.Context, sunulan *gqlerror.Error, err error) *gqlerror.Error {
	yakalayici := &yanitYakalayici{basliklar: http.Header{}}
	corehttp.WriteError(ctx, yakalayici, err)

	var zarf corehttp.ErrorResponse
	if json.Unmarshal(yakalayici.govde.Bytes(), &zarf) != nil {
		// Buraya düşmek çekirdeğin kendi zarfını çözemediği anlamına gelir.
		// Hatayı YUTMAK yerine gqlgen'in sunduğu hâli döneriz; ama mesajı
		// maskelenmemiş olabileceği için sınıf adına indirilir.
		sunulan.Message = hataSinifi(err).String()

		return sunulan
	}

	sunulan.Message = zarf.Error.Message
	sunulan.Extensions = map[string]any{"code": zarf.Error.Code}

	if zarf.Error.RequestID != "" {
		sunulan.Extensions["request_id"] = zarf.Error.RequestID
	}

	if len(zarf.Error.Details) > 0 {
		sunulan.Extensions["details"] = zarf.Error.Details
	}

	return sunulan
}

// hataSinifi hatanın sınıfını çekirdekle AYNI kuralla belirler.
//
// Tipsiz (ve tipli-nil) hata KindInternal sayılır; corehttp.StatusFor de aynı
// varsayımla çalışır. Sınıf adı yalnızca zarfın çözülemediği yolda kullanılır,
// yani istemciye giden metnin son çaresidir — sınıflandırılmamış bir hatanın
// oradan istemci hatası gibi çıkması, maskelemenin kaçağı olurdu.
func hataSinifi(err error) coreerrors.Kind {
	var typed *coreerrors.Error
	if coreerrors.As(err, &typed) && typed != nil {
		return typed.Kind
	}

	return coreerrors.KindInternal
}

// panikYakala resolver paniklerini yapılandırılmış logger'a yazar.
//
// gqlgen'in varsayılanı yığın izini doğrudan os.Stderr'a basar; bu depoda
// loglar slog ile yapılandırılmış olduğu için o satır ne istek kimliği taşır
// ne de toplayıcıya düzgün girer.
//
// Panik İKİ satır üretir ve bu bilinçlidir: buradaki satır yığın izini taşır
// (başka hiçbir yerde yoktur), [hataSunucusu]'nun çağırdığı
// corehttp.WriteError'ınki ise istek kimliğini ve istemciye ne döndüğünü.
func panikYakala(ctx context.Context, panikDegeri any) error {
	corehttp.LoggerFromContext(ctx).ErrorContext(ctx, "graphql resolver panikledi",
		"panic", panikDegeri,
		"stack", string(debug.Stack()),
		"request_id", corehttp.RequestIDFromContext(ctx),
	)

	return coreerrors.Internal(codePanic, "graphql isteği işlenemedi")
}

// yanitYakalayici corehttp.WriteError'ın yazdığı yanıtı belleğe alır.
//
// httptest.ResponseRecorder KULLANILMADI: o paket test ikilisine aittir ve
// üretim kodunda kullanılması, test yardımcılarını sunucu ikilisine taşırdı.
// İhtiyaç duyulan yüzey zaten üç metottur.
type yanitYakalayici struct {
	basliklar http.Header
	govde     bytes.Buffer
}

// Header yazılacak başlıkları döner.
func (y *yanitYakalayici) Header() http.Header { return y.basliklar }

// WriteHeader durum kodunu YOK SAYAR.
//
// GraphQL yanıtının HTTP durumu 200'dür; hatanın sınıfı gövdedeki koda
// bakılarak anlaşılır. Kodu saklayıp extensions'a yazmak, istemciye asla
// görmeyeceği bir durum kodu bildirmek olurdu.
func (y *yanitYakalayici) WriteHeader(int) {}

// Write gövdeyi belleğe alır.
func (y *yanitYakalayici) Write(p []byte) (int, error) { return y.govde.Write(p) }

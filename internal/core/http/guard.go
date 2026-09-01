package http

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// Scoped bir middleware yığınını YALNIZCA belirli bir yol öneki altında
// çalıştırır.
//
// # Neden gerekli
//
// Modüller route'larını TAM YOLLA ve TEK bir router üzerine kaydeder
// (bkz. herhangi bir modülün api.Handler.Routes): "/admin/v1" için alt router
// açmak, aynı deseni ikinci kez mount eden ikinci modülde chi'yi panikletirdi.
// Bunun bedeli, chi'nin doğal kapsamlama aracının (Route/Group) elden
// gitmesidir: r.Use ile eklenen middleware TÜM isteklere, /health ve /ready
// dâhil, uygulanır.
//
// Scoped o bedeli öder: kapsam, router ağacında değil middleware'in kendi
// içinde kurulur.
//
//	router.Use(corehttp.Scoped("/admin/v1", []string{authapi.LoginPath},
//	    corehttp.RequireAdmin(auth)))
//
// # Eşleşme kuralı
//
// Önek SEGMENT sınırında eşleşir: "/admin/v1" yalnızca "/admin/v1" ve
// "/admin/v1/..." yollarını yakalar, "/admin/v1x" yakalanmaz. Aksi hâlde
// yeni bir "/admin/v1x" öneki sessizce korumaya girer ve orada tanımlanmış
// hiçbir uç, tasarlanmadığı bir middleware'den geçerdi.
//
// # Neden r.URL.Path
//
// chi, yönlendirmeyi RawPath doluysa onun üzerinden yapar; biz Path (çözülmüş
// hâl) üzerinden bakarız. Fark yalnızca kodlanmış istekte ortaya çıkar
// (örn. "/admin%2Fv1/users"): Path korumayı DEVREYE sokar, chi ise route'u
// bulamayıp 404 döner. Yani ayrışma her zaman KORUMA LEHİNEDİR.
//
// # exempt
//
// exempt, önek altında olmasına rağmen middleware'den MUAF tutulacak tam
// yollardır (örn. giriş ucu: kimliği doğrulanacak istek, kimliği daha yeni
// kuracaktır). Eşleşme tam yol üzerindendir; metoda bakmaz — muaf bir yolun
// tanımsız metodu zaten router tarafından reddedilir.
func Scoped(prefix string, exempt []string, mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	muaf := make(map[string]struct{}, len(exempt))
	for _, yol := range exempt {
		muaf[yol] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		// Zincir bir kez kurulur; her istekte yeniden sarmalamak, aynı işi
		// istek başına tekrarlamak olurdu.
		korumali := next
		for i := len(mws) - 1; i >= 0; i-- {
			if mws[i] == nil {
				continue
			}
			korumali = mws[i](korumali)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !kapsamda(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := muaf[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			korumali.ServeHTTP(w, r)
		})
	}
}

// kapsamda yolun öneği segment sınırında taşıyıp taşımadığını bildirir.
func kapsamda(path, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}

	prefix = strings.TrimSuffix(prefix, "/")

	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// GuardOptions [APIGuards] yığınının girdileridir.
type GuardOptions struct {
	// Authenticator kimlikleri çözer. nil ise koruma yine takılır ve TÜM
	// istekleri reddeder (ADR 0007): korumasız bir yönetim yüzeyi, sessizce
	// açık kalmaktansa gürültüyle kapalı kalmalıdır.
	Authenticator Authenticator
	// AdminPrefix yönetim yüzeyinin yol önekidir; boşsa [DefaultAdminPrefix].
	AdminPrefix string
	// StorePrefix mağaza yüzeyinin yol önekidir; boşsa [DefaultStorePrefix].
	StorePrefix string
	// AdminExempt yönetim yüzeyinde kimlik doğrulamadan MUAF tutulacak tam
	// yollardır — pratikte yalnızca giriş ucu.
	//
	// Çekirdek bu yolu kendisi BİLEMEZ: auth bir modüldür ve çekirdek
	// modülleri import edemez (Prensip 2.4). Yol, uygulamayı kuran taraftan
	// parametre olarak gelir.
	AdminExempt []string
	// Limiter hız sınırlayıcıdır; nil ise hız sınırı takılmaz.
	Limiter RateLimiter
	// LimitKey istemciyi tanımlayan anahtarı üretir; nil ise [ClientIPKey].
	LimitKey KeyFunc
	// IdempotencyStore idempotency kayıtlarının deposudur; nil ise
	// idempotency takılmaz.
	IdempotencyStore IdempotencyStore
	// IdempotencyExempt idempotency halkasından MUAF tutulacak tam yollardır.
	//
	// [Idempotency]'nin "geçici arızayı kalıcıya çevirme" koruması yalnızca
	// HTTP DURUMUNA bakar: 5xx kaydedilmez, gerisi kaydedilir. Yanıtını hata
	// hâlinde de 200 ile veren bir yüzeyde o koruma HİÇ devreye girmez ve
	// geçici bir iç hata TTL boyunca (varsayılan 24 saat) çalınır — arıza
	// giderilse bile istemci aynı hata gövdesini almaya devam eder. Bu alan,
	// böyle bir ucu yığına hiç sokmamanın yoludur.
	//
	// Muafiyet en çok OKUMA yüzeylerine yakışır: idempotency kaydının işi yan
	// etkiyi (tahsilat, sipariş) ikinci kez üretmemektir ve okuma ucunda
	// üretecek yan etki yoktur — kayıt yalnızca bayat veri saklar. Bir okuma
	// ucu POST olduğu için (gövdesinde sorgu taşıyan GraphQL gibi) yığına
	// takılıyorsa, oraya ait olan şey muafiyettir.
	//
	// Çekirdek bu yolu kendisi BİLEMEZ: uç bir modülde yaşar ve çekirdek
	// modülleri import edemez (Prensip 2.4). Yol, [GuardOptions.AdminExempt]
	// gibi, uygulamayı kuran taraftan parametre olarak gelir.
	//
	// Muafiyet YALNIZCA idempotency halkasınadır: yol hız sınırından ve
	// kimlikten geçmeye devam eder.
	IdempotencyExempt []string
	// PublishableKeyHeader publishable anahtarın okunacağı başlıktır; boşsa
	// [PublishableKeyHeader].
	PublishableKeyHeader string
	// OpenPrefixes kimlik İSTEMEYEN ama yine de hız sınırına tabi olması
	// gereken yol önekleridir (örn. yüklenen dosyaların sunulduğu "/files").
	//
	// Kimlik ve kota AYRI kararlardır. Bir uç, istemcisi başlık gönderemediği
	// için kimliksiz olabilir (vitrindeki <img>), ama bu onun bedava olduğu
	// anlamına gelmez: her istek bir veritabanı okuması ya da disk erişimi
	// yapıyorsa, kotasızlık kimlik doğrulama maliyeti bile ödemeden atılabilen
	// bir yük demektir.
	//
	// Sağlık uçları BURAYA KONMAZ: orkestratörün gördüğü yolun kotaya takılması,
	// sağlıklı bir örneği trafikten çektirirdi.
	OpenPrefixes []string
}

// Varsayılan API önekleri.
const (
	// DefaultAdminPrefix yönetim yüzeyinin yol önekidir.
	DefaultAdminPrefix = "/admin/v1"
	// DefaultStorePrefix mağaza yüzeyinin yol önekidir.
	DefaultStorePrefix = "/store/v1"
)

// APIGuards iki API yüzeyini koruyan middleware yığınını sırayla üretir.
//
// Yığın [RouterOptions.Middlewares] alanına verilir. Sağlık uçları
// (/health, /ready) hiçbir önekle eşleşmediği için yığından etkilenmez.
//
// # Sıra
//
//  1. HIZ SINIRI — kimlik doğrulamadan ÖNCE. Aksi hâlde parola deneyen bir
//     saldırgan her denemede kimlik doğrulama maliyetini (bcrypt + veritabanı
//     araması) ödetir, kotası ancak ondan sonra düşerdi. Sınır önce çalışınca
//     reddedilen istek neredeyse bedava olur.
//  2. KİMLİK — giriş ucu hariç tüm yönetim yüzeyi, publishable anahtar
//     olmadan tüm mağaza yüzeyi reddedilir.
//  3. IDEMPOTENCY — kimlikten SONRA. Kayıt anahtarı çağıranın kimliğiyle
//     birlikte tutulur (bkz. [Idempotency]); kimlik henüz çözülmemişken
//     çalışsaydı iki farklı çağıranın aynı anahtarı çakışırdı.
//     [GuardOptions.IdempotencyExempt] yolları bu halkayı — YALNIZCA bu
//     halkayı — atlar.
//
// Bu fonksiyonun çekirdekte durmasının sebebi, sıranın TEK bir yerde
// yazılmasıdır: uygulama ile uçtan uca testler aynı yığını kurar, yani test
// ettiğimiz koruma üretimdekinin ta kendisidir.
func APIGuards(opts GuardOptions) []func(http.Handler) http.Handler {
	admin := opts.AdminPrefix
	if admin == "" {
		admin = DefaultAdminPrefix
	}

	store := opts.StorePrefix
	if store == "" {
		store = DefaultStorePrefix
	}

	yigin := make([]func(http.Handler) http.Handler, 0, 6)

	if opts.Limiter != nil {
		anahtar := opts.LimitKey
		if anahtar == nil {
			anahtar = ClientIPKey
		}

		sinir := RateLimit(opts.Limiter, anahtar)
		yigin = append(yigin, Scoped(admin, nil, sinir), Scoped(store, nil, sinir))

		// KİMLİKSİZ önekler de sınırlanır.
		//
		// Kimliksiz olmak "korumasız" demek DEĞİLDİR: kimlik istemeyen bir uç,
		// tam da bu yüzden kotası olmayan bir uç olmamalıdır. Dosya sunumu
		// örnektir — vitrindeki <img> etiketi başlık gönderemez, dolayısıyla uç
		// kimliksizdir; ama her istek bir veritabanı okuması yapar ve
		// sınırsızlık, kimlik doğrulama maliyeti ödemeden atılabilen bir yük
		// demektir.
		for _, onek := range opts.OpenPrefixes {
			yigin = append(yigin, Scoped(onek, nil, sinir))
		}
	}

	yigin = append(yigin,
		Scoped(admin, opts.AdminExempt, RequireAdmin(opts.Authenticator)),
		Scoped(store, nil, RequireStore(opts.Authenticator, opts.PublishableKeyHeader)),
	)

	if opts.IdempotencyStore != nil {
		idem := Idempotency(opts.IdempotencyStore)
		// Muaf listesi iki önek için de AYNIDIR: bir yol zaten yalnızca birinin
		// altındadır, listeyi ikiye bölmek çağırana anlamsız bir karar
		// sordurmak olurdu.
		yigin = append(yigin,
			Scoped(admin, opts.IdempotencyExempt, idem),
			Scoped(store, opts.IdempotencyExempt, idem))
	}

	return yigin
}

// codeAuthNotBound kimlik doğrulayıcının henüz bağlanmadığını bildirir.
const codeAuthNotBound = "auth_not_bound"

// DeferredAuthenticator kimlik doğrulayıcıyı SONRADAN bağlanabilir kılar.
//
// # Neden gerekli
//
// Koruma middleware'i router kurulurken takılmalıdır — chi, route
// kaydedildikten sonra r.Use çağrılmasını panikle reddeder. Kimlik
// doğrulayıcı ise auth modülü Register olduğunda, yani modül bootstrap'ı
// SIRASINDA doğar. İki an aynı değildir ve router, modüllerin route'larını
// alabilmek için bootstrap'tan önce var olmak zorundadır.
//
// Bu tip aradaki boşluğu kapatır: router kurulurken takılır, doğrulayıcı
// hazır olunca [DeferredAuthenticator.Bind] ile doldurulur.
//
// # Bağlanmadan gelen istek
//
// REDDEDİLİR (ADR 0007). Korumasız bir yönetim yüzeyi, sessizce açık
// kalmaktansa gürültüyle kapalı kalmalıdır. Uygulamayı kuran tarafın
// bootstrap'tan hemen sonra bağlaması ve bağlayamazsa açılışı durdurması
// beklenir; bu 401, o sözleşmenin unutulduğu durumda son savunmadır.
//
// Eşzamanlı kullanıma güvenlidir: bağlama bir kez, okuma her istekte olur.
type DeferredAuthenticator struct {
	// deger her zaman bir authnHolder tutar; atomic.Value tek bir somut tip
	// ister ve arayüz değerini doğrudan saklamak dinamik tip değiştiğinde
	// panik üretirdi.
	deger atomic.Value
}

// authnHolder arayüz değerini atomic.Value için tek bir somut tipe sarar.
type authnHolder struct {
	iç Authenticator
}

var _ Authenticator = (*DeferredAuthenticator)(nil)

// Bind gerçek doğrulayıcıyı yerine koyar.
func (d *DeferredAuthenticator) Bind(a Authenticator) {
	d.deger.Store(authnHolder{iç: a})
}

// coz bağlanmış doğrulayıcıyı döner; bağlanmamışsa hata.
func (d *DeferredAuthenticator) coz() (Authenticator, error) {
	h, ok := d.deger.Load().(authnHolder)
	if !ok || h.iç == nil {
		return nil, coreerrors.Unauthorized(codeAuthNotBound,
			"kimlik doğrulayıcı henüz bağlanmadı")
	}

	return h.iç, nil
}

// AuthenticateAdmin çağrıyı bağlı doğrulayıcıya iletir.
func (d *DeferredAuthenticator) AuthenticateAdmin(
	ctx context.Context, scheme, credential string,
) (Principal, error) {
	a, err := d.coz()
	if err != nil {
		return Principal{}, err
	}

	return a.AuthenticateAdmin(ctx, scheme, credential)
}

// AuthenticateStore çağrıyı bağlı doğrulayıcıya iletir.
func (d *DeferredAuthenticator) AuthenticateStore(ctx context.Context, key string) (Principal, error) {
	a, err := d.coz()
	if err != nil {
		return Principal{}, err
	}

	return a.AuthenticateStore(ctx, key)
}

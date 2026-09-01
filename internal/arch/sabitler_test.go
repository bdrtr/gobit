package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/config"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/auth"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/internal/modules/file"
	"github.com/bdrtr/gobit/internal/modules/file/local"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestSaglayiciKayitAdlariUyusuyor eklenti paketiyle modüllerin sağlayıcı
// kayıt adlarının aynı olduğunu doğrular.
//
// Çekirdek modülleri import EDEMEZ (Prensip 2.4), bu yüzden
// [coreplugin.PaymentProvidersName] modülün sabitine bağlanamaz; değeri elle
// tekrarlar. Elle tekrarlanan her sabit, sessizce ayrışmaya açıktır: adlardan
// biri değişirse eklenti sağlayıcısı container'da hiçbir şey bulamaz ve
// "stripe kurulu" sanılan bir kurulum hiç ödeme alamaz.
//
// Bu test, o bağı DERLEME zamanına taşır. Burada yaşamasının nedeni,
// arch paketinin test-only olması ve hem çekirdeği hem modülleri import
// edebilmesidir; çekirdeğin kendisi bu testi barındıramazdı.
//
// Bildirim iddiasının bedeli diğerlerinden FARKLIDIR ve daha sessizdir: ödeme
// ayrışırsa müşteri ödeyemez ve bunu hemen söyler, bildirim ayrışırsa eklenti
// sağlayıcısı hiç kaydedilemez ve kurulum sipariş onaylarını yalnızca loga
// yazmaya devam eder — hiç hata üretmeden.
//
// DÖRDÜNCÜ sağlayıcının ([coreplugin.FileProvidersName]) iddiası bir süre
// EKSİK kaldı: sözleşme ve kayıt noktası, onları tüketecek modülden önce
// yazılmıştı ve olmayan bir paketi import etmek derlemeyi kırardı. Dosya
// modülü geldiğinde satır eklendi — çünkü eksik bir iddia, yanlış bir iddia
// gibi testi patlatmaz, hiç sesini çıkarmaz.
//
// Dosyada ayrışmanın bedeli bildiriminkinden de sinsidir: eklenti sağlayıcısı
// (örn. S3) hiç kaydedilemez, kurulum yerel diske yazmaya devam eder ve fark
// ancak kap yeniden başlatılıp yüklenen görseller kaybolduğunda edilir —
// üstelik o an ürün kayıtlarındaki adresler hâlâ yerinde durur.
func TestSaglayiciKayitAdlariUyusuyor(t *testing.T) {
	t.Parallel()

	assert.Equal(t, payment.ProvidersName, coreplugin.PaymentProvidersName,
		"eklenti paketindeki ödeme sağlayıcı kayıt adı payment modülüyle aynı olmalı")
	assert.Equal(t, fulfillment.ProvidersName, coreplugin.FulfillmentProvidersName,
		"eklenti paketindeki kargo sağlayıcı kayıt adı fulfillment modülüyle aynı olmalı")
	assert.Equal(t, notification.ProvidersName, coreplugin.NotificationProvidersName,
		"eklenti paketindeki bildirim sağlayıcı kayıt adı notification modülüyle aynı olmalı")
	assert.Equal(t, file.ProvidersName, coreplugin.FileProvidersName,
		"eklenti paketindeki dosya sağlayıcı kayıt adı file modülüyle aynı olmalı")
}

// TestDosyaVarsayilanSaglayicisiConfigleUyusuyor config'in varsayılan dosya
// sağlayıcısının GERÇEKTEN kayıtlı bir sağlayıcıya karşılık geldiğini
// doğrular.
//
// İki sabit iki ayrı pakette yaşar ve aralarında derleyici bağı YOKTUR:
// çekirdek modülleri import edemez (Prensip 2.4), bu yüzden
// [config.DefaultFileProvider] local.ID'ye bağlanamaz ve değeri elle tekrarlar.
//
// Ayrışmanın bedeli, hiçbir ortam değişkeni verilmemiş bir kurulumun
// AÇILMAMASI olurdu: cmd/server, seçili sağlayıcıyı kayıtta bulamayınca
// açılışı durdurur (bkz. dosyaSaglayicisiniDogrula). Yani ayrışma sessiz
// değil, ama en kötü anda — hiçbir şeyi yapılandırmamış birinin ilk
// denemesinde — patlardı.
func TestDosyaVarsayilanSaglayicisiConfigleUyusuyor(t *testing.T) {
	t.Parallel()

	varsayilanSaglayiciIddiasi(t, "dosya", local.ID, config.DefaultFileProvider, file.DefaultProviderID)
}

// TestDosyaIzinListesiCekirdekSabitleriyleUyusuyor config'in varsayılan izin
// listesinin çekirdeğin içerik tipi sabitleriyle aynı kümeyi anlattığını
// doğrular.
//
// Değer config'te tek bir DİZE olarak durur (envDefault etiketleri sabit
// referansı kabul etmez) ve çekirdekte dört ayrı sabit olarak. Ayrışma iki
// yönde de sessizdir: listeye yazım hatasıyla girmiş bir tip hiçbir dosyayı
// geçirmez (kimse fark etmez, "o biçim desteklenmiyor" sanılır), listeden
// düşen bir tip ise dünkü yüklemelerin bugün reddedilmesi demektir.
//
// SVG'nin listede OLMADIĞI ayrıca iddia edilir: bu, unutulmuş bir eksiklik
// değil VERİLMİŞ bir karardır (SVG bir belgedir, script taşır ve aynı
// kökenden sunulduğunda depolanmış XSS olur). Kararın bir testi yoksa, bir
// gün "eksik" diye tamamlanır.
func TestDosyaIzinListesiCekirdekSabitleriyleUyusuyor(t *testing.T) {
	t.Parallel()

	tipler := strings.Split(config.DefaultFileAllowedTypes, ",")

	assert.ElementsMatch(t, []string{
		coreprovider.ContentTypeJPEG,
		coreprovider.ContentTypePNG,
		coreprovider.ContentTypeGIF,
		coreprovider.ContentTypeWebP,
	}, tipler, "config'in varsayılan izin listesi çekirdeğin sabitleriyle aynı olmalı")

	assert.NotContains(t, tipler, "image/svg+xml",
		"SVG varsayılan izin listesinde OLMAMALI; belge olduğu için depolanmış XSS taşır")
}

// TestBildirimVarsayilanSaglayicisiConfigleUyusuyor config'in varsayılan
// sağlayıcı adının GERÇEKTEN kayıtlı bir sağlayıcıya karşılık geldiğini
// doğrular.
//
// İki sabit iki ayrı pakette yaşar ve aralarında derleyici bağı YOKTUR:
// çekirdek modülleri import edemez (Prensip 2.4), bu yüzden
// [config.DefaultNotificationProvider] logonly.ID'ye bağlanamaz ve değeri elle
// tekrarlar.
//
// Ayrışmanın bedeli, hiçbir ortam değişkeni verilmemiş bir kurulumun AÇILMAMASI
// olurdu: cmd/server, seçili sağlayıcıyı kayıtta bulamayınca açılışı durdurur.
// Yani ayrışma sessiz değil, ama en kötü anda — hiçbir şeyi yapılandırmamış
// birinin ilk denemesinde — patlardı.
func TestBildirimVarsayilanSaglayicisiConfigleUyusuyor(t *testing.T) {
	t.Parallel()

	varsayilanSaglayiciIddiasi(t, "bildirim", logonly.ID,
		config.DefaultNotificationProvider, notification.DefaultProviderID)
}

// TestGraphQLSinirVarsayilanlariConfigleUyusuyor GraphQL sertleştirme
// sınırlarının İKİ ayrı yerdeki varsayılanlarının aynı olduğunu doğrular.
//
// Sınırları UYGULAYAN taraf product modülünün graph paketidir; okuyan taraf
// çekirdeğin yapılandırmasıdır ve çekirdek modülleri import EDEMEDİĞİ için
// (Prensip 2.4) sabitlere bağlanamaz, değerlerini elle tekrarlar.
//
// Ayrışmanın bedeli, bu dosyadaki komşularının aksine, açılışta patlamaz —
// hiç patlamaz. Ortam değişkeni vermeyen bir kurulum config'in sayısıyla
// çalışır, gömülü kullanım (product'ı kendi binary'sine alan biri) graph'ın
// sayısıyla; ikisi ayrıştığı gün aynı belge bir kurulumda geçer, ötekinde
// reddedilir ve iki taraf da kendi belgesinde yazanı doğru sanar.
//
// İç gözlemin varsayılanı da buraya dâhildir çünkü graph tarafında alan
// OLUMSUZ adlandırılmıştır (IntrospectionDisabled): sıfır değeri "açık"
// demektir ve config'in varsayılanı da true, yani açık olmalıdır. İkisi
// ayrışırsa yüzey, kimsenin istemediği hâlde kapanır.
func TestGraphQLSinirVarsayilanlariConfigleUyusuyor(t *testing.T) {
	t.Parallel()

	// Eşleme graph.Options'ın SAYISAL alan adından çekirdeğin varsayılanına
	// gider. Liste elle yazılır ama EKSİK KALAMAZ: aşağıdaki yansıma denetimi,
	// Options'a eklenip buraya eklenmeyen her sınırı düşürür.
	beklenen := map[string]int{
		"MaxDepth":              config.DefaultGraphQLMaxDepth,
		"MaxComplexity":         config.DefaultGraphQLMaxComplexity,
		"MaxFieldRepetition":    config.DefaultGraphQLMaxFieldRepetition,
		"MaxResponseBytes":      config.DefaultGraphQLMaxResponseBytes,
		"MaxIntrospectionRoots": config.DefaultGraphQLMaxIntrospectionRoots,
		"MaxIntrospectionDepth": config.DefaultGraphQLMaxIntrospectionDepth,
		"MaxSelections":         config.DefaultGraphQLMaxSelections,
	}
	uygulanan := map[string]int{
		"MaxDepth":              graph.DefaultMaxDepth,
		"MaxComplexity":         graph.DefaultMaxComplexity,
		"MaxFieldRepetition":    graph.DefaultMaxFieldRepetition,
		"MaxResponseBytes":      graph.DefaultMaxResponseBytes,
		"MaxIntrospectionRoots": graph.DefaultMaxIntrospectionRoots,
		"MaxIntrospectionDepth": graph.DefaultMaxIntrospectionDepth,
		"MaxSelections":         graph.DefaultMaxSelections,
	}

	// Yansıma denetimi kuralın KENDİSİNİ zorlar: "her sınırın bir ortam
	// değişkeni ve eşleşen bir varsayılanı vardır". Elle yazılmış bir
	// karşılaştırma listesi bu kuralı yalnızca bugün için uygular; yarın
	// eklenen sekizinci sınır sessizce dışarıda kalır ve operatör onu
	// ayarlayamadığını ancak üretimde fark eder. Bu tam olarak GRAPHQL
	// sertleştirmesinde bir kez YAŞANMIŞ durumdur.
	tip := reflect.TypeOf(graph.Options{})
	for i := range tip.NumField() {
		alan := tip.Field(i)
		if !strings.HasPrefix(alan.Name, "Max") {
			continue
		}
		require.Contains(t, beklenen, alan.Name,
			"graph.Options.%s bir sınırdır ama çekirdekte varsayılanı YOK; "+
				"her sınırın bir ortam değişkeni ve eşleşen bir varsayılanı olmalı, "+
				"aksi hâlde operatör onu ayarlayamaz", alan.Name)
	}

	for ad, cfgDeger := range beklenen {
		assert.Equal(t, uygulanan[ad], cfgDeger,
			"config'in %s varsayılanı sınırı uygulayan paketinkiyle aynı olmalı", ad)
	}

	varsayilan := graph.Options{}
	assert.Equal(t, config.DefaultGraphQLIntrospection, !varsayilan.IntrospectionDisabled,
		"graph'ın sıfır değeri iç gözlemi açık bırakıyorsa config'in varsayılanı da açık olmalı")
}

// TestSatisKanaliEntityAdiUyusuyor product'ın link tanımına yazdığı satış
// kanalı entity adının, auth'un sağlayıcısını KAYDETTİĞİ adla aynı olduğunu
// doğrular.
//
// product, auth'u import EDEMEZ (Prensip 2.4, ADR 0001), bu yüzden
// [productsvc.EntitySalesChannel] auth'un sabitine bağlanamaz; değeri elle
// tekrarlar. Gerekçe [TestSaglayiciKayitAdlariUyusuyor] ile aynıdır ve
// tekrarlanmıyor — burada ayrışmanın somut bedeli şudur: Query, genişletmenin
// hedefini link'in To ucundaki entity adından bulup sağlayıcıyı
// "<ad>.query" ile arar. Adlar ayrışırsa arama boşa düşer ve ürün ↔ satış
// kanalı genişletmesi çalışma zamanında errors.NotFound verir.
//
// İkinci iddia zinciri kapatır: auth'un container'a yazdığı ad gerçekten
// entity adından türemelidir; auth bir gün sağlayıcısını modül adıyla
// kaydetseydi ilk iddia hâlâ geçer ama arama yine boşa düşerdi.
func TestSatisKanaliEntityAdiUyusuyor(t *testing.T) {
	t.Parallel()

	assert.Equal(t, authsvc.Entity, productsvc.EntitySalesChannel,
		"product'ın link ucuna yazdığı entity adı auth'un sunduğu entity adıyla aynı olmalı")
	assert.Equal(t, productsvc.EntitySalesChannel+query.ProviderSuffix, auth.ProviderName,
		"auth sağlayıcısını entity adından türeyen adla kaydetmeli; Query onu bu adla arar")
}

// TestEklentilerModulleriImportEtmez Faz 9'un "çekirdeğe dokunmadan takılıp
// seçilebiliyor" iddiasını zorlar.
//
// internal/core/plugin paketinin godoc'u eklentinin hiçbir commerce modülünü
// import ETMEYECEĞİNİ yazıyor ve plugins/paymentstripe bunu bugün sağlıyor.
// Ama hiçbir kural bunu ZORLAMIYOR: depguard kuralları internal/modules
// altındaki dosyalar için yazılmış, plugins/ ağacı hiçbirinin kapsamında
// değil.
//
// İhlalin bedeli somuttur: payment modülünü import eden bir eklenti, o modülün
// somut tipine derleme zamanında bağlanır. O andan sonra modülü ayrı bir
// servise çıkarmak eklentiyi kırar ve eklentiyi test etmek tüm payment
// zincirini ayağa kaldırmayı gerektirir. Eklentinin doğru yolu, sözleşmeyi
// internal/core/provider'dan, kayıt noktasını da coreplugin.Host'tan almaktır.
func TestEklentilerModulleriImportEtmez(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRoot, "plugins")
	if _, err := os.Stat(root); err != nil {
		t.Skip("henüz eklenti yok")
	}

	prefix := modulePath + "/" + modulesDir + "/"

	for _, file := range goFiles(t, root) {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", file, err)
		}

		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, prefix) {
				t.Errorf("%s: eklenti %q modülünü import ediyor.\n"+
					"Eklenti sözleşmeyi internal/core/provider'dan, kayıt noktasını "+
					"coreplugin.Host'tan almalıdır; modülün somut tipine bağlanmamalıdır.",
					file, strings.TrimPrefix(path, prefix))
			}
		}
	}
}

// TestTohumParolaTabaniAuthinUstunde config'in ilk yönetici için istediği
// parola uzunluğunun, auth'un HERKESE uyguladığı tabanın ÜSTÜNDE kaldığını
// doğrular.
//
// İki sabit iki ayrı pakette yaşar ve aralarında derleyici bağı yoktur.
// auth'un tabanı bir gün config'inkine yetişirse, config'deki kapı SESSİZCE
// etkisizleşir: auth'un zaten reddettiği bir parolayı ikinci kez reddetmek
// hiçbir şey eklemez ve "paylaşılan ortamda daha uzun parola isteniyor"
// iddiası gerçekliğini kaybeder. Sessiz etkisizleşme, kaldırılmış bir
// korumadan daha kötüdür: koruma hâlâ varmış gibi görünür.
//
// Testin burada yaşamasının nedeni, arch paketinin test-only olması ve hem
// çekirdeği hem modülleri import edebilmesidir; iki paketin hiçbiri diğerini
// import edemez (Prensip 2.4).
func TestTohumParolaTabaniAuthinUstunde(t *testing.T) {
	t.Parallel()

	assert.Greater(t, config.MinBootstrapPasswordLen, authsvc.MinPasswordLen,
		"ilk yönetici parolası, auth'un genel tabanından KESİN olarak uzun olmalı; "+
			"eşitlenirse config'deki kapı hiçbir şey eklemez")
}

// Simetri denetiminin taradığı ağaçlar ve kendi adı.
const (
	// configDizini çekirdeğin yapılandırma paketidir. Kaynağı AYRIŞTIRILIR,
	// import edilmez: Go sabitleri yansımayla gezilemez, yani "bu pakette hangi
	// sabitler var" sorusunun tek yapısal cevabı kaynağın kendisidir.
	configDizini = "internal/core/config"

	// archDizini simetri iddialarının yaşadığı test paketidir; kapsam bu
	// ağaçtaki Test* gövdelerinden okunur.
	//
	// Yalnızca bu dosya değil TÜM paket taranır: bir iddia komşu bir dosyada
	// yazıldığında (yapılandırma denetimleri de config'i import eder) denetim
	// onu da görmelidir. Görmeseydi, doğru yere yazılmış bir iddia yüzünden
	// düşer ve insanlar testi susturmak için iddiayı bu dosyaya TAŞIRDI.
	archDizini = "internal/arch"

	// simetriDenetimiAdi kuralı zorlayan testin kendi adıdır ve taramanın
	// DIŞINDA tutulur.
	//
	// Denetim bir gün config'ten bir sabit okursa (örneğin bir hata mesajında
	// örnek göstermek için), o sabiti kendi eliyle "iddia edilmiş" sayardı:
	// kural kendi kendini karşılayan bir cümleye döner ve hiçbir şeyi zorlamaz.
	//
	// Bu sabitin kendisi de elle tekrardır ve aynı kurala tabidir: böyle bir
	// testin var olduğu [simetriIddiasindakiSabitler] içinde DOĞRULANIR, çünkü
	// ad ayrıştığında dışlama sessizce boşa düşerdi.
	simetriDenetimiAdi = "TestConfigSabitleriSimetriIddiasinaBagli"
)

// simetrisizConfigSabitleri modül tarafında karşılığı OLMAYAN config
// sabitlerini gerekçeleriyle listeler.
//
// Muafiyet borçtur: bir sabit buraya yazıldığında "bu değer başka hiçbir
// pakette tekrarlanmıyor" İDDİA EDİLMİŞ olur. İddia bugün doğru olsa bile
// yarın yanlışlaşabilir — biri değeri modül tarafında da yazdığı gün buradaki
// satır, tam da bu dosyanın önlemek için var olduğu ayrışmayı GİZLER. Bu
// yüzden gerekçe zorunludur ve bayatlığı denetlenir
// (bkz. [bayatMuafiyetleriDenetle]).
var simetrisizConfigSabitleri = map[string]string{
	"BackendRedis": `"redis" adı yalnızca çekirdekte yaşar: config'in enum ` +
		`listeleri ve cmd/server'ın Redis istemcisini kurma kararı. Hiçbir modül ` +
		`bu dizeyi kendi sabitinde tekrarlamaz, yani ayrışacak bir uç yok.`,

	"DefaultRedisKeyPrefix": `Öneki tüketen redisguard'ın KENDİ varsayılanı ` +
		`yoktur; önek zorunlu bir kurucu parametresidir ve boş verilirse ` +
		`reddedilir (redisguard.dogrulaOnek). Değer bir tekrar değil tek ` +
		`kaynaktır; geriye uyumluluk iddiası da değerin ikinci bir kopyasında ` +
		`değil, sabitin godoc'unda durur.`,

	"DefaultFileRoot": `local sağlayıcının Options.Root alanı ZORUNLUDUR ve ` +
		`varsayılanı yoktur; kök dizin yalnızca burada seçilir. Modül tarafında ` +
		`tekrarlanan bir değer olmadığı için karşılaştırılacak ikinci uç da yok.`,

	"DefaultFileMaxUploadBytes": `file servisi pozitif bir sınır olmadan ` +
		`KURULMAZ (service.New, MaxUploadBytes <= 0'ı reddeder), yani modül ` +
		`tarafında varsayılan yoktur — sınırın tek kaynağı budur.`,

	"DefaultDatabaseURL": `Eşi bir Go paketi değil, deploy/docker-compose.yml ` +
		`ve .env.example'dır. Zincir zaten kapalı: sabit ile envDefault etiketini ` +
		`config paketinin TestDefaultTagsMatchConstants'ı, etiketi de ` +
		`TestOrtamOrnegiConfigVarsayilanlariylaUyusuyor bağlar. Buraya ikinci bir ` +
		`kopya yazmak iddiayı güçlendirmez, yalnızca bir yerde daha tutar.`,

	"DefaultRedisURL": `Gerekçe DefaultDatabaseURL ile aynıdır: eşi compose ` +
		`dosyası ve .env.example'dır, Go tarafında bir modül sabiti değil.`,
}

// TestConfigSabitleriSimetriIddiasinaBagli config'in dışa açık HER sabitinin
// ya bir simetri iddiasında geçtiğini ya da gerekçesiyle muaf tutulduğunu
// zorlar.
//
// # Hangi arıza sınıfı
//
// Bu dosyadaki testlerin hepsi aynı kuralın ayrı ayrı uygulanmış hâlidir:
// "çekirdek modülleri import EDEMEDİĞİ için (Prensip 2.4) değeri elle tekrarlar;
// tekrar ayrışırsa hiçbir derleyici uyarmaz". Kural altı vakaya TEK TEK
// uygulanmıştı — altıncısının İÇİ yapısallaşmış olsa bile
// ([TestGraphQLSinirVarsayilanlariConfigleUyusuyor] graph.Options'ı gezer), o
// iddianın VAR OLMASI hâlâ birinin elle yazmasına bağlıydı. Elle uygulanan bir
// kural ise YEDİNCİ vakayı kapsamaz: yarın config'e eklenen bir varsayılan,
// karşılığı bir modülde dursa bile testsiz kalırdı — üstelik sessizce, çünkü
// EKSİK bir iddia yanlış bir iddia gibi testi düşürmez, hiç ses çıkarmaz. Bu,
// [TestSaglayiciKayitAdlariUyusuyor] godoc'unda anlatılan dördüncü sağlayıcı
// vakasında bir kez YAŞANMIŞTIR.
//
// Bu denetim o alışkanlığın yerini alır: kuralı zorlayan şey artık altı yerde
// tekrarlanan bir dikkat değil, eksik kalamayan tek bir gezintidir.
//
// # Kapsam neden "dışa açık her sabit", neden "Default*" değil
//
// Ada göre süzmek, kuralı gene ELLE uygulamak olurdu: [config.BackendRedis] ya
// da [config.MinBootstrapPasswordLen] gibi Default ile başlamayan sabitler de
// pekâlâ başka bir paketteki değerin tekrarıdır (ikincisi gerçekten öyledir,
// bkz. [TestTohumParolaTabaniAuthinUstunde]). Yarın "SeedAdminEmail" diye bir
// sabit eklenirse ön ek kuralı onu sessizce dışarıda bırakırdı. Dışa açık
// olmak yeterli ve doğru ölçüttür: bir sabit yalnızca BAŞKA bir paket onu
// okuyacaksa dışa açılır, yani dışa açıklık zaten "paket dışında bir ucu var"
// demektir.
//
// var bildirimleri de gezilir. Bugün config'te dışa açık bir var yoktur; yine
// de bakılır, çünkü sabit yerine değişken yazmak kuralın dışına çıkmanın en
// ucuz yoludur ve o kaçış yolu bilinçli bir karar değil, bir dalgınlık olurdu.
//
// # Kapsam nasıl ölçülür
//
// "İddia edilmiş" demek, adın internal/arch'taki bir Test* GÖVDESİNDE
// config.<Ad> biçiminde geçmesi demektir. Ölçüt AST'dir, metin değil: godoc'ta
// [config.DefaultFileProvider] diye ANMAK kapsam saymaz. Saysaydı, sabiti bir
// yorumda anan herkes denetimi susturabilirdi — ve yorumda anılan bir değer
// hiçbir zaman karşılaştırılmaz.
//
// Ölçüt "değer gerçekten karşılaştırılıyor mu" kadar dar DEĞİLDİR; bir testin
// sabiti okuyup hiçbir şey iddia etmemesi teknik olarak mümkündür. Daha darı
// (örn. assert çağrısının argümanı olma şartı) iddianın BİÇİMİNİ sabitler ve
// testi ifade tanımaya dönüştürürdü; buradaki ölçüt niyeti değil, BAĞI arar:
// sabit bir denetimin gözünün önünde olsun yeter — çünkü ayrışma anında düşecek
// olan da o denetimdir.
func TestConfigSabitleriSimetriIddiasinaBagli(t *testing.T) {
	t.Parallel()

	sabitler := disaAcikConfigSabitleri(t)
	iddialar := simetriIddiasindakiSabitler(t)

	for _, ad := range slices.Sorted(maps.Keys(sabitler)) {
		if _, iddiaVar := iddialar[ad]; iddiaVar {
			continue
		}
		if gerekce, muaf := simetrisizConfigSabitleri[ad]; muaf {
			t.Logf("config.%s modül tarafında karşılıksız: %s", ad, gerekce)
			continue
		}
		t.Errorf("config.%s (%s) hiçbir simetri iddiasında GEÇMİYOR.\n"+
			"Çekirdek modülleri import edemez (Prensip 2.4), bu yüzden dışa açık bir "+
			"config sabiti ya başka bir paketteki değerin ELLE tekrarıdır ya da hiçbir "+
			"yerde eşi olmayan tek kaynaktır. İlkiyse ayrışması derleme zamanında "+
			"görünmez ve çalışma zamanında da çoğu kez sessizdir; ikinciyse bunu "+
			"YAZMAK gerekir.\n"+
			"Yapılacak: ya %s/ altındaki bir Test* gövdesinde eşiyle karşılaştırın, "+
			"ya da simetrisizConfigSabitleri'ne GEREKÇESİYLE ekleyin.",
			ad, depoYolu(sabitler[ad].String()), archDizini)
	}

	bayatMuafiyetleriDenetle(t, simetrisizConfigSabitleri, sabitler,
		"config paketinin dışa açık bir sabiti", iddialar, archDizini)
}

// disaAcikConfigSabitleri config paketinin dışa açık const ve var adlarını
// kaynaktan okuyup bildirim konumlarıyla döner.
//
// Kaynak ayrıştırılır çünkü Go'da sabitler çalışma zamanında YOKTUR: reflect
// bir paketin sabit listesini veremez, derleyici onları kullanım yerine gömer.
// Kuralı yansımayla zorlamanın yolu olmadığı için gezinti derleyicinin gördüğü
// tek yere, kaynağın kendisine bakar.
func disaAcikConfigSabitleri(t *testing.T) map[string]token.Position {
	t.Helper()

	fset := token.NewFileSet()
	sabitler := map[string]token.Position{}

	for _, dosya := range ayristir(t, fset, filepath.Join(repoRoot, configDizini), false) {
		for _, decl := range dosya.agac.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				deger, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, ad := range deger.Names {
					if !ad.IsExported() {
						continue
					}
					sabitler[ad.Name] = fset.Position(ad.Pos())
				}
			}
		}
	}

	require.NotEmpty(t, sabitler,
		"%s içinde hiç dışa açık sabit bulunamadı; gezinti bozulmuş olmalı — "+
			"hiçbir şey bulamayan bir denetim boşlukta yeşil kalır", configDizini)

	return sabitler
}

// simetriIddiasindakiSabitler internal/arch'taki Test* gövdelerinde
// config.<Ad> biçiminde geçen dışa açık adları konumlarıyla döner.
//
// Yalnızca Test* gövdelerine bakılır: paket düzeyindeki bir bildirim (örneğin
// bir tablo değişkeni) hiçbir koşuda değerlendirilmeyebilir, oysa iddianın
// değeri KOŞUYOR olmasındadır. Yardımcı fonksiyonlar da kapsam dışıdır; bir
// yardımcı yalnızca çağrıldığında bir şey iddia eder ve çağrı zincirini
// izlemek, denetimi tip denetleyicisi yazmaya doğru sürüklerdi — buradaki
// yardımcılar sabitleri zaten çağıranından PARAMETRE olarak alır
// (bkz. [varsayilanSaglayiciIddiasi]), yani ad çağrı yerinde görünür.
func simetriIddiasindakiSabitler(t *testing.T) map[string]token.Position {
	t.Helper()

	fset := token.NewFileSet()
	configYolu := modulePath + "/" + configDizini
	iddialar := map[string]token.Position{}
	denetimBulundu := false

	for _, dosya := range ayristir(t, fset, filepath.Join(repoRoot, archDizini), true) {
		takmaAd := ""
		for ad, yol := range dosya.importlar {
			if yol == configYolu {
				takmaAd = ad
				break
			}
		}

		for _, decl := range dosya.agac.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			// Dışlanan testin GERÇEKTEN bulunduğu, config'i import etmeyen
			// dosyalarda da aranır: dışlamanın doğruluğu import'a bağlı
			// olmamalıdır.
			if fn.Name.Name == simetriDenetimiAdi {
				denetimBulundu = true
				continue
			}
			if takmaAd == "" {
				continue
			}

			ast.Inspect(fn.Body, func(dugum ast.Node) bool {
				secici, ok := dugum.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				paket, ok := secici.X.(*ast.Ident)
				if !ok || paket.Name != takmaAd || !secici.Sel.IsExported() {
					return true
				}
				if _, gorulduMu := iddialar[secici.Sel.Name]; !gorulduMu {
					iddialar[secici.Sel.Name] = fset.Position(secici.Sel.Pos())
				}

				return true
			})
		}
	}

	require.NotEmpty(t, iddialar,
		"%s içinde hiç config.<Ad> kullanımı bulunamadı; tarama bozulmuş olmalı — "+
			"boş bir kapsam kümesi, TÜM sabitleri iddiasız gösterip denetimi "+
			"gürültüye boğardı", archDizini)

	require.True(t, denetimBulundu,
		"%s içinde %q adında bir test YOK; simetriDenetimiAdi bayatlamış olmalı.\n"+
			"Sabit, denetimin KENDİ adının elle tekrarıdır — yani bu dosyanın "+
			"kapatmaya çalıştığı sınıfın bir örneği. Ad ayrıştığında dışlama boşa "+
			"düşer ve denetim kendi gövdesindeki config kullanımlarını kapsam "+
			"sayarak kendini sessizce onaylamaya başlar.", archDizini, simetriDenetimiAdi)

	return iddialar
}

// varsayilanSaglayiciIddiasi bir sağlayıcı ailesinin ÜÇ ucunun aynı kimliği
// gösterdiğini doğrular: config'in varsayılanı, modülün varsayılanı ve
// sağlayıcının kendi kimliği.
//
// Üçüncü uç zinciri kapatır. Yalnızca config ile modülü karşılaştırmak, ikisi
// BİRLİKTE kayarsa (aynı yanlış değere) sessiz kalırdı; sağlayıcının kendi
// kimliği ise kayıt anahtarının ta kendisidir, yani çalışma zamanında aranan
// addır.
func varsayilanSaglayiciIddiasi(t *testing.T, aile, saglayiciID, configVarsayilani, modulVarsayilani string) {
	t.Helper()

	assert.Equal(t, saglayiciID, configVarsayilani,
		"config'in varsayılan %s sağlayıcısı, modülün kutudan çıkan sağlayıcısı olmalı", aile)
	assert.Equal(t, saglayiciID, modulVarsayilani,
		"%s modülünün varsayılanı da aynı sağlayıcı olmalı", aile)
}

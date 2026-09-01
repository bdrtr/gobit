package arch_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
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

	assert.Equal(t, local.ID, config.DefaultFileProvider,
		"config'in varsayılan dosya sağlayıcısı, modülün kutudan çıkan sağlayıcısı olmalı")
	assert.Equal(t, local.ID, file.DefaultProviderID,
		"modülün varsayılanı da aynı sağlayıcı olmalı")
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

	assert.Equal(t, logonly.ID, config.DefaultNotificationProvider,
		"config'in varsayılan bildirim sağlayıcısı, modülün kutudan çıkan sağlayıcısı olmalı")
	assert.Equal(t, logonly.ID, notification.DefaultProviderID,
		"modülün varsayılanı da aynı sağlayıcı olmalı")
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

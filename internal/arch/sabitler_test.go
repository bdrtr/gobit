package arch_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/core/config"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/auth"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/payment"
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
func TestSaglayiciKayitAdlariUyusuyor(t *testing.T) {
	t.Parallel()

	assert.Equal(t, payment.ProvidersName, coreplugin.PaymentProvidersName,
		"eklenti paketindeki ödeme sağlayıcı kayıt adı payment modülüyle aynı olmalı")
	assert.Equal(t, fulfillment.ProvidersName, coreplugin.FulfillmentProvidersName,
		"eklenti paketindeki kargo sağlayıcı kayıt adı fulfillment modülüyle aynı olmalı")
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

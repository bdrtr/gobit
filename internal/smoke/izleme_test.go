//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIzlemeUcununIkiBicimiDeKabulEdilir D senaryosudur: OTLP adresinin iki
// yazımı da kabul edilir ve izleme arızası uygulamayı DÜŞÜRMEZ (ADR 0007).
//
// # Yakalanan iki arıza
//
// Birincisi ADRESİN BİÇİMİDİR. OpenTelemetry belirtimi
// OTEL_EXPORTER_OTLP_ENDPOINT'i bir URL olarak tanımlar
// ("http://collector:4317"); Go SDK'sının WithEndpoint seçeneği ise ŞEMASIZ
// bir "host:port" bekler. İkisi karıştırıldığında hiçbir hata çıkmaz: gRPC
// tembel bağlanır, uygulama "izleme kuruldu" loglar ve tek bir span bile
// gitmez. Sessiz kayıp, ancak bir arıza incelenirken — yani en kötü anda —
// fark edilir.
//
// İkincisi METRİK ARALIĞININ ADIDIR. Aralık uygulamada METRIC_EXPORT_INTERVAL
// adıyla ve Go süresi olarak okunur; belirtim ise OTEL_METRIC_EXPORT_INTERVAL
// adını AYIRMIŞ ve değerini MİLİSANİYE TAMSAYI olarak tanımlamıştır. Ad ödünç
// alındığında çakışma iki yönde birden keser: belirtime uyan değer (60000) Go
// süresi olarak ayrıştırılamaz ve uygulama HİÇ AÇILMAZ; uygulamaya uyan değer
// (60s) ise OTel SDK'sının kendi okuyucusunda her açılışta "parse duration"
// hatası loglatır.
//
// Senaryo iki değişkeni de KENDİ belirtimine uyan değerle verir. Ad çakışması
// geri gelirse ikisinden biri mutlaka patlar: uygulama 60000'i süre olarak
// okuyamayıp açılışta durur, ya da SDK 60s'yi milisaniye sayısı sanıp hata
// loglar. Tek bir değişkenle sınamak, arızanın yalnızca bir yönünü görürdü.
//
// # Neden toplayıcı kurulmuyor ve bu testin sınırı
//
// Sınanan şey span'ların TESLİMİ değil, uygulamanın açılıp ÇALIŞMAYA DEVAM
// ETMESİDİR. İzleme ürünün doğruluğu için değil görünürlüğü için vardır ve
// toplayıcının kesintisi mağazayı kapatmamalıdır (ADR 0007); bir toplayıcı
// kaldırmak, tam da kanıtlanmak istenen "toplayıcı yokken de çalışır"
// durumunu ortadan kaldırırdı.
//
// Sınır açıkça söylenmelidir: toplayıcı olmadan bu test span'ların GERÇEKTEN
// gittiğini gösteremez, yalnızca iki yazımın da açılışı geçtiğini gösterir.
// Adresin doğru SDK seçeneğine (WithEndpoint / WithEndpointURL) çevrildiği
// bir birim testiyle sabitlenmiştir (bkz. observability paketindeki
// TestUcSemaliMiIkiBicimiDeAyirtEder). İş bölümü bilinçlidir: seçeneğin
// doğruluğu ucuzca ve kesin biçimde orada sınanır, "uygulama bu adresle
// gerçekten açılıyor mu" sorusu ise ancak burada cevaplanabilir.
func TestIzlemeUcununIkiBicimiDeKabulEdilir(t *testing.T) {
	uclar := map[string]string{
		"şemasız host:port (Go SDK yazımı)": "localhost:4317",
		"şemalı URL (belirtim yazımı)":      "http://localhost:4317",
	}

	for ad, uc := range uclar {
		t.Run(ad, func(t *testing.T) {
			ayar := temelAyarlar(senaryoVeritabani(t), bosPort(t))
			ayar["OTEL_EXPORTER_OTLP_ENDPOINT"] = uc
			ayar["OTEL_EXPORTER_OTLP_INSECURE"] = "true"
			// Her değişken KENDİ belirtimine uyan değerle verilir; gerekçe
			// testin godoc'undadır.
			ayar["METRIC_EXPORT_INTERVAL"] = "60s"
			ayar["OTEL_METRIC_EXPORT_INTERVAL"] = "60000"

			s := sunucuBaslat(t, ayar)
			s.hazirBekle(acilisSuresi)

			kod, govde := s.iste(http.MethodGet, "/ready", "")
			assert.Equal(t, http.StatusOK, kod,
				"toplayıcı erişilemezken de hazır olmalı; gövde: %s", govde)

			assert.True(t, s.gunlukIceriyorMu("izleme kuruldu"),
				"kurulum başarılı loglanmalı; adres reddedilseydi \"izleme kurulamadı\" görürdük\n%s",
				s.gunluk())
			assert.False(t, s.gunlukIceriyorMu("izleme kurulamadı"),
				"adresin bu yazımı kabul edilmeli\n%s", s.gunluk())
			assert.True(t, s.gunlukIceriyorMu(uc),
				"kurulum logu hangi adresin kullanıldığını söylemeli\n%s", s.gunluk())

			// "parse duration" OTel SDK'sının kendi hata metnidir ve stderr'e
			// düşer (bkz. sdk/metric env.go). Ad çakışmasının tek görünür izi
			// budur: metrikler yine gönderilir, yalnızca aralık sessizce
			// varsayılana döner.
			assert.NotContains(t, s.stderr.String(), "parse duration",
				"OTel SDK'sı metrik aralığını ayrıştıramamış: ad çakışması geri gelmiş olabilir")

			assert.False(t, s.oldu(),
				"izleme toplayıcısı erişilemezken uygulama DÜŞMEMELİ (ADR 0007)\n%s", s.gunluk())
		})
	}
}

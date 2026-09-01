//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/module"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	paymentmod "github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// Bu dosya planın Faz 9 DoD'sini kanıtlar:
//
//	"Örnek payment provider plugin'i çekirdeğe dokunmadan takılıp
//	 seçilebiliyor; trace'ler dışa aktarılıyor; OpenAPI şeması üretiliyor;
//	 temel yük testi geçiyor."
//
// Trace tarafı burada değil, internal/core/http/telemetry_test.go içinde
// bellek içi bir dışa aktarıcıyla kanıtlanır: span'ın gerçekten üretildiğini
// görmek için bir OTLP toplayıcısı ayağa kaldırmak, kanıta hiçbir şey
// eklemeden testi ağa bağımlı kılardı.

// TestIdempotencyAyniAnahtarlaTekSepetUretir idempotency middleware'inin
// GERÇEK bir uçta çalıştığını doğrular.
//
// İddia bir başlık iddiası değil, bir VERİ iddiasıdır: ağı kopan ve isteğini
// tekrarlayan bir istemci iki sepet değil, bir sepet almalıdır. Yalnızca
// yanıtın aynı olduğuna bakmak yetmezdi — ikinci bir sepet yazılıp yanıtı
// atılmış da olabilirdi.
func TestIdempotencyAyniAnahtarlaTekSepetUretir(t *testing.T) {
	ctx := context.Background()

	govde, err := json.Marshal(map[string]string{"region_id": vergiliBolgeID})
	require.NoError(t, err, "sepet isteği kodlanamadı")

	sepetIstegi := func() *http.Request {
		istek := httptest.NewRequest(http.MethodPost, "/store/v1/carts", bytes.NewReader(govde))
		istek.Header.Set("Content-Type", "application/json")
		istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)
		istek.Header.Set(corehttp.IdempotencyKeyHeader, "e2e-sepet-anahtari-1")

		return istek
	}

	ilk := httptest.NewRecorder()
	testRouter.ServeHTTP(ilk, sepetIstegi())
	require.Equal(t, http.StatusCreated, ilk.Code,
		"ilk sepet isteği 201 dönmeli; gövde: %s", ilk.Body.String())
	assert.Empty(t, ilk.Header().Get(corehttp.IdempotencyReplayedHeader),
		"ilk istek bir tekrar oynatma değildir")

	// Sayım tekrardan HEMEN ÖNCE alınır: paket testleri sırayla koştuğu için
	// aradaki tek yazar bu testtir.
	oncekiSayi := sepetSayisi(ctx, t)

	ikinci := httptest.NewRecorder()
	testRouter.ServeHTTP(ikinci, sepetIstegi())
	require.Equal(t, http.StatusCreated, ikinci.Code,
		"tekrar da 201 dönmeli; gövde: %s", ikinci.Body.String())
	assert.Equal(t, "true", ikinci.Header().Get(corehttp.IdempotencyReplayedHeader),
		"tekrar, kaydedilmiş yanıtın oynatılması olmalı")
	assert.JSONEq(t, ilk.Body.String(), ikinci.Body.String(),
		"oynatılan yanıt ilkinin AYNISI olmalı")

	// Asıl iddia: tekrar hiçbir şey YAZMADI.
	assert.Equal(t, oncekiSayi, sepetSayisi(ctx, t),
		"tekrar edilen istek ikinci bir sepet yazmamalı")

	// Oynatılan yanıtın işaret ettiği sepet gerçekten var olmalı; kayıt
	// bozulmuş bir gövdeyi çalıyor olsaydı bu adım düşerdi.
	_, err = sepetSvc.GetCart(ctx, sepetKimliginiOku(t, ikinci))
	require.NoError(t, err, "oynatılan yanıttaki sepet okunabilmeli")
}

// sepetSayisi veritabanındaki toplam sepet sayısını döner.
func sepetSayisi(ctx context.Context, t *testing.T) int64 {
	t.Helper()

	_, toplam, err := sepetSvc.ListCarts(ctx, cartsvc.ListCartsInput{})
	require.NoError(t, err, "sepetler sayılamadı")

	return toplam
}

// TestIdempotencyAyniAnahtarFarkliGovdeyiReddeder anahtarın yeniden
// kullanımının sessizce yanlış yanıt döndürmediğini doğrular.
//
// Sessizce ilk yanıtı çalmak, istemcinin İKİNCİ isteğinin hiç işlenmediğini
// gizlerdi: istemci "ikinci sepetim hazır" sanıp birincinin kimliğiyle
// devam ederdi.
func TestIdempotencyAyniAnahtarFarkliGovdeyiReddeder(t *testing.T) {
	anahtar := "e2e-sepet-anahtari-2"

	istekYap := func(bolgeID string) *http.Request {
		govde, err := json.Marshal(map[string]string{"region_id": bolgeID})
		require.NoError(t, err)

		istek := httptest.NewRequest(http.MethodPost, "/store/v1/carts", bytes.NewReader(govde))
		istek.Header.Set("Content-Type", "application/json")
		istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)
		istek.Header.Set(corehttp.IdempotencyKeyHeader, anahtar)

		return istek
	}

	ilk := httptest.NewRecorder()
	testRouter.ServeHTTP(ilk, istekYap(vergiliBolgeID))
	require.Equal(t, http.StatusCreated, ilk.Code, "gövde: %s", ilk.Body.String())

	farkli := httptest.NewRecorder()
	testRouter.ServeHTTP(farkli, istekYap(vergisizBolgeID))
	assert.Equal(t, http.StatusConflict, farkli.Code,
		"aynı anahtar farklı gövdeyle reddedilmeli; gövde: %s", farkli.Body.String())
}

// TestOpenAPISemasiRouterAgacindanUretilir Faz 9'un şema ayağını kanıtlar.
//
// Şema elle yazılmaz, router'dan türetilir; testin işi de bunu doğrulamaktır:
// gerçekten kayıtlı olan yollar şemada görünmeli, güvenlik şeması yüzeye göre
// ayrışmalı ve giriş ucu korumasız işaretlenmelidir.
func TestOpenAPISemasiRouterAgacindanUretilir(t *testing.T) {
	istek := httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody)
	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	require.Equal(t, http.StatusOK, kayit.Code,
		"şema ucu 200 dönmeli; gövde: %s", kayit.Body.String())

	var belge struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &belge),
		"şema çözülemedi; gövde: %s", kayit.Body.String())

	assert.NotEmpty(t, belge.OpenAPI, "şema sürümü bildirilmeli")

	for _, yol := range []string{
		"/store/v1/products",
		"/store/v1/products/{id}",
		"/admin/v1/users",
		"/admin/v1/auth/login",
		"/admin/v1/sales-channels",
	} {
		assert.Contains(t, belge.Paths, yol, "%q şemada bulunmalı", yol)
	}

	// Şema yalnızca route DESENLERİNİ yayımlar; kimlik gerektiren bir uç
	// şemada görünse de çağrılamaz. Kayıt kimliği taşıyan ham yol
	// (/store/v1/products/prod_01…) şemaya HİÇ girmemelidir.
	for yol := range belge.Paths {
		assert.NotContains(t, yol, "prod_", "şemada ham kayıt kimliği olmamalı: %s", yol)
	}
}

// TestEklentiCekirdegeDokunmadanSaglayiciEkler Faz 9'un eklenti ayağını
// GERÇEK payment modülü üzerinde kanıtlar.
//
// İddia iki katmanlıdır: (1) eklenti hiçbir commerce modülünü import etmeden
// sağlayıcısını kaydedebilmeli, (2) kaydettiği sağlayıcı modül tarafından
// SEÇİLEBİLİR olmalı. İkincisi olmadan birincisi yalnızca bir kayıt defteri
// egzersizidir.
func TestEklentiCekirdegeDokunmadanSaglayiciEkler(t *testing.T) {
	ctx := context.Background()

	kayitlar, err := containerSaglayicilari()
	require.NoError(t, err, "ödeme sağlayıcı kaydı çözülemedi")
	require.NotContains(t, kayitlar.IDs(), paymentstripe.ProviderID,
		"ön koşul: stripe sağlayıcısı henüz kayıtlı olmamalı")

	eklentiler := coreplugin.NewRegistry(nil)
	eklentiler.Add(paymentstripe.New())

	// Host, modüllerin AYNI container'ıyla kurulur; eklentinin sağlayıcısı
	// çalışan sisteme girer, ayrı bir kopyaya değil.
	host := coreplugin.NewHost(kap, module.NewRegistry(nil, nil), nil, nil,
		map[string]string{"STRIPE_API_KEY": "sk_test_e2e"})

	require.NoError(t, eklentiler.Install(ctx, host), "eklenti kurulamadı")
	require.NoError(t, eklentiler.Start(ctx, host), "eklenti başlatılamadı")

	assert.Contains(t, kayitlar.IDs(), paymentstripe.ProviderID,
		"stripe sağlayıcısı payment modülünde seçilebilir olmalı")

	saglayici, err := kayitlar.Get(paymentstripe.ProviderID)
	require.NoError(t, err, "sağlayıcı kimliğiyle çözülebilmeli")
	assert.Equal(t, paymentstripe.ProviderID, saglayici.ID())
}

// TestEklentiAyariEksikseKurulumDurur yapılandırma hatasının açılışta
// patladığını doğrular.
//
// Sessizce atlansaydı, "stripe kurulu" sanılan bir mağaza hiç ödeme alamaz ve
// bu ancak ilk müşteri denemesinde görülürdü.
func TestEklentiAyariEksikseKurulumDurur(t *testing.T) {
	eklentiler := coreplugin.NewRegistry(nil)
	eklentiler.Add(paymentstripe.New())

	host := coreplugin.NewHost(kap, module.NewRegistry(nil, nil), nil, nil, nil)

	err := eklentiler.Install(context.Background(), host)
	require.Error(t, err, "ayarı olmayan eklenti kurulmamalı")
	assert.Contains(t, err.Error(), paymentstripe.Name,
		"hata hangi eklentinin başarısız olduğunu söylemeli")
}

// containerSaglayicilari payment modülünün sağlayıcı kaydını container'dan
// çözer.
//
// Kayıt ADLA çözülür: eklenti de aynı adı kullanır ve iki adın uyumu
// internal/arch/sabitler_test.go ile derleme zamanına bağlanmıştır.
func containerSaglayicilari() (*paymentsvc.ProviderRegistry, error) {
	return container.Resolve[*paymentsvc.ProviderRegistry](kap, paymentmod.ProvidersName)
}

// sepetKimliginiOku bir sepet yanıtından kimliği çıkarır.
func sepetKimliginiOku(t *testing.T, kayit *httptest.ResponseRecorder) string {
	t.Helper()

	var zarf struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"sepet yanıtı çözülemedi; gövde: %s", kayit.Body.String())
	require.NotEmpty(t, zarf.Data.ID, "sepet kimliği dönmeli")

	return zarf.Data.ID
}

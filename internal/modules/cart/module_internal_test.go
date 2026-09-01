package cart

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
)

// Test DAHİLİ pakettedir çünkü sınanan tipler ([linePricing], [cartCompletion])
// dışa kapalıdır ve kapalı kalmalıdır: ikisi de bu modülün kendi kablolama
// ayrıntısıdır, dışarıdan kurulacak bir yüzey değil.

// stubPricing container'a kaydedilen sahte fiyatlandırma akışıdır.
type stubPricing struct {
	lineID string
	calls  int
}

// Sahtenin beklenen yüzeyi karşıladığı derleme zamanında sabitlenir.
var _ api.LinePricing = (*stubPricing)(nil)

// AddPricedLineItem satırın kimliğini döner.
func (s *stubPricing) AddPricedLineItem(
	_ context.Context, _, _ string, _ int64, _ json.RawMessage,
) (string, error) {
	s.calls++
	return s.lineID, nil
}

// SetLineItemQuantity satırın kaldırılmadığını bildirir.
func (s *stubPricing) SetLineItemQuantity(_ context.Context, _, _ string, _ int64) (bool, error) {
	s.calls++
	return false, nil
}

// yabanciTip container'da doğru ADLA duran ama yüzeyi karşılaMAYAN tiptir.
type yabanciTip struct{}

// sessizLog testin çıktısını iddialarla bırakan logger'dır.
func sessizLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestSatirFiyatlandirmaAkisiAdlaCozulur akışın container'dan ADLA çözüldüğünü
// ve yüzeyin yapısal olarak karşılandığını doğrular.
//
// Bağ derleme zamanında YOKTUR: modül internal/workflows'u import edemez
// (ADR 0006), yani somut akışla bu arayüzü birbirine bağlayan tek şey
// [LinePricingName] dizesidir. Test o dizenin gerçekten çözüm anahtarı
// olduğunu sabitler.
func TestSatirFiyatlandirmaAkisiAdlaCozulur(t *testing.T) {
	t.Parallel()

	kap := container.New(nil)
	akis := &stubPricing{lineID: "li_1"}
	require.NoError(t, kap.Provide(LinePricingName, akis))

	sarmalayici := &linePricing{c: kap, log: sessizLog()}
	lineID, err := sarmalayici.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)

	require.NoError(t, err)
	assert.Equal(t, "li_1", lineID)
	assert.Equal(t, 1, akis.calls)
}

// TestSatirFiyatlandirmaAkisiYokkenKapaliArizalanir fiyat yolunun eksik
// kablolamada SESSİZCE DEVAM ETMEDİĞİNİ doğrular.
//
// Bu testin sınadığı şey, order modülünün harcama kuralıyla arasındaki
// bilinçli farktır: orada sağlayıcı yoksa kural uygulanmaz ve akış sürer
// ("limit yok" doğru cevaptır), burada ise akış yoksa satır HİÇ EKLENMEZ.
// Sıfır fiyatla ya da istemcinin gönderdiği fiyatla devam etmek, sessizce
// bedava mal satmak olurdu.
func TestSatirFiyatlandirmaAkisiYokkenKapaliArizalanir(t *testing.T) {
	t.Parallel()

	sarmalayici := &linePricing{c: container.New(nil), log: sessizLog()}

	lineID, err := sarmalayici.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)
	require.Error(t, err, "çözülemeyen akış hata döndürmeli")
	assert.Empty(t, lineID)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"kayıtsız ad container'da KindNotFound üretir; devralınsaydı uç 404 döner "+
			"ve istemciye \"böyle bir uç yok\" derdi — oysa arıza SUNUCU "+
			"YAPILANDIRMASINDADIR ve 5xx uyarısı çalmalıdır")
	assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(err),
		"iddia sınıf adında değil ÜRETİMDEKİ eşlemede sabitlenir: ucun gerçekten "+
			"hangi status kodunu döndüğünü belirleyen tek yer burasıdır")
	assert.Contains(t, err.Error(), LinePricingName, "hata hangi adın çözülemediğini yazmalı")

	// Adet güncelleme yolu da aynı kapıdan geçer.
	_, err = sarmalayici.SetLineItemQuantity(t.Context(), "cart_1", "li_1", 5)
	assert.Error(t, err)
}

// TestSatirFiyatlandirmaAkisiUyumsuzTipiReddeder doğru adla kayıtlı ama yüzeyi
// karşılamayan bir tipin de satır yazdırMADIĞINI doğrular.
//
// "Kayıtlı değil" ile "kayıtlı ama tanınmıyor" farklı arızalardır; ikisinin de
// sonucu aynı olmalıdır, çünkü ikisinde de fiyatı belirleyecek taraf yoktur.
func TestSatirFiyatlandirmaAkisiUyumsuzTipiReddeder(t *testing.T) {
	t.Parallel()

	kap := container.New(nil)
	require.NoError(t, kap.Provide(LinePricingName, yabanciTip{}))

	sarmalayici := &linePricing{c: kap, log: sessizLog()}
	_, err := sarmalayici.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)

	require.Error(t, err)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"yanlış tipte kayıt container'da KindInvalid üretir; devralınsaydı uç 422 "+
			"ile \"gövden geçersiz\" derdi, oysa gövde kusursuz olsa da sonuç aynıydı")
}

// TestSatirFiyatlandirmaKarariBirKezVerilir çözümün her istekte
// tekrarlanmadığını doğrular.
//
// Akışlar açılışta, ilk istekten önce kaydedilir; ilk çözümde bulunmayan bir ad
// sonradan da bulunmayacaktır. Her istekte yeniden denemek aynı hatayı sonsuza
// kadar yeniden üretmekten başka bir şey yapmazdı — ve kararın değişebilir
// olması, mağazanın açılıştan sonra sessizce davranış değiştirmesi demek
// olurdu.
func TestSatirFiyatlandirmaKarariBirKezVerilir(t *testing.T) {
	t.Parallel()

	kap := container.New(nil)
	sarmalayici := &linePricing{c: kap, log: sessizLog()}

	_, err := sarmalayici.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)
	require.Error(t, err)

	// Akış SONRADAN kaydedilse bile karar değişmez.
	require.NoError(t, kap.Provide(LinePricingName, &stubPricing{lineID: "li_1"}))

	_, err = sarmalayici.AddPricedLineItem(t.Context(), "cart_1", "var_1", 3, nil)
	assert.Error(t, err, "karar bir kez verilir ve saklanır")
}

// stubCompletion container'a kaydedilen sahte tamamlama akışıdır.
type stubCompletion struct{ yanit json.RawMessage }

// Sahtenin beklenen yüzeyi karşıladığı derleme zamanında sabitlenir.
var _ api.CartCompletion = (*stubCompletion)(nil)

// CompleteCartJSON betiklenen yanıtı döner.
func (s *stubCompletion) CompleteCartJSON(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return s.yanit, nil
}

// TestSepetTamamlamaAkisiAdlaCozulur tamamlama akışının da ADLA çözüldüğünü
// doğrular.
func TestSepetTamamlamaAkisiAdlaCozulur(t *testing.T) {
	t.Parallel()

	kap := container.New(nil)
	require.NoError(t, kap.Provide(CartCompletionName,
		&stubCompletion{yanit: json.RawMessage(`{"order_id":"order_1"}`)}))

	sarmalayici := &cartCompletion{c: kap, log: sessizLog()}
	yanit, err := sarmalayici.CompleteCartJSON(t.Context(), json.RawMessage(`{"cart_id":"cart_1"}`))

	require.NoError(t, err)
	assert.JSONEq(t, `{"order_id":"order_1"}`, string(yanit))
}

// TestSepetTamamlamaAkisiYokkenKapaliArizalanir akış olmadan sepetin
// tamamlanMADIĞINI doğrular.
//
// "Sepeti tamamlandı say" diye bir kestirme yol olamaz: akış yoksa sipariş,
// ödeme ve stok rezervasyonu da yoktur.
func TestSepetTamamlamaAkisiYokkenKapaliArizalanir(t *testing.T) {
	t.Parallel()

	sarmalayici := &cartCompletion{c: container.New(nil), log: sessizLog()}
	yanit, err := sarmalayici.CompleteCartJSON(t.Context(), json.RawMessage(`{"cart_id":"cart_1"}`))

	require.Error(t, err)
	assert.Nil(t, yanit)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"tamamlama ucu da kurulum arızasını 5xx olarak bildirmeli")
}

// TestAkisAdlariSozlesmedir container adlarının değerini sabitler.
//
// Adlar internal/workflows paketlerinindir ve bu modülde DİZE olarak
// tekrarlanır (modüller workflow paketlerini import edemez). Tekrarın bedeli,
// bir tarafın adı değiştirdiğinde diğerinin sessizce çözülememesidir; bu test
// değeri en azından TEK yerde sabitler ve değişikliğin bilinçli olmasını
// zorlar.
func TestAkisAdlariSozlesmedir(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "workflows.cart.interop", LinePricingName,
		"ad sepet akışlarının InteropName sabitiyle aynı olmalı")
	assert.Equal(t, "workflows.checkout.interop", CartCompletionName,
		"ad sipariş tamamlama akışının InteropName sabitiyle aynı olmalı")
}

// stubRegions container'a kaydedilen sahte bölge yüzeyidir.
type stubRegions struct {
	kod   string
	calls int
}

// Sahtenin beklenen yüzeyi karşıladığı derleme zamanında sabitlenir.
var _ api.RegionCurrencyReader = (*stubRegions)(nil)

// RegionCurrency bölgenin para birimini döner.
func (s *stubRegions) RegionCurrency(
	_ context.Context,
	_ string,
) (code string, decimalDigits int32, err error) {
	s.calls++
	return s.kod, 2, nil
}

// TestBolgeYuzeyiAdlaCozulur bölge yüzeyinin container'dan ADLA çözüldüğünü ve
// yapısal olarak karşılandığını doğrular.
//
// Bağ derleme zamanında YOKTUR: bu modül region'ı import edemez (ADR 0001),
// yani somut region servisiyle [api.RegionCurrencyReader] arayüzünü birbirine
// bağlayan tek şey [RegionServiceName] dizesidir. Test o dizenin gerçekten
// çözüm anahtarı olduğunu sabitler.
func TestBolgeYuzeyiAdlaCozulur(t *testing.T) {
	t.Parallel()

	kap := container.New(nil)
	bolgeler := &stubRegions{kod: "TRY"}
	require.NoError(t, kap.Provide(RegionServiceName, bolgeler))

	sarmalayici := &regionCurrency{c: kap, log: sessizLog()}
	kod, basamak, err := sarmalayici.RegionCurrency(t.Context(), "reg_1")

	require.NoError(t, err)
	assert.Equal(t, "TRY", kod)
	assert.Equal(t, int32(2), basamak)
	assert.Equal(t, 1, bolgeler.calls)
}

// TestBolgeYuzeyiYokkenKapaliArizalanir para birimi türetilemediğinde sepet
// açma yolunun SESSİZCE DEVAM ETMEDİĞİNİ doğrular.
//
// Sepetin para birimi hangi fiyat listesinin uygulanacağını seçer. Bir
// varsayılana düşmek ya da istemcinin dediğini kullanmak, tam olarak
// kapatılan yetki kapısını geri açardı; tek doğru sonuç sepetin HİÇ
// AÇILMAMASIDIR.
func TestBolgeYuzeyiYokkenKapaliArizalanir(t *testing.T) {
	t.Parallel()

	sarmalayici := &regionCurrency{c: container.New(nil), log: sessizLog()}

	kod, _, err := sarmalayici.RegionCurrency(t.Context(), "reg_1")
	require.Error(t, err, "çözülemeyen bölge yüzeyi hata döndürmeli")
	assert.Empty(t, kod, "para birimi ASLA varsayılana düşmemeli")
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"kayıtsız ad container'da KindNotFound üretir; devralınsaydı sepet açma ucu "+
			"404 döner ve istemciye \"böyle bir uç yok\" derdi — oysa arıza SUNUCU "+
			"YAPILANDIRMASINDADIR")
	assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(err),
		"iddia sınıf adında değil ÜRETİMDEKİ eşlemede sabitlenir")
	assert.Contains(t, err.Error(), RegionServiceName, "hata hangi adın çözülemediğini yazmalı")
}

// TestBolgeYuzeyiUyumsuzTipiReddeder doğru adla kayıtlı ama yüzeyi
// karşılamayan bir tipin de sepet açtırMADIĞINI doğrular.
//
// İmza region'ın modüller arası yüzeyiyle birebir aynı olmak zorundadır ve
// uyumu derleyici denetleyemez; ondalık basamak sayısının imzada kalmasının
// sebebi de budur. Ayrışma bu kapıda görünür.
func TestBolgeYuzeyiUyumsuzTipiReddeder(t *testing.T) {
	t.Parallel()

	kap := container.New(nil)
	require.NoError(t, kap.Provide(RegionServiceName, yabanciTip{}))

	sarmalayici := &regionCurrency{c: kap, log: sessizLog()}
	_, _, err := sarmalayici.RegionCurrency(t.Context(), "reg_1")

	require.Error(t, err)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"yanlış tipte kayıt container'da KindInvalid üretir; devralınsaydı uç 422 "+
			"ile \"gövden geçersiz\" derdi, oysa gövde kusursuz olsa da sonuç aynıydı")
}

// TestBolgeYuzeyiAdiSozlesmedir container adının değerini sabitler.
//
// Ad region modülünündür ve burada DİZE olarak tekrarlanır; bir taraf adı
// değiştirdiğinde diğeri sessizce çözülemez hâle gelir. Test değeri TEK yerde
// sabitler ve değişikliğin bilinçli olmasını zorlar.
func TestBolgeYuzeyiAdiSozlesmedir(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "region.service", RegionServiceName,
		"ad region modülünün ServiceName sabitiyle aynı olmalı")
}

package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// provideAll bir container'a altı yüzeyin de sahtesini gerçek ADLARIYLA kaydeder.
func provideAll(t *testing.T, c *container.Container, h *harness) {
	t.Helper()

	require.NoError(t, c.Provide(ServiceCart, h.carts))
	require.NoError(t, c.Provide(ServicePricing, h.prices))
	require.NoError(t, c.Provide(ServiceRegion, h.regions))
	require.NoError(t, c.Provide(ServiceCustomer, h.customers))
	require.NoError(t, c.Provide(ServiceLink, h.links))
	require.NoError(t, c.Provide(ServiceQuery, h.catalog))
}

// TestFromContainerAdlaCozer bağımlılıkların container'dan ADLA çözüldüğünü ve
// çözülen akışların çalıştığını doğrular (ADR 0006).
func TestFromContainerAdlaCozer(t *testing.T) {
	h := newHarness(t)
	recordOpenCart(h.carts, testCartID)

	c := container.New(nil)
	provideAll(t, c, h)

	wf, err := FromContainer(c)
	require.NoError(t, err)

	out, err := wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "TR"})
	require.NoError(t, err)
	assert.Equal(t, testCartID, out.CartID)
}

// TestFromContainerEksikServisiBildirir kayıtsız bir adın teşhis edilebilir
// hata ürettiğini doğrular.
//
// ADR 0006'nın kabul edilen bedeli budur: uyumsuzluk derleme zamanında değil,
// çözüm anında yakalanır — o yüzden hata HANGİ adın aranıp bulunamadığını
// yazmalıdır.
func TestFromContainerEksikServisiBildirir(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)
	require.NoError(t, c.Provide(ServiceCart, h.carts))

	_, err := FromContainer(c)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServicePricing)
}

// TestFromContainerUyumsuzTipiBildirir kayıtlı ama yüzeyi karşılamayan bir
// servisin sessizce kabul edilmediğini doğrular.
func TestFromContainerUyumsuzTipiBildirir(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)
	provideAll(t, c, h)

	uyumsuz := container.New(nil)
	// "cart.service" adına, Carts yüzeyini KARŞILAMAYAN bir değer konur;
	// cart modülünün bugünkü servisinin durumu da tam olarak budur
	// (bkz. paket yorumu).
	require.NoError(t, uyumsuz.Provide(ServiceCart, h.regions))
	require.NoError(t, uyumsuz.Provide(ServicePricing, h.prices))
	require.NoError(t, uyumsuz.Provide(ServiceRegion, h.regions))
	require.NoError(t, uyumsuz.Provide(ServiceCustomer, h.customers))
	require.NoError(t, uyumsuz.Provide(ServiceLink, h.links))
	require.NoError(t, uyumsuz.Provide(ServiceQuery, h.catalog))

	_, err := FromContainer(uyumsuz)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServiceCart)
}

// TestFromContainerContainersizReddedilir nil container'ın panik değil hata
// ürettiğini doğrular.
func TestFromContainerContainersizReddedilir(t *testing.T) {
	_, err := FromContainer(nil)
	require.Error(t, err)
	assert.Equal(t, CodeNotReady, errors.CodeOf(err))
}

// TestNewEksikBagimliligiReddeder eksik bir yüzeyin KURULUM anında hata
// verdiğini doğrular.
func TestNewEksikBagimliligiReddeder(t *testing.T) {
	full := func(h *harness) Deps {
		return Deps{
			Carts:     h.carts,
			Prices:    h.prices,
			Regions:   h.regions,
			Customers: h.customers,
			Links:     h.links,
			Catalog:   h.catalog,
		}
	}

	tests := map[string]func(*Deps){
		ServiceCart:     func(d *Deps) { d.Carts = nil },
		ServicePricing:  func(d *Deps) { d.Prices = nil },
		ServiceRegion:   func(d *Deps) { d.Regions = nil },
		ServiceCustomer: func(d *Deps) { d.Customers = nil },
		ServiceLink:     func(d *Deps) { d.Links = nil },
		ServiceQuery:    func(d *Deps) { d.Catalog = nil },
	}

	for name, drop := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			deps := full(h)
			drop(&deps)

			_, err := New(deps)
			require.Error(t, err)
			assert.Equal(t, CodeNotReady, errors.CodeOf(err))
			assert.Contains(t, err.Error(), name)
		})
	}
}

// TestNewLoggersizKurulabilir logger verilmediğinde akışların yine de
// çalıştığını doğrular.
func TestNewLoggersizKurulabilir(t *testing.T) {
	h := newHarness(t)
	recordOpenCart(h.carts, testCartID)

	wf, err := New(Deps{
		Carts:     h.carts,
		Prices:    h.prices,
		Regions:   h.regions,
		Customers: h.customers,
		Links:     h.links,
		Catalog:   h.catalog,
	})
	require.NoError(t, err)

	_, err = wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "TR"})
	require.NoError(t, err)
}

// TestFromContainerOpsiyonelYuzeyleriCozer promotion ve tax kayıtlıysa
// çözüldüklerini ve hesapta KULLANILDIKLARINI doğrular.
//
// Yalnızca "hata dönmedi" demek yetmezdi: yanlış adla çözülen bir yüzey de
// hatasız kurulur ve sepet sessizce degrade yolda koşardı. İddia bu yüzden
// sonuçtaki vergi kaynağına dayanır.
func TestFromContainerOpsiyonelYuzeyleriCozer(t *testing.T) {
	h := newModuleHarness(t)
	h.taxes.rateBps = 1000
	serveSnapshot(h.carts, twoLineCart(1))

	c := container.New(nil)
	provideAll(t, c, h)
	require.NoError(t, c.Provide(ServicePromotion, h.discounts))
	require.NoError(t, c.Provide(ServiceTax, h.taxes))

	wf, err := FromContainer(c)
	require.NoError(t, err)

	totals, err := wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)
	assert.Equal(t, TaxSourceTax, totals.TaxSource)
	assert.Equal(t, int64(275), totals.TaxTotal, "tax'ın %10 oranı; region %20 taşıyor")
}

// TestFromContainerOpsiyonelYuzeySizKurulur promotion ve tax KAYITSIZKEN
// akışların yine de kurulduğunu ve degrade yolda koştuğunu doğrular.
//
// Zorunlu sayılsalardı bu iki modülü kurmayan bir dağıtımda sepet hiç
// çalışmazdı; modülerlik tam olarak bunun mümkün olması demektir.
func TestFromContainerOpsiyonelYuzeySizKurulur(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, twoLineCart(1))

	c := container.New(nil)
	provideAll(t, c, h)

	wf, err := FromContainer(c)
	require.NoError(t, err)

	totals, err := wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)
	assert.Equal(t, TaxSourceRegion, totals.TaxSource)
	assert.Zero(t, totals.DiscountTotal)
}

// TestFromContainerOpsiyonelUyumsuzTipiBildirir kayıtlı ama yüzeyi
// KARŞILAMAYAN bir opsiyonel servisin sessizce yok sayılmadığını doğrular.
//
// Ayrım kritiktir: "kayıtlı değil" bir dağıtım kararıdır, "kayıtlı ama yanlış
// tip" bir kablolama hatasıdır. İkincisi degradasyona uğrasaydı, yanlış
// kaydedilmiş bir tax modülü sonsuza kadar görünmez kalırdı.
func TestFromContainerOpsiyonelUyumsuzTipiBildirir(t *testing.T) {
	for _, name := range []string{ServicePromotion, ServiceTax} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			c := container.New(nil)
			provideAll(t, c, h)
			require.NoError(t, c.Provide(name, h.regions))

			_, err := FromContainer(c)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
			assert.Contains(t, err.Error(), name)
		})
	}
}

// TestNewOpsiyonelBagimlilikSiz eksik opsiyonel yüzeyin KURULUMU düşürmediğini
// doğrular; zorunlu yüzeylerin testi TestNewEksikBagimliligiReddeder'dir.
func TestNewOpsiyonelBagimlilikSiz(t *testing.T) {
	h := newHarness(t)

	wf, err := New(Deps{
		Carts:     h.carts,
		Prices:    h.prices,
		Regions:   h.regions,
		Customers: h.customers,
		Links:     h.links,
		Catalog:   h.catalog,
	})
	require.NoError(t, err)
	assert.NotNil(t, wf)
}

package checkout

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Sepet hesabının container'dan çözdüğü servis adları.
//
// Bu paket onları ADLA çözmez; adlar yalnızca [FromContainer]'ın kurduğu sepet
// hesabının bağımlılıklarını kaydetmek için gerekir.
const (
	svcPricing  = "pricing.service"
	svcRegion   = "region.service"
	svcCustomer = "customer.service"
)

// stubPricing sepet hesabının pricing yüzeyini karşılar.
type stubPricing struct{}

// CalculateAmount fiyat kümesinin birim tutarını döner.
func (stubPricing) CalculateAmount(
	_ context.Context,
	priceSetID, _ string,
	_ int32,
	_ map[string]string,
) (int64, error) {
	switch priceSetID {
	case testPriceSetA:
		return 1000, nil
	case testPriceSetB:
		return 500, nil
	default:
		return 0, errUnexpected("CalculateAmount: " + priceSetID)
	}
}

// CalculateAmountsJSON sepet hesabının TOPLU fiyat yüzeyini karşılar.
//
// Yanıt istekle aynı sırada ve aynı uzunluktadır; fiyatı olmayan kalem hata
// değil bayrakla bildirilir (gerçek pricing modülünün sözleşmesi budur).
func (s stubPricing) CalculateAmountsJSON(
	ctx context.Context,
	request json.RawMessage,
) (json.RawMessage, error) {
	var req struct {
		Items []struct {
			PriceSetID string `json:"price_set_id"`
			Quantity   int32  `json:"quantity"`
		} `json:"items"`
	}
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}

	type item struct {
		Amount int64 `json:"amount"`
		Priced bool  `json:"priced"`
	}
	out := struct {
		Items []item `json:"items"`
	}{Items: make([]item, 0, len(req.Items))}

	for i := range req.Items {
		amount, err := s.CalculateAmount(ctx, req.Items[i].PriceSetID, "", req.Items[i].Quantity, nil)
		if err != nil {
			out.Items = append(out.Items, item{})
			continue
		}
		out.Items = append(out.Items, item{Amount: amount, Priced: true})
	}
	return json.Marshal(out)
}

// stubRegions sepet hesabının region yüzeyini karşılar.
type stubRegions struct{}

// RegionIDForCountry bu akışta çağrılmaz.
func (stubRegions) RegionIDForCountry(_ context.Context, _ string) (string, error) {
	return "", errUnexpected("RegionIDForCountry")
}

// RegionCurrency bu akışta çağrılmaz.
func (stubRegions) RegionCurrency(_ context.Context, _ string) (code string, decimalDigits int32, err error) {
	return "", 0, errUnexpected("RegionCurrency")
}

// RegionTax %20'lik otomatik vergiyi bildirir.
func (stubRegions) RegionTax(_ context.Context, _ string) (rateBps int32, automatic bool, err error) {
	return 2000, true, nil
}

// stubCustomers sepet hesabının customer yüzeyini karşılar.
type stubCustomers struct{}

// CustomerEmail bu akışta çağrılmaz.
func (stubCustomers) CustomerEmail(_ context.Context, _ string) (string, error) {
	return "", errUnexpected("CustomerEmail")
}

// provideCheckout container'a bu akışın kendi yüzeylerini kaydeder.
func provideCheckout(t *testing.T, c *container.Container, h *harness) {
	t.Helper()

	require.NoError(t, c.Provide(ServiceCart, h.carts))
	require.NoError(t, c.Provide(ServiceInventory, h.inventory))
	require.NoError(t, c.Provide(ServiceFulfillment, h.fulfillment))
	require.NoError(t, c.Provide(ServiceOrder, h.orders))
	require.NoError(t, c.Provide(ServicePayment, h.payments))
	require.NoError(t, c.Provide(ServiceLink, h.links))
	require.NoError(t, c.Provide(ServiceQuery, h.catalog))
	require.NoError(t, c.Provide(ServiceWorkflow, workflow.NewInMemory(slog.New(slog.DiscardHandler))))
}

// provideCartTotals container'a sepet hesabının bağımlılıklarını kaydeder.
func provideCartTotals(t *testing.T, c *container.Container) {
	t.Helper()

	require.NoError(t, c.Provide(svcPricing, stubPricing{}))
	require.NoError(t, c.Provide(svcRegion, stubRegions{}))
	require.NoError(t, c.Provide(svcCustomer, stubCustomers{}))
}

// TestFromContainerAdlaCozer bağımlılıkların container'dan ADLA çözüldüğünü ve
// çözülen akışın uçtan uca çalıştığını doğrular (ADR 0006).
//
// Test aynı zamanda paketler arası TEK derleme zamanı bağını sınar: hesabı
// üreten GERÇEK sepet akışıdır (sahtesi değil) ve ürettiği satır tutarları
// siparişe olduğu gibi geçer. Sepet %20 vergili iki satır taşır; hesap 2500 +
// 500 = 3000 üretmelidir.
func TestFromContainerAdlaCozer(t *testing.T) {
	h := newHarness(t)
	h.carts.setTotalsFn = func(context.Context, string) error { return nil }

	c := container.New(nil)
	provideCheckout(t, c, h)
	provideCartTotals(t, c)

	wf, err := FromContainer(c)
	require.NoError(t, err)

	out, err := wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, testOrderID, out.OrderID)
	assert.Equal(t, testAmount, out.Amount)
	require.Len(t, h.orders.placed, 1)
	assert.Equal(t, testAmount, h.orders.placed[0].Total)
	assert.Equal(t, int64(1000), h.orders.placed[0].Items[0].UnitPrice)
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
	assert.Contains(t, err.Error(), ServiceInventory)
}

// TestFromContainerUyumsuzTipiBildirir kayıtlı ama yüzeyi karşılamayan bir
// servisin sessizce kabul edilmediğini doğrular.
func TestFromContainerUyumsuzTipiBildirir(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)

	require.NoError(t, c.Provide(ServiceCart, h.carts))
	require.NoError(t, c.Provide(ServiceInventory, h.inventory))
	require.NoError(t, c.Provide(ServiceFulfillment, h.fulfillment))
	// "order.interop" adına [Orders] yüzeyini KARŞILAMAYAN bir değer konur.
	require.NoError(t, c.Provide(ServiceOrder, h.links))
	require.NoError(t, c.Provide(ServicePayment, h.payments))
	require.NoError(t, c.Provide(ServiceLink, h.links))
	require.NoError(t, c.Provide(ServiceQuery, h.catalog))
	require.NoError(t, c.Provide(ServiceWorkflow, workflow.NewInMemory(slog.New(slog.DiscardHandler))))

	_, err := FromContainer(c)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServiceOrder)
}

// TestFromContainerSepetHesabiniKuramazsaBildirir hesabın bağımlılıkları eksik
// olduğunda hatanın anlaşılır olduğunu doğrular.
func TestFromContainerSepetHesabiniKuramazsaBildirir(t *testing.T) {
	h := newHarness(t)
	c := container.New(nil)
	provideCheckout(t, c, h)

	_, err := FromContainer(c)
	require.Error(t, err)
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "sepet hesabını kuramadı")
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
			Carts:       h.carts,
			Totals:      h.totals,
			Inventory:   h.inventory,
			Fulfillment: h.fulfillment,
			Orders:      h.orders,
			Payments:    h.payments,
			Links:       h.links,
			Catalog:     h.catalog,
			Executor:    workflow.NewInMemory(slog.New(slog.DiscardHandler)),
		}
	}

	tests := map[string]func(*Deps){
		ServiceCart:        func(d *Deps) { d.Carts = nil },
		serviceCartTotals:  func(d *Deps) { d.Totals = nil },
		ServiceInventory:   func(d *Deps) { d.Inventory = nil },
		ServiceFulfillment: func(d *Deps) { d.Fulfillment = nil },
		ServiceOrder:       func(d *Deps) { d.Orders = nil },
		ServicePayment:     func(d *Deps) { d.Payments = nil },
		ServiceLink:        func(d *Deps) { d.Links = nil },
		ServiceQuery:       func(d *Deps) { d.Catalog = nil },
		ServiceWorkflow:    func(d *Deps) { d.Executor = nil },
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

// TestNewLoggersizKurulabilir logger verilmediğinde akışın yine çalıştığını
// doğrular.
func TestNewLoggersizKurulabilir(t *testing.T) {
	h := newHarness(t)

	wf, err := New(Deps{
		Carts:       h.carts,
		Totals:      h.totals,
		Inventory:   h.inventory,
		Fulfillment: h.fulfillment,
		Orders:      h.orders,
		Payments:    h.payments,
		Links:       h.links,
		Catalog:     h.catalog,
		Executor:    workflow.NewInMemory(slog.New(slog.DiscardHandler)),
	})
	require.NoError(t, err)

	out, err := wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)
	assert.Equal(t, testOrderID, out.OrderID)
}

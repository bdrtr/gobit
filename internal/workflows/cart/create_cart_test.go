package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// recordOpenCart sahte sepet servisini, açılan sepetin argümanlarını
// kaydedecek biçimde betikler.
func recordOpenCart(carts *stubCarts, cartID string) *[]string {
	seen := &[]string{}
	carts.openCartFn = func(_ context.Context, regionID, currencyCode, customerID, email string) (string, error) {
		*seen = []string{regionID, currencyCode, customerID, email}
		return cartID, nil
	}
	return seen
}

// TestCreateCartMisafir misafir sepetinin customer modülüne HİÇ dokunmadan
// açıldığını doğrular.
func TestCreateCartMisafir(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "TR"})
	require.NoError(t, err)

	assert.Equal(t, CreateCartResult{
		CartID:       testCartID,
		RegionID:     testRegionID,
		CurrencyCode: testCurrency,
		Guest:        true,
	}, out)
	assert.Equal(t, []string{testRegionID, testCurrency, "", ""}, *seen)
	assert.Zero(t, h.customers.calls, "misafir akışı customer servisine bağımlı olmamalı")
}

// TestCreateCartMisafirEpostaTasinir misafir sepetinin verilen e-postayla
// açıldığını doğrular.
func TestCreateCartMisafirEpostaTasinir(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		Email:       " misafir@example.com ",
	})
	require.NoError(t, err)

	assert.Equal(t, "misafir@example.com", out.Email)
	assert.Equal(t, "misafir@example.com", (*seen)[3])
	assert.True(t, out.Guest)
}

// TestCreateCartKayitliMusteri kayıtlı müşteri sepetinin müşteriye bağlandığını
// ve e-postanın müşteri kaydından geldiğini doğrular.
func TestCreateCartKayitliMusteri(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		CustomerID:  testCustomerID,
	})
	require.NoError(t, err)

	assert.False(t, out.Guest)
	assert.Equal(t, testCustomerID, out.CustomerID)
	assert.Equal(t, "kayitli@example.com", out.Email)
	assert.Equal(t, []string{testRegionID, testCurrency, testCustomerID, "kayitli@example.com"}, *seen)
}

// TestCreateCartVerilenEpostaMusterininkiniEzmez çağıranın e-postasının
// korunduğunu doğrular.
//
// Sepetin adresi, o siparişin gönderileceği adrestir; müşteri defterindeki
// güncel adresle EZİLMESİ, müşterinin bu sipariş için bilinçli olarak girdiği
// adresi sessizce atmak olurdu.
func TestCreateCartVerilenEpostaMusterininkiniEzmez(t *testing.T) {
	h := newHarness(t)
	recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		CustomerID:  testCustomerID,
		Email:       "bu-siparis@example.com",
	})
	require.NoError(t, err)

	assert.Equal(t, "bu-siparis@example.com", out.Email)
}

// TestCreateCartBilinmeyenMusteriReddedilir var olmayan müşteriye sepet
// açılmadığını doğrular.
func TestCreateCartBilinmeyenMusteriReddedilir(t *testing.T) {
	h := newHarness(t)
	opened := false
	h.carts.openCartFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		opened = true
		return testCartID, nil
	}

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		CustomerID:  "cust_yok",
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.False(t, opened, "doğrulama düşerse sepet hiç açılmamalı")
}

// TestCreateCartBilinmeyenUlkeReddedilir bölgesi olmayan ülkede sepet
// açılmadığını doğrular.
func TestCreateCartBilinmeyenUlkeReddedilir(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "ZZ"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestCreateCartGecersizGirdiReddedilir biçimsiz girdinin hiçbir modüle
// ulaşmadığını doğrular.
func TestCreateCartGecersizGirdiReddedilir(t *testing.T) {
	tests := map[string]CreateCartInput{
		"ülke boş":              {CountryCode: "   "},
		"müşteri boşluk içerir": {CountryCode: "TR", CustomerID: " cust_1"},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.wf.CreateCart(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
			assert.Zero(t, h.customers.calls)
		})
	}
}

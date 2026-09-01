package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// recordOpenCart sahte sepet servisini, açılan sepetin argümanlarını
// kaydedecek biçimde betikler.
func recordOpenCart(carts *stubCarts, cartID string) *[]string {
	seen := &[]string{}
	carts.openCartFn = func(
		_ context.Context, regionID, currencyCode, customerID, email string, _ json.RawMessage,
	) (string, error) {
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
	h.carts.openCartFn = func(_ context.Context, _, _, _, _ string, _ json.RawMessage) (string, error) {
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

// TestCreateCartMetadataOlduguGibiTasinir sepete iliştirilen serbest verinin
// akış tarafından DEĞİŞTİRİLMEDEN geçtiğini doğrular.
//
// Alan, bölge ve para biriminden ayrı bir sınıftadır: gerçekten çağıranın
// kendi verisidir ve türetilecek bir karşılığı yoktur. Akışın onu okuması ya
// da yeniden kodlaması, vitrinin gönderdiği gövdeyi sessizce değiştirebilecek
// tek yer olurdu; iddia tam da bunun olmadığıdır. Aynı karar satır
// metadata'sında da verilmişti.
func TestCreateCartMetadataOlduguGibiTasinir(t *testing.T) {
	h := newHarness(t)

	beklenen := json.RawMessage(`{"kaynak":"vitrin"}`)
	var gelen json.RawMessage
	h.carts.openCartFn = func(
		_ context.Context, _, _, _, _ string, metadata json.RawMessage,
	) (string, error) {
		gelen = metadata
		return testCartID, nil
	}

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		Metadata:    beklenen,
	})
	require.NoError(t, err)

	assert.JSONEq(t, string(beklenen), string(gelen),
		"metadata sepete olduğu gibi ulaşmalı")
}

// TestOpenCartForCountryBolgeyiTuretir modüller arası yüzeyin bölgeyi ÜLKEDEN
// çözdüğünü ve dışarıya yalnızca kimlik verdiğini doğrular.
//
// Yüzeyin bölge parametresi YOKTUR ve bu, sepet açma ucunun akışa
// bağlanmasının çekirdek gerekçesidir: parametre olsaydı çağıranın (yani
// vitrin ucunun) onu istemciden gelen bir değerle doldurmasının önünde hiçbir
// şey kalmazdı.
func TestOpenCartForCountryBolgeyiTuretir(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	cartID, err := NewInterop(h.wf).OpenCartForCountry(
		context.Background(), "TR", "", "misafir@example.com", nil)
	require.NoError(t, err)

	assert.Equal(t, testCartID, cartID)
	assert.Equal(t, []string{testRegionID, testCurrency, "", "misafir@example.com"}, *seen,
		"bölge ve para birimi ÜLKEDEN türetilmeli")
}

// TestOpenCartForCountryBilinmeyenUlkedeSepetAcmaz yüzeyin hatayı yutmadığını
// doğrular.
//
// İddia kimliğin boş dönmesinden ibaret değildir: sepet HİÇ açılmamalıdır.
// Bölgesi olmayan bir ülkede "bir şekilde" sepet açmak, bölgeyi bir
// varsayılana düşürmekle aynı kapıya çıkardı.
func TestOpenCartForCountryBilinmeyenUlkedeSepetAcmaz(t *testing.T) {
	h := newHarness(t)
	opened := false
	h.carts.openCartFn = func(_ context.Context, _, _, _, _ string, _ json.RawMessage) (string, error) {
		opened = true
		return testCartID, nil
	}

	cartID, err := NewInterop(h.wf).OpenCartForCountry(context.Background(), "ZZ", "", "", nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Empty(t, cartID)
	assert.False(t, opened, "bölgesi olmayan ülkede sepet YAZILMAMALI")
}

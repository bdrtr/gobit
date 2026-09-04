package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// TestNormalizeEmail e-posta normalizasyonunun kırpma ve küçük harfe indirme
// yaptığını kanıtlar.
func TestNormalizeEmail(t *testing.T) {
	durumlar := map[string]struct{ girdi, beklenen string }{
		"büyük harf":      {"ALI@EXAMPLE.COM", "ali@example.com"},
		"karışık":         {"Ali.Veli@Example.Com", "ali.veli@example.com"},
		"boşluk":          {"  ali@example.com \t", "ali@example.com"},
		"zaten normalize": {"ali@example.com", "ali@example.com"},
		"boş":             {"   ", ""},
	}

	for ad, durum := range durumlar {
		t.Run(ad, func(t *testing.T) {
			assert.Equal(t, durum.beklenen, models.NormalizeEmail(durum.girdi))
		})
	}
}

// TestNormalizeEmailIdempotenttir normalizasyonun ikinci uygulamasının değeri
// değiştirmediğini kanıtlar.
//
// İdempotans şart: aynı değer hem yazma hem okuma yolunda normalize edilir ve
// iki geçişin farklı sonuç vermesi, kaydın kendi e-postasıyla bulunamaması
// demekti.
func TestNormalizeEmailIdempotenttir(t *testing.T) {
	bir := models.NormalizeEmail("  Ali@Example.COM ")
	iki := models.NormalizeEmail(bir)
	assert.Equal(t, bir, iki)
}

// TestNormalizeCountryCode ülke kodunun BÜYÜK harfe çevrildiğini kanıtlar.
func TestNormalizeCountryCode(t *testing.T) {
	assert.Equal(t, "TR", models.NormalizeCountryCode(" tr "))
	assert.Equal(t, "DE", models.NormalizeCountryCode("De"))
	assert.Equal(t, "", models.NormalizeCountryCode("  "))
}

// TestKimlikBicimi kimliklerin önekli ve sabit uzunlukta olduğunu kanıtlar.
func TestKimlikBicimi(t *testing.T) {
	an := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	durumlar := map[string]struct {
		uret func(time.Time) string
		onek string
	}{
		"müşteri":        {models.NewCustomerID, models.CustomerIDPrefix},
		"grup":           {models.NewCustomerGroupID, models.CustomerGroupIDPrefix},
		"müşteri adresi": {models.NewAddressID, models.AddressIDPrefix},
	}

	for ad, durum := range durumlar {
		t.Run(ad, func(t *testing.T) {
			id := durum.uret(an)
			assert.True(t, strings.HasPrefix(id, durum.onek), "%q öneki bekleniyor: %q", id, durum.onek)
			assert.Len(t, strings.TrimPrefix(id, durum.onek), models.IDBodyLength())
		})
	}
}

// TestKimlikTekildir aynı anda üretilen kimliklerin çakışmadığını kanıtlar.
func TestKimlikTekildir(t *testing.T) {
	an := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	gorulen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := models.NewCustomerID(an)
		_, dup := gorulen[id]
		require.False(t, dup, "the id repeated: %s", id)
		gorulen[id] = struct{}{}
	}
}

// TestKimlikZamanSiralidir kimliğin sözlüksel sırasının zaman sırasını
// koruduğunu kanıtlar.
//
// Sıralanabilirlik "ORDER BY id" ile doğal oluşturma sırası almanın tek
// dayanağıdır; zaman damgası gövdenin BAŞINDA olmasaydı sıra rastgele olurdu.
func TestKimlikZamanSiralidir(t *testing.T) {
	once := models.NewCustomerID(time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC))
	sonra := models.NewCustomerID(time.Date(2026, 8, 23, 12, 0, 1, 0, time.UTC))
	assert.Less(t, once, sonra, "sonraki kimlik sözlüksel olarak büyük olmalı")
}

// TestIsGuest misafir/hesap ayrımının modelde okunabildiğini kanıtlar.
func TestIsGuest(t *testing.T) {
	assert.True(t, models.Customer{HasAccount: false}.IsGuest())
	assert.False(t, models.Customer{HasAccount: true}.IsGuest())
}

// TestDefaultKind varsayılan adresin tür doğrulamasını kanıtlar.
//
// Tip dışa açıktır ve çağıran enum dışında bir değer kurabilir; böyle bir değer
// sessizce kargoya düşseydi istemci fatura adresini işaretlediğini sanırken
// kargo adresini değiştirirdi.
func TestDefaultKind(t *testing.T) {
	assert.True(t, models.DefaultShipping.Valid())
	assert.True(t, models.DefaultBilling.Valid())
	assert.False(t, models.DefaultKind(42).Valid(), "tanımsız değer geçersiz olmalı")

	assert.Equal(t, "shipping", models.DefaultShipping.String())
	assert.Equal(t, "billing", models.DefaultBilling.String())
}

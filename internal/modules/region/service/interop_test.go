package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// TestInteropSurfaceUsesPrimitiveTypes modüller arası yüzeyin, tüketicinin
// KENDİ paketinde tanımlayabileceği dar arayüzleri yapısal olarak
// karşıladığını kanıtlar (ADR 0001).
//
// Buradaki arayüzler bilinçli olarak region'ın hiçbir tipini adlandırmaz;
// cart modülü Faz 5'te tam olarak böyle bir arayüz yazacak ve somut servisi
// container'dan "region.service" adıyla çözecektir. Bu atama derlenmezse,
// tüketici tarafında da derlenmeyecek demektir — hata, çalışma zamanında
// container'dan çözüm anında değil BURADA yakalanır.
func TestInteropSurfaceUsesPrimitiveTypes(t *testing.T) {
	// Tüketici modülün yazacağı arayüzlerin birebir kopyası.
	type regionResolver interface {
		RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
	}
	type regionCurrencyReader interface {
		RegionCurrency(ctx context.Context, regionID string) (string, int32, error)
	}
	type regionTaxReader interface {
		RegionTax(ctx context.Context, regionID string) (int32, bool, error)
	}
	type currencyReader interface {
		CurrencyDecimalDigits(ctx context.Context, currencyCode string) (int32, error)
	}

	svc, _ := newTestService(t)

	var (
		_ regionResolver       = svc
		_ regionCurrencyReader = svc
		_ regionTaxReader      = svc
		_ currencyReader       = svc
	)
}

// TestRegionIDForCountry ülkeden bölge kimliğine giden dar yüzeyi kanıtlar.
func TestRegionIDForCountry(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err)

	id, err := svc.RegionIDForCountry(ctx, "tr")
	require.NoError(t, err)
	assert.Equal(t, region.ID, id)

	_, err = svc.RegionIDForCountry(ctx, "DE")
	require.Error(t, err)
	assert.Equal(t, CodeCountryUnassigned, errors.CodeOf(err))
}

// TestRegionCurrencyReturnsDecimalDigits bölgenin para birimiyle birlikte
// ONDALIK BASAMAK sayısının da döndüğünü kanıtlar.
//
// Basamak sayısı olmadan sepet, minor unit tam sayıyı hangi çarpanla
// göstereceğini bilemez; sabit 100 varsayan bir sunum katmanı yen tutarlarını
// yüz kat küçük gösterirdi.
func TestRegionCurrencyReturnsDecimalDigits(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	tryRegion := newRegion(t, svc, "TRY")
	jpyRegion := newRegion(t, svc, "JPY")
	kwdRegion := newRegion(t, svc, "KWD")

	code, digits, err := svc.RegionCurrency(ctx, tryRegion.ID)
	require.NoError(t, err)
	assert.Equal(t, "TRY", code)
	assert.Equal(t, int32(2), digits)

	code, digits, err = svc.RegionCurrency(ctx, jpyRegion.ID)
	require.NoError(t, err)
	assert.Equal(t, "JPY", code)
	assert.Equal(t, int32(0), digits, "JPY ondalıksızdır")

	code, digits, err = svc.RegionCurrency(ctx, kwdRegion.ID)
	require.NoError(t, err)
	assert.Equal(t, "KWD", code)
	assert.Equal(t, int32(3), digits, "KWD üç basamaklıdır")
}

// TestRegionCurrencyRejectsUnknownRegion olmayan bölge için bulunamadı
// döndüğünü kanıtlar.
func TestRegionCurrencyRejectsUnknownRegion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	_, _, err := svc.RegionCurrency(ctx, "reg_YOK")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, _, err = svc.RegionCurrency(ctx, "cart_01")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "yanlış önek doğrulama hatasıdır")
}

// TestRegionTaxReturnsBasisPoints vergi oranının BAZ PUAN tam sayı olarak
// döndüğünü ve otomatik vergi bayrağını taşıdığını kanıtlar.
func TestRegionTaxReturnsBasisPoints(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	rate, automatic, err := svc.RegionTax(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2000), rate, "%20 = 2000 baz puan")
	assert.True(t, automatic)

	// Vergi tam sayı aritmetiğiyle hesaplanır: 19,99 TRY (1999 kuruş) için
	// 1999 * 2000 / 10000 = 399 kuruş. float bir oranla aynı hesap kuruş
	// düzeyinde sessizce kayardı.
	const subtotal int64 = 1999
	tax := subtotal * int64(rate) / int64(models.MaxTaxRate)
	assert.Equal(t, int64(399), tax)

	off := false
	zero := int32(0)
	_, err = svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{AutomaticTaxes: &off, TaxRate: &zero})
	require.NoError(t, err)

	rate, automatic, err = svc.RegionTax(ctx, region.ID)
	require.NoError(t, err)
	assert.Zero(t, rate)
	assert.False(t, automatic)
}

// TestCurrencyDecimalDigits kod üzerinden ondalık basamak okumasını kanıtlar.
func TestCurrencyDecimalDigits(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	digits, err := svc.CurrencyDecimalDigits(ctx, "jpy")
	require.NoError(t, err)
	assert.Zero(t, digits)

	digits, err = svc.CurrencyDecimalDigits(ctx, "KWD")
	require.NoError(t, err)
	assert.Equal(t, int32(3), digits)

	_, err = svc.CurrencyDecimalDigits(ctx, "XYZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

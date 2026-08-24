package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestRegistryAyniKimlikleIkinciKayitCakisirVeMevcutuKorur sessizce üzerine
// yazmanın reddedildiğini doğrular.
//
// İki eklentinin aynı kimliği kullandığı bir kurulumda üzerine yazmak, hangi
// sağlayıcının çalıştığını YÜKLEME SIRASINA bırakırdı; ödemede bunun bedeli
// paranın beklenmedik bir kuruluşa gitmesidir.
func TestRegistryAyniKimlikleIkinciKayitCakisirVeMevcutuKorur(t *testing.T) {
	registry := service.NewProviderRegistry()
	ilk := newFakeProvider("manual")
	ikinci := newFakeProvider("manual")

	require.NoError(t, registry.Register(ilk))
	err := registry.Register(ikinci)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeProviderExists, errors.CodeOf(err))

	cozulen, getErr := registry.Get("manual")
	require.NoError(t, getErr)
	assert.Same(t, ilk, cozulen, "mevcut sağlayıcı KORUNMALI")
}

// TestRegistryBilinmeyenKimlikTeshisEdilebilirHataVerir sağlayıcının
// kaydedilmeyi unutulmasının okunabilir bir hata verdiğini doğrular (ADR 0002).
func TestRegistryBilinmeyenKimlikTeshisEdilebilirHataVerir(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("manual")))
	require.NoError(t, registry.Register(newFakeProvider("stripe")))

	_, err := registry.Get("adyen")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)
	assert.Contains(t, err.Error(), "adyen", "aranan kimlik yazılmalı")
	assert.Contains(t, err.Error(), "manual", "kayıtlı kimlikler yazılmalı")
	assert.Contains(t, err.Error(), "stripe")
}

// TestRegistryGecersizKayitlarReddedilir nil ve kimliksiz sağlayıcıların
// kaydedilemeyeceğini doğrular.
func TestRegistryGecersizKayitlarReddedilir(t *testing.T) {
	registry := service.NewProviderRegistry()

	require.Error(t, registry.Register(nil))

	err := registry.Register(newFakeProvider("   "))
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)

	_, err = registry.Get("")
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
}

// TestRegistryIDsSiralidir kimlik listesinin SABİT sırada döndüğünü doğrular.
//
// Harita üzerinde dönerek üretilen bir liste her çağrıda başka bir sırada
// çıkar; hem API yanıtı hem hata mesajı öngörülemez olurdu.
func TestRegistryIDsSiralidir(t *testing.T) {
	registry := service.NewProviderRegistry()
	for _, id := range []string{"stripe", "adyen", "manual"} {
		require.NoError(t, registry.Register(newFakeProvider(id)))
	}

	assert.Equal(t, []string{"adyen", "manual", "stripe"}, registry.IDs())
}

// TestRegistryKimlikKirpilir baştaki ve sondaki boşlukların çözümde sorun
// çıkarmadığını doğrular.
func TestRegistryKimlikKirpilir(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("  manual  ")))

	_, err := registry.Get("manual")
	require.NoError(t, err)
	assert.Equal(t, []string{"manual"}, registry.IDs())
}

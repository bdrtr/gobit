package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestSaglayiciKaydiUzerineYazmaz aynı kimliğin ikinci kez kaydedilemediğini
// kanıtlar.
//
// Sessizce üzerine yazmak, iki eklentinin aynı kimliği kullandığı bir
// kurulumda hangi sağlayıcının çalıştığını yükleme sırasına bırakırdı; kargoda
// bunun bedeli, paketin beklenmedik bir firmaya verilmesidir.
func TestSaglayiciKaydiUzerineYazmaz(t *testing.T) {
	t.Parallel()

	kayit := service.NewProviderRegistry()
	ilk := newFakeProvider("manual")
	require.NoError(t, kayit.Register(ilk))

	ikinci := newFakeProvider("manual")
	err := kayit.Register(ikinci)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeProviderExists, errors.CodeOf(err))

	cozulen, err := kayit.Get("manual")
	require.NoError(t, err)
	assert.Same(t, ilk, cozulen, "mevcut sağlayıcı korunmalı")
}

// TestSaglayiciKaydiBosKimligiReddeder kimliksiz sağlayıcının kaydedilmediğini
// kanıtlar.
func TestSaglayiciKaydiBosKimligiReddeder(t *testing.T) {
	t.Parallel()

	kayit := service.NewProviderRegistry()

	require.Error(t, kayit.Register(nil))
	err := kayit.Register(newFakeProvider("   "))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
}

// TestSaglayiciBulunamadigindaKayitliListelenir hata mesajının teşhis
// edilebilir olduğunu kanıtlar (ADR 0002).
func TestSaglayiciBulunamadigindaKayitliListelenir(t *testing.T) {
	t.Parallel()

	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(newFakeProvider("manual")))
	require.NoError(t, kayit.Register(newFakeProvider("aras")))

	_, err := kayit.Get("yurtici")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata errors.NotFound olmalı: %v", err)
	assert.Contains(t, err.Error(), "yurtici", "aranan kimlik yazılmalı")
	assert.Contains(t, err.Error(), "manual", "kayıtlı kimlikler yazılmalı")
	assert.Contains(t, err.Error(), "aras")
}

// TestSaglayiciKimlikleriSirali sıranın sabit olduğunu kanıtlar.
//
// Harita üzerinde dönerek üretilseydi her çağrıda başka bir sırada çıkar,
// teşhisi ve testi zorlaştırırdı.
func TestSaglayiciKimlikleriSirali(t *testing.T) {
	t.Parallel()

	kayit := service.NewProviderRegistry()
	for _, id := range []string{"ptt", "aras", "manual"} {
		require.NoError(t, kayit.Register(newFakeProvider(id)))
	}
	assert.Equal(t, []string{"aras", "manual", "ptt"}, kayit.IDs())
}

// TestSaglayiciHasKayitliligiBildirir Has'ın kayıt durumunu doğru döndüğünü
// kanıtlar.
func TestSaglayiciHasKayitliligiBildirir(t *testing.T) {
	t.Parallel()

	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(newFakeProvider("manual")))

	assert.True(t, kayit.Has("manual"))
	assert.True(t, kayit.Has(" manual "), "boşluklar kırpılmalı")
	assert.False(t, kayit.Has("aras"))
	assert.False(t, kayit.Has(""))
}

// TestServisEksikBagimlilikylaKurulamaz kurulum hatasının açıkça döndüğünü
// kanıtlar.
//
// nil bir depoyla kurulmuş servis ilk istekte panik üretirdi ve hata,
// kurulumdan çok sonra ortaya çıkardı.
func TestServisEksikBagimlilikylaKurulamaz(t *testing.T) {
	t.Parallel()

	_, err := service.New(service.Options{Providers: service.NewProviderRegistry()})
	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))

	_, err = service.New(service.Options{Store: newFakeStore()})
	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
}

// TestProviderIDsServistenOkunur servisin kayıtlı sağlayıcıları yansıttığını
// kanıtlar.
func TestProviderIDsServistenOkunur(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	assert.Equal(t, []string{"sahte"}, kurulum.svc.ProviderIDs(t.Context()))
}

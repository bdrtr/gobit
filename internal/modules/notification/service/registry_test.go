package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// TestRegistryAyniKimlikleIkinciKayitCakisirVeMevcutuKorur sessizce üzerine
// yazmanın reddedildiğini doğrular.
//
// İki eklentinin aynı kimliği kullandığı bir kurulumda üzerine yazmak, hangi
// sağlayıcının çalıştığını YÜKLEME SIRASINA bırakırdı; bildirimde bunun bedeli,
// sipariş onaylarının yanlış hesaptan gitmesi ya da hiç gitmemesidir.
func TestRegistryAyniKimlikleIkinciKayitCakisirVeMevcutuKorur(t *testing.T) {
	registry := service.NewProviderRegistry()
	ilk := newFakeProvider("log")
	ikinci := newFakeProvider("log")

	require.NoError(t, registry.Register(ilk))
	err := registry.Register(ikinci)

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata: %v", err)
	assert.Equal(t, service.CodeProviderExists, errors.CodeOf(err))

	cozulen, getErr := registry.Get("log")
	require.NoError(t, getErr)
	assert.Same(t, ilk, cozulen, "mevcut sağlayıcı KORUNMALI")
}

// TestRegistryBilinmeyenKimlikTeshisEdilebilirHataVerir sağlayıcının
// kaydedilmeyi unutulmasının (ya da adın yanlış yazılmasının) okunabilir bir
// hata verdiğini doğrular (ADR 0002).
func TestRegistryBilinmeyenKimlikTeshisEdilebilirHataVerir(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("log")))
	require.NoError(t, registry.Register(newFakeProvider("sendgrid")))

	_, err := registry.Get("mailgun")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata: %v", err)
	assert.Contains(t, err.Error(), "mailgun", "aranan kimlik yazılmalı")
	assert.Contains(t, err.Error(), "log", "kayıtlı kimlikler yazılmalı")
	assert.Contains(t, err.Error(), "sendgrid")
}

// TestRegistryGecersizKayitlarReddedilir nil ve kimliksiz sağlayıcıların
// kaydedilemeyeceğini doğrular.
//
// Kimliksiz bir sağlayıcı kaydedilebilseydi, hiçbir yapılandırma onu
// seçemeyecek ama kayıt "dolu" görünecekti.
func TestRegistryGecersizKayitlarReddedilir(t *testing.T) {
	registry := service.NewProviderRegistry()

	require.Error(t, registry.Register(nil))

	err := registry.Register(newFakeProvider("   "))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata: %v", err)
	assert.Empty(t, registry.IDs())
}

// TestRegistryIDsSiraliDoner kimliklerin kararlı sırada döndüğünü doğrular.
//
// Sıra map üzerinden gelseydi hata mesajları ve açılış logu her çalıştırmada
// başka bir sırada çıkar, teşhisi zorlaştırırdı.
func TestRegistryIDsSiraliDoner(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("sendgrid")))
	require.NoError(t, registry.Register(newFakeProvider("log")))
	require.NoError(t, registry.Register(newFakeProvider("mailgun")))

	assert.Equal(t, []string{"log", "mailgun", "sendgrid"}, registry.IDs())
}

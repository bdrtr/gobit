package paymentstripe_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// sahteKayit payment modülünün sağlayıcı kaydını taklit eder.
type sahteKayit struct {
	kayitli []coreprovider.PaymentProvider
}

// Register sağlayıcıyı listeye alır.
func (k *sahteKayit) Register(p coreprovider.PaymentProvider) error {
	k.kayitli = append(k.kayitli, p)

	return nil
}

// kurulum eklentiyi verilen ayarlarla kurup Start'a kadar götürür.
func kurulum(t *testing.T, ayarlar map[string]string) (*sahteKayit, error) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	kayit := &sahteKayit{}
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName, kayit))

	reg := coreplugin.NewRegistry(log)
	reg.Add(paymentstripe.New())

	h := coreplugin.NewHost(c, nil, nil, log, ayarlar)
	if err := reg.Install(t.Context(), h); err != nil {
		return kayit, err
	}

	return kayit, reg.Start(t.Context(), h)
}

// TestEklentiSaglayiciyiKaydeder eklentinin çekirdeğe dokunmadan takıldığını
// ve sağlayıcının kimliğiyle SEÇİLEBİLİR olduğunu doğrular (Faz 9 DoD).
func TestEklentiSaglayiciyiKaydeder(t *testing.T) {
	t.Parallel()

	kayit, err := kurulum(t, map[string]string{"STRIPE_API_KEY": "sk_test_1"})
	require.NoError(t, err)

	require.Len(t, kayit.kayitli, 1)
	assert.Equal(t, paymentstripe.ProviderID, kayit.kayitli[0].ID())
}

// TestAnahtarsizKurulumReddedilir yapılandırma eksikliğinin AÇILIŞTA
// patladığını doğrular.
//
// Sessizce atlansaydı, "stripe kurulu" sanılan bir mağaza hiç ödeme alamaz ve
// bu ancak ilk müşteri denemesinde görülürdü.
func TestAnahtarsizKurulumReddedilir(t *testing.T) {
	t.Parallel()

	tests := map[string]map[string]string{
		"ayar hiç yok":  nil,
		"ayar boş":      {"STRIPE_API_KEY": ""},
		"sadece boşluk": {"STRIPE_API_KEY": "   "},
	}

	for name, ayarlar := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kayit, err := kurulum(t, ayarlar)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "STRIPE_API_KEY")
			assert.Empty(t, kayit.kayitli, "eksik yapılandırmada sağlayıcı kaydedilmemeli")
		})
	}
}

// TestParaHareketiMetodlariSahteBasariDonmez iskeletin hiçbir metodunun
// sessizce "başarılı" DÖNMEDİĞİNİ doğrular.
//
// Bu testin koruduğu senaryo şudur: iskelet kazara üretime alınırsa, sahte
// başarı dönen bir Capture siparişleri ödenmiş gösterir ve mağaza hiç ödeme
// almadan mal gönderir. Gürültülü hata, sessiz yalandan ucuzdur.
func TestParaHareketiMetodlariSahteBasariDonmez(t *testing.T) {
	t.Parallel()

	kayit, err := kurulum(t, map[string]string{"STRIPE_API_KEY": "sk_test_1"})
	require.NoError(t, err)
	require.Len(t, kayit.kayitli, 1)

	p := kayit.kayitli[0]
	ctx := t.Context()

	t.Run("CreateSession", func(t *testing.T) {
		t.Parallel()

		_, err := p.CreateSession(ctx, coreprovider.CreateSessionInput{})
		assert.Error(t, err)
	})

	t.Run("Authorize", func(t *testing.T) {
		t.Parallel()

		_, err := p.Authorize(ctx, "sess_1")
		assert.Error(t, err)
	})

	t.Run("Capture", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, p.Capture(ctx, "sess_1", 1000))
	})

	t.Run("Refund", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, p.Refund(ctx, "sess_1", 1000))
	})

	t.Run("Cancel", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, p.Cancel(ctx, "sess_1"))
	})
}

// TestAnahtarHataMesajinaSizmaz gizli anahtarın hata metinlerine
// karışmadığını doğrular.
func TestAnahtarHataMesajinaSizmaz(t *testing.T) {
	t.Parallel()

	const sir = "sk_live_COKGIZLI123"

	kayit, err := kurulum(t, map[string]string{"STRIPE_API_KEY": sir})
	require.NoError(t, err)
	require.Len(t, kayit.kayitli, 1)

	_, hata := kayit.kayitli[0].Authorize(t.Context(), "sess_1")
	require.Error(t, hata)
	assert.NotContains(t, hata.Error(), sir, "gizli anahtar hata mesajına sızmamalı")
}

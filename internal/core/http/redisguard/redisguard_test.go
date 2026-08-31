package redisguard_test

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/http/redisguard"
)

// Bu dosya Redis GEREKTİRMEZ: yalnızca kurucuların hızlı başarısızlık yolunu
// sınar. Gerçek Redis isteyen sözleşme testleri redisguard_integration_test.go
// içindedir.

// yapayIstemci bağlanmayan ama nil de olmayan bir Redis istemcisi üretir.
//
// go-redis bağlantıyı ilk komutta kurar; kurucuların ayar doğrulaması hiç
// komut çalıştırmadığı için bu testler Docker'sız koşar.
func yapayIstemci() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
}

func TestNewLimiterGecersizAyariReddeder(t *testing.T) {
	t.Parallel()

	durumlar := map[string]struct {
		limit  int
		window time.Duration
	}{
		"sıfır limit":            {limit: 0, window: time.Minute},
		"negatif limit":          {limit: -1, window: time.Minute},
		"sıfır pencere":          {limit: 10, window: 0},
		"negatif pencere":        {limit: 10, window: -time.Second},
		"limit ve pencere sıfır": {limit: 0, window: 0},
	}

	for ad, d := range durumlar {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			lim, err := redisguard.NewLimiter(yapayIstemci(), d.limit, d.window)

			require.Error(t, err, "geçersiz ayar hata döndürmeli")
			assert.Nil(t, lim)
			assert.True(t, coreerrors.IsInvalid(err), "hata KindInvalid olmalı")
			assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))
		})
	}
}

func TestKuruculariNilIstemciyiReddeder(t *testing.T) {
	t.Parallel()

	lim, err := redisguard.NewLimiter(nil, 10, time.Minute)
	require.Error(t, err, "nil istemci hata döndürmeli")
	assert.Nil(t, lim)
	assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))

	depo, err := redisguard.NewIdempotencyStore(nil, time.Hour)
	require.Error(t, err, "nil istemci hata döndürmeli")
	assert.Nil(t, depo)
	assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))
}

// TestNewLimiterHataDondurupNilDonmez sınırlayıcının "typed-nil" tuzağına
// düşmediğini sabitler.
//
// corehttp.NewMemoryLimiter geçersiz ayarda nil döner ve o nil doğrudan
// corehttp.RateLimit'e verilirse arayüz değeri nil ÇIKMAZ; middleware'in
// limiter == nil kontrolü kaçar ve ilk istekte panik olur. Bu test, Redis
// sürümünde aynı yolun kapalı kaldığını (hata dönüp çağıranı durdurduğunu)
// garanti eder.
func TestNewLimiterHataDondurupNilDonmez(t *testing.T) {
	t.Parallel()

	lim, err := redisguard.NewLimiter(yapayIstemci(), 0, time.Minute)
	require.Error(t, err)
	require.Nil(t, lim)

	// Çağıran hatayı yok saysaydı ne olurdu: nil *Limiter arayüze konduğunda
	// "nil değil" görünür. Kurucunun hata dönmesi, bu değerin middleware'e
	// hiç ulaşmamasını sağlayan tek koruma.
	//
	// Karşılaştırma testify'ın NotNil'iyle değil DİL düzeyinde yapılır:
	// testify reflect ile bakıp içi nil olan arayüzü de "nil" sayar, oysa
	// middleware'deki "limiter == nil" kontrolü tam olarak aşağıdaki
	// karşılaştırmadır.
	assert.True(t, arayuzeKoy(lim) != nil,
		"nil *Limiter arayüzde nil GÖRÜNMEZ; kurucu bu yüzden hata döner")
}

// arayuzeKoy verilen sınırlayıcıyı arayüz değerine sarar.
//
// Ayrı fonksiyon olmasının nedeni, karşılaştırmanın çağrı yerinde sabit
// katlanmasını engellemektir; testin ölçtüğü şey ÇALIŞMA ZAMANI davranışıdır.
func arayuzeKoy(l *redisguard.Limiter) corehttp.RateLimiter { return l }

// TestNewIdempotencyStoreGecersizTTLdeVarsayilanaDuser bellek içi depoyla
// davranış eşitliğini sabitler.
//
// İki depo birbirinin yerine takılabilir; aynı girdide farklı davranmaları
// (biri varsayılana düşerken diğerinin hata dönmesi) backend değiştiren
// kurulumda sessiz bir sürprize dönüşürdü.
func TestNewIdempotencyStoreGecersizTTLdeVarsayilanaDuser(t *testing.T) {
	t.Parallel()

	for ad, ttl := range map[string]time.Duration{
		"sıfır":   0,
		"negatif": -time.Hour,
	} {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			depo, err := redisguard.NewIdempotencyStore(yapayIstemci(), ttl)

			require.NoError(t, err, "geçersiz ttl varsayılana düşmeli, hata dönmemeli")
			assert.NotNil(t, depo)
		})
	}
}

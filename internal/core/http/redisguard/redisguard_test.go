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

// gecerliOnek biçim denetimini geçen, bugünkü varsayılanla aynı önektir.
const gecerliOnek = "gobit"

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

			lim, err := redisguard.NewLimiter(yapayIstemci(), gecerliOnek, d.limit, d.window)

			require.Error(t, err, "geçersiz ayar hata döndürmeli")
			assert.Nil(t, lim)
			assert.True(t, coreerrors.IsInvalid(err), "hata KindInvalid olmalı")
			assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))
		})
	}
}

func TestKuruculariNilIstemciyiReddeder(t *testing.T) {
	t.Parallel()

	lim, err := redisguard.NewLimiter(nil, gecerliOnek, 10, time.Minute)
	require.Error(t, err, "nil istemci hata döndürmeli")
	assert.Nil(t, lim)
	assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))

	depo, err := redisguard.NewIdempotencyStore(nil, gecerliOnek, time.Hour)
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

	lim, err := redisguard.NewLimiter(yapayIstemci(), gecerliOnek, 0, time.Minute)
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

			depo, err := redisguard.NewIdempotencyStore(yapayIstemci(), gecerliOnek, ttl)

			require.NoError(t, err, "geçersiz ttl varsayılana düşmeli, hata dönmemeli")
			assert.NotNil(t, depo)
		})
	}
}

// TestKurucularGecersizOnegiReddeder ayırıcı içeren ya da boş bir ad alanı
// önekinin SESSİZCE kabul edilmediğini doğrular.
//
// Önek, aynı Redis'i paylaşan iki kurulumu ayıran tek şeydir; sessizce
// düzeltilen (kırpılan ya da varsayılana düşürülen) bir önek iki kurulumu yine
// aynı ad alanına bindirir ve paketin çözmeye çalıştığı arıza — birinin
// yanıtının ötekinin istemcisine gitmesi — geri gelir. Bu yüzden kurucu
// düzeltmez, DURUR.
func TestKurucularGecersizOnegiReddeder(t *testing.T) {
	t.Parallel()

	onekler := map[string]string{
		// Ad alanı yok demektir; oysa çağıran önek parametresi vererek tam da
		// ad alanı istediğini söylemiştir.
		"boş": "",
		// Ayırıcı içeren önek gerçek bir çakışma açar: istemcinin uydurduğu bir
		// idempotency anahtarı iki kurulumu aynı anahtara düşürebilir.
		"ayırıcı içeren":   "gobit:staging",
		"ayırıcıyla biten": "gobit:",
		// Görünmez karakterler kurulumu fark edilmeden başka bir ad alanına
		// taşır; tüm sayaçlar ve işlemdeki kayıtlar bir anda yok sayılır.
		"sondan boşluklu": "gobit ",
		"baştan boşluklu": " gobit",
		"sekme içeren":    "gobit\tprod",
		"yeni satır":      "gobit\n",
		// Glob imleri operatörün "<önek>:idem:*" taramasını bozar.
		"yıldız içeren":          "gobit*",
		"köşeli parantez içeren": "gobit[1]",
		"soru işareti içeren":    "gobit?",
		// Görsel olarak ayırt edilemeyen karakterler iki ayrı ad alanını AYNI
		// gösterir (buradaki 'а' Kiril'dir).
		"latin dışı harf": "gоbit",
	}

	for ad, onek := range onekler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			lim, err := redisguard.NewLimiter(yapayIstemci(), onek, 10, time.Minute)
			require.Error(t, err, "geçersiz önek sınırlayıcıda hata döndürmeli")
			assert.Nil(t, lim)
			assert.True(t, coreerrors.IsInvalid(err), "hata KindInvalid olmalı")
			assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))

			depo, err := redisguard.NewIdempotencyStore(yapayIstemci(), onek, time.Hour)
			require.Error(t, err, "geçersiz önek depoda hata döndürmeli")
			assert.Nil(t, depo)
			assert.True(t, coreerrors.IsInvalid(err), "hata KindInvalid olmalı")
			assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))
		})
	}
}

// TestKurucularGecerliOnegiKabulEder biçim denetiminin meşru kurulum adlarını
// da kapıda tutmadığını doğrular.
//
// Denetim fazla sıkı olsaydı bedeli somut olurdu: operatör kurulumları
// ayıramaz, ayıramadığı için de varsayılanda bırakır — yani reddetmek istediği
// arızayı kendi eliyle geri getirirdi.
func TestKurucularGecerliOnegiKabulEder(t *testing.T) {
	t.Parallel()

	for _, onek := range []string{
		"gobit", "gobit-staging", "gobit_prod", "magaza.42", "GOBIT", "g",
	} {
		t.Run(onek, func(t *testing.T) {
			t.Parallel()

			lim, err := redisguard.NewLimiter(yapayIstemci(), onek, 10, time.Minute)
			require.NoError(t, err, "geçerli önek reddedilmemeli")
			assert.NotNil(t, lim)

			depo, err := redisguard.NewIdempotencyStore(yapayIstemci(), onek, time.Hour)
			require.NoError(t, err, "geçerli önek reddedilmemeli")
			assert.NotNil(t, depo)
		})
	}
}

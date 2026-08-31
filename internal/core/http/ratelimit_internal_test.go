package http

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryLimiterZamanlaYenilenir kotanın gerçekten zamanla dolduğunu
// doğrular.
//
// Paket İÇİ testtir: yenilenme davranışı ancak saat kontrol edilerek
// güvenilir biçimde sınanabilir. Gerçek saatle beklemek testi hem yavaş
// hem de CI'da kırılgan yapardı.
func TestMemoryLimiterZamanlaYenilenir(t *testing.T) {
	t.Parallel()

	simdi := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(2, 2*time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return simdi }

	// Kotayı tüket.
	for range 2 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.False(t, d.Allowed)

	// Yarım saniye: 2/2sn = 1 jeton/sn hızda 0.5 jeton birikir, yetmez.
	simdi = simdi.Add(500 * time.Millisecond)
	d, err = lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "yarım jeton bir isteğe yetmemeli")

	// Bir saniye daha: tam bir jeton birikir.
	simdi = simdi.Add(time.Second)
	d, err = lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "biriken jeton kullanılabilmeli")
}

// TestMemoryLimiterKovasiTasmaz birikimin limitin ÜSTÜNE çıkmadığını doğrular.
//
// Boşluk kasıtlı olarak PENCEREDEN KISA tutulur. Uzun bir boşluk kovayı
// çöp toplamaya sildirir ve kota zaten sıfırlanır; o senaryoda tavan
// kaldırılsa bile test geçerdi — yani hiçbir şey ölçmezdi. Bu testin ilk
// hâli tam olarak bu yüzden yanlış pozitifti.
//
// 3 jeton / 1 sn ⇒ 0,9 sn'de 2,7 jeton birikir. Tavansız: 2 + 2,7 = 4,7
// jetonla 4 istek geçerdi. Tavanlı: 3'te durur.
func TestMemoryLimiterKovasiTasmaz(t *testing.T) {
	t.Parallel()

	simdi := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(3, time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return simdi }

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	// Pencereden kısa bir boşluk: kova çöp toplamaya takılmaz.
	simdi = simdi.Add(900 * time.Millisecond)
	require.Len(t, lim.buckets, 1, "kova hâlâ yaşıyor olmalı")

	gecen := 0

	for range 10 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)

		if !d.Allowed {
			break
		}

		gecen++
	}

	assert.Equal(t, 3, gecen, "birikim limiti aşmamalı")
}

// TestMemoryLimiterUzunSessizlikKotayiTazeler pencereden uzun süre sessiz
// kalan bir anahtarın tam kotayla döndüğünü doğrular.
//
// Bu, [TestMemoryLimiterKovasiTasmaz] ile çelişmez: pencere boyunca sessiz
// kalmış bir kova zaten dolmuş olurdu, dolayısıyla çöp toplamanın onu silmesi
// kotayı DEĞİŞTİRMEZ. İki testin birlikte söylediği şudur: kova ne taşar
// ne de hak edilmiş kotayı yutar.
func TestMemoryLimiterUzunSessizlikKotayiTazeler(t *testing.T) {
	t.Parallel()

	simdi := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(3, time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return simdi }

	for range 3 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.False(t, d.Allowed, "kota bitmiş olmalı")

	simdi = simdi.Add(24 * time.Hour)

	gecen := 0

	for range 10 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)

		if !d.Allowed {
			break
		}

		gecen++
	}

	assert.Equal(t, 3, gecen, "uzun sessizlikten sonra tam kota dönmeli")
}

// TestMemoryLimiterOluKovalariTemizler bellek kullanımının anahtar sayısıyla
// sınırsız büyümediğini doğrular.
//
// Temizlik olmasaydı, her istekte yeni bir kaynak IP gören bir sunucu
// belleğini tüketirdi: sınırlayıcının kendisi DoS vektörü olurdu.
func TestMemoryLimiterOluKovalariTemizler(t *testing.T) {
	t.Parallel()

	simdi := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(5, time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return simdi }

	for i := range 1000 {
		_, err := lim.Allow(context.Background(), strconv.Itoa(i))
		require.NoError(t, err)
	}

	require.Len(t, lim.buckets, 1000, "kovalar önce birikmiş olmalı")

	// gcInterval kadar ilerle ki temizlik tetiklensin; pencere de dolmuş olur.
	simdi = simdi.Add(gcInterval + time.Second)

	_, err := lim.Allow(context.Background(), "yeni")
	require.NoError(t, err)

	assert.Len(t, lim.buckets, 1, "ölü kovalar atılmalı, yalnızca yeni anahtar kalmalı")
}

// TestMemoryLimiterAktifKovayiSilmez sınıra çarpmış bir istemcinin kotasının
// temizlikle SIFIRLANMADIĞINI doğrular.
//
// Aktif kova erken silinseydi, sınırı aşan istemci her temizlik turunda taze
// bir kota bulur ve sınır etkisiz kalırdı.
func TestMemoryLimiterAktifKovayiSilmez(t *testing.T) {
	t.Parallel()

	simdi := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(1, time.Hour)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return simdi }

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	// Temizliği tetikle ama pencere (1sa) henüz dolmasın.
	simdi = simdi.Add(gcInterval + time.Second)

	d, err = lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "penceresi dolmamış kova temizlikte silinmemeli")
}

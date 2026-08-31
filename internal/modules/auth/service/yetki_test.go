package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// Bu dosya YETKİ YÜKSELTMEYİ sınar: bir çağıranın kendisinde olmayan bir
// yetkiyi başkasına (ya da yeni bir anahtara) verememesi.
//
// Denetim route katmanında da vardır — ayrıcalıklı uçlar corehttp.ScopeAdmin
// ister — ama oradan HİÇ geçilemeyen bir yol kalır: modülü gömen ya da
// servisi doğrudan çağıran kod. Servisteki kapı o yolu da kapatır ve uçların
// yetki haritası bir gün gevşetilirse tek savunma o olur.

// darYetki testlerde çağıranın taşıdığı örnek yetkidir.
//
// auth'un kendi sözlüğünden DEĞİL, başka bir modülün yetkisinden seçilmiştir:
// denetimin "tanıdığı yetkiler" listesine değil, çağıranın gerçek yetkilerine
// baktığı böyle görünür.
const darYetki = "orders:read"

// yeniServis sahte depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) (*service.Service, *sahteDepo) {
	t.Helper()

	depo := &sahteDepo{}
	svc := service.New(depo, service.Options{
		Now:       func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) },
		JWTSecret: "test-imza-sirri-yeterince-uzun",
	})
	return svc, depo
}

// yetkiliCtx verilen yetkileri taşıyan doğrulanmış bir kimlikle context üretir.
//
// Üretimde bu kimliği corehttp.RequireAdmin koyar; testte elle konması,
// servisin kararını HTTP yığını olmadan sınamayı sağlar.
func yetkiliCtx(scopes ...string) context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID:     "apikey_test",
		Kind:   "api_key",
		Scopes: scopes,
	})
}

// yukseltmeHatasi hatanın yetki yükseltme reddi olduğunu doğrular.
func yukseltmeHatasi(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err, "yetki yükseltme denemesi hata dönmeli")
	assert.True(t, errors.IsForbidden(err),
		"hata 403'e çevrilmeli; 401 dönseydi istemci jetonunu yenilemeye çalışırdı: %v", err)
	assert.Equal(t, service.CodeScopeEscalation, errors.CodeOf(err))
}

// TestAnahtarUretimiCagiraninYetkisiniAsamaz dar yetkili bir çağıranın
// kendisinden geniş yetkili bir API anahtarı üretemediğini kanıtlar.
//
// Arızanın somut hâli buydu: yalnızca "orders:read" taşıyan bir sk_ anahtarı,
// {"type":"secret","scopes":["admin"]} gövdesiyle tam yetkili bir halef
// üretebiliyordu.
func TestAnahtarUretimiCagiraninYetkisiniAsamaz(t *testing.T) {
	testler := map[string]struct {
		girdi   service.CreateAPIKeyInput
		reddet  bool
		beklem  []string
		aciklam string
	}{
		"admin isteği": {
			girdi:   service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "yükseltme", Scopes: []string{models.ScopeAdmin}},
			reddet:  true,
			aciklam: "çağıranda olmayan admin yetkisi verilemez",
		},
		"yetki alanı hiç doldurulmamış": {
			girdi:   service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "varsayılan"},
			reddet:  true,
			aciklam: "gizli anahtarın varsayılanı admin'dir; 'vermedim' demek vermemiş olmaya yetmez",
		},
		"tanımadığı başka bir yetki": {
			girdi:   service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "yan", Scopes: []string{"products:write"}},
			reddet:  true,
			aciklam: "yükseltme yalnızca admin'e doğru olmak zorunda değildir",
		},
		"kendi yetkisi": {
			girdi:  service.CreateAPIKeyInput{Type: models.APIKeySecret, Title: "eş", Scopes: []string{darYetki}},
			beklem: []string{darYetki},
		},
		"publishable anahtar": {
			girdi:  service.CreateAPIKeyInput{Type: models.APIKeyPublishable, Title: "vitrin"},
			beklem: []string{},
		},
	}

	for ad, tt := range testler {
		t.Run(ad, func(t *testing.T) {
			svc, depo := yeniServis(t)
			ctx := yetkiliCtx(darYetki)

			_, duzMetin, err := svc.CreateAPIKey(ctx, tt.girdi)

			if tt.reddet {
				yukseltmeHatasi(t, err)
				assert.Empty(t, duzMetin, "reddedilen istekte anahtar ÜRETİLMEMELİ")
				assert.Zero(t, depo.yazmaSayisi, "%s", tt.aciklam)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.beklem, depo.sonAnahtar.Scopes,
				"kabul edilen istekte çözülmüş yetkiler olduğu gibi yazılmalı")
		})
	}
}

// TestKullaniciGuncellemesiCagiraninYetkisiniAsamaz dar yetkili bir çağıranın
// bir kullanıcıyı (kendisi dâhil) admin'e yükseltemediğini kanıtlar.
func TestKullaniciGuncellemesiCagiraninYetkisiniAsamaz(t *testing.T) {
	svc, depo := yeniServis(t)
	ctx := yetkiliCtx(darYetki)

	_, err := svc.UpdateUser(ctx, "user_1", service.UpdateUserInput{
		Scopes: []string{models.ScopeAdmin},
	})

	yukseltmeHatasi(t, err)
	assert.Zero(t, depo.yazmaSayisi, "reddedilen güncelleme depoya HİÇ ulaşmamalı")
}

// TestKullaniciGuncellemesiYetkiDaraltmayaIzinVerir denetimin YALNIZCA
// yükseltmeyi engellediğini kanıtlar.
//
// Ayrı bir test olması gerekir: her yetki isteğini reddeden bir denetim
// yukarıdaki testi geçer ama yetki KALDIRMAYI da imkânsızlaştırır ve sızmış
// bir hesabı kapatmanın yolunu kapatırdı.
func TestKullaniciGuncellemesiYetkiDaraltmayaIzinVerir(t *testing.T) {
	testler := map[string][]string{
		"yetkiler tümüyle kaldırılıyor": {},
		"çağıranın kendi yetkisi":       {darYetki},
	}

	for ad, scopes := range testler {
		t.Run(ad, func(t *testing.T) {
			svc, depo := yeniServis(t)
			ctx := yetkiliCtx(darYetki)

			_, err := svc.UpdateUser(ctx, "user_1", service.UpdateUserInput{Scopes: scopes})

			require.NoError(t, err)
			assert.Equal(t, scopes, depo.sonYama.Scopes)
		})
	}
}

// TestKullaniciOlusturmaCagiraninYetkisiniAsamaz aynı kapının yeni kullanıcıda
// da kapalı olduğunu kanıtlar.
//
// Güncelleme kapatılıp oluşturma açık bırakılsaydı yükseltme iki adıma
// bölünürdü: önce tam yetkili bir kullanıcı yarat, sonra onunla giriş yap.
func TestKullaniciOlusturmaCagiraninYetkisiniAsamaz(t *testing.T) {
	svc, depo := yeniServis(t)
	ctx := yetkiliCtx(darYetki)

	// Yetki alanı HİÇ verilmiyor: varsayılan tam yetkidir ve denetim
	// varsayılan uygulandıktan sonra çalışmalıdır.
	_, err := svc.CreateUser(ctx, service.CreateUserInput{Email: "yeni@ornek.com"}, "")

	yukseltmeHatasi(t, err)
	assert.Zero(t, depo.yazmaSayisi, "reddedilen oluşturma depoya HİÇ ulaşmamalı")
}

// TestAdminCagiranHerYetkiyiVerebilir tam yetkili çağıranın kısıtlanmadığını
// kanıtlar.
//
// Denetim corehttp.Principal.HasScope'a dayanır ve admin oradaki üst yetkidir;
// bu test o bağın kopmadığını gösterir. Kopsaydı yönetim yüzeyi kendi kendini
// kilitlerdi.
func TestAdminCagiranHerYetkiyiVerebilir(t *testing.T) {
	svc, depo := yeniServis(t)
	ctx := yetkiliCtx(corehttp.ScopeAdmin)

	_, _, err := svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:   models.APIKeySecret,
		Title:  "yeni yönetim anahtarı",
		Scopes: []string{models.ScopeAdmin},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{models.ScopeAdmin}, depo.sonAnahtar.Scopes)

	_, err = svc.UpdateUser(ctx, "user_1", service.UpdateUserInput{
		Scopes: []string{"products:write"},
	})
	require.NoError(t, err, "admin, kendisinde ADI GEÇMEYEN bir yetkiyi de verebilmeli")
}

// TestKimliksizCagriTohumAdimiIcinSerbesttir kimlik taşımayan çağrının
// denetime takılmadığını kanıtlar.
//
// İlk yönetici HTTP'den yaratılamaz — yönetim uçları zaten korumalıdır — ve
// bir tohum adımıyla doğar. O adım kimseden yetki devralmadığı için kimseyi
// yükseltemez; denetim orada uygulansaydı sistem hiç kurulamazdı.
func TestKimliksizCagriTohumAdimiIcinSerbesttir(t *testing.T) {
	svc, depo := yeniServis(t)
	ctx := context.Background()

	_, _, err := svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:  models.APIKeySecret,
		Title: "tohum anahtarı",
	})
	require.NoError(t, err, "tohum adımı tam yetkili anahtar üretebilmeli")
	assert.Equal(t, []string{models.ScopeAdmin}, depo.sonAnahtar.Scopes)

	_, err = svc.CreateUser(ctx, service.CreateUserInput{Email: "ilk@ornek.com"}, "")
	require.NoError(t, err, "tohum adımı ilk yöneticiyi yaratabilmeli")
	assert.Equal(t, []string{models.ScopeAdmin}, depo.sonKullanici.Scopes)
}

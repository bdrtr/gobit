package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// Bu dosya oturum jetonunun YAŞAM SÜRESİ dışındaki geçerlilik koşullarını
// kanıtlar; asıl konusu parola değişiminin açık oturumları düşürmesidir.
//
// Jeton durum tutmaz ve bir iptal listesi yoktur: sızmış bir yönetici jetonunu
// süresi dolmadan geri almanın TEK yolu parolayı değiştirmektir. Bu yüzden
// aşağıdaki iddiaların hepsi bir güvenlik iddiasıdır, bir davranış tercihi
// değil.

// Testlerin paylaştığı sabitler. Parolalar politikanın alt sınırını
// ([service.MinPasswordLen]) aşacak uzunluktadır.
const (
	oturumSir          = "test-jwt-imza-sirri"
	oturumEposta       = "yonetici@gobit.test"
	oturumParola       = "eski-Parola-1234"
	oturumYeniParola   = "yeni-Parola-1234"
	oturumYanlisParola = "yanlis-Parola-9999"
	oturumKullaniciID  = "user_test"
	oturumKimlikID     = "authid_test"
)

// oturumSaati testin ilerlettiği zaman kaynağıdır.
//
// Gerçek saatle çalışmak bu dosyada mümkün DEĞİLDİR: iddiaların konusu jetonun
// "iat" değeri ile parola değişim anı arasındaki SANİYE farkıdır ve o fark
// gerçek saatte testin hızına bağlı olurdu.
type oturumSaati struct {
	an time.Time
}

// simdi geçerli anı döner.
func (s *oturumSaati) simdi() time.Time { return s.an }

// ilerlet saati verilen süre kadar ileri alır.
func (s *oturumSaati) ilerlet(d time.Duration) { s.an = s.an.Add(d) }

// oturumDeposu servisin depo yüzeyinin bellekte çalışan, TEK kullanıcılı
// uygulamasıdır.
//
// [service.Repository] gömülü tutulur ve yalnızca bu testlerin dokunduğu
// metotlar yazılır: arayüz otuzdan fazla metot taşır ve hepsini elle yazmak
// dosyayı ilgisiz gövdelerle doldururdu. Yazılmamış bir metoda dokunulursa
// çağrı nil arayüz üzerinden PANİKLER; sessizce sıfır değer dönmez, yani
// "bu akış oraya hiç uğramamalı" iddiası test yeşilken çürüyemez.
//
// Yazma metotları ilgili SQL sorgusunun SÖZLEŞMESİNİ taklit eder; hangi
// sorgunun updated_at'i taşıdığı bu testlerin tam konusudur
// (bkz. queries/identities.sql).
type oturumDeposu struct {
	service.Repository

	kullanici models.User
	kimlik    models.AuthIdentity
	// kimlikSilindi true ise GetIdentity errors.NotFound döner; kimliği
	// silinmiş bir kullanıcının jetonu senaryosu böyle kurulur.
	kimlikSilindi bool
}

// GetUser kullanıcıyı döner; kimlik tutmuyorsa errors.NotFound.
func (d *oturumDeposu) GetUser(_ context.Context, id string) (models.User, error) {
	if id != d.kullanici.ID {
		return models.User{}, errors.NotFound("test_kullanici_yok", "kullanıcı yok: %s", id)
	}
	return d.kullanici, nil
}

// GetUserByEmail kullanıcıyı e-postasına göre döner; yoksa errors.NotFound.
func (d *oturumDeposu) GetUserByEmail(_ context.Context, email string) (models.User, error) {
	if email != d.kullanici.Email {
		return models.User{}, errors.NotFound("test_kullanici_yok", "kullanıcı yok: %s", email)
	}
	return d.kullanici, nil
}

// GetIdentity giriş kimliğini döner; yoksa errors.NotFound.
func (d *oturumDeposu) GetIdentity(_ context.Context, userID, provider string) (models.AuthIdentity, error) {
	if d.kimlikSilindi || userID != d.kimlik.UserID || provider != d.kimlik.Provider {
		return models.AuthIdentity{}, errors.NotFound("test_kimlik_yok",
			"%s kullanıcısının %q kimliği yok", userID, provider)
	}
	return d.kimlik, nil
}

// SetPasswordHash hash'i yazar, kilit sayaçlarını sıfırlar ve updated_at'i
// ilerletir.
//
// updated_at'in ilerlemesi UpdatePasswordHash sorgusunun sözleşmesidir ve
// oturum iptalinin çapasıdır.
func (d *oturumDeposu) SetPasswordHash(
	_ context.Context,
	_, _, _, hash string,
	now time.Time,
) (models.AuthIdentity, error) {
	d.kimlik.PasswordHash = hash
	d.kimlik.FailedAttempts = 0
	d.kimlik.LockedUntil = nil
	d.kimlik.UpdatedAt = now
	return d.kimlik, nil
}

// RegisterLoginSuccess sayaçları temizler ve son giriş anını yazar.
//
// updated_at'e DOKUNMAZ; sorgunun sözleşmesi budur.
func (d *oturumDeposu) RegisterLoginSuccess(_ context.Context, _ string, now time.Time) error {
	d.kimlik.FailedAttempts = 0
	d.kimlik.LockedUntil = nil
	d.kimlik.LastLoginAt = &now
	return nil
}

// RegisterLoginFailure başarısız denemeyi sayar ve eşikte kilitler.
//
// updated_at'e DOKUNMAZ; sorgunun sözleşmesi budur.
func (d *oturumDeposu) RegisterLoginFailure(
	_ context.Context,
	_ string,
	threshold int,
	lockUntil, _ time.Time,
) (models.AuthIdentity, error) {
	d.kimlik.FailedAttempts++
	if d.kimlik.FailedAttempts >= threshold {
		kilit := lockUntil
		d.kimlik.LockedUntil = &kilit
	}
	return d.kimlik, nil
}

// oturumKur sabit saatli bir servis, kimlik doğrulayıcısını ve sahte depoyu
// üretir.
//
// bcrypt maliyeti en düşük değerdedir: bu dosyadaki hiçbir iddia maliyetle
// ilgili değildir ve varsayılan maliyet her testi çeyrek saniye yavaşlatırdı.
func oturumKur(t *testing.T) (*service.Service, *service.Interop, *oturumDeposu, *oturumSaati) {
	t.Helper()

	baslangic := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	saat := &oturumSaati{an: baslangic}

	hash, err := bcrypt.GenerateFromPassword([]byte(oturumParola), bcrypt.MinCost)
	require.NoError(t, err, "test parolası hash'lenemedi")

	depo := &oturumDeposu{
		kullanici: models.User{
			ID:        oturumKullaniciID,
			Email:     oturumEposta,
			Scopes:    []string{models.ScopeAdmin},
			CreatedAt: baslangic,
			UpdatedAt: baslangic,
		},
		kimlik: models.AuthIdentity{
			ID:               oturumKimlikID,
			UserID:           oturumKullaniciID,
			Provider:         models.ProviderEmailPass,
			ProviderIdentity: oturumEposta,
			PasswordHash:     string(hash),
			CreatedAt:        baslangic,
			UpdatedAt:        baslangic,
		},
	}

	svc := service.New(depo, service.Options{
		Now:        saat.simdi,
		JWTSecret:  oturumSir,
		BcryptCost: bcrypt.MinCost,
	})
	return svc, service.NewInterop(svc), depo, saat
}

// oturumJetonuAl giriş yapar ve oturum jetonunu döner.
func oturumJetonuAl(t *testing.T, svc *service.Service, parola string) string {
	t.Helper()

	jeton, _, err := svc.Login(context.Background(), oturumEposta, parola)
	require.NoError(t, err, "giriş başarılı olmalı")
	require.NotEmpty(t, jeton, "giriş jeton dönmeli")
	return jeton
}

// oturumKimligiCoz jetonu yönetim yüzeyinden doğrular.
func oturumKimligiCoz(interop *service.Interop, jeton string) (corehttp.Principal, error) {
	return interop.AuthenticateAdmin(context.Background(), "Bearer", jeton)
}

// oturumRediniDogrula jetonun errors.Unauthorized ile düştüğünü doğrular.
func oturumRediniDogrula(t *testing.T, err error, neden string) {
	t.Helper()

	require.Error(t, err, neden)
	assert.True(t, errors.IsUnauthorized(err),
		"%s — beklenen tür Unauthorized, gelen: %v", neden, errors.KindOf(err))
	assert.Equal(t, service.CodeTokenInvalid, errors.CodeOf(err),
		"%s — jeton reddi tek bir kodla bildirilmeli", neden)
}

// TestParolaDegisimiOncekiJetonuReddeder parola değişiminin açık oturumları
// düşürdüğünü kanıtlar.
//
// Denetim olmasaydı sızmış bir yönetici jetonu, parola değiştirilse bile
// [service.DefaultJWTTTL] boyunca (varsayılan 12 saat) tam yetkili kimlik
// üretmeye devam ederdi.
func TestParolaDegisimiOncekiJetonuReddeder(t *testing.T) {
	svc, interop, depo, saat := oturumKur(t)
	ctx := context.Background()

	eskiJeton := oturumJetonuAl(t, svc, oturumParola)
	_, err := oturumKimligiCoz(interop, eskiJeton)
	require.NoError(t, err, "jeton parola değişmeden önce kabul edilmeli")

	saat.ilerlet(2 * time.Second)
	require.NoError(t, svc.SetPassword(ctx, depo.kullanici.ID, oturumYeniParola),
		"parola değişimi başarılı olmalı")

	_, err = oturumKimligiCoz(interop, eskiJeton)
	oturumRediniDogrula(t, err, "parola değiştikten sonra eski jeton kabul edilmemeli")

	saat.ilerlet(time.Second)
	yeniJeton := oturumJetonuAl(t, svc, oturumYeniParola)
	kimlik, err := oturumKimligiCoz(interop, yeniJeton)
	require.NoError(t, err, "değişimden SONRA alınan jeton çalışmalı")
	assert.Equal(t, oturumKullaniciID, kimlik.ID)
	assert.Equal(t, []string{models.ScopeAdmin}, kimlik.Scopes)
}

// TestParolaDegisimiyleAyniSaniyedeUretilenJetonGecerliKalir sınır durumundaki
// tercihi sabitler.
//
// "iat" saniye çözünürlüklüdür; değişimle aynı saniyede üretilen bir jetonun
// önce mi sonra mı doğduğu jetondan okunamaz. Belirsizlik KULLANILABİLİRLİK
// lehine çözülür (gerekçe: service/token.go, issuedBefore). Ters tercih,
// parolasını değiştirip hemen giriş yapan kullanıcının taze jetonunu
// düşürürdü — kurulum betiklerinin tam olarak yaptığı şey budur.
func TestParolaDegisimiyleAyniSaniyedeUretilenJetonGecerliKalir(t *testing.T) {
	svc, interop, depo, saat := oturumKur(t)
	ctx := context.Background()

	saat.ilerlet(500 * time.Millisecond)
	require.NoError(t, svc.SetPassword(ctx, depo.kullanici.ID, oturumYeniParola))

	// Aynı saniyenin içinde kalınır: 10:00:00.500 → 10:00:00.900.
	saat.ilerlet(400 * time.Millisecond)
	jeton := oturumJetonuAl(t, svc, oturumYeniParola)

	_, err := oturumKimligiCoz(interop, jeton)
	require.NoError(t, err,
		"değişimle aynı saniyede üretilen jeton reddedilmemeli")
}

// TestBasarisizGirisDenemesiOturumuDusurmez saldırganın kurbanı dışarı
// atamayacağını kanıtlar.
//
// Oturum iptalinin çapası kimliğin updated_at değeridir. Başarısız deneme
// sayacı o sütunu ilerletseydi, kurbanın e-postasını bilen herkes tek bir
// yanlış parola denemesiyle bütün oturumlarını kapatabilirdi: hedefli bir
// hizmet dışı bırakma aracı.
func TestBasarisizGirisDenemesiOturumuDusurmez(t *testing.T) {
	svc, interop, _, saat := oturumKur(t)
	ctx := context.Background()

	jeton := oturumJetonuAl(t, svc, oturumParola)

	saat.ilerlet(5 * time.Second)
	_, _, err := svc.Login(ctx, oturumEposta, oturumYanlisParola)
	require.Error(t, err, "yanlış parola reddedilmeli")

	_, err = oturumKimligiCoz(interop, jeton)
	require.NoError(t, err, "başarısız bir deneme kurbanın oturumunu kapatmamalı")
}

// TestIkinciGirisIlkOturumuDusurmez aynı kullanıcının iki cihazda açık
// kalabildiğini kanıtlar.
//
// Başarılı giriş de updated_at'i ilerletseydi, ikinci cihazdan giriş yapmak
// birincinin oturumunu sessizce kapatırdı.
func TestIkinciGirisIlkOturumuDusurmez(t *testing.T) {
	svc, interop, _, saat := oturumKur(t)

	ilkJeton := oturumJetonuAl(t, svc, oturumParola)

	saat.ilerlet(5 * time.Second)
	ikinciJeton := oturumJetonuAl(t, svc, oturumParola)
	require.NotEqual(t, ilkJeton, ikinciJeton, "iki giriş farklı jeton üretmeli")

	_, err := oturumKimligiCoz(interop, ilkJeton)
	require.NoError(t, err, "ilk cihazın oturumu ikinci girişten sonra da geçerli olmalı")

	_, err = oturumKimligiCoz(interop, ikinciJeton)
	require.NoError(t, err, "ikinci cihazın oturumu geçerli olmalı")
}

// TestUretimAniOlmayanJetonReddedilir "iat" iddiasının zorunlu olduğunu
// kanıtlar.
//
// Oturum iptali bu iddiaya dayanır; iddiası olmayan bir jetonun ne zaman
// üretildiği bilinemez ve karşılaştırma yapılamaz. İmza sırrı burada bilerek
// DOĞRU olanıdır: reddin gerekçesi imza değil, eksik iddiadır.
func TestUretimAniOlmayanJetonReddedilir(t *testing.T) {
	_, interop, _, saat := oturumKur(t)

	iddialar := jwt.MapClaims{
		"sub":    oturumKullaniciID,
		"iss":    service.DefaultIssuer,
		"exp":    saat.simdi().Add(time.Hour).Unix(),
		"scopes": []string{models.ScopeAdmin},
	}
	jeton, err := jwt.NewWithClaims(jwt.SigningMethodHS256, iddialar).SignedString([]byte(oturumSir))
	require.NoError(t, err, "test jetonu imzalanamadı")

	_, err = oturumKimligiCoz(interop, jeton)
	oturumRediniDogrula(t, err, "\"iat\" iddiası olmayan jeton kabul edilmemeli")
}

// TestGirisKimligiSilinmisJetonReddedilir kimliği silinmiş kullanıcının
// jetonunun düştüğünü kanıtlar.
//
// Kimlik satırı yoksa jetonun ne zaman geçersizleştiğini söyleyecek bir değer
// de yoktur; kabul etmek, denetimi kimliği silerek atlatmaya kapı bırakmak
// olurdu.
func TestGirisKimligiSilinmisJetonReddedilir(t *testing.T) {
	svc, interop, depo, _ := oturumKur(t)

	jeton := oturumJetonuAl(t, svc, oturumParola)
	depo.kimlikSilindi = true

	_, err := oturumKimligiCoz(interop, jeton)
	oturumRediniDogrula(t, err, "giriş kimliği silinmiş kullanıcının jetonu kabul edilmemeli")
}

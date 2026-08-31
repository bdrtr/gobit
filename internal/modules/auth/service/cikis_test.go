package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// Bu dosya çıkış ucunun SÖZLEŞMESİNİ kanıtlar.
//
// Fikstürler token_test.go'dadır ([oturumKur], [oturumJetonuAl],
// [oturumKimligiCoz], [oturumRediniDogrula]) ve bilerek paylaşılır: çıkış ile
// parola değişimi AYNI çapaya yazar, dolayısıyla ikisinin testleri de aynı
// zemin üzerinde durmalıdır.

// TestCikisAyniJetonuDusurur çıkışın kendisini çağıran jetonu düşürdüğünü
// kanıtlar.
//
// Bulgu tam olarak buydu: açık bir çıkış yolu yoktu ve tüm oturumları
// düşürmenin tek yolu parola değiştirmekti. Bu test o yolun açıldığını ve
// gerçekten ETKİLİ olduğunu sabitler — 204 dönüp hiçbir şey yazmayan bir uç,
// yalnızca status koduna bakan bir testi geçerdi.
func TestCikisAyniJetonuDusurur(t *testing.T) {
	svc, interop, _, saat := oturumKur(t)
	ctx := context.Background()

	jeton := oturumJetonuAl(t, svc, oturumParola)
	_, err := oturumKimligiCoz(interop, jeton)
	require.NoError(t, err, "jeton çıkıştan önce kabul edilmeli")

	saat.ilerlet(2 * time.Second)
	iptalAni, err := svc.Logout(ctx, oturumKullaniciID, service.PrincipalKindUser)
	require.NoError(t, err, "kullanıcı çıkış yapabilmeli")
	assert.Equal(t, saat.simdi(), iptalAni,
		"dönen an, kimliğe yazılan çapa olmalı; istemci jetonunu buna göre eler")

	_, err = oturumKimligiCoz(interop, jeton)
	oturumRediniDogrula(t, err, "çıkıştan sonra aynı jeton kabul edilmemeli")
}

// TestCikistanSonraAlinanJetonCalisir çıkışın kapıyı KİLİTLEMEDİĞİNİ kanıtlar.
//
// Çapa ileri alınırken karşılaştırma ters kurulsaydı (ya da çapa geleceğe
// yazılsaydı) kullanıcı çıkış yaptıktan sonra bir daha içeri giremezdi: giriş
// 200 döner, jetonla atılan ilk istek 401 alırdı.
func TestCikistanSonraAlinanJetonCalisir(t *testing.T) {
	svc, interop, _, saat := oturumKur(t)
	ctx := context.Background()

	_ = oturumJetonuAl(t, svc, oturumParola)

	saat.ilerlet(2 * time.Second)
	_, err := svc.Logout(ctx, oturumKullaniciID, service.PrincipalKindUser)
	require.NoError(t, err)

	// Saniye sınırındaki belirsizlik KULLANILABİLİRLİK lehine çözüldüğü için
	// (bkz. service/token.go, issuedBefore) aynı saniyede alınan jeton da
	// çalışırdı; test yine de bir sonraki saniyeye geçer, çünkü kanıtlamak
	// istediği şey sınır durumu değil normal akıştır.
	saat.ilerlet(time.Second)
	yeniJeton := oturumJetonuAl(t, svc, oturumParola)

	kimlik, err := oturumKimligiCoz(interop, yeniJeton)
	require.NoError(t, err, "çıkıştan SONRA alınan jeton çalışmalı")
	assert.Equal(t, oturumKullaniciID, kimlik.ID)
	assert.Equal(t, []string{models.ScopeAdmin}, kimlik.Scopes)
}

// TestCikisTumCihazlariDusurur çıkışın TOPTAN olduğunu kanıtlar.
//
// Sözleşmenin en kolay yanlış anlaşılan yanı budur: "çıkış yaptım" sanan
// kullanıcının diğer cihazları da düşer. Tek cihazı düşürmek jti bazlı bir
// kara liste isterdi ve o karar bilinçli olarak ERTELENMİŞTİR; bu test
// bugünkü davranışı, sessizce değişemeyeceği biçimde sabitler.
func TestCikisTumCihazlariDusurur(t *testing.T) {
	svc, interop, _, saat := oturumKur(t)
	ctx := context.Background()

	telefon := oturumJetonuAl(t, svc, oturumParola)
	saat.ilerlet(time.Second)
	dizustu := oturumJetonuAl(t, svc, oturumParola)
	require.NotEqual(t, telefon, dizustu, "iki giriş farklı jeton üretmeli")

	saat.ilerlet(2 * time.Second)
	_, err := svc.Logout(ctx, oturumKullaniciID, service.PrincipalKindUser)
	require.NoError(t, err)

	_, err = oturumKimligiCoz(interop, telefon)
	oturumRediniDogrula(t, err, "çıkış yapılan cihazın jetonu düşmeli")

	_, err = oturumKimligiCoz(interop, dizustu)
	oturumRediniDogrula(t, err, "ÖTEKİ cihazın jetonu da düşmeli")
}

// TestCikisParolayiDegistirmez çıkışın kimlik bilgisine dokunmadığını
// kanıtlar.
//
// Çıkış ile parola değişimi aynı çapayı ilerletir; bunun yan etkisi olarak
// çıkış ucunun parolayı da yazması, kullanıcının bir daha giriş yapamaması
// demek olurdu.
func TestCikisParolayiDegistirmez(t *testing.T) {
	svc, _, _, saat := oturumKur(t)
	ctx := context.Background()

	saat.ilerlet(time.Second)
	_, err := svc.Logout(ctx, oturumKullaniciID, service.PrincipalKindUser)
	require.NoError(t, err)

	saat.ilerlet(time.Second)
	_, _, err = svc.Login(ctx, oturumEposta, oturumParola)
	require.NoError(t, err, "çıkış eski parolayı geçersizleştirmemeli")
}

// TestApiAnahtariCikisYapamaz anahtarın çıkış ucundan reddedildiğini
// kanıtlar.
//
// Anahtarın oturumu yoktur: jetonla değil kalıcı bir sırla gelir ve o sır bu
// çağrıdan sonra da çalışmaya devam ederdi. Sessizce başarılı dönmek,
// çağıranda anahtarın kapatıldığı yanılgısını bırakırdı — asıl yol
// POST /admin/v1/api-keys/{id}/revoke ucudur.
func TestApiAnahtariCikisYapamaz(t *testing.T) {
	svc, _, depo, _ := oturumKur(t)
	ctx := context.Background()

	oncekiCapa := depo.kimlik.UpdatedAt

	_, err := svc.Logout(ctx, "apk_01JABC", service.PrincipalKindAPIKey)

	require.Error(t, err, "api anahtarı çıkış yapamamalı")
	assert.True(t, errors.IsInvalid(err),
		"beklenen tür Invalid (422), gelen: %v", errors.KindOf(err))
	assert.Equal(t, service.CodeNoSession, errors.CodeOf(err),
		"ret, ayrı bir kodla bildirilmeli; istemci bunu bir kimlik hatasından ayırabilmeli")
	assert.Equal(t, oncekiCapa, depo.kimlik.UpdatedAt,
		"reddedilen çıkış hiçbir kimliğin çapasını ilerletmemeli")
}

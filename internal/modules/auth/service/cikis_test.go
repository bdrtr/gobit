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

	oncekiCapa := depo.capa(models.ProviderEmailPass)

	_, err := svc.Logout(ctx, "apk_01JABC", service.PrincipalKindAPIKey)

	require.Error(t, err, "api anahtarı çıkış yapamamalı")
	assert.True(t, errors.IsInvalid(err),
		"beklenen tür Invalid (422), gelen: %v", errors.KindOf(err))
	assert.Equal(t, service.CodeNoSession, errors.CodeOf(err),
		"ret, ayrı bir kodla bildirilmeli; istemci bunu bir kimlik hatasından ayırabilmeli")
	assert.Equal(t, oncekiCapa, depo.capa(models.ProviderEmailPass),
		"reddedilen çıkış hiçbir kimliğin çapasını ilerletmemeli")
}

// oturumIkinciSaglayici testte ELLE kurulan ikinci kimlik sağlayıcısıdır.
//
// Ham dize bilinçlidir: models paketinde yalnızca [models.ProviderEmailPass]
// sabiti vardır ve uygulanmamış bir sağlayıcı için oraya sabit eklemek, kodun
// desteklemediği bir giriş yolu varmış gibi gösterirdi. Değerin kendisi
// iddiaya girmez; anlamı yalnızca "emailpass OLMAYAN bir satır"dır.
const oturumIkinciSaglayici = "google"

// ikinciSaglayiciEkle depoya ikinci bir sağlayıcının kimlik satırını kurar.
//
// Satır elle kurulur çünkü servisin ikinci bir sağlayıcı AÇAN ucu yoktur:
// bugün tek giriş yolu emailpass'tır. Şema ise böyle bir satırı BUGÜN DE
// kabul eder — benzersizlik (user_id, provider) üzerindedir, yani sağlayıcı
// başına bir satıra izin verir. Test bu yüzden hayali bir durumu değil,
// şemanın halihazırda ifade ettiği bir durumu kurar.
func ikinciSaglayiciEkle(depo *oturumDeposu, olusturuldu time.Time) {
	depo.kimlikler = append(depo.kimlikler, models.AuthIdentity{
		ID:               oturumKimlikID + "_ikinci",
		UserID:           oturumKullaniciID,
		Provider:         oturumIkinciSaglayici,
		ProviderIdentity: "google-oauth-sub-123",
		// Parola YOKTUR: OAuth kimliğinin parolası olmaz ve boş hash ile giriş
		// zaten reddedilir (bkz. password.go, Login).
		CreatedAt: olusturuldu,
		UpdatedAt: olusturuldu,
	})
}

// TestCikisTumSaglayicilarinCapasiniIlerletir çıkışın TEK BİR sağlayıcıyı
// değil, kullanıcının bütün kimliklerini ilerlettiğini kanıtlar.
//
// # Test neyi kanıtlar, neyi kanıtlamaz
//
// Bugün canlı sağlayıcı tek olduğu için gözlemlenebilir davranış
// DEĞİŞMEMİŞTİR; kanıtlanan iki şey vardır:
//
//   - emailpass kimliğinin çapası eskisi gibi ilerler (mevcut davranış
//     korunuyor),
//   - ELLE kurulmuş ikinci bir sağlayıcı satırının çapası da ilerler.
//
// İkincisi bir kullanıcı hikâyesi değil, gelecek için konmuş bir kilittir:
// OAuth eklendiği gün çıkış tek sağlayıcıyı seçseydi o sağlayıcıdan alınmış
// jetonlar düşmez ve bu SESSİZ kalırdı — uç yine 204 döner, "çıkış yaptım"
// diyen kullanıcı hâlâ oturumda olurdu.
func TestCikisTumSaglayicilarinCapasiniIlerletir(t *testing.T) {
	svc, interop, depo, saat := oturumKur(t)
	ctx := context.Background()
	ikinciSaglayiciEkle(depo, saat.simdi())

	jeton := oturumJetonuAl(t, svc, oturumParola)
	_, err := oturumKimligiCoz(interop, jeton)
	require.NoError(t, err, "jeton çıkıştan önce kabul edilmeli")

	saat.ilerlet(2 * time.Second)
	iptalAni, err := svc.Logout(ctx, oturumKullaniciID, service.PrincipalKindUser)
	require.NoError(t, err, "kullanıcı çıkış yapabilmeli")

	assert.Equal(t, saat.simdi(), depo.capa(models.ProviderEmailPass),
		"emailpass kimliğinin çapası ilerlemeli; bugünkü davranış korunmalı")
	assert.Equal(t, saat.simdi(), depo.capa(oturumIkinciSaglayici),
		"ikinci sağlayıcının çapası da ilerlemeli — ilerlemeseydi o sağlayıcıdan "+
			"alınmış jetonlar çıkıştan sonra da kabul edilirdi")
	assert.Equal(t, saat.simdi(), iptalAni,
		"dönen an, kimliklere yazılan çapa olmalı")

	_, err = oturumKimligiCoz(interop, jeton)
	oturumRediniDogrula(t, err, "çıkıştan sonra jeton kabul edilmemeli")
}

// TestIkinciSaglayidakiCapaJetonuDusurur doğrulama tarafının da tek bir
// sağlayıcıya BAKMADIĞINI kanıtlar.
//
// Zincirin bu ucu olmadan çıkış tarafındaki değişiklik işe yaramazdı: çıkış
// bütün satırları ilerletse bile doğrulama yalnızca emailpass satırına
// bakıyorsa, öteki satıra yazılan çapa hiç okunmaz.
//
// Test bunu, çıkışın ilerletemeyeceği bir asimetri kurarak sınar: ikinci
// sağlayıcının çapası ilerletilir, emailpass satırı YERİNDE bırakılır. Sabit
// bir sağlayıcıya bakan bir doğrulama bu jetonu kabul etmeye devam ederdi.
//
// Kabul edilen bedel açıktır ve bilinçlidir: bir sağlayıcıdaki iptal ötekinin
// jetonlarını da düşürür (gerekçe interop.go, principalFromToken).
func TestIkinciSaglayidakiCapaJetonuDusurur(t *testing.T) {
	svc, interop, depo, saat := oturumKur(t)
	ikinciSaglayiciEkle(depo, saat.simdi())

	jeton := oturumJetonuAl(t, svc, oturumParola)
	_, err := oturumKimligiCoz(interop, jeton)
	require.NoError(t, err, "hiçbir çapa ilerlemeden jeton kabul edilmeli")

	saat.ilerlet(2 * time.Second)
	ikinci := depo.kimlik(oturumIkinciSaglayici)
	require.NotNil(t, ikinci, "test zemini: ikinci sağlayıcı satırı kurulmuş olmalı")
	ikinci.UpdatedAt = saat.simdi()

	_, err = oturumKimligiCoz(interop, jeton)
	oturumRediniDogrula(t, err,
		"ikinci sağlayıcıda ilerleyen çapa da jetonu düşürmeli")
}

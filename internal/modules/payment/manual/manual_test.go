package manual_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// Testlerde kullanılan sabitler.
const (
	referans = "paycol_TEST"
	paraKodu = "TRY"
	tutar    = int64(12_500)
)

// yeniSaglayici bellek içi defter üzerinde çalışan bir sağlayıcı kurar.
func yeniSaglayici(t *testing.T) (*manual.Provider, *memStore) {
	t.Helper()

	store := newMemStore()
	return manual.New(store, nil), store
}

// oturumAc test için bir oturum açar ve kimliğini döner.
func oturumAc(t *testing.T, p *manual.Provider, key string, data map[string]any) string {
	t.Helper()

	ses, err := p.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount:         tutar,
		CurrencyCode:   paraKodu,
		Reference:      referans,
		IdempotencyKey: key,
		Data:           data,
	})
	require.NoError(t, err)
	return ses.ID
}

// TestAyniAnahtarlaIkinciCreateSessionYeniOturumAcmaz çekirdek sözleşmesinin
// idempotency şartını doğrular.
//
// Yalnızca dönen kimliğin aynı olması yetmez: deftere GERÇEKTEN ikinci bir
// satır yazılmadığı da kanıtlanır. Kimliği aynı döndürüp arka planda ikinci bir
// oturum açan bir uygulama, müşteriden iki kez tahsilat denemesine yol açardı.
func TestAyniAnahtarlaIkinciCreateSessionYeniOturumAcmaz(t *testing.T) {
	p, store := yeniSaglayici(t)
	ctx := context.Background()

	in := coreprovider.CreateSessionInput{
		Amount:         tutar,
		CurrencyCode:   paraKodu,
		Reference:      referans,
		IdempotencyKey: "key-1",
	}

	ilk, err := p.CreateSession(ctx, in)
	require.NoError(t, err)
	ikinci, err := p.CreateSession(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, ilk.ID, ikinci.ID, "aynı anahtar aynı oturumu dönmeli")
	inserts, _ := store.sayimlar()
	assert.Equal(t, 1, inserts, "deftere yalnızca BİR satır yazılmalı")
}

// TestAyniAnahtarFarkliTutarlaCakisir idempotency anahtarının yeniden
// kullanımını reddettiğimizi doğrular.
//
// Sessizce mevcut oturumu dönmek, çağıranın gönderdiğini sandığı tutarın hiç
// uygulanmaması demek olurdu; sonuç, beklenenden farklı bir tutarın tahsil
// edilmesidir.
func TestAyniAnahtarFarkliTutarlaCakisir(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()

	_, err := p.CreateSession(ctx, coreprovider.CreateSessionInput{
		Amount: tutar, CurrencyCode: paraKodu, Reference: referans, IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = p.CreateSession(ctx, coreprovider.CreateSessionInput{
		Amount: tutar + 1, CurrencyCode: paraKodu, Reference: referans, IdempotencyKey: "key-1",
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, manual.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateSessionGirdiDogrulamasi para doğrulamasının her dalını sınar.
func TestCreateSessionGirdiDogrulamasi(t *testing.T) {
	p, _ := yeniSaglayici(t)

	tests := []struct {
		ad string
		in coreprovider.CreateSessionInput
	}{
		{"anahtarsiz", coreprovider.CreateSessionInput{Amount: tutar, CurrencyCode: paraKodu, Reference: referans}},
		{"referanssiz", coreprovider.CreateSessionInput{Amount: tutar, CurrencyCode: paraKodu, IdempotencyKey: "k"}},
		{"sifir tutar", coreprovider.CreateSessionInput{
			Amount: 0, CurrencyCode: paraKodu, Reference: referans, IdempotencyKey: "k",
		}},
		{"negatif tutar", coreprovider.CreateSessionInput{
			Amount: -1, CurrencyCode: paraKodu, Reference: referans, IdempotencyKey: "k",
		}},
		{"tavani asan tutar", coreprovider.CreateSessionInput{
			Amount: models.MaxAmount + 1, CurrencyCode: paraKodu, Reference: referans, IdempotencyKey: "k",
		}},
		{"gecersiz para birimi", coreprovider.CreateSessionInput{
			Amount: tutar, CurrencyCode: "TRYY", Reference: referans, IdempotencyKey: "k",
		}},
		{"taninmayan outcome", coreprovider.CreateSessionInput{
			Amount: tutar, CurrencyCode: paraKodu, Reference: referans, IdempotencyKey: "k",
			Data: map[string]any{manual.DataKeyOutcome: "bilinmeyen"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.ad, func(t *testing.T) {
			_, err := p.CreateSession(context.Background(), tt.in)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
		})
	}
}

// TestAuthorizeTutariBlokeEder mutlu yolu doğrular.
func TestAuthorizeTutariBlokeEder(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)

	result, err := p.Authorize(ctx, id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, tutar, result.AuthorizedAmount)
	assert.Empty(t, result.DeclineReason)
}

// TestIkinciAuthorizeHataVermezVeDefteriDegistirmez çekirdek sözleşmesinin
// "tekrar çağrılabilir" şartını doğrular.
//
// Yazma sayacı, ikinci çağrının deftere hiç dokunmadığını kanıtlar; yalnızca
// dönen değere bakan bir test, tutarı ikinci kez bloke eden bir uygulamayı
// yakalayamazdı.
func TestIkinciAuthorizeHataVermezVeDefteriDegistirmez(t *testing.T) {
	p, store := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)

	require.NoError(t, mustAuthorize(t, p, id))
	_, oncekiUpdates := store.sayimlar()

	result, err := p.Authorize(ctx, id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	_, sonrakiUpdates := store.sayimlar()
	assert.Equal(t, oncekiUpdates, sonrakiUpdates, "ikinci Authorize deftere yazmamalı")
}

// TestAuthorizeRedEnjeksiyonu saga testlerinin ödeme adımını PATLATABİLMESİ
// için gereken davranışı doğrular.
//
// Ret bir HATA DEĞİLDİR: sağlayıcı başarıyla yanıt vermiştir ve sonucu
// "failed"tır. Hata dönmek, saga'nın telafi zincirini yanlış sebeple
// tetiklemesi ve ret sebebinin kaybolması demek olurdu.
func TestAuthorizeRedEnjeksiyonu(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", map[string]any{
		manual.DataKeyOutcome:       manual.OutcomeDecline,
		manual.DataKeyDeclineReason: "yetersiz bakiye",
	})

	result, err := p.Authorize(ctx, id)

	require.NoError(t, err, "ret sağlayıcı açısından başarılı bir yanıttır")
	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	assert.Equal(t, "yetersiz bakiye", result.DeclineReason)
	assert.Zero(t, result.AuthorizedAmount)
}

// TestAuthorizeRedSebepsizVarsayilaniKullanir sebep verilmediğinde de bir
// gerekçe yazıldığını doğrular; boş bir sebep teşhiste hiçbir işe yaramaz.
func TestAuthorizeRedSebepsizVarsayilaniKullanir(t *testing.T) {
	p, _ := yeniSaglayici(t)
	id := oturumAc(t, p, "key-1", map[string]any{manual.DataKeyOutcome: manual.OutcomeDecline})

	result, err := p.Authorize(context.Background(), id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	assert.NotEmpty(t, result.DeclineReason)
}

// TestAuthorizeHataEnjeksiyonuDurumuDegistirmez sağlayıcının erişilemez
// olduğu senaryoyu doğrular.
//
// Ret ile hata arasındaki fark burada görünür: hata YENİDEN DENENEBİLİR olmak
// zorundadır, bu yüzden oturum "pending" kalmalıdır. Durumu "failed" yazan bir
// uygulama, geçici bir ağ hatasını kalıcı bir redde çevirirdi.
func TestAuthorizeHataEnjeksiyonuDurumuDegistirmez(t *testing.T) {
	p, store := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", map[string]any{manual.DataKeyOutcome: manual.OutcomeError})

	_, err := p.Authorize(ctx, id)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "hata: %v", err)

	ses, getErr := p.GetSession(ctx, id)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionPending, ses.Status, "durum değişmemeli; istek yeniden denenebilir")
	_, updates := store.sayimlar()
	assert.Zero(t, updates, "hata dalında deftere yazılmamalı")
}

// TestAuthorizeKismiTutar kısmi yetkilendirmenin sınanabilir olduğunu
// doğrular; çekirdek sözleşmesi AuthorizedAmount'ın istenenden küçük
// olabileceğini açıkça söyler.
func TestAuthorizeKismiTutar(t *testing.T) {
	p, _ := yeniSaglayici(t)
	id := oturumAc(t, p, "key-1", map[string]any{manual.DataKeyAuthorizedAmount: 5_000})

	result, err := p.Authorize(context.Background(), id)

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, int64(5_000), result.AuthorizedAmount)
}

// TestAuthorizeKismiTutarSinirlari oturum tutarını aşan ya da sıfır olan bir
// kısmi tutarın reddedildiğini doğrular.
func TestAuthorizeKismiTutarSinirlari(t *testing.T) {
	for ad, deger := range map[string]int64{"asan": tutar + 1, "sifir": 0, "negatif": -5} {
		t.Run(ad, func(t *testing.T) {
			p, _ := yeniSaglayici(t)
			id := oturumAc(t, p, "key-"+ad, map[string]any{manual.DataKeyAuthorizedAmount: deger})

			_, err := p.Authorize(context.Background(), id)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
		})
	}
}

// TestCaptureVeIkinciCapture tahsilatın ve tekrarının davranışını doğrular.
func TestCaptureVeIkinciCapture(t *testing.T) {
	p, store := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	require.NoError(t, p.Capture(ctx, id, 0), "sıfır tutar bloke edilenin tamamını çeker")

	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCaptured, ses.Status)
	assert.Equal(t, tutar, ses.CapturedAmount)

	_, oncekiUpdates := store.sayimlar()
	require.NoError(t, p.Capture(ctx, id, 0), "ikinci çağrı hata vermemeli")
	require.NoError(t, p.Capture(ctx, id, tutar), "aynı tutarla tekrar da hata vermemeli")
	_, sonrakiUpdates := store.sayimlar()
	assert.Equal(t, oncekiUpdates, sonrakiUpdates, "tekrarlar deftere yazmamalı")
}

// TestKismiCaptureKalanBlokajiSerbestBirakir defterdeki bloke tutarın fiilen
// çekilen tutara indiğini doğrular.
//
// Gerçek sağlayıcılar tahsilatta kalan blokajı bırakır. Taklit bırakmasaydı
// sağlayıcının defteri ile modülün kaydı ayrışır ve mutabakatta müşterinin
// üzerinde olmayan bir blokaj görünürdü; oturum "captured" olduğu için iptal
// yoluyla düzeltmek de mümkün değildir.
func TestKismiCaptureKalanBlokajiSerbestBirakir(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	require.NoError(t, p.Capture(ctx, id, 1))

	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), ses.CapturedAmount)
	assert.Equal(t, int64(1), ses.AuthorizedAmount,
		"çekilmeyen blokaj defterde ASILI kalmamalı")
}

// TestCaptureFarkliTutarlaCakisir tahsil edilmiş bir oturumun BAŞKA bir
// tutarla yeniden çekilemeyeceğini doğrular; o bir tekrar değil, yeni bir
// istektir.
func TestCaptureFarkliTutarlaCakisir(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, tutar))

	err := p.Capture(ctx, id, tutar-1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestCaptureBlokeTutariAsamaz bloke edilenden fazlasının çekilemeyeceğini
// doğrular.
func TestCaptureBlokeTutariAsamaz(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", map[string]any{manual.DataKeyAuthorizedAmount: 5_000})
	require.NoError(t, mustAuthorize(t, p, id))

	err := p.Capture(ctx, id, 5_001)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestCaptureYetkilendirilmemisOturumdaCakisir geçersiz geçişi doğrular.
func TestCaptureYetkilendirilmemisOturumdaCakisir(t *testing.T) {
	p, _ := yeniSaglayici(t)
	id := oturumAc(t, p, "key-1", nil)

	err := p.Capture(context.Background(), id, 0)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestRefundKismiVeTamIade iade akışını doğrular.
func TestRefundKismiVeTamIade(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))

	require.NoError(t, p.Refund(ctx, id, 2_500))
	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2_500), ses.RefundedAmount)

	require.NoError(t, p.Refund(ctx, id, 0), "sıfır tutar KALANI iade eder")
	ses, err = p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, tutar, ses.RefundedAmount)
}

// TestTamIadeSonrasiIkinciSifirIadeHataVermez çekirdek sözleşmesinin "Refund
// tekrar çağrılabilir" şartını doğrular.
//
// Kalan sıfırdır ve hiçbir şey yapılmaz; böylece tam iade isteği güvenle
// yeniden denenebilir.
func TestTamIadeSonrasiIkinciSifirIadeHataVermez(t *testing.T) {
	p, store := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))
	require.NoError(t, p.Refund(ctx, id, 0))
	_, oncekiUpdates := store.sayimlar()

	require.NoError(t, p.Refund(ctx, id, 0))

	_, sonrakiUpdates := store.sayimlar()
	assert.Equal(t, oncekiUpdates, sonrakiUpdates, "iade edilecek bir şey kalmamışken yazılmamalı")
}

// TestRefundKalaniAsamaz olmayan parayı iade etme isteğinin reddedildiğini
// doğrular.
func TestRefundKalaniAsamaz(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))

	err := p.Refund(ctx, id, tutar+1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestRefundTahsilEdilmemisOturumdaCakisir geçersiz geçişi doğrular.
func TestRefundTahsilEdilmemisOturumdaCakisir(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	err := p.Refund(ctx, id, 100)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestCancelIkiKezCagrilabilir saga telafisinin İDEMPOTENT olduğunu doğrular.
//
// Faz 6 saga'sı ödeme adımı patladığında bunu çağırır ve bir workflow yeniden
// denendiğinde ya da çift tetiklendiğinde ikinci çağrı akışı patlatmamalıdır.
// Yazma sayacı, ikinci çağrının deftere hiç dokunmadığını da kanıtlar.
func TestCancelIkiKezCagrilabilir(t *testing.T) {
	p, store := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))

	require.NoError(t, p.Cancel(ctx, id))
	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, ses.Status)
	assert.Zero(t, ses.AuthorizedAmount, "blokaj serbest bırakılmalı")

	_, oncekiUpdates := store.sayimlar()
	require.NoError(t, p.Cancel(ctx, id), "ikinci iptal hata VERMEMELİ")
	_, sonrakiUpdates := store.sayimlar()
	assert.Equal(t, oncekiUpdates, sonrakiUpdates, "ikinci iptal deftere yazmamalı")
}

// TestCancelReddedilmisOturumuKapatirVeSebebiKorur reddedilmiş bir oturumun
// iptal edilebildiğini ve ret sebebinin KAYBOLMADIĞINI doğrular.
//
// Saga, oturumu açan adımın telafisi olarak Cancel çağırır; ret yüzünden
// patlayan bir akışta o oturum "failed" durumdadır ve telafi hata vermemelidir.
func TestCancelReddedilmisOturumuKapatirVeSebebiKorur(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", map[string]any{
		manual.DataKeyOutcome:       manual.OutcomeDecline,
		manual.DataKeyDeclineReason: "kart reddedildi",
	})
	_, err := p.Authorize(ctx, id)
	require.NoError(t, err)

	require.NoError(t, p.Cancel(ctx, id))

	ses, err := p.GetSession(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, ses.Status)
	assert.Equal(t, "kart reddedildi", ses.DeclineReason, "ret sebebi korunmalı")
}

// TestCancelTahsilEdilmisOturumdaCakisir çekilen paranın iptalle geri
// alınamayacağını doğrular; yol iadedir.
func TestCancelTahsilEdilmisOturumdaCakisir(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()
	id := oturumAc(t, p, "key-1", nil)
	require.NoError(t, mustAuthorize(t, p, id))
	require.NoError(t, p.Capture(ctx, id, 0))

	err := p.Cancel(ctx, id)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestBilinmeyenOturumNotFound idempotentliğin "her şeyi sessizce yut" demek
// OLMADIĞINI doğrular.
//
// İki kez iptal edilen gerçek bir oturum ile hiç var olmamış bir kimlik farklı
// durumlardır; ikincisi çağıran tarafta bir hatadır ve görünmelidir.
func TestBilinmeyenOturumNotFound(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()

	assert.True(t, errors.HasKind(p.Cancel(ctx, "manses_YOK"), errors.KindNotFound))
	assert.True(t, errors.HasKind(p.Capture(ctx, "manses_YOK", 0), errors.KindNotFound))
	assert.True(t, errors.HasKind(p.Refund(ctx, "manses_YOK", 0), errors.KindNotFound))
	_, err := p.Authorize(ctx, "manses_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound))
}

// TestBosOturumKimligiInvalid boş kimliğin "bulunamadı" değil "geçersiz"
// olduğunu doğrular; ikisi çağıran için farklı hatalardır.
func TestBosOturumKimligiInvalid(t *testing.T) {
	p, _ := yeniSaglayici(t)
	ctx := context.Background()

	assert.True(t, errors.HasKind(p.Cancel(ctx, "  "), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Capture(ctx, "", 0), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Refund(ctx, "", 0), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Capture(ctx, "manses_X", -1), errors.KindInvalid))
	assert.True(t, errors.HasKind(p.Refund(ctx, "manses_X", -1), errors.KindInvalid))
	_, err := p.Authorize(ctx, "")
	assert.True(t, errors.HasKind(err, errors.KindInvalid))
	_, err = p.GetSession(ctx, "")
	assert.True(t, errors.HasKind(err, errors.KindInvalid))
}

// TestSaglayiciKimligi kaydın ve akışların kullandığı kimliği doğrular.
func TestSaglayiciKimligi(t *testing.T) {
	p, _ := yeniSaglayici(t)
	assert.Equal(t, manual.ID, p.ID())
	assert.Equal(t, "manual", p.ID())
}

// mustAuthorize oturumu yetkilendirir ve sonucun gerçekten "authorized"
// olduğunu doğrular.
func mustAuthorize(t *testing.T, p *manual.Provider, sessionID string) error {
	t.Helper()

	result, err := p.Authorize(context.Background(), sessionID)
	if err != nil {
		return err
	}
	require.Equal(t, coreprovider.SessionAuthorized, result.Status)
	return nil
}

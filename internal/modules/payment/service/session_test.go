package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestCreateSessionKoleksiyonuAwaitingYapar oturum açmanın koleksiyonun
// türetilmiş durumunu güncellediğini doğrular.
func TestCreateSessionKoleksiyonuAwaitingYapar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)

	ses := oturumAc(t, svc, col.ID, "key-1")

	assert.Equal(t, models.SessionPending, ses.Status)
	assert.Equal(t, tutar, ses.Amount, "tutar verilmezse koleksiyonun kalanı kullanılır")
	assert.Equal(t, paraKodu, ses.CurrencyCode, "para birimi koleksiyondan gelir")
	assert.Equal(t, "ext_key-1", ses.ExternalID, "sağlayıcının kimliği saklanmalı")

	guncel, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAwaiting, guncel.Status)
}

// TestAyniAnahtarlaIkinciCreateSessionTekOturumUretir plan Bölüm 2.6'nın
// idempotency şartını doğrular.
//
// Yalnızca dönen kimliğin aynı olması yetmez: SAĞLAYICIYA ikinci kez
// gidilmediği de kanıtlanır. Sağlayıcıya her seferinde giden bir uygulama,
// idempotency'yi tamamen sağlayıcının insafına bırakmış olurdu ve her sağlayıcı
// bunu sunmaz.
func TestAyniAnahtarlaIkinciCreateSessionTekOturumUretir(t *testing.T) {
	svc, store, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)

	ilk := oturumAc(t, svc, col.ID, "key-1")
	ikinci := oturumAc(t, svc, col.ID, "key-1")

	assert.Equal(t, ilk.ID, ikinci.ID, "aynı anahtar aynı oturumu dönmeli")
	create, _, _, _, _ := prov.cagrilar()
	assert.Equal(t, 1, create, "sağlayıcıya YALNIZCA bir kez gidilmeli")

	oturumlar, err := store.ListPaymentSessionsByCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, oturumlar, 1, "tek oturum kaydı olmalı")
}

// TestAyniAnahtarBaskaKoleksiyondaCakisir idempotency anahtarının yeniden
// kullanımını reddettiğimizi doğrular.
//
// Sessizce mevcut oturumu dönmek, çağıranın BAŞKA bir sipariş için açtığını
// sandığı oturumun aslında eski siparişe ait olması demekti; ödeme yanlış
// koleksiyona yazılırdı.
func TestAyniAnahtarBaskaKoleksiyondaCakisir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	ilkKol := koleksiyonAc(t, svc, tutar)
	ikinciKol := koleksiyonAc(t, svc, tutar)
	oturumAc(t, svc, ilkKol.ID, "key-1")

	_, err := svc.CreateSession(ctx, ikinciKol.ID, saglayiciID,
		service.CreateSessionInput{IdempotencyKey: "key-1"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateSessionKalanTutariAsamaz koleksiyondan fazlasının bloke
// edilemeyeceğini doğrular.
func TestCreateSessionKalanTutariAsamaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)

	_, err := svc.CreateSession(ctx, col.ID, saglayiciID, service.CreateSessionInput{
		Amount:         tutar + 1,
		IdempotencyKey: "key-1",
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestAcikOturumluKoleksiyonaIkinciTamOturumAcilamaz ÇİFT TAHSİLATIN kapısını
// kapatan kuralı doğrular.
//
// Kalan tutar yalnızca YETKİLENDİRİLMİŞ tutara bakılarak hesaplanırsa, henüz
// hiçbiri yetkilendirilmemişken aynı koleksiyona her biri TAM tutarlı iki
// oturum açılabilir. İkisi de yetkilendirilince koleksiyonun İKİ KATI bloke
// edilir, ikisi de tahsil edilince müşteriden iki kez para çekilir ve
// koleksiyon ödenmiş görünür. Açık oturum da tutar REZERVE eder.
func TestAcikOturumluKoleksiyonaIkinciTamOturumAcilamaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ilk := oturumAc(t, svc, col.ID, "key-1")
	require.Equal(t, models.SessionPending, ilk.Status, "ilk oturum henüz yetkilendirilmedi")

	_, err := svc.CreateSession(ctx, col.ID, saglayiciID, service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestAcikOturumlarinToplamiKoleksiyonuAsamaz bölünmüş ödemenin de tavanı
// olduğunu doğrular.
//
// Kalan tutar açık oturumları saydığı için ikinci oturum yalnızca ARTAN tutar
// kadar açılabilir; fazlası çakışma verir. Bölme işleminin kendisi meşrudur,
// toplamın koleksiyonu aşması değildir.
func TestAcikOturumlarinToplamiKoleksiyonuAsamaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	_, err := svc.CreateSession(ctx, col.ID, saglayiciID, service.CreateSessionInput{
		Amount:         tutar / 4,
		IdempotencyKey: "key-1",
	})
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, saglayiciID, service.CreateSessionInput{
		Amount:         tutar,
		IdempotencyKey: "key-2",
	})
	require.Error(t, err, "kalan yalnızca dörtte üçtür")
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))

	kalan, err := svc.CreateSession(ctx, col.ID, saglayiciID,
		service.CreateSessionInput{IdempotencyKey: "key-3"})
	require.NoError(t, err, "kalanın tamamı için oturum açılabilmeli")
	assert.Equal(t, tutar-tutar/4, kalan.Amount)
}

// TestIptalEdilenOturumRezervasyonuSerbestBirakir telafi sonrasında
// koleksiyonun yeniden ödenebildiğini doğrular.
//
// Açık oturumların tutar rezerve etmesi, iptal edilen bir oturumun
// koleksiyonu sonsuza kadar kilitlemesi anlamına GELMEMELİDİR; saga bir adımı
// telafi ettikten sonra müşteri yeni bir ödeme yolu deneyebilmelidir.
func TestIptalEdilenOturumRezervasyonuSerbestBirakir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ilk := oturumAc(t, svc, col.ID, "key-1")
	require.NoError(t, svc.CancelPayment(ctx, ilk.ID))

	ikinci, err := svc.CreateSession(ctx, col.ID, saglayiciID,
		service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.NoError(t, err)
	assert.Equal(t, tutar, ikinci.Amount, "iptal edilen oturumun rezervasyonu düşmeli")
}

// TestSonlanmisOturumunAnahtariYenidenKullanilamaz telafi sonrası aynı
// anahtarla ilerlemenin AÇIKÇA reddedildiğini doğrular.
//
// İptal edilmiş oturumu olduğu gibi dönmek, çağıranın bir sonraki adımda
// anlaşılmaz bir geçiş çakışması alması demekti: iptal edilmiş oturum
// yetkilendirilemez ve saga sonsuza kadar aynı hatayla düşerdi. Hata kodu
// çağırana YENİ bir anahtar gerektiğini söyler.
func TestSonlanmisOturumunAnahtariYenidenKullanilamaz(t *testing.T) {
	tests := map[string]func(t *testing.T, svc *service.Service, sessionID string){
		"canceled": func(t *testing.T, svc *service.Service, sessionID string) {
			require.NoError(t, svc.CancelPayment(context.Background(), sessionID))
		},
		"failed": func(t *testing.T, svc *service.Service, sessionID string) {
			_, err := svc.AuthorizePayment(context.Background(), sessionID)
			require.Error(t, err)
		},
	}

	for ad, hazirla := range tests {
		t.Run(ad, func(t *testing.T) {
			svc, _, prov := yeniServis(t)
			ctx := context.Background()
			col := koleksiyonAc(t, svc, tutar)
			ses := oturumAc(t, svc, col.ID, "key-1")
			prov.senaryo(coreprovider.SessionFailed, 0, "kart reddedildi")
			hazirla(t, svc, ses.ID)

			_, err := svc.CreateSession(ctx, col.ID, saglayiciID,
				service.CreateSessionInput{IdempotencyKey: "key-1"})

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
			assert.Equal(t, service.CodeSessionTerminal, errors.CodeOf(err))
		})
	}
}

// TestTamBlokeliKoleksiyondaYeniOturumCakisir koleksiyonun tamamı bloke
// edilmişken açılacak tutar kalmadığını doğrular.
func TestTamBlokeliKoleksiyondaYeniOturumCakisir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, saglayiciID, service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestTahsilatliKoleksiyondaYeniOturumCakisir çift tahsilatın kapısını kapatan
// kuralı doğrular.
func TestTahsilatliKoleksiyondaYeniOturumCakisir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, saglayiciID, service.CreateSessionInput{IdempotencyKey: "key-2"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestKayitsizSaglayiciNotFound sağlayıcının kaydedilmeyi unutulmasının
// teşhis edilebilir bir hata verdiğini doğrular.
func TestKayitsizSaglayiciNotFound(t *testing.T) {
	svc, _, _ := yeniServis(t)
	col := koleksiyonAc(t, svc, tutar)

	_, err := svc.CreateSession(context.Background(), col.ID, "stripe",
		service.CreateSessionInput{IdempotencyKey: "key-1"})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)
	assert.Contains(t, err.Error(), saglayiciID, "mesaj KAYITLI sağlayıcıları yazmalı")
}

// TestCreateSessionHatasindaHicbirSeyYazilmaz sağlayıcı patladığında işlemin
// geri alındığını doğrular.
func TestCreateSessionHatasindaHicbirSeyYazilmaz(t *testing.T) {
	svc, store, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	prov.createErr = errors.Unavailable("saglayici_kapali", "sağlayıcıya ulaşılamadı")

	_, err := svc.CreateSession(ctx, col.ID, saglayiciID, service.CreateSessionInput{IdempotencyKey: "key-1"})

	require.Error(t, err)
	oturumlar, listErr := store.ListPaymentSessionsByCollection(ctx, col.ID)
	require.NoError(t, listErr)
	assert.Empty(t, oturumlar, "işlem geri alınmalı")

	guncel, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.CollectionNotPaid, guncel.Status, "koleksiyon durumu değişmemeli")
}

// TestAuthorizeKoleksiyonuAuthorizedYapar mutlu yolu doğrular.
func TestAuthorizeKoleksiyonuAuthorizedYapar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")

	guncelOturum, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized, guncelOturum.Status)
	assert.Equal(t, tutar, guncelOturum.AuthorizedAmount,
		"sağlayıcı sıfır bildirdiyse oturumun tamamı bloke sayılır")

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAuthorized, guncelKol.Status)
	assert.Equal(t, tutar, guncelKol.AuthorizedAmount)
}

// TestIkinciAuthorizeSaglayiciyaGitmez idempotent dalın sağlayıcıya
// gitmediğini ve tutarı İKİNCİ KEZ eklemediğini doğrular.
//
// Koleksiyonun bloke tutarına iki kez eklemek, iki kat bloke edilmiş gibi
// görünmesi ve koleksiyonun kalanının yanlış hesaplanması demek olurdu.
func TestIkinciAuthorizeSaglayiciyaGitmez(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	tekrar, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err, "ikinci yetkilendirme hata VERMEMELİ")
	assert.Equal(t, models.SessionAuthorized, tekrar.Status)
	_, authorize, _, _, _ := prov.cagrilar()
	assert.Equal(t, 1, authorize, "sağlayıcıya YALNIZCA bir kez gidilmeli")

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, tutar, guncelKol.AuthorizedAmount, "bloke tutar İKİ KAT olmamalı")
}

// TestAuthorizeRedHataDondururAmaOturumuKaliciYazar Faz 6 saga'sının ödeme
// adımını patlatan davranışı doğrular.
//
// İki iddia birden kritiktir ve birbirini tamamlar:
//
//   - Metot HATA döner. Ret sessizce başarı sayılsaydı, durumu kontrol etmeyi
//     unutan bir akış ödenmemiş bir siparişi onaylardı.
//   - Oturum yine de "failed" olarak KALICI yazılır. Hata dönmek için işlemi
//     geri alan bir uygulama reddi de silerdi ve oturum sonsuza kadar "pending"
//     görünürdü.
func TestAuthorizeRedHataDondururAmaOturumuKaliciYazar(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	prov.senaryo(coreprovider.SessionFailed, 0, "yetersiz bakiye")

	_, err := svc.AuthorizePayment(ctx, ses.ID)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeAuthorizationDeclined, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "yetersiz bakiye")

	guncelOturum, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionFailed, guncelOturum.Status, "ret KALICI yazılmalı")
	assert.Equal(t, "yetersiz bakiye", guncelOturum.DeclineReason)

	guncelKol, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Zero(t, guncelKol.AuthorizedAmount)
	assert.Equal(t, models.CollectionNotPaid, guncelKol.Status,
		"yalnızca reddedilmiş oturumu olan koleksiyon yeniden denenebilir olmalı")
}

// TestAuthorizeKismiBlokeKoleksiyonuAwaitingBirakir kısmi yetkilendirmenin
// koleksiyonu "authorized" YAPMADIĞINI doğrular.
//
// Eksik bloke edilmiş bir koleksiyonu "authorized" saymak, tahsilat adımının
// olmayan parayı çekmeye çalışması demek olurdu.
func TestAuthorizeKismiBlokeKoleksiyonuAwaitingBirakir(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	prov.senaryo(coreprovider.SessionAuthorized, tutar/2, "")

	guncelOturum, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.Equal(t, tutar/2, guncelOturum.AuthorizedAmount)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAwaiting, guncelKol.Status)
	assert.Equal(t, tutar/2, guncelKol.AuthorizedAmount)
}

// TestBosSaglayiciYanitiOturumVerisiniSilmez sağlayıcının GÖVDESİZ yanıtının
// oturumda saklanan veriyi korudugunu doğrular.
//
// Gerçek sağlayıcıların çoğu yetkilendirme yanıtında gövde döndürmez. Boş
// yanıtla üzerine yazan bir uygulama, oturum açılırken saklanan bilgiyi (örn.
// istemcinin kullanacağı client_secret) silerdi ve hata ancak üretimde, ödeme
// akışının ortasında görünürdü.
func TestBosSaglayiciYanitiOturumVerisiniSilmez(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	require.NotEmpty(t, ses.Data, "oturum açılışta sağlayıcı verisi saklamalı")
	prov.yetkilendirmeVerisi(nil)

	guncel, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.JSONEq(t, string(ses.Data), string(guncel.Data),
		"boş yanıt mevcut veriyi SİLMEMELİ")
}

// TestSaglayiciYanitiVarsaOturumVerisiUzerineYazilir dolu bir yanıtın
// gerçekten uygulandığını doğrular; koruma kuralı "hiç güncelleme" demek
// değildir.
func TestSaglayiciYanitiVarsaOturumVerisiUzerineYazilir(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	prov.yetkilendirmeVerisi(json.RawMessage(`{"client_secret":"cs_1"}`))

	guncel, err := svc.AuthorizePayment(ctx, ses.ID)

	require.NoError(t, err)
	assert.JSONEq(t, `{"client_secret":"cs_1"}`, string(guncel.Data))
}

// TestAuthorizeGecersizGecisler durum makinesinin çakışma dallarını doğrular.
func TestAuthorizeGecersizGecisler(t *testing.T) {
	tests := map[string]func(t *testing.T, svc *service.Service, sessionID string){
		"captured": func(t *testing.T, svc *service.Service, sessionID string) {
			_, err := svc.AuthorizePayment(context.Background(), sessionID)
			require.NoError(t, err)
			_, err = svc.CapturePayment(context.Background(), sessionID, 0)
			require.NoError(t, err)
		},
		"canceled": func(t *testing.T, svc *service.Service, sessionID string) {
			require.NoError(t, svc.CancelPayment(context.Background(), sessionID))
		},
	}

	for ad, hazirla := range tests {
		t.Run(ad, func(t *testing.T) {
			svc, _, _ := yeniServis(t)
			col := koleksiyonAc(t, svc, tutar)
			ses := oturumAc(t, svc, col.ID, "key-1")
			hazirla(t, svc, ses.ID)

			_, err := svc.AuthorizePayment(context.Background(), ses.ID)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
			assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
		})
	}
}

// TestReddedilmisOturumYenidenYetkilendirilemez ret nihaidir; yeni bir oturum
// açılmalıdır.
func TestReddedilmisOturumYenidenYetkilendirilemez(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	prov.senaryo(coreprovider.SessionFailed, 0, "kart reddedildi")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err)

	prov.senaryo(coreprovider.SessionAuthorized, 0, "")
	_, err = svc.AuthorizePayment(ctx, ses.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestAuthorizeSaglayiciSozlesmeIhlalleri sözleşme dışı yanıtların Internal
// olarak sınıflandırıldığını doğrular.
//
// Sözleşme ihlali istemcinin düzeltebileceği bir şey değildir; 409 dönmek
// entegrasyonu yazanın sorunu kendi tarafında aramasına yol açardı.
func TestAuthorizeSaglayiciSozlesmeIhlalleri(t *testing.T) {
	tests := map[string]struct {
		status coreprovider.SessionStatus
		amount int64
	}{
		"beklenmeyen durum":      {status: coreprovider.SessionPending},
		"taninmayan durum":       {status: coreprovider.SessionStatus("weird")},
		"tutari asan bloke":      {status: coreprovider.SessionAuthorized, amount: tutar + 1},
		"negatif bloke tutari":   {status: coreprovider.SessionAuthorized, amount: -1},
		"iptal edilmis bildirim": {status: coreprovider.SessionCanceled},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			svc, _, prov := yeniServis(t)
			ctx := context.Background()
			col := koleksiyonAc(t, svc, tutar)
			ses := oturumAc(t, svc, col.ID, "key-1")
			prov.senaryo(tt.status, tt.amount, "")

			_, err := svc.AuthorizePayment(ctx, ses.ID)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInternal), "hata: %v", err)
			assert.Equal(t, service.CodeProviderContract, errors.CodeOf(err))
		})
	}
}

// TestAuthorizeKilitSirasi kilitlerin KANONİK sırada alındığını doğrular.
//
// Sıra bir eşzamanlılık sözleşmesidir: koleksiyon her zaman oturumdan ÖNCE
// kilitlenir. Gerçek veritabanında ihlali ancak yarış altında, kilitlenme
// (deadlock) olarak görünürdü; burada sıra doğrudan okunur.
func TestAuthorizeKilitSirasi(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")

	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	sira := store.kilitSirasi()
	require.GreaterOrEqual(t, len(sira), 2)
	assert.Equal(t, []string{"collection", "collection", "session"}, sira,
		"önce oturum açılırken koleksiyon, sonra yetkilendirmede koleksiyon -> oturum")
}

// TestCancelIkiKezCagrilabilir saga telafisinin İDEMPOTENT olduğunu doğrular.
//
// Faz 6 saga'sı ödeme adımı patladığında bunu çağırır. İkinci çağrının hata
// vermemesi yetmez: sağlayıcıya ikinci kez GİTMEDİĞİ ve koleksiyonun bloke
// tutarına İKİNCİ KEZ dokunmadığı da kanıtlanır — aksi hâlde tutar negatife
// düşerdi.
func TestCancelIkiKezCagrilabilir(t *testing.T) {
	svc, store, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))
	oncekiKol, _ := store.yazimlar()

	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "ikinci iptal hata VERMEMELİ")

	sonrakiKol, _ := store.yazimlar()
	assert.Equal(t, oncekiKol, sonrakiKol, "ikinci iptal koleksiyona yazmamalı")
	_, _, _, _, cancel := prov.cagrilar()
	assert.Equal(t, 1, cancel, "sağlayıcıya YALNIZCA bir kez gidilmeli")
}

// TestCancelBlokajiSerbestBirakir iptalin koleksiyonun bloke tutarını geri
// aldığını ve durumu "canceled" yaptığını doğrular.
func TestCancelBlokajiSerbestBirakir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	guncelOturum, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, guncelOturum.Status)
	assert.Zero(t, guncelOturum.AuthorizedAmount)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKol.AuthorizedAmount, "blokaj koleksiyondan da düşmeli")
	assert.Equal(t, models.CollectionCanceled, guncelKol.Status)
}

// TestCancelBekleyenOturumdaCalisir yetkilendirilmemiş bir oturumun da
// kapatılabildiğini doğrular; saga oturumu açtıktan sonra başka bir adımda
// patlarsa telafi tam olarak bu durumu bulur.
func TestCancelBekleyenOturumdaCalisir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	guncelOturum, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, guncelOturum.Status)
}

// TestCancelReddedilmisOturumuKapatir ret yüzünden patlayan bir akışın
// telafisinin hata VERMEDİĞİNİ doğrular.
func TestCancelReddedilmisOturumuKapatir(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	prov.senaryo(coreprovider.SessionFailed, 0, "kart reddedildi")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	guncelOturum, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionCanceled, guncelOturum.Status)
	assert.Equal(t, "kart reddedildi", guncelOturum.DeclineReason, "ret sebebi korunmalı")
}

// TestCancelTahsilEdilmisOturumdaCakisir çekilen paranın iptalle geri
// alınamayacağını doğrular; yol iadedir.
func TestCancelTahsilEdilmisOturumdaCakisir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	err = svc.CancelPayment(ctx, ses.ID)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestCancelBilinmeyenOturumNotFound idempotentliğin "her şeyi sessizce yut"
// demek OLMADIĞINI doğrular.
func TestCancelBilinmeyenOturumNotFound(t *testing.T) {
	svc, _, _ := yeniServis(t)

	err := svc.CancelPayment(context.Background(), "payses_YOK")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)
}

// TestCancelSaglayiciHatasindaHicbirSeyYazilmaz sağlayıcı iptali reddederse
// modülün de kaydını değiştirmediğini doğrular.
//
// Sağlayıcıda hâlâ açık olan bir blokajı modülde "iptal edildi" diye yazmak,
// müşterinin parasının asılı kalması ve kimsenin bunu fark etmemesi demekti.
func TestCancelSaglayiciHatasindaHicbirSeyYazilmaz(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	prov.cancelErr = errors.Unavailable("saglayici_kapali", "sağlayıcıya ulaşılamadı")

	err = svc.CancelPayment(ctx, ses.ID)

	require.Error(t, err)
	guncelOturum, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionAuthorized, guncelOturum.Status, "işlem geri alınmalı")

	guncelKol, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Equal(t, tutar, guncelKol.AuthorizedAmount)
}

// TestCancelKilitSirasi iptal akışının kilit sırasını doğrular.
func TestCancelKilitSirasi(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	store.kilitler = nil

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	assert.Equal(t, []string{"collection", "session"}, store.kilitSirasi())
}

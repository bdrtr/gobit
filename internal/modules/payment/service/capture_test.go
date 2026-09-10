package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// yetkilendirilmisOturum tahsilat testleri için bloke edilmiş bir oturum kurar.
func yetkilendirilmisOturum(t *testing.T, svc *service.Service) (models.PaymentCollection, models.PaymentSession) {
	t.Helper()

	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	guncel, err := svc.AuthorizePayment(context.Background(), ses.ID)
	require.NoError(t, err)
	return col, guncel
}

// TestCaptureTahsilatUretirVeKoleksiyonuCapturedYapar mutlu yolu doğrular.
func TestCaptureTahsilatUretirVeKoleksiyonuCapturedYapar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)

	payment, err := svc.CapturePayment(ctx, ses.ID, 0)

	require.NoError(t, err)
	assert.Equal(t, tutar, payment.Amount, "sıfır tutar bloke edilenin tamamını çeker")
	assert.Equal(t, ses.ID, payment.PaymentSessionID)
	assert.Equal(t, col.ID, payment.PaymentCollectionID)
	assert.Equal(t, paraKodu, payment.CurrencyCode)
	assert.False(t, payment.CapturedAt.IsZero(), "tahsilat anı damgalanmalı")
	assert.Equal(t, models.PaymentIDPrefix, payment.ID[:len(models.PaymentIDPrefix)])

	guncelOturum, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCaptured, guncelOturum.Status)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured, guncelKol.Status)
	assert.Equal(t, tutar, guncelKol.CapturedAmount)
}

// TestIkinciCaptureAyniTahsilatiDonerVeSaglayiciyaGitmez idempotent dalı
// doğrular.
//
// Bir oturumdan en fazla BİR tahsilat çıkar. İkinci çağrının yeni bir kayıt
// üretmesi, müşteriden iki kez para çekilmiş gibi görünmesi demek olurdu.
func TestIkinciCaptureAyniTahsilatiDonerVeSaglayiciyaGitmez(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)
	ilk, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	ikinci, err := svc.CapturePayment(ctx, ses.ID, 0)

	require.NoError(t, err, "ikinci tahsilat hata VERMEMELİ")
	assert.Equal(t, ilk.ID, ikinci.ID, "aynı tahsilat dönmeli")
	_, _, capture, _, _ := prov.cagrilar()
	assert.Equal(t, 1, capture, "sağlayıcıya YALNIZCA bir kez gidilmeli")

	tahsilatlar, err := svc.ListPayments(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, tahsilatlar, 1, "tek tahsilat kaydı olmalı")

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, tutar, guncelKol.CapturedAmount, "tahsil edilen tutar İKİ KAT olmamalı")
}

// TestCaptureAyniTutarlaTekrarHataVermez tekrarın açık tutarla da güvenli
// olduğunu doğrular.
func TestCaptureAyniTutarlaTekrarHataVermez(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	ilk, err := svc.CapturePayment(ctx, ses.ID, tutar)
	require.NoError(t, err)

	ikinci, err := svc.CapturePayment(ctx, ses.ID, tutar)

	require.NoError(t, err)
	assert.Equal(t, ilk.ID, ikinci.ID)
}

// TestCaptureFarkliTutarlaCakisir tahsil edilmiş bir oturumun BAŞKA bir
// tutarla yeniden çekilemeyeceğini doğrular; o bir tekrar değil, yeni bir
// istektir.
func TestCaptureFarkliTutarlaCakisir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	_, err := svc.CapturePayment(ctx, ses.ID, tutar)
	require.NoError(t, err)

	_, err = svc.CapturePayment(ctx, ses.ID, tutar-1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestCaptureBlokeTutariAsamaz olmayan parayı çekmenin engellendiğini
// doğrular.
func TestCaptureBlokeTutariAsamaz(t *testing.T) {
	svc, _, prov := yeniServis(t)
	ctx := context.Background()
	col := koleksiyonAc(t, svc, tutar)
	ses := oturumAc(t, svc, col.ID, "key-1")
	prov.senaryo(coreprovider.SessionAuthorized, tutar/2, "")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	_, err = svc.CapturePayment(ctx, ses.ID, tutar)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
}

// TestKismiTahsilatKoleksiyonuCapturedYapmaz eksik ödemenin TAM ödeme gibi
// görünmediğini doğrular.
//
// Kısmi tahsilat oturumu KAPATIR (bir oturumdan ikinci tahsilat çıkmaz) ama
// koleksiyonu ödenmiş yapmaz: 50.000'lik bir koleksiyondan çekilen 1 birim
// "captured" sayılsaydı, ödemenin tamamlandığını durumdan okuyan bir saga
// ödenmemiş bir siparişi onaylardı.
func TestKismiTahsilatKoleksiyonuCapturedYapmaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)

	payment, err := svc.CapturePayment(ctx, ses.ID, 1)

	require.NoError(t, err)
	assert.Equal(t, int64(1), payment.Amount)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyCaptured, guncelKol.Status)
	assert.Equal(t, int64(1), guncelKol.CapturedAmount)
}

// TestKismiTahsilatCekilmeyenBlokajiSerbestBirakir tahsil edilmeyen blokajın
// koleksiyonda ASILI KALMADIĞINI doğrular.
//
// Oturum tahsilattan sonra "captured"tır ve iptal EDİLEMEZ; fark burada
// bırakılmazsa serbest bırakılmasının hiçbir yolu kalmaz ve koleksiyon
// "müşterinin üzerinde ne kadar bloke var" sorusuna sonsuza kadar fazla cevap
// verir.
func TestKismiTahsilatCekilmeyenBlokajiSerbestBirakir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)

	_, err := svc.CapturePayment(ctx, ses.ID, 1)
	require.NoError(t, err)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKol.AuthorizedAmount,
		"tahsil edilen oturumun blokajı koleksiyonda KALMAMALI")

	guncelOturum, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), guncelOturum.AuthorizedAmount,
		"oturumun bloke tutarı fiilen çekilen tutara inmeli")

	// İptal yolu kapalıdır; bu yüzden serbest bırakma tahsilat anında olmak
	// ZORUNDADIR.
	require.Error(t, svc.CancelPayment(ctx, ses.ID))
}

// TestTamTahsilatBlokajiKapatir tahsil edilen tutarın koleksiyonda bloke
// olarak da sayılmadığını doğrular.
//
// Aynı paranın hem "bloke" hem "tahsil edildi" görünmesi, mutabakatta iki kat
// para demektir.
func TestTamTahsilatBlokajiKapatir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)

	_, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKol.AuthorizedAmount)
	assert.Equal(t, tutar, guncelKol.CapturedAmount)
	assert.Equal(t, models.CollectionCaptured, guncelKol.Status)
}

// TestCaptureGecersizGecisler durum makinesinin çakışma dallarını doğrular.
func TestCaptureGecersizGecisler(t *testing.T) {
	tests := map[string]func(t *testing.T, svc *service.Service, sessionID string){
		"pending": func(_ *testing.T, _ *service.Service, _ string) {},
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

			_, err := svc.CapturePayment(context.Background(), ses.ID, 0)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
			assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
		})
	}
}

// TestCaptureNegatifTutarInvalid para doğrulamasını sınar.
func TestCaptureNegatifTutarInvalid(t *testing.T) {
	svc, _, _ := yeniServis(t)
	_, ses := yetkilendirilmisOturum(t, svc)

	_, err := svc.CapturePayment(context.Background(), ses.ID, -1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
}

// TestCaptureYazmaHatasindaHicbirSeyYazilmaz tahsilat kaydı yazılamazsa
// oturumun ve koleksiyonun DEĞİŞMEDİĞİNİ doğrular.
//
// Oturumu "captured" bırakıp tahsilat kaydını yazamamak, parası çekilmiş ama
// kaydı olmayan bir ödeme demekti; mutabakatta bulunması imkânsız bir fark.
func TestCaptureYazmaHatasindaHicbirSeyYazilmaz(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)
	store.failCreatePayment = errors.Internal("fake_write_failed", "yazılamadı")

	_, err := svc.CapturePayment(ctx, ses.ID, 0)

	require.Error(t, err)
	guncelOturum, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionAuthorized, guncelOturum.Status, "işlem geri alınmalı")

	guncelKol, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Zero(t, guncelKol.CapturedAmount)
	assert.Equal(t, models.CollectionAuthorized, guncelKol.Status)
}

// TestRefundKismiIadeKoleksiyonuPartiallyRefundedYapar kısmi iade akışını
// doğrular.
func TestRefundKismiIadeKoleksiyonuPartiallyRefundedYapar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	refund, err := svc.RefundPayment(ctx, payment.ID, tutar/4, "müşteri talebi")

	require.NoError(t, err)
	assert.Equal(t, tutar/4, refund.Amount)
	assert.Equal(t, "müşteri talebi", refund.Reason)
	assert.Equal(t, models.RefundIDPrefix, refund.ID[:len(models.RefundIDPrefix)])

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyRefunded, guncelKol.Status)
	assert.Equal(t, tutar/4, guncelKol.RefundedAmount)
}

// TestRefundTamIadeKoleksiyonuRefundedYapar tam iade akışını doğrular.
//
// Sıfır tutarın KALANI iade ettiği de sınanır: iki kısmi iade sonrası sıfırla
// yapılan çağrı kalanı kapatmalıdır.
func TestRefundTamIadeKoleksiyonuRefundedYapar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, tutar/4, "")
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, payment.ID, 0, "kalan")
	require.NoError(t, err)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded, guncelKol.Status)
	assert.Equal(t, tutar, guncelKol.RefundedAmount)

	iadeler, err := svc.ListRefunds(ctx, payment.ID)
	require.NoError(t, err)
	assert.Len(t, iadeler, 2, "her iade AYRI bir kayıt üretir")
}

// TestRefundKalaniAsamaz olmayan parayı iade etme isteğinin reddedildiğini
// doğrular.
func TestRefundKalaniAsamaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, tutar+1, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestTamIadeSonrasiIkinciIadeCakisir iade edilecek bir şey kalmadığında
// çakışma döndüğünü doğrular.
//
// Bu metot BİLİNÇLİ olarak idempotent DEĞİLDİR: iki kez çağrılan bir iade,
// gerçek dünyada iki kat para geri ödemesidir ve sessizce yutulması, ikinci
// isteğin uygulanmadığını fark etmeyen bir operatöre yanlış bilgi verirdi.
func TestTamIadeSonrasiIkinciIadeCakisir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, payment.ID, 0, "")
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, 0, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeNothingToRefund, errors.CodeOf(err))
}

// TestRefundIkiKezCagrilirsaIkiKatIadeOlur iadenin idempotent OLMADIĞINI
// açıkça kanıtlar.
//
// Bu, davranışın belgelenmiş hâlidir ve testin varlığı bilinçlidir: birinin
// "iade de idempotent olsun" diye değiştirmesi, 10 birimlik iki gerçek iadeyi
// tek iadeye indirger ve müşteriye eksik para döner.
func TestRefundIkiKezCagrilirsaIkiKatIadeOlur(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, 1_000, "")
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, payment.ID, 1_000, "")
	require.NoError(t, err)

	guncel, err := svc.GetPayment(ctx, payment.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2_000), guncel.RefundedAmount)
}

// TestRefundTahsilatsizKimlikNotFound olmayan tahsilatın iade edilemeyeceğini
// doğrular.
func TestRefundTahsilatsizKimlikNotFound(t *testing.T) {
	svc, _, _ := yeniServis(t)

	_, err := svc.RefundPayment(context.Background(), "pay_YOK", 0, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)
}

// TestRefundNegatifTutarInvalid para doğrulamasını sınar.
func TestRefundNegatifTutarInvalid(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, -1, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
}

// TestRefundKilitSirasi iade akışının kilit sırasını doğrular.
//
// Sıra koleksiyon -> oturum -> tahsilat olmalıdır; tahsilat kilidini önce alan
// bir uygulama, aynı koleksiyona dokunan başka bir akışla kilitlenirdi.
func TestRefundKilitSirasi(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	store.kilitler = nil

	_, err = svc.RefundPayment(ctx, payment.ID, 100, "")
	require.NoError(t, err)

	assert.Equal(t, []string{"collection", "session", "payment"}, store.kilitSirasi())
}

// TestCaptureKilitSirasi tahsilat akışının kilit sırasını doğrular.
func TestCaptureKilitSirasi(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	store.kilitler = nil

	_, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	assert.Equal(t, []string{"collection", "session"}, store.kilitSirasi())
}

// TestTahsilatOlayiIslemIcindeYazilirVeSonrasindaYayimlanir para hareketinin
// duyurusunun kaybolamayacağını doğrular.
//
// İki yazma var ve ikisi de gerekli: outbox satırı GARANTİ, doğrudan yayım
// HIZ. Satır para hareketiyle aynı işlemde commit olur, yani süreç commit ile
// yayım arasında ölse bile olay kaybolmaz; röle onu gönderir.
func TestTahsilatOlayiIslemIcindeYazilirVeSonrasindaYayimlanir(t *testing.T) {
	svc, store, otobus := yeniServisOtobusle(t)
	ctx := context.Background()
	col, ses := yetkilendirilmisOturum(t, svc)

	odeme, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	require.Len(t, store.outbox, 1, "olay işlem içinde outbox'a yazılmalı")
	assert.Equal(t, service.EventPaymentCaptured, store.outbox[0].Name)
	assert.Equal(t, col.ID, store.outbox[0].Data[service.EventFieldCollectionID],
		"olay KOLEKSİYONU adlandırır")
	assert.NotContains(t, store.outbox[0].Data, "amount",
		"olay TUTAR taşımaz: iade idempotent değil, yani yükteki tutar bir ARTIM olurdu")
	assert.Equal(t, service.EventPaymentCaptured+":"+odeme.ID, store.outbox[0].ID,
		"kimlik TAHSİLAT satırından türer, koleksiyondan değil")

	// Ve commit'ten sonra doğrudan da yayımlanır. İkisinin AYNI kimliği
	// taşıması, outbox satırı ile bu yayımı İKİ olay değil TEK olay yapan şey:
	// olay kimliğinde idempotent bir abone — otobüsün sözleşmesi zaten bunu
	// şart koşuyor — iki teslimatı birbirinden ayırt edemez.
	yayimlanan := otobus.events()
	require.Len(t, yayimlanan, 1, "commit'ten sonra hızlı yol da yayımlamalı")
	assert.Equal(t, store.outbox[0].ID, yayimlanan[0].ID,
		"iki teslimat TEK olaydır; farklı kimlik iki kez işlenen bir para hareketi demek")
	assert.Equal(t, store.outbox[0].Data, yayimlanan[0].Data,
		"aynı kimliğin iki farklı gövde taşıması, abonenin hangisini gördüğüne göre "+
			"başka davranması demek olurdu")
}

// TestIkiIadeIkiAyriOlayKimligiUretir outbox'un sessiz yutmasını engelleyen
// kuralı doğrular.
//
// Outbox satırı ON CONFLICT (id) DO NOTHING ile yazılıyor, yani aynı kimlik
// HATA vermez, sessizce DÜŞER. Koleksiyona anahtarlı bir kimlik ikinci iadeyi
// birincinin tekrarı sanıp yok ederdi; kimlik iade SATIRINDAN türediği için iki
// gerçek iade iki gerçek olaydır.
func TestIkiIadeIkiAyriOlayKimligiUretir(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	_, ses := yetkilendirilmisOturum(t, svc)
	odeme, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	ilk, err := svc.RefundPayment(ctx, odeme.ID, tutar/4, "")
	require.NoError(t, err)
	ikinci, err := svc.RefundPayment(ctx, odeme.ID, tutar/4, "")
	require.NoError(t, err)
	require.NotEqual(t, ilk.ID, ikinci.ID)

	var iadeOlaylari []string
	for _, satir := range store.outbox {
		if satir.Name == service.EventPaymentRefunded {
			iadeOlaylari = append(iadeOlaylari, satir.ID)
		}
	}

	require.Len(t, iadeOlaylari, 2, "iki gerçek iade iki olaydır")
	assert.NotEqual(t, iadeOlaylari[0], iadeOlaylari[1],
		"aynı kimlik outbox'ta sessizce düşürülürdü")
}

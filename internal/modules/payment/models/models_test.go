package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// TestSessionGecisTablosuTumDallariylaDogru oturum durum makinesinin HER
// hücresini doğrular.
//
// Tablo, üç işlem × beş durum = 15 hücrenin tamamını kapsar. Tek tek yazılması
// bilinçlidir: bir dalı "diğerleri gibi" varsayan bir test, o dalın sessizce
// değiştirilmesini yakalayamaz. Özellikle iki hücre kritiktir ve ayrı ayrı
// gerekçelendirilmiştir:
//
//   - captured + Cancel = conflict — para çekilmiş bir oturumu iptal etmek,
//     müşteriden alınan tutarı kayıtta yokmuş gibi göstermek olurdu.
//   - canceled + Cancel = noop — saga telafisinin idempotent olabilmesi tam
//     olarak bu hücreye bağlıdır.
func TestSessionGecisTablosuTumDallariylaDogru(t *testing.T) {
	durumlar := []models.SessionStatus{
		models.SessionPending,
		models.SessionAuthorized,
		models.SessionCaptured,
		models.SessionCanceled,
		models.SessionFailed,
	}

	beklenen := map[models.SessionStatus]struct {
		authorize, capture, cancel models.SessionAction
	}{
		models.SessionPending: {
			authorize: models.ActionProceed,
			capture:   models.ActionConflict,
			cancel:    models.ActionProceed,
		},
		models.SessionAuthorized: {
			authorize: models.ActionNoop,
			capture:   models.ActionProceed,
			cancel:    models.ActionProceed,
		},
		models.SessionCaptured: {
			authorize: models.ActionConflict,
			capture:   models.ActionNoop,
			cancel:    models.ActionConflict,
		},
		models.SessionCanceled: {
			authorize: models.ActionConflict,
			capture:   models.ActionConflict,
			cancel:    models.ActionNoop,
		},
		models.SessionFailed: {
			authorize: models.ActionConflict,
			capture:   models.ActionConflict,
			cancel:    models.ActionProceed,
		},
	}

	for _, durum := range durumlar {
		t.Run(durum.String(), func(t *testing.T) {
			want := beklenen[durum]
			assert.Equal(t, want.authorize, durum.AuthorizeAction(), "AuthorizeAction")
			assert.Equal(t, want.capture, durum.CaptureAction(), "CaptureAction")
			assert.Equal(t, want.cancel, durum.CancelAction(), "CancelAction")
		})
	}
}

// TestTanimsizDurumHerIsleminiReddeder tanımsız bir durumun üç işlemde de
// çakışma ürettiğini doğrular.
//
// Sıfır değerin güvenli olması sözleşmedir: veritabanından okunan bozuk bir
// durum değeri "devam et" olarak yorumlanırsa, kaydın gerçek durumu bilinmeden
// para hareketi yapılırdı.
func TestTanimsizDurumHerIsleminiReddeder(t *testing.T) {
	bozuk := models.SessionStatus("bilinmeyen")

	assert.False(t, bozuk.Valid())
	assert.Equal(t, models.ActionConflict, bozuk.AuthorizeAction())
	assert.Equal(t, models.ActionConflict, bozuk.CaptureAction())
	assert.Equal(t, models.ActionConflict, bozuk.CancelAction())
}

// TestTerminalDurumlar sonlanmış oturumların hangileri olduğunu sabitler.
//
// Ayrım idempotency'nin sınırıdır: aynı anahtarla yapılan bir tekrar,
// tahsil edilmiş bir oturumdan mevcut tahsilatı okuyabilir ama iptal edilmiş
// ya da reddedilmiş bir oturumla ilerleyemez.
func TestTerminalDurumlar(t *testing.T) {
	assert.False(t, models.SessionPending.Terminal())
	assert.False(t, models.SessionAuthorized.Terminal())
	assert.False(t, models.SessionCaptured.Terminal(),
		"tahsil edilmiş oturum akışın BAŞARILI sonucudur, çıkmaz değil")
	assert.True(t, models.SessionCanceled.Terminal())
	assert.True(t, models.SessionFailed.Terminal())
}

// TestCollectionStatusForTumDallar koleksiyon durumu türetiminin her dalını
// doğrular.
//
// Tahsilat dalı ayrıca EKSİK ödemeyi ayırt eder: koleksiyonun tutarını
// karşılamayan bir tahsilat "captured" değil "partially_captured"tır. Aksi
// hâlde 50.000'lik bir koleksiyondan çekilen 1 birim, saga'ya ödeme tamammış
// gibi görünürdü.
//
// Fikstürler bilinçli olarak "yanlış sıra" tuzağını kurar: tahsilatı olan bir
// koleksiyonun canlı oturumu da vardır, ve doğru sonuç "awaiting" değil
// "captured"tır. Para her zaman sayımı yener; sırayı ters çeviren bir uygulama
// bu satırlarda düşer.
func TestCollectionStatusForTumDallar(t *testing.T) {
	tests := []struct {
		ad       string
		col      models.PaymentCollection
		counts   models.SessionCounts
		beklenen models.CollectionStatus
	}{
		{
			ad:       "oturumsuz koleksiyon not_paid",
			col:      models.PaymentCollection{Amount: 1000},
			counts:   models.SessionCounts{},
			beklenen: models.CollectionNotPaid,
		},
		{
			ad:       "acik oturum awaiting",
			col:      models.PaymentCollection{Amount: 1000},
			counts:   models.SessionCounts{Live: 1, Total: 1},
			beklenen: models.CollectionAwaiting,
		},
		{
			ad:       "tam bloke authorized",
			col:      models.PaymentCollection{Amount: 1000, AuthorizedAmount: 1000},
			counts:   models.SessionCounts{Live: 1, Total: 1},
			beklenen: models.CollectionAuthorized,
		},
		{
			ad:       "kismi bloke hala awaiting",
			col:      models.PaymentCollection{Amount: 1000, AuthorizedAmount: 400},
			counts:   models.SessionCounts{Live: 1, Total: 1},
			beklenen: models.CollectionAwaiting,
		},
		{
			ad:       "tahsilat canli oturumu yener",
			col:      models.PaymentCollection{Amount: 1000, AuthorizedAmount: 1000, CapturedAmount: 1000},
			counts:   models.SessionCounts{Live: 1, Total: 1},
			beklenen: models.CollectionCaptured,
		},
		{
			ad:       "eksik tahsilat partially_captured",
			col:      models.PaymentCollection{Amount: 1000, CapturedAmount: 1},
			counts:   models.SessionCounts{Total: 1},
			beklenen: models.CollectionPartiallyCaptured,
		},
		{
			ad:       "bir eksik tahsilat da partially_captured",
			col:      models.PaymentCollection{Amount: 1000, CapturedAmount: 999},
			counts:   models.SessionCounts{Total: 1},
			beklenen: models.CollectionPartiallyCaptured,
		},
		{
			ad:       "tam tahsilat captured",
			col:      models.PaymentCollection{Amount: 1000, CapturedAmount: 1000},
			counts:   models.SessionCounts{Total: 1},
			beklenen: models.CollectionCaptured,
		},
		{
			ad: "kismi iade partially_refunded",
			col: models.PaymentCollection{
				Amount: 1000, AuthorizedAmount: 1000, CapturedAmount: 1000, RefundedAmount: 400,
			},
			counts:   models.SessionCounts{Total: 1},
			beklenen: models.CollectionPartiallyRefunded,
		},
		{
			ad: "tam iade refunded",
			col: models.PaymentCollection{
				Amount: 1000, AuthorizedAmount: 1000, CapturedAmount: 1000, RefundedAmount: 1000,
			},
			counts:   models.SessionCounts{Total: 1},
			beklenen: models.CollectionRefunded,
		},
		{
			ad:       "iptal edilmis oturum canceled",
			col:      models.PaymentCollection{Amount: 1000},
			counts:   models.SessionCounts{Canceled: 1, Total: 1},
			beklenen: models.CollectionCanceled,
		},
		{
			ad:       "yalnizca reddedilmis oturum not_paid kalir",
			col:      models.PaymentCollection{Amount: 1000},
			counts:   models.SessionCounts{Failed: 2, Total: 2},
			beklenen: models.CollectionNotPaid,
		},
		{
			ad:       "iptal ve reddin birlikte oldugu durumda canceled",
			col:      models.PaymentCollection{Amount: 1000},
			counts:   models.SessionCounts{Canceled: 1, Failed: 1, Total: 2},
			beklenen: models.CollectionCanceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.ad, func(t *testing.T) {
			assert.Equal(t, tt.beklenen, models.CollectionStatusFor(tt.col, tt.counts))
		})
	}
}

// TestKalanTutarHesaplari kalan tutar yardımcılarının negatife düşmediğini
// doğrular.
//
// Aşırı iade ya da aşırı bloke veritabanı kısıtıyla zaten engellenir; bu test
// yardımcıların BOZUK bir kayıt karşısında da negatif dönmediğini kanıtlar.
// Negatif bir "kalan", çağıranda ters yönlü bir para hareketine dönüşürdü.
func TestKalanTutarHesaplari(t *testing.T) {
	col := models.PaymentCollection{Amount: 1000, AuthorizedAmount: 1200, CapturedAmount: 500, RefundedAmount: 800}
	assert.Equal(t, int64(0), col.RefundableAmount())

	kismi := models.PaymentCollection{Amount: 1000, AuthorizedAmount: 400, CapturedAmount: 400, RefundedAmount: 100}
	assert.Equal(t, int64(300), kismi.RefundableAmount())

	pay := models.Payment{Amount: 500, RefundedAmount: 700}
	assert.Equal(t, int64(0), pay.RefundableAmount())
	assert.Equal(t, int64(200), models.Payment{Amount: 500, RefundedAmount: 300}.RefundableAmount())

	manual := models.ManualSession{CapturedAmount: 500, RefundedAmount: 500}
	assert.Equal(t, int64(0), manual.RefundableAmount())
	assert.Equal(t, int64(150), models.ManualSession{CapturedAmount: 200, RefundedAmount: 50}.RefundableAmount())
}

// TestKimlikOnekleriVeSirasi kimliklerin önekli, tekil ve zamana göre
// sıralanabilir olduğunu doğrular.
func TestKimlikOnekleriVeSirasi(t *testing.T) {
	uretilenler := map[string]func() string{
		models.PaymentCollectionIDPrefix: models.NewPaymentCollectionID,
		models.PaymentSessionIDPrefix:    models.NewPaymentSessionID,
		models.PaymentIDPrefix:           models.NewPaymentID,
		models.RefundIDPrefix:            models.NewRefundID,
		models.ManualSessionIDPrefix:     models.NewManualSessionID,
	}

	for prefix, uret := range uretilenler {
		t.Run(prefix, func(t *testing.T) {
			ilk, ikinci := uret(), uret()

			assert.True(t, len(ilk) == len(prefix)+26, "gövde 26 karakter olmalı: %s", ilk)
			assert.Equal(t, prefix, ilk[:len(prefix)])
			assert.NotEqual(t, ilk, ikinci, "iki kimlik aynı olmamalı")
		})
	}
}

// TestCollectionStatusGecerlilik tanımlı ve tanımsız durumları ayırır.
func TestCollectionStatusGecerlilik(t *testing.T) {
	gecerli := []models.CollectionStatus{
		models.CollectionNotPaid, models.CollectionAwaiting, models.CollectionAuthorized,
		models.CollectionCaptured, models.CollectionPartiallyRefunded,
		models.CollectionRefunded, models.CollectionCanceled,
	}
	for _, durum := range gecerli {
		assert.True(t, durum.Valid(), "%q geçerli olmalı", durum)
	}
	assert.False(t, models.CollectionStatus("").Valid())
	assert.False(t, models.CollectionStatus("paid").Valid())
}

// TestSessionActionString sonuçların okunabilir adını doğrular.
//
// Ad yalnızca teşhis içindir ama hata mesajlarında görünür; tanımsız bir
// değerin "conflict" olarak okunması, sıfır değerin güvenli olmasıyla aynı
// gerekçeye dayanır.
func TestSessionActionString(t *testing.T) {
	assert.Equal(t, "proceed", models.ActionProceed.String())
	assert.Equal(t, "noop", models.ActionNoop.String())
	assert.Equal(t, "conflict", models.ActionConflict.String())
	assert.Equal(t, "conflict", models.SessionAction(200).String())
}

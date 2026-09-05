package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// paymentInterop saga'nın (internal/workflows) payment modülünden ihtiyaç
// duyduğu yüzeyin BİREBİR kopyasıdır.
//
// Testin asıl işi budur: saga bu modülü import EDEMEZ (ADR 0006) ve yalnızca
// ilkel tiplerle yazılmış bir arayüz tanımlayabilir. Somut [service.Interop]
// tipinin o arayüzü YAPISAL olarak karşıladığı derleyici tarafından burada
// kanıtlanır; imza kayması container'dan çözüm anına kalmaz.
//
// Buradaki tanımın workflow tarafındaki tanımla aynı kalması bir sözleşmedir;
// ayrışırlarsa e2e testi düşer.
type paymentInterop interface {
	CreateCollection(ctx context.Context, reference, currencyCode string, amount int64) (string, error)
	OpenSession(ctx context.Context, collectionID, providerID, idempotencyKey string) (string, error)
	OpenSessionWithData(
		ctx context.Context,
		collectionID, providerID, idempotencyKey string,
		data json.RawMessage,
	) (string, error)
	Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error)
	Capture(ctx context.Context, sessionID string, amount int64) (string, error)
	Cancel(ctx context.Context, sessionID string) error
	Refund(ctx context.Context, paymentID string, amount int64, reason string) (string, error)
	Collection(ctx context.Context, collectionID string) (
		status string,
		amount, authorized, captured, refunded int64,
		err error,
	)
	SessionStatus(ctx context.Context, sessionID string) (string, error)
}

// Interop'un saga'nın beklediği İLKEL yüzeyi karşıladığı derleme zamanında
// sabitlenir.
var _ paymentInterop = (*service.Interop)(nil)

// yeniInterop sahte depo üzerinde çalışan bir interop yüzeyi kurar.
func yeniInterop(t *testing.T) (*service.Interop, *fakeProvider) {
	t.Helper()

	svc, _, prov := yeniServis(t)
	return service.NewInterop(svc), prov
}

// TestInteropUctanUcaAkis saga'nın izleyeceği yolu ilkel yüzeyden yürütür.
func TestInteropUctanUcaAkis(t *testing.T) {
	iop, _ := yeniInterop(t)
	ctx := context.Background()

	colID, err := iop.CreateCollection(ctx, referans, paraKodu, tutar)
	require.NoError(t, err)
	assert.NotEmpty(t, colID)

	sesID, err := iop.OpenSession(ctx, colID, saglayiciID, "key-1")
	require.NoError(t, err)

	durum, bloke, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), durum)
	assert.Equal(t, tutar, bloke, "yüzey bloke edilen TUTARI da taşımalı")

	payID, err := iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, payID)

	kolDurum, kolTutar, kolBloke, kolTahsil, kolIade := koleksiyon(t, iop, colID)
	assert.Equal(t, models.CollectionCaptured.String(), kolDurum)
	assert.Equal(t, tutar, kolTutar)
	assert.Zero(t, kolBloke, "tahsilat blokajı kapatır")
	assert.Equal(t, tutar, kolTahsil)
	assert.Zero(t, kolIade)

	refundID, err := iop.Refund(ctx, payID, 0, "test iadesi")
	require.NoError(t, err)
	assert.NotEmpty(t, refundID)

	kolDurum, _, _, _, kolIade = koleksiyon(t, iop, colID)
	assert.Equal(t, models.CollectionRefunded.String(), kolDurum)
	assert.Equal(t, tutar, kolIade)
}

// koleksiyon interop yüzeyinden koleksiyonun durumunu ve tutarlarını okur.
func koleksiyon(t *testing.T, iop *service.Interop, colID string) (
	durum string,
	tutar, bloke, tahsil, iade int64,
) {
	t.Helper()

	durum, tutar, bloke, tahsil, iade, err := iop.Collection(context.Background(), colID)
	require.NoError(t, err)
	return durum, tutar, bloke, tahsil, iade
}

// TestInteropEksikTahsilatTutarlardanGorunur saga'nın ödemenin TAM olduğunu kendi
// doğrulayabildiğini kanıtlar.
//
// Faz 6'nın ödeme atlatması tam buradan geçiyordu: yüzey yalnızca durum dizesi
// döndüğü için saga'nın kontrol edecek hiçbir sayısı yoktu. Tutarlar
// döndüğünde kural tek satırdır — captured >= amount değilse sipariş
// onaylanmaz.
func TestInteropEksikTahsilatTutarlardanGorunur(t *testing.T) {
	iop, prov := yeniInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, referans, paraKodu, tutar)
	require.NoError(t, err)
	sesID, err := iop.OpenSession(ctx, colID, saglayiciID, "key-1")
	require.NoError(t, err)
	prov.senaryo(coreprovider.SessionAuthorized, 1, "")

	durum, bloke, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), durum,
		"kısmi bloke de sağlayıcı açısından başarılıdır")
	assert.Equal(t, int64(1), bloke, "saga eksik blokajı SAYIDAN görmeli")

	_, err = iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)

	kolDurum, kolTutar, _, kolTahsil, _ := koleksiyon(t, iop, colID)
	assert.Less(t, kolTahsil, kolTutar, "ödeme EKSİKTİR")
	assert.Equal(t, models.CollectionPartiallyCaptured.String(), kolDurum,
		"eksik tahsilat koleksiyonu captured YAPMAMALI")
}

// TestInteropAyniAnahtarTekOturum saga'nın bir adımı yeniden denemesinin
// müşteriden ikinci kez tahsilat denemesine yol açmadığını doğrular.
func TestInteropAyniAnahtarTekOturum(t *testing.T) {
	iop, prov := yeniInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, referans, paraKodu, tutar)
	require.NoError(t, err)

	ilk, err := iop.OpenSession(ctx, colID, saglayiciID, "key-1")
	require.NoError(t, err)
	ikinci, err := iop.OpenSession(ctx, colID, saglayiciID, "key-1")
	require.NoError(t, err)

	assert.Equal(t, ilk, ikinci)
	create, _, _, _, _ := prov.cagrilar()
	assert.Equal(t, 1, create)
}

// TestInteropCancelIkiKezCagrilabilir saga telafisinin ilkel yüzeyde de
// idempotent olduğunu doğrular.
func TestInteropCancelIkiKezCagrilabilir(t *testing.T) {
	iop, _ := yeniInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, referans, paraKodu, tutar)
	require.NoError(t, err)
	sesID, err := iop.OpenSession(ctx, colID, saglayiciID, "key-1")
	require.NoError(t, err)
	_, _, err = iop.Authorize(ctx, sesID)
	require.NoError(t, err)

	require.NoError(t, iop.Cancel(ctx, sesID))
	require.NoError(t, iop.Cancel(ctx, sesID), "ikinci telafi hata VERMEMELİ")

	durum, err := iop.SessionStatus(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled.String(), durum)
}

// TestInteropAuthorizeRedHataDondurur saga'nın ödeme adımının PATLADIĞINI
// doğrular.
//
// Faz 6'nın DoD'si bunu şart koşar: ödeme adımı başarısızken telafi zinciri
// çalışmalıdır ve zincirin tetiklenmesi için adımın hata dönmesi gerekir.
func TestInteropAuthorizeRedHataDondurur(t *testing.T) {
	iop, prov := yeniInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, referans, paraKodu, tutar)
	require.NoError(t, err)
	sesID, err := iop.OpenSession(ctx, colID, saglayiciID, "key-1")
	require.NoError(t, err)
	prov.senaryo(coreprovider.SessionFailed, 0, "test reddi")

	_, _, err = iop.Authorize(ctx, sesID)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeAuthorizationDeclined, errors.CodeOf(err))
}

// TestInteropOpenSessionWithDataSayilariBozmaz para taşıyan bir davranış
// anahtarının kayan noktaya uğramadan sağlayıcıya ulaştığını doğrular.
//
// Harita üzerinden geçen bir tam sayı float64'e dönerse yeniden kodlanırken
// üstel gösterime kayabilir ("1e+15") ve sağlayıcı tarafında tam sayı olarak
// çözülemez. Para hiçbir aşamada kayan noktaya uğramamalıdır (plan Bölüm 8).
func TestInteropOpenSessionWithDataSayilariBozmaz(t *testing.T) {
	iop, _ := yeniInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, referans, paraKodu, models.MaxAmount)
	require.NoError(t, err)

	sesID, err := iop.OpenSessionWithData(ctx, colID, saglayiciID, "key-1",
		json.RawMessage(`{"manual_authorized_amount":1000000000000}`))

	require.NoError(t, err)
	assert.NotEmpty(t, sesID)
}

// TestInteropBozukDataReddedilir gövdesi JSON nesnesi olmayan bir isteğin
// açıkça reddedildiğini doğrular.
func TestInteropBozukDataReddedilir(t *testing.T) {
	iop, _ := yeniInterop(t)
	ctx := context.Background()
	colID, err := iop.CreateCollection(ctx, referans, paraKodu, tutar)
	require.NoError(t, err)

	_, err = iop.OpenSessionWithData(ctx, colID, saglayiciID, "key-1", json.RawMessage(`[1,2]`))

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
}

// TestInteropHatalariOlduguGibiTasir yüzeyin hataları sarmalayıp
// sınıflandırmasını DEĞİŞTİRMEDİĞİNİ doğrular.
//
// Interop hiçbir karar vermez; bir hatayı burada yeniden sınıflandırmak, aynı
// kuralın iki yerde ayrışması demek olurdu.
func TestInteropHatalariOlduguGibiTasir(t *testing.T) {
	iop, _ := yeniInterop(t)
	ctx := context.Background()

	_, err := iop.CreateCollection(ctx, "", paraKodu, tutar)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)

	_, err = iop.OpenSession(ctx, "paycol_YOK", saglayiciID, "key-1")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, _, err = iop.Authorize(ctx, "payses_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, err = iop.Capture(ctx, "payses_YOK", 0)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	assert.True(t, errors.HasKind(iop.Cancel(ctx, "payses_YOK"), errors.KindNotFound))

	_, err = iop.Refund(ctx, "pay_YOK", 0, "")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, _, _, _, _, err = iop.Collection(ctx, "paycol_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, err = iop.SessionStatus(ctx, "payses_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)
}

package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// TestDurumGecisTablosu durum makinesinin TAMAMINI tek tabloda sınar.
//
// Tablo godoc'lardaki geçiş tablolarının birebir karşılığıdır: bir dal
// değişirse hem belge hem test aynı anda güncellenmek zorunda kalır.
func TestDurumGecisTablosu(t *testing.T) {
	t.Parallel()

	durumlar := []struct {
		durum   models.FulfillmentStatus
		iptal   models.Action
		kargo   models.Action
		teslim  models.Action
		gecerli bool
	}{
		{models.StatusPending, models.ActionProceed, models.ActionProceed, models.ActionConflict, true},
		{models.StatusShipped, models.ActionProceed, models.ActionNoop, models.ActionProceed, true},
		{models.StatusDelivered, models.ActionConflict, models.ActionConflict, models.ActionNoop, true},
		{models.StatusCanceled, models.ActionNoop, models.ActionConflict, models.ActionConflict, true},
		{models.FulfillmentStatus("bilinmeyen"), models.ActionConflict, models.ActionConflict, models.ActionConflict, false},
	}

	for _, satir := range durumlar {
		t.Run(satir.durum.String(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, satir.iptal, satir.durum.CancelAction(), "iptal dalı")
			assert.Equal(t, satir.kargo, satir.durum.ShipAction(), "kargoya verme dalı")
			assert.Equal(t, satir.teslim, satir.durum.DeliverAction(), "teslim dalı")
			assert.Equal(t, satir.gecerli, satir.durum.Valid(), "geçerlilik")
		})
	}
}

// TestTeslimEdilmisIptalEdilemez Faz 7'nin açıkça sorduğu kararı, durum
// makinesi düzeyinde sabitler.
//
// Teslim geri alınamayan fiziksel bir olgudur; çaresi iptal değil iadedir.
// Kargodaki bir gönderi ise geri çağrılabilir ve iptali AÇIKTIR.
func TestTeslimEdilmisIptalEdilemez(t *testing.T) {
	t.Parallel()

	assert.Equal(t, models.ActionConflict, models.StatusDelivered.CancelAction(),
		"teslim edilmiş gönderi iptal edilemez")
	assert.Equal(t, models.ActionProceed, models.StatusShipped.CancelAction(),
		"yoldaki gönderi geri çağrılabilir")
	assert.Equal(t, models.ActionNoop, models.StatusCanceled.CancelAction(),
		"idempotentlik noop dalından gelir")
}

// TestActionSifirDegeriConflict tanımsız bir durumun kazara "devam et"
// olarak yorumlanmadığını kanıtlar.
func TestActionSifirDegeriConflict(t *testing.T) {
	t.Parallel()

	var sifir models.Action
	assert.Equal(t, models.ActionConflict, sifir)
	assert.Equal(t, "conflict", sifir.String())
	assert.Equal(t, "conflict", models.Action(200).String(), "tanımsız değer de conflict yazmalı")
}

// TestFiyatTuruDogrulamasi tanımlı fiyat türlerini sabitler.
func TestFiyatTuruDogrulamasi(t *testing.T) {
	t.Parallel()

	assert.True(t, models.PriceFlat.Valid())
	assert.True(t, models.PriceCalculated.Valid())
	assert.False(t, models.PriceType("dynamic").Valid())
}

// TestProfilTuruDogrulamasi tanımlı profil türlerini sabitler.
func TestProfilTuruDogrulamasi(t *testing.T) {
	t.Parallel()

	assert.True(t, models.ProfileDefault.Valid())
	assert.True(t, models.ProfileGiftCard.Valid())
	assert.True(t, models.ProfileCustom.Valid())
	assert.False(t, models.ProfileType("digital").Valid())
}

// TestKuralIsleci işleç sınıflandırmasını sabitler.
//
// Sayısal işleçlerin ayrı tanınması şarttır: ara toplam gibi para alanları
// dizge olarak karşılaştırılsaydı "9" > "50000" çıkardı.
func TestKuralIsleci(t *testing.T) {
	t.Parallel()

	sayisal := []models.RuleOperator{models.OpGt, models.OpGte, models.OpLt, models.OpLte}
	for _, op := range sayisal {
		assert.True(t, op.Valid(), "%s tanımlı olmalı", op)
		assert.True(t, op.Numeric(), "%s sayısal olmalı", op)
		assert.False(t, op.MultiValue(), "%s tek değer almalı", op)
	}

	dizge := []models.RuleOperator{models.OpEq, models.OpNe}
	for _, op := range dizge {
		assert.True(t, op.Valid())
		assert.False(t, op.Numeric())
		assert.False(t, op.MultiValue())
	}

	cokDegerli := []models.RuleOperator{models.OpIn, models.OpNin}
	for _, op := range cokDegerli {
		assert.True(t, op.Valid())
		assert.False(t, op.Numeric())
		assert.True(t, op.MultiValue(), "%s birden çok değer almalı", op)
	}

	assert.False(t, models.RuleOperator("like").Valid())
}

// TestKimlikOnekleri plan Bölüm 8'in önek konvansiyonunu sabitler.
func TestKimlikOnekleri(t *testing.T) {
	t.Parallel()

	uretilenler := map[string]string{
		models.FulfillmentIDPrefix:        models.NewFulfillmentID(),
		models.ShippingOptionIDPrefix:     models.NewShippingOptionID(),
		models.ShippingProfileIDPrefix:    models.NewShippingProfileID(),
		models.ShippingOptionRuleIDPrefix: models.NewShippingOptionRuleID(),
		models.FulfillmentItemIDPrefix:    models.NewFulfillmentItemID(),
		models.ManualShipmentIDPrefix:     models.NewManualShipmentID(),
	}

	for onek, kimlik := range uretilenler {
		assert.True(t, strings.HasPrefix(kimlik, onek), "%q, %q önekiyle başlamalı", kimlik, onek)
		assert.Len(t, kimlik, len(onek)+26, "gövde 26 karakter olmalı")
	}
}

// TestKimlikTekilVeZamanSirali kimliklerin çakışmadığını ve zamana göre
// sıralanabilir kaldığını kanıtlar.
//
// Sıra iddiası önemlidir: liste sorguları "created_at DESC, id DESC" ile
// sayfalanır ve rastgele bir kimlik, aynı milisaniyedeki kayıtların sırasını
// belirsiz bırakırdı.
func TestKimlikTekilVeZamanSirali(t *testing.T) {
	t.Parallel()

	const adet = 500
	gorulen := make(map[string]struct{}, adet)
	oncekiler := make([]string, 0, adet)

	for range adet {
		kimlik := models.NewFulfillmentID()
		_, cakisma := gorulen[kimlik]
		require.False(t, cakisma, "kimlik çakıştı: %s", kimlik)
		gorulen[kimlik] = struct{}{}
		oncekiler = append(oncekiler, kimlik)
	}

	// Aynı milisaniyede üretilen kimlikler rastgele bölümde ayrışır; sıra
	// iddiası ancak zaman ilerlediğinde tutar.
	time.Sleep(2 * time.Millisecond)
	sonraki := models.NewFulfillmentID()
	assert.Less(t, oncekiler[0], sonraki, "sonra üretilen kimlik sözlüksel olarak büyük olmalı")
}

// TestTutarSinirlari para sınırlarının belgelenen değerlerde olduğunu
// sabitler.
//
// Alt sınırın SIFIR olması bilinçlidir: ücretsiz kargo gerçek bir iş
// kararıdır.
func TestTutarSinirlari(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(0), models.MinAmount, "ücretsiz kargo geçerli olmalı")
	assert.Equal(t, int64(1_000_000_000_000), models.MaxAmount)
	assert.Equal(t, int64(1), models.MinQuantity)
}

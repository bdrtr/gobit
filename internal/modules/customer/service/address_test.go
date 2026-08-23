package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// yeniMusteri test için bir müşteri açar.
func yeniMusteri(ctx context.Context, t *testing.T, svc *Service, eposta string) models.Customer {
	t.Helper()

	c, err := svc.CreateCustomer(ctx, CustomerInput{Email: eposta})
	require.NoError(t, err)
	return c
}

// TestUlkeKoduNormalizeEdilir ülke kodunun BÜYÜK harfe çevrildiğini ve
// biçiminin doğrulandığını kanıtlar.
func TestUlkeKoduNormalizeEdilir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	musteri := yeniMusteri(ctx, t, svc, "ulke@example.com")

	adresi, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	assert.Equal(t, "TR", adresi.CountryCode, "ülke kodu BÜYÜK harfe çevrilmeli")

	for _, kod := range []string{"", "T", "TUR", "T1"} {
		girdi := gecerliAdres()
		girdi.CountryCode = kod
		_, err := svc.CreateAddress(ctx, musteri.ID, girdi)
		require.Error(t, err, "geçersiz ülke kodu: %q", kod)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}
}

// TestZorunluAdresAlanlari boş ilk satır ve şehrin reddedildiğini kanıtlar.
func TestZorunluAdresAlanlari(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	musteri := yeniMusteri(ctx, t, svc, "zorunlu@example.com")

	bosSatir := gecerliAdres()
	bosSatir.Address1 = "   "
	_, err := svc.CreateAddress(ctx, musteri.ID, bosSatir)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	bosSehir := gecerliAdres()
	bosSehir.City = ""
	_, err = svc.CreateAddress(ctx, musteri.ID, bosSehir)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestVarsayilanAdresTekildir müşteri başına tek varsayılan kargo ve tek
// varsayılan fatura adresi olduğunu kanıtlar.
//
// Yeni varsayılan atandığında ESKİSİNİN işareti kalkmalıdır; kalksaydığını
// varsayan bir uygulama, müşteriye iki varsayılan kargo adresi bırakırdı ve
// sepet hangisini seçeceğini bilemezdi. Kuralın veritabanı kısıtıyla da
// zorlandığı entegrasyon testinde kanıtlanır.
func TestVarsayilanAdresTekildir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	musteri := yeniMusteri(ctx, t, svc, "varsayilan@example.com")

	ilk, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	ikinci, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.SetDefaultShippingAddress(ctx, musteri.ID, ilk.ID)
	require.NoError(t, err)

	guncel, err := svc.SetDefaultShippingAddress(ctx, musteri.ID, ikinci.ID)
	require.NoError(t, err)
	assert.True(t, guncel.IsDefaultShipping)

	adresler, err := svc.ListAddresses(ctx, musteri.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, varsayilanSayisi(adresler, models.DefaultShipping),
		"müşteri başına tek varsayılan kargo adresi olmalı")

	eskisi, err := svc.GetAddress(ctx, musteri.ID, ilk.ID)
	require.NoError(t, err)
	assert.False(t, eskisi.IsDefaultShipping, "eski varsayılanın işareti kalkmalı")
}

// TestKargoVeFaturaVarsayilanlariBagimsizdir iki işaretin birbirini
// etkilemediğini kanıtlar.
func TestKargoVeFaturaVarsayilanlariBagimsizdir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	musteri := yeniMusteri(ctx, t, svc, "bagimsiz@example.com")

	kargo, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	fatura, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.SetDefaultShippingAddress(ctx, musteri.ID, kargo.ID)
	require.NoError(t, err)
	_, err = svc.SetDefaultBillingAddress(ctx, musteri.ID, fatura.ID)
	require.NoError(t, err)

	okunanKargo, err := svc.GetAddress(ctx, musteri.ID, kargo.ID)
	require.NoError(t, err)
	assert.True(t, okunanKargo.IsDefaultShipping)
	assert.False(t, okunanKargo.IsDefaultBilling, "fatura işareti kargoyu etkilememeli")

	okunanFatura, err := svc.GetAddress(ctx, musteri.ID, fatura.ID)
	require.NoError(t, err)
	assert.True(t, okunanFatura.IsDefaultBilling)
	assert.False(t, okunanFatura.IsDefaultShipping)
}

// TestOlusturmadaVarsayilanIsareti adresi yaratılırken verilen varsayılan
// işaretinin eskisini temizlediğini kanıtlar.
func TestOlusturmadaVarsayilanIsareti(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	musteri := yeniMusteri(ctx, t, svc, "olusturma@example.com")

	ilkGirdi := gecerliAdres()
	ilkGirdi.IsDefaultShipping = true
	ilk, err := svc.CreateAddress(ctx, musteri.ID, ilkGirdi)
	require.NoError(t, err)
	assert.True(t, ilk.IsDefaultShipping)

	ikinciGirdi := gecerliAdres()
	ikinciGirdi.IsDefaultShipping = true
	_, err = svc.CreateAddress(ctx, musteri.ID, ikinciGirdi)
	require.NoError(t, err)

	adresler, err := svc.ListAddresses(ctx, musteri.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, varsayilanSayisi(adresler, models.DefaultShipping),
		"yeni varsayılan eskisini temizlemeli")
}

// TestVarsayilanAdresSilinince silinen varsayılanın yerine yenisinin
// atanabildiğini kanıtlar.
//
// Kısmi benzersiz indeks deleted_at IS NULL koşuluyla tanımlıdır; silinen satır
// indeksin kapsamından çıkar. Koşul olmasaydı silinmiş bir varsayılan yeri
// sonsuza dek işgal ederdi.
func TestVarsayilanAdresSilinince(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	musteri := yeniMusteri(ctx, t, svc, "silinen@example.com")

	ilk, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	_, err = svc.SetDefaultShippingAddress(ctx, musteri.ID, ilk.ID)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteAddress(ctx, musteri.ID, ilk.ID))

	ikinci, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	yeni, err := svc.SetDefaultShippingAddress(ctx, musteri.ID, ikinci.ID)
	require.NoError(t, err)
	assert.True(t, yeni.IsDefaultShipping)
}

// TestBaskaMusterininAdresiOkunamaz sahiplik denetiminin sorguda olduğunu
// kanıtlar.
func TestBaskaMusterininAdresiOkunamaz(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	sahibi := yeniMusteri(ctx, t, svc, "sahip@example.com")
	yabanci := yeniMusteri(ctx, t, svc, "yabanci@example.com")

	adresi, err := svc.CreateAddress(ctx, sahibi.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.GetAddress(ctx, yabanci.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi okunamamalı")

	err = svc.DeleteAddress(ctx, yabanci.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi silinememeli")

	_, err = svc.SetDefaultShippingAddress(ctx, yabanci.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi varsayılan yapılamamalı")
}

// TestOlmayanMusteriyeAdres eksik müşteri için NotFound döndüğünü kanıtlar.
func TestOlmayanMusteriyeAdres(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	_, err := svc.CreateAddress(ctx, models.NewCustomerID(sabitSaat), gecerliAdres())
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = svc.ListAddresses(ctx, models.NewCustomerID(sabitSaat))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"olmayan müşteri için boş liste değil NotFound dönmeli")
}

// TestAdresGuncellemeKismidir verilmeyen alanların korunduğunu kanıtlar.
func TestAdresGuncellemeKismidir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	musteri := yeniMusteri(ctx, t, svc, "kismi@example.com")

	adresi, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)

	yeniSehir := "Ankara"
	guncel, err := svc.UpdateAddress(ctx, musteri.ID, adresi.ID, UpdateAddressInput{City: &yeniSehir})
	require.NoError(t, err)
	assert.Equal(t, "Ankara", guncel.City)
	assert.Equal(t, adresi.Address1, guncel.Address1, "verilmeyen alan korunmalı")
	assert.Equal(t, adresi.CountryCode, guncel.CountryCode)

	// Zorunlu bir alan verilirse BOŞ olamaz.
	bos := ""
	_, err = svc.UpdateAddress(ctx, musteri.ID, adresi.ID, UpdateAddressInput{City: &bos})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// varsayilanSayisi verilen türde kaç adresin işaretli olduğunu döner.
func varsayilanSayisi(adresler []models.CustomerAddress, kind models.DefaultKind) int {
	var n int
	for i := range adresler {
		a := &adresler[i]
		if (kind == models.DefaultShipping && a.IsDefaultShipping) ||
			(kind == models.DefaultBilling && a.IsDefaultBilling) {
			n++
		}
	}
	return n
}

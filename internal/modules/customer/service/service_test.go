package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// sabitSaat testlerin belirlenimci zaman kaynağıdır.
var sabitSaat = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// yeniServis sahte depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	return New(repo, Options{Now: func() time.Time { return sabitSaat }}), repo
}

// TestEpostaKucukHarfeNormalizeEdilir e-postanın SAKLAMADA normalize edildiğini
// kanıtlar.
//
// Normalizasyon okuma anına bırakılsaydı benzersizlik indeksi ham sütun
// üzerinde çalıştığı için "Ali@X.com" ile "ali@x.com" iki ayrı hesap olurdu.
func TestEpostaKucukHarfeNormalizeEdilir(t *testing.T) {
	ctx := context.Background()
	svc, repo := yeniServis(t)

	created, err := svc.CreateCustomer(ctx, CustomerInput{Email: "  Ali.Veli@Example.COM  "})
	require.NoError(t, err)
	assert.Equal(t, "ali.veli@example.com", created.Email, "e-posta küçük harfe indirilip kırpılmalı")

	// Depoya yazılan değer de normalize edilmiş olmalı; DTO'da düzeltilen ama
	// tabloya ham giden bir değer indeksi işlevsiz bırakırdı.
	stored, ok := repo.customers[created.ID]
	require.True(t, ok)
	assert.Equal(t, "ali.veli@example.com", stored.Email)

	// Okuma yolu da aynı normalizasyonu uygular.
	found, err := svc.GetCustomerByEmail(ctx, "ALI.VELI@EXAMPLE.COM")
	require.NoError(t, err)
	assert.Equal(t, created.ID, found.ID)
}

// TestGecersizEpostaReddedilir doğrulamanın veritabanına gitmeden elediğini
// kanıtlar.
func TestGecersizEpostaReddedilir(t *testing.T) {
	ctx := context.Background()

	durumlar := map[string]string{
		"boş":                       "",
		"yalnızca boşluk":           "   ",
		"@ yok":                     "aliexample.com",
		"yerel bölüm yok":           "@example.com",
		"alan adı yok":              "ali@",
		"alan adında nokta yok":     "ali@example",
		"alan adı noktayla bitiyor": "ali@example.",
		"iki @":                     "ali@veli@example.com",
		"boşluk içeriyor":           "ali veli@example.com",
		"çok uzun":                  strings.Repeat("a", 320) + "@example.com",
	}

	for ad, eposta := range durumlar {
		t.Run(ad, func(t *testing.T) {
			svc, repo := yeniServis(t)

			_, err := svc.CreateCustomer(ctx, CustomerInput{Email: eposta})
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Zero(t, repo.calls["CreateCustomer"], "geçersiz girdi için depoya gidilmemeli")
		})
	}
}

// TestMisafirVeHesapAyrimi iki kayıt yolunun has_account alanını farklı
// kurduğunu kanıtlar.
//
// Ayrım bir gövde bayrağına bağlı olsaydı yönetim ucuna gelen bir istek
// sessizce benzersizlik kuralının dışına düşerdi.
func TestMisafirVeHesapAyrimi(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	hesap, err := svc.CreateCustomer(ctx, CustomerInput{Email: "hesap@example.com"})
	require.NoError(t, err)
	assert.True(t, hesap.HasAccount, "CreateCustomer kayıtlı hesap açmalı")
	assert.False(t, hesap.IsGuest())

	misafir, err := svc.RegisterGuest(ctx, CustomerInput{Email: "misafir@example.com"})
	require.NoError(t, err)
	assert.False(t, misafir.HasAccount, "RegisterGuest misafir kaydı açmalı")
	assert.True(t, misafir.IsGuest())

	assert.True(t, strings.HasPrefix(hesap.ID, models.CustomerIDPrefix), "kimlik önekli olmalı")
}

// TestAyniEpostaylaCokMisafirKabulEdilir Faz 5 DoD'sinin misafir senaryosunu
// kanıtlar.
//
// Aynı e-postayla ikinci bir misafir kaydı REDDEDİLMEMELİDİR: misafir kaydı bir
// kimlik değil, tek seferlik bir alışverişin iletişim bilgisidir.
func TestAyniEpostaylaCokMisafirKabulEdilir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	birinci, err := svc.RegisterGuest(ctx, CustomerInput{Email: "ayni@example.com"})
	require.NoError(t, err)

	ikinci, err := svc.RegisterGuest(ctx, CustomerInput{Email: "AYNI@example.com"})
	require.NoError(t, err, "aynı e-postayla ikinci misafir kaydı kabul edilmeli")

	assert.NotEqual(t, birinci.ID, ikinci.ID, "iki ayrı kayıt olmalı")
	assert.Equal(t, birinci.Email, ikinci.Email, "normalizasyon iki kayıtta da aynı sonucu vermeli")
}

// TestKayitliEpostaTekildir hesapların e-postasının benzersiz olduğunu
// kanıtlar.
func TestKayitliEpostaTekildir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	_, err := svc.CreateCustomer(ctx, CustomerInput{Email: "tek@example.com"})
	require.NoError(t, err)

	_, err = svc.CreateCustomer(ctx, CustomerInput{Email: "TEK@example.com"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"aynı e-postayla ikinci hesap çakışma olmalı")
}

// TestMisafirdenHesabaGecis dönüşümün üç sonucunu da kanıtlar: başarı,
// e-posta çakışması ve zaten hesap olma.
func TestMisafirdenHesabaGecis(t *testing.T) {
	ctx := context.Background()

	t.Run("basarili", func(t *testing.T) {
		svc, _ := yeniServis(t)

		misafir, err := svc.RegisterGuest(ctx, CustomerInput{Email: "gecis@example.com"})
		require.NoError(t, err)

		require.NoError(t, svc.ConvertGuestToAccount(ctx, misafir.ID))

		okunan, err := svc.GetCustomer(ctx, misafir.ID)
		require.NoError(t, err)
		assert.True(t, okunan.HasAccount, "dönüşüm sonrası kayıt hesap olmalı")
	})

	t.Run("eposta baska hesapta", func(t *testing.T) {
		svc, _ := yeniServis(t)

		_, err := svc.CreateCustomer(ctx, CustomerInput{Email: "dolu@example.com"})
		require.NoError(t, err)
		misafir, err := svc.RegisterGuest(ctx, CustomerInput{Email: "dolu@example.com"})
		require.NoError(t, err)

		err = svc.ConvertGuestToAccount(ctx, misafir.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))

		okunan, getErr := svc.GetCustomer(ctx, misafir.ID)
		require.NoError(t, getErr)
		assert.False(t, okunan.HasAccount, "çakışan dönüşüm kaydı DEĞİŞTİRMEMELİ")
	})

	t.Run("zaten hesap", func(t *testing.T) {
		svc, _ := yeniServis(t)

		hesap, err := svc.CreateCustomer(ctx, CustomerInput{Email: "zaten@example.com"})
		require.NoError(t, err)

		err = svc.ConvertGuestToAccount(ctx, hesap.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err),
			"zaten hesap olan kayıt için sessiz no-op DEĞİL, çakışma dönmeli")
	})

	t.Run("olmayan musteri", func(t *testing.T) {
		svc, _ := yeniServis(t)

		err := svc.ConvertGuestToAccount(ctx, models.NewCustomerID(sabitSaat))
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	})
}

// TestKimlikOnekiDogrulanir yanlış türden bir kimliğin veritabanına hiç
// gitmeden elendiğini kanıtlar.
func TestKimlikOnekiDogrulanir(t *testing.T) {
	ctx := context.Background()
	svc, repo := yeniServis(t)

	_, err := svc.GetCustomer(ctx, models.NewCustomerGroupID(sabitSaat))
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.calls["GetCustomer"], "önek hatalıysa depoya gidilmemeli")
}

// TestSayfalamaSinirlari limit/offset doğrulamasını kanıtlar.
func TestSayfalamaSinirlari(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	_, err := svc.ListCustomers(ctx, ListCustomersInput{Limit: MaxLimit + 1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "azami limit aşılamaz")

	_, err = svc.ListCustomers(ctx, ListCustomersInput{Offset: -1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "negatif offset reddedilmeli")

	page, err := svc.ListCustomers(ctx, ListCustomersInput{})
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, page.Limit, "limit verilmezse varsayılan uygulanmalı")
}

// TestListelemeSuzgeci misafir/hesap ve grup süzgeçlerini kanıtlar.
func TestListelemeSuzgeci(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	_, err := svc.CreateCustomer(ctx, CustomerInput{Email: "h1@example.com"})
	require.NoError(t, err)
	misafir, err := svc.RegisterGuest(ctx, CustomerInput{Email: "m1@example.com"})
	require.NoError(t, err)

	yanlis := true
	page, err := svc.ListCustomers(ctx, ListCustomersInput{HasAccount: &yanlis})
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Count)
	require.Len(t, page.Items, 1)
	assert.True(t, page.Items[0].HasAccount)

	dogru := false
	page, err = svc.ListCustomers(ctx, ListCustomersInput{HasAccount: &dogru})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, misafir.ID, page.Items[0].ID)

	// E-posta süzgeci de normalize edilir.
	eposta := "M1@EXAMPLE.COM"
	page, err = svc.ListCustomers(ctx, ListCustomersInput{Email: &eposta})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, misafir.ID, page.Items[0].ID)
}

// TestSilinenMusteriOkunamaz yumuşak silmenin okuma yollarını süzdüğünü
// kanıtlar.
func TestSilinenMusteriOkunamaz(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	musteri, err := svc.CreateCustomer(ctx, CustomerInput{Email: "silinecek@example.com"})
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, musteri.ID))

	_, err = svc.GetCustomer(ctx, musteri.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	page, err := svc.ListCustomers(ctx, ListCustomersInput{})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "yumuşak silinen müşteri listede görünmemeli")

	// Adresler de gider: cascade yalnızca gerçek silmede çalışır, yumuşak silme
	// bir UPDATE olduğu için adresleri kendiliğinden götürmez.
	_, err = svc.ListAddresses(ctx, musteri.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// gecerliAdres testlerde kullanılan geçerli bir adresin girdisidir.
func gecerliAdres() AddressInput {
	return AddressInput{
		FirstName:   "Ali",
		LastName:    "Veli",
		Address1:    "Atatürk Cad. 1",
		City:        "İstanbul",
		CountryCode: "tr",
		PostalCode:  "34000",
	}
}

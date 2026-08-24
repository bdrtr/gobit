package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestProfilVarsayilanTuru tür verilmediğinde "default" uygulandığını
// kanıtlar.
func TestProfilVarsayilanTuru(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profile, err := kurulum.svc.CreateShippingProfile(context.Background(), service.CreateProfileInput{
		Name: "varsayilan",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ProfileDefault, profile.Type)
}

// TestProfilAdiTektir aynı adla ikinci profilin reddedildiğini kanıtlar.
//
// İki aynı adlı profil, yöneticinin hangi kuralı düzenlediğini belirsiz
// bırakırdı.
func TestProfilAdiTektir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	kurulum.profilAc(t, "varsayilan")

	_, err := kurulum.svc.CreateShippingProfile(context.Background(), service.CreateProfileInput{
		Name: "varsayilan",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
}

// TestSecenegiOlanProfilSilinemez silmenin ürünlerin kargo kuralını sessizce
// ortadan kaldırmadığını kanıtlar.
func TestSecenegiOlanProfilSilinemez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})

	err := kurulum.svc.DeleteShippingProfile(context.Background(), profilID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeProfileInUse, errors.CodeOf(err))

	require.NoError(t, kurulum.svc.DeleteShippingOption(context.Background(), secenekID))
	require.NoError(t, kurulum.svc.DeleteShippingProfile(context.Background(), profilID),
		"seçenek kaldırıldıktan sonra profil silinebilmeli")

	_, err = kurulum.svc.GetShippingProfile(context.Background(), profilID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "silinen profil okunamaz olmalı: %v", err)
}

// TestProfilYollariSatiriKilitler kontrol-sonra-yaz yarışının KİLİTLE
// kapatıldığını kanıtlar.
//
// Regresyon: iki yol da profil satırını kilitsiz okuyordu. Silme yolu profili
// "boş" görüp yumuşak siliyor, araya giren seçenek oluşturma ise beklemeden
// tamamlanıyordu; Postgres'te yumuşak silmenin aldığı FOR NO KEY UPDATE ile
// INSERT'ün foreign key için aldığı FOR KEY SHARE ÇAKIŞMAZ. Sonuç: silinmiş
// bir profile bağlı CANLI bir seçenek (gerçek Postgres'te üretildi).
//
// Sahte depo kilit alan metotları yalnızca işlem içinde verir; kilit
// alınmazsa ya da işlem dışına çıkılırsa iddia düşer.
func TestProfilYollariSatiriKilitler(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	kurulum.store.kilitleriSifirla()
	secenek, err := kurulum.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
		CurrencyCode:      "TRY",
		ProviderID:        "sahte",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"profil-paylasimli"}, kurulum.store.kilitSirasi(),
		"seçenek oluşturma profili PAYLAŞIMLI kilitle okumalı")

	require.NoError(t, kurulum.svc.DeleteShippingOption(context.Background(), secenek.ID))

	kurulum.store.kilitleriSifirla()
	require.NoError(t, kurulum.svc.DeleteShippingProfile(context.Background(), profilID))
	assert.Equal(t, []string{"profil"}, kurulum.store.kilitSirasi(),
		"profil silme, sayımdan ÖNCE satırı YAZMA kilidiyle almalı")
}

// TestKayitliOlmayanSaglayiciylaSecenekAcilamaz kurulum hatasının seçenek
// YARATILIRKEN görüldüğünü kanıtlar.
//
// Görülmeseydi, hata ancak müşteriye gösterileceği ya da gönderi açılacağı
// anda patlardı.
func TestKayitliOlmayanSaglayiciylaSecenekAcilamaz(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	_, err := kurulum.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Bilinmeyen kargo",
		ProviderID:        "yok-boyle-bir-saglayici",
		ShippingProfileID: profilID,
		CurrencyCode:      "TRY",
		Amount:            1_000,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata errors.NotFound olmalı: %v", err)
	assert.Equal(t, service.CodeProviderNotFound, errors.CodeOf(err))
}

// TestHesaplananSecenekTutarAlmaz iki kaynaklı fiyatın reddedildiğini
// kanıtlar.
//
// Sessizce sıfırlansaydı, yöneticinin girdiği ücret hiç uygulanmaz ve bunu
// ancak fatura ile görürdü.
func TestHesaplananSecenekTutarAlmaz(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	_, err := kurulum.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Hesaplanan kargo",
		ProviderID:        "sahte",
		ShippingProfileID: profilID,
		CurrencyCode:      "TRY",
		PriceType:         "calculated",
		Amount:            1_000,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
}

// TestSecenekGirdisiDogrulanir geçersiz girdilerin reddedildiğini kanıtlar.
func TestSecenekGirdisiDogrulanir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	durumlar := []struct {
		ad    string
		girdi service.CreateOptionInput
	}{
		{"ad yok", service.CreateOptionInput{
			ProviderID: "sahte", ShippingProfileID: profilID, CurrencyCode: "TRY",
		}},
		{"profil yok", service.CreateOptionInput{
			Name: "Kargo", ProviderID: "sahte", CurrencyCode: "TRY",
		}},
		{"profil öneki yanlış", service.CreateOptionInput{
			Name: "Kargo", ProviderID: "sahte", ShippingProfileID: "sopt_XYZ", CurrencyCode: "TRY",
		}},
		{"para birimi bozuk", service.CreateOptionInput{
			Name: "Kargo", ProviderID: "sahte", ShippingProfileID: profilID, CurrencyCode: "TR",
		}},
		{"negatif tutar", service.CreateOptionInput{
			Name: "Kargo", ProviderID: "sahte", ShippingProfileID: profilID,
			CurrencyCode: "TRY", Amount: -1,
		}},
		{"tanınmayan fiyat türü", service.CreateOptionInput{
			Name: "Kargo", ProviderID: "sahte", ShippingProfileID: profilID,
			CurrencyCode: "TRY", PriceType: "dynamic",
		}},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			_, err := kurulum.svc.CreateShippingOption(context.Background(), durum.girdi)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err) || errors.IsNotFound(err),
				"hata istemci hatası olmalı: %v", err)
		})
	}
}

// TestParaBirimiBuyukHarfeCevrilir küçük harf kodun normalleştiğini kanıtlar.
//
// Çevrilmeseydi "try" ve "TRY" iki farklı para birimi gibi davranır ve
// uygunluk süzgeci seçeneği hiç bulamazdı.
func TestParaBirimiBuyukHarfeCevrilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	option, err := kurulum.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Standart kargo",
		ProviderID:        "sahte",
		ShippingProfileID: profilID,
		CurrencyCode:      " try ",
		Amount:            2_000,
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", option.CurrencyCode)

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "try",
	})
	require.NoError(t, err)
	require.Len(t, secenekler, 1, "küçük harf sorgu da seçeneği bulmalı")
}

// TestSecenekGuncellemesiSaglayiciyiDegistirmez seçeneğin sağlayıcısının ve
// profilinin güncelleme yüzeyinde OLMADIĞINI kanıtlar.
//
// Değişebilseydi, o seçenekle açılmış gönderilerin hangi sağlayıcıda olduğu
// geçmişe dönük yanıltıcı hâle gelirdi.
func TestSecenekGuncellemesiSaglayiciyiDegistirmez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})

	yeniAd := "Ekonomik kargo"
	yeniTutar := int64(1_500)
	guncel, err := kurulum.svc.UpdateShippingOption(context.Background(), secenekID,
		service.UpdateOptionInput{Name: &yeniAd, Amount: &yeniTutar})
	require.NoError(t, err)
	assert.Equal(t, yeniAd, guncel.Name)
	assert.Equal(t, yeniTutar, guncel.Amount)
	assert.Equal(t, "sahte", guncel.ProviderID)
	assert.Equal(t, profilID, guncel.ShippingProfileID)
}

// TestHesaplananaCevrilenSecenekTutariSifirlanir tür değişiminde eski sabit
// tutarın kalmadığını kanıtlar.
//
// Kalsaydı şemadaki kısıt patlar ve istemci sebebini anlamayacağı bir hata
// alırdı.
func TestHesaplananaCevrilenSecenekTutariSifirlanir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})

	hesaplanan := "calculated"
	guncel, err := kurulum.svc.UpdateShippingOption(context.Background(), secenekID,
		service.UpdateOptionInput{PriceType: &hesaplanan})
	require.NoError(t, err)
	assert.Equal(t, models.PriceCalculated, guncel.PriceType)
	assert.Zero(t, guncel.Amount, "hesaplanan seçenekte satırdaki tutar sıfırlanmalı")
}

// TestKuralDogrulamasi tek değer bekleyen işlece iki değer verilemeyeceğini ve
// değersiz kuralın reddedildiğini kanıtlar.
//
// Sessizce yutulsaydı, yönetici koyduğunu sandığı koşulun çalışmadığını ancak
// siparişler yanlış aktığında görürdü.
func TestKuralDogrulamasi(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})

	durumlar := []struct {
		ad    string
		girdi service.CreateRuleInput
	}{
		{"alan yok", service.CreateRuleInput{Operator: "eq", Values: []string{"a"}}},
		{"işleç tanınmıyor", service.CreateRuleInput{
			Attribute: "region_id", Operator: "like", Values: []string{"a"},
		}},
		{"değer yok", service.CreateRuleInput{Attribute: "region_id", Operator: "eq"}},
		{"boş değer", service.CreateRuleInput{
			Attribute: "region_id", Operator: "eq", Values: []string{"  "},
		}},
		{"tek değerli işlece iki değer", service.CreateRuleInput{
			Attribute: "region_id", Operator: "eq", Values: []string{"a", "b"},
		}},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), secenekID, durum.girdi)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
		})
	}

	cokDegerli, err := kurulum.svc.CreateShippingOptionRule(context.Background(), secenekID,
		service.CreateRuleInput{Attribute: "region_id", Operator: "in", Values: []string{"a", "b"}})
	require.NoError(t, err, "in işleci birden çok değer almalı")
	assert.Len(t, cokDegerli.Values, 2)
}

// TestKuralSilinceSecenekKosulsuzlasir kuralın yumuşak silinmesinin uygunluk
// hesabından da düştüğünü kanıtlar.
func TestKuralSilinceSecenekKosulsuzlasir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Kısıtlı kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	kural, err := kurulum.svc.CreateShippingOptionRule(context.Background(), secenekID,
		service.CreateRuleInput{Attribute: "customer_group_id", Operator: "eq", Values: []string{"vip"}})
	require.NoError(t, err)

	oncesi, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Empty(t, oncesi, "kural eşleşmediği için seçenek sunulmamalı")

	require.NoError(t, kurulum.svc.DeleteShippingOptionRule(context.Background(), kural.ID))

	sonrasi, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, sonrasi, 1, "kural silinince seçenek koşulsuzlaşmalı")
}

// TestSilinenSecenekListelenmez yumuşak silmenin kataloğu ve uygunluk
// listesini etkilediğini kanıtlar.
func TestSilinenSecenekListelenmez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})

	require.NoError(t, kurulum.svc.DeleteShippingOption(context.Background(), secenekID))

	_, err := kurulum.svc.GetShippingOption(context.Background(), secenekID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata errors.NotFound olmalı: %v", err)

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, secenekler)
}

// TestSecenekKurallariylaOkunur GetShippingOption'ın kuralları iliştirdiğini
// kanıtlar.
func TestSecenekKurallariylaOkunur(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), secenekID,
		service.CreateRuleInput{Attribute: "region_id", Operator: "eq", Values: []string{"reg_tr"}})
	require.NoError(t, err)

	option, err := kurulum.svc.GetShippingOption(context.Background(), secenekID)
	require.NoError(t, err)
	require.Len(t, option.Rules, 1)
	assert.Equal(t, "region_id", option.Rules[0].Attribute)
}

// TestSayfalamaSinirlari limit doğrulamasını sınar.
func TestSayfalamaSinirlari(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	_, _, err := kurulum.svc.ListShippingProfiles(context.Background(), service.ListProfilesInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)

	_, _, err = kurulum.svc.ListShippingOptions(context.Background(), service.ListOptionsAdminInput{
		Page: service.Page{Offset: -1},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
}

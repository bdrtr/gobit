package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// interopSecenek yanıt şemasının test tarafındaki KOPYASIDIR.
//
// Bilinçli olarak servis paketindeki tipe değil, interop.go'nun godoc'unda
// BEYAN EDİLEN şemaya bağlıdır: tüketici modül de bu paketi import edemeyeceği
// için aynı şeyi yapacaktır (ADR 0006). Alan adı değişirse test düşer ve
// sessiz bir şema kayması yakalanır.
type interopSecenek struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Amount            int64  `json:"amount"`
	CurrencyCode      string `json:"currency_code"`
	PriceType         string `json:"price_type"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	IsReturn          bool   `json:"is_return"`
	AdminOnly         bool   `json:"admin_only"`
}

// interopYanit liste yanıtının test tarafındaki kopyasıdır.
type interopYanit struct {
	Options []interopSecenek `json:"options"`
}

// TestInteropListOptionsSemasi yayımlanan JSON şemasının belgelendiği gibi
// olduğunu kanıtlar.
func TestInteropListOptionsSemasi(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_500,
	})

	istek, err := json.Marshal(map[string]any{
		"region_id":            "reg_tr",
		"currency_code":        "TRY",
		"country_code":         "TR",
		"shipping_profile_ids": []string{profilID},
		"subtotal":             50_000,
		"item_count":           3,
		"total_weight":         1_500,
		"attributes":           map[string]string{"customer_group_id": "vip"},
		"include_admin_only":   false,
		"is_return":            false,
	})
	require.NoError(t, err)

	ham, err := interop.ListOptionsJSON(context.Background(), istek)
	require.NoError(t, err)

	var yanit interopYanit
	require.NoError(t, json.Unmarshal(ham, &yanit))
	require.Len(t, yanit.Options, 1)

	secenek := yanit.Options[0]
	assert.Equal(t, secenekID, secenek.ID)
	assert.Equal(t, "Standart kargo", secenek.Name)
	assert.Equal(t, int64(2_500), secenek.Amount)
	assert.Equal(t, "TRY", secenek.CurrencyCode)
	assert.Equal(t, "flat", secenek.PriceType)
	assert.Equal(t, "sahte", secenek.ProviderID)
	assert.Equal(t, profilID, secenek.ShippingProfileID)
	assert.False(t, secenek.IsReturn)
	assert.False(t, secenek.AdminOnly)

	assert.NotContains(t, string(ham), "\"data\"",
		"sağlayıcının ham verisi modüller arası yüzeye çıkmamalı")
}

// TestInteropAdminOnlyBayragiTasinir yönetim akışlarının admin_only
// seçenekleri isteyebildiğini, varsayılanın ise mağaza davranışı olduğunu
// kanıtlar.
func TestInteropAdminOnlyBayragiTasinir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)
	profilID := kurulum.profilAc(t, "varsayilan")
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Elden teslim",
		ShippingProfileID: profilID,
		AdminOnly:         true,
	})

	varsayilan, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY"}`))
	require.NoError(t, err)

	var bos interopYanit
	require.NoError(t, json.Unmarshal(varsayilan, &bos))
	assert.Empty(t, bos.Options, "varsayılan mağaza davranışı olmalı")

	yonetim, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","include_admin_only":true}`))
	require.NoError(t, err)

	var dolu interopYanit
	require.NoError(t, json.Unmarshal(yonetim, &dolu))
	require.Len(t, dolu.Options, 1)
	assert.True(t, dolu.Options[0].AdminOnly)
}

// TestInteropAraToplamEsigiTamSayiKarsilastirilir kuralın eşiğinde BİR
// KURUŞUN bile fark yarattığını kanıtlar.
//
// Karşılaştırma tam sayı üzerindedir: eşiğin bir kuruş altındaki bir sepet
// ücretsiz kargoyu AÇMAMALI, eşikteki ise açmalıdır. Dizge karşılaştırması ya
// da yuvarlanmış bir ara toplam bu sınırı kaydırırdı.
func TestInteropAraToplamEsigiTamSayiKarsilastirilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Ücretsiz kargo",
		ShippingProfileID: profilID,
	})
	_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), secenekID,
		service.CreateRuleInput{
			Attribute: service.AttrSubtotal,
			Operator:  "gte",
			Values:    []string{"50000"},
		})
	require.NoError(t, err)

	altinda, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","subtotal":49999}`))
	require.NoError(t, err)
	var bos interopYanit
	require.NoError(t, json.Unmarshal(altinda, &bos))
	assert.Empty(t, bos.Options, "eşiğin bir kuruş altı seçeneği açmamalı")

	esikte, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","subtotal":50000}`))
	require.NoError(t, err)
	var dolu interopYanit
	require.NoError(t, json.Unmarshal(esikte, &dolu))
	require.Len(t, dolu.Options, 1, "eşikte seçenek sunulmalı")
}

// TestInteropTaninmayanAlanReddedilir iki paketteki şemanın ayrışmasının ilk
// işaretinin yakalandığını kanıtlar.
func TestInteropTaninmayanAlanReddedilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	_, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","cart_id":"cart_1"}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
	assert.Equal(t, service.CodeInteropRequestInvalid, errors.CodeOf(err))
}

// TestInteropBosIstekReddedilir boş gövdenin sessizce boş liste dönmediğini
// kanıtlar.
func TestInteropBosIstekReddedilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	for _, gövde := range []json.RawMessage{nil, json.RawMessage("null")} {
		_, err := interop.ListOptionsJSON(context.Background(), gövde)
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
	}
}

// TestInteropOndalikliSayiReddedilir para ve adet alanlarının TAM SAYI
// olduğunu kanıtlar.
//
// json.Number yerine float64 üzerinden çözen bir uygulama aynı gövdeyi
// SESSİZCE 100'e kırpar ve ara toplam bir kuruş kaybederdi; bu test o yolun
// kapalı olduğunu sabitler.
func TestInteropOndalikliSayiReddedilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	_, err := interop.ListOptionsJSON(context.Background(),
		json.RawMessage(`{"currency_code":"TRY","subtotal":100.5}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
}

// TestInteropGonderiAcVeIptalEt saga yüzeyinin uçtan uca çalıştığını kanıtlar:
// aynı anahtarla iki çağrı TEK gönderi üretir, iptal İDEMPOTENTTİR ve durum
// okunabilir.
func TestInteropGonderiAcVeIptalEt(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)
	secenekID := hazirSecenek(t, kurulum)

	ilk, err := interop.CreateFulfillment(context.Background(), "order_1", secenekID, "anahtar-1")
	require.NoError(t, err)
	require.NotEmpty(t, ilk)

	ikinci, err := interop.CreateFulfillment(context.Background(), "order_1", secenekID, "anahtar-1")
	require.NoError(t, err)
	assert.Equal(t, ilk, ikinci, "aynı anahtar tek gönderi üretmeli")

	durum, err := interop.FulfillmentStatus(context.Background(), ilk)
	require.NoError(t, err)
	assert.Equal(t, "pending", durum)

	require.NoError(t, interop.CancelFulfillment(context.Background(), ilk))
	require.NoError(t, interop.CancelFulfillment(context.Background(), ilk),
		"telafi iki kez çağrılabilmeli")

	durum, err = interop.FulfillmentStatus(context.Background(), ilk)
	require.NoError(t, err)
	assert.Equal(t, "canceled", durum, "telafinin izi durumdan okunabilmeli")

	_, create, cancel := kurulum.provider.cagriSayilari()
	assert.Equal(t, 1, create, "sağlayıcıda tek gönderi açılmalı")
	assert.Equal(t, 1, cancel, "sağlayıcıya tek iptal gitmeli")
}

// TestInteropSelectLocationDeterministik aynı adaylarla yapılan ikinci
// çağrının aynı lokasyonu döndüğünü ve sonucun adayların GELİŞ SIRASINDAN
// bağımsız olduğunu kanıtlar.
//
// İddia saga içindir: sıraya bağlı bir seçim, yeniden denemede BAŞKA bir
// depodan ayırır ve ilk denemenin rezervasyonu yetim kalırdı.
func TestInteropSelectLocationDeterministik(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	ilk, err := interop.SelectLocation(context.Background(),
		[]string{"sloc_izmir", "sloc_ankara", "sloc_bursa"})
	require.NoError(t, err)

	ikinci, err := interop.SelectLocation(context.Background(),
		[]string{"sloc_izmir", "sloc_ankara", "sloc_bursa"})
	require.NoError(t, err)
	assert.Equal(t, ilk, ikinci, "aynı adaylar aynı lokasyonu vermeli")

	karisik, err := interop.SelectLocation(context.Background(),
		[]string{"sloc_bursa", "sloc_izmir", "sloc_ankara"})
	require.NoError(t, err)
	assert.Equal(t, ilk, karisik, "seçim adayların sırasına bağlı olmamalı")

	assert.Equal(t, "sloc_ankara", ilk, "kimliği en küçük aday seçilmeli")
}

// TestInteropSelectLocationTekAdayKendisi tek adaylı listede o adayın
// döndüğünü kanıtlar.
func TestInteropSelectLocationTekAdayKendisi(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	secilen, err := interop.SelectLocation(context.Background(), []string{"sloc_tek"})
	require.NoError(t, err)
	assert.Equal(t, "sloc_tek", secilen)
}

// TestInteropSelectLocationBosListeConflict boş aday listesinin SESSİZCE boş
// dize dönmediğini kanıtlar.
//
// Boş dize dönseydi çağıran onunla stok ayırmaya kalkar ve hata, sebebinden
// bir modül uzakta patlardı. Sınıf Conflict'tir: istekte düzeltilecek bir şey
// yoktur, hiçbir lokasyonda yeterli stok yoktur.
func TestInteropSelectLocationBosListeConflict(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	for _, adaylar := range [][]string{nil, {}} {
		secilen, err := interop.SelectLocation(context.Background(), adaylar)
		require.Error(t, err)
		assert.Empty(t, secilen)
		assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
		assert.Equal(t, service.CodeNoShippingLocation, errors.CodeOf(err))
	}
}

// TestInteropSelectLocationBosAdayReddedilir listedeki boş bir kimliğin
// seçilmediğini kanıtlar.
//
// "Kimliği en küçük aday" kuralı boş dizeyi seçerdi; test o yolun kapalı
// olduğunu sabitler.
func TestInteropSelectLocationBosAdayReddedilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	secilen, err := interop.SelectLocation(context.Background(),
		[]string{"sloc_ankara", "   "})
	require.Error(t, err)
	assert.Empty(t, secilen)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestInteropBilinmeyenGonderiIptaliNotFound telafinin var olmayan bir kaydı
// sessizce yutmadığını kanıtlar.
func TestInteropBilinmeyenGonderiIptaliNotFound(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	interop := service.NewInterop(kurulum.svc)

	err := interop.CancelFulfillment(context.Background(), "ful_YOKBOYLEBIRSEY")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata errors.NotFound olmalı: %v", err)
}

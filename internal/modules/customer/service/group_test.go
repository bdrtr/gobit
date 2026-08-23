package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// yeniGrup test için bir müşteri grubu açar.
func yeniGrup(ctx context.Context, t *testing.T, svc *Service, ad string) models.CustomerGroup {
	t.Helper()

	g, err := svc.CreateGroup(ctx, GroupInput{Name: ad})
	require.NoError(t, err)
	return g
}

// TestGrupAdiZorunluVeTekildir boş ve tekrarlanan grup adının reddedildiğini
// kanıtlar.
func TestGrupAdiZorunluVeTekildir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	_, err := svc.CreateGroup(ctx, GroupInput{Name: "   "})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = svc.CreateGroup(ctx, GroupInput{Name: "VIP"})
	require.NoError(t, err)

	_, err = svc.CreateGroup(ctx, GroupInput{Name: "VIP"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestGrupGuncellenebilir grubun adının ve metadata'sının düzeltilebildiğini
// kanıtlar.
//
// Ad canlı gruplar arasında benzersizdir; düzeltme yolu olmasaydı yanlış
// girilmiş bir ad o adı sonsuza dek işgal ederdi ve pricing'in segment bağlamı
// düzeltilemeyen bir kimliğe çakılı kalırdı.
func TestGrupGuncellenebilir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	grup := yeniGrup(ctx, t, svc, "VIPP")

	duzeltilmis := "  VIP  "
	guncel, err := svc.UpdateGroup(ctx, grup.ID, UpdateGroupInput{
		Name:     &duzeltilmis,
		Metadata: map[string]any{"indirim": "10"},
	})
	require.NoError(t, err)
	assert.Equal(t, "VIP", guncel.Name, "ad kırpılarak yazılmalı")
	assert.Equal(t, "10", guncel.Metadata["indirim"])

	// Verilmeyen alanlar OLDUĞU GİBİ kalır.
	guncel, err = svc.UpdateGroup(ctx, grup.ID, UpdateGroupInput{})
	require.NoError(t, err)
	assert.Equal(t, "VIP", guncel.Name, "verilmeyen ad korunmalı")
	assert.Equal(t, "10", guncel.Metadata["indirim"], "verilmeyen metadata korunmalı")

	// Ad VERİLİRSE boş olamaz; kısmi güncelleme bir zorunluluğu kaldıramaz.
	bos := "   "
	_, err = svc.UpdateGroup(ctx, grup.ID, UpdateGroupInput{Name: &bos})
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "boş ad reddedilmeli")

	// Başka bir canlı grubun adı alınamaz.
	digeri := yeniGrup(ctx, t, svc, "B2B")
	alinmis := "VIP"
	_, err = svc.UpdateGroup(ctx, digeri.ID, UpdateGroupInput{Name: &alinmis})
	assert.Equal(t, errors.KindConflict, errors.KindOf(err), "kullanılan ad çakışma vermeli")

	// Olmayan grup NotFound.
	_, err = svc.UpdateGroup(ctx, models.NewCustomerGroupID(sabitSaat), UpdateGroupInput{})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestSilinenGrupHicbirOkumadaGorunmez yumuşak silinen grubun her okuma
// yolundan düştüğünü kanıtlar.
//
// Üyelik satırları silinmez; görünmezliği sağlayan tek şey grup okuyan her
// sorgunun deleted_at IS NULL süzgecidir. Süzgeç düşerse silinmiş bir grup
// müşterinin segmentlerinde kalır ve fiyat hesabına taşınırdı.
func TestSilinenGrupHicbirOkumadaGorunmez(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	uye := yeniMusteri(ctx, t, svc, "segment@example.com")
	grup := yeniGrup(ctx, t, svc, "VIP")
	require.NoError(t, svc.AddToGroup(ctx, uye.ID, grup.ID))

	require.NoError(t, svc.DeleteGroup(ctx, grup.ID))

	_, err := svc.GetGroup(ctx, grup.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "silinen grup okunmamalı")

	sayfa, err := svc.ListGroups(ctx, 0, 0)
	require.NoError(t, err)
	assert.Zero(t, sayfa.Count, "silinen grup listede görünmemeli")

	gruplar, err := svc.ListGroupsOf(ctx, uye.ID)
	require.NoError(t, err)
	assert.Empty(t, gruplar, "silinen grup müşterinin gruplarında görünmemeli")

	kimlikler, err := svc.CustomerGroupIDs(ctx, uye.ID)
	require.NoError(t, err)
	assert.Empty(t, kimlikler, "silinen grup fiyat bağlamına taşınmamalı")

	page, err := svc.ListCustomers(ctx, ListCustomersInput{GroupID: &grup.ID})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "silinen grubun üyeleri süzgeçle listelenmemeli")

	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.AddToGroup(ctx, uye.ID, grup.ID)),
		"silinen gruba üye eklenememeli")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.DeleteGroup(ctx, grup.ID)),
		"silinen grup ikinci kez silinememeli")

	// Ad kısmi benzersiz indeksin kapsamından çıktığı için serbest kalır.
	_, err = svc.CreateGroup(ctx, GroupInput{Name: "VIP"})
	require.NoError(t, err, "silinen grubun adı yeniden kullanılabilmeli")
}

// TestGrubaEklemeIdempotenttir aynı üyeliğin iki kez eklenmesinin hata
// vermediğini kanıtlar.
//
// Üyelik bir KÜMEDİR; yeniden deneme ya da çift tıklama aynı sonucu vermelidir.
func TestGrubaEklemeIdempotenttir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	musteri := yeniMusteri(ctx, t, svc, "uye@example.com")
	grup := yeniGrup(ctx, t, svc, "VIP")

	require.NoError(t, svc.AddToGroup(ctx, musteri.ID, grup.ID))
	require.NoError(t, svc.AddToGroup(ctx, musteri.ID, grup.ID), "ikinci ekleme hata vermemeli")

	gruplar, err := svc.ListGroupsOf(ctx, musteri.ID)
	require.NoError(t, err)
	require.Len(t, gruplar, 1, "üyelik çoklanmamalı")
	assert.Equal(t, grup.ID, gruplar[0].ID)
}

// TestGruptanCikarmaIdempotentDegildir olmayan bir üyeliğin kaldırılmasının
// NotFound döndüğünü kanıtlar.
//
// Ekleme idempotent, çıkarma değildir: olmayan bir üyeliği kaldırmak istemcinin
// yanlış kimlikle çağırdığının en yaygın işaretidir ve sessizce başarı dönmek o
// hatayı gizlerdi.
func TestGruptanCikarmaIdempotentDegildir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	musteri := yeniMusteri(ctx, t, svc, "cikar@example.com")
	grup := yeniGrup(ctx, t, svc, "B2B")

	err := svc.RemoveFromGroup(ctx, musteri.ID, grup.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	require.NoError(t, svc.AddToGroup(ctx, musteri.ID, grup.ID))
	require.NoError(t, svc.RemoveFromGroup(ctx, musteri.ID, grup.ID))

	gruplar, err := svc.ListGroupsOf(ctx, musteri.ID)
	require.NoError(t, err)
	assert.Empty(t, gruplar)
}

// TestEksikTarafNotFound müşteri ya da grup yoksa NotFound döndüğünü kanıtlar.
func TestEksikTarafNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	musteri := yeniMusteri(ctx, t, svc, "eksik@example.com")
	grup := yeniGrup(ctx, t, svc, "Toptan")

	err := svc.AddToGroup(ctx, models.NewCustomerID(sabitSaat), grup.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "olmayan müşteri")

	err = svc.AddToGroup(ctx, musteri.ID, models.NewCustomerGroupID(sabitSaat))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "olmayan grup")

	_, err = svc.ListGroupsOf(ctx, models.NewCustomerID(sabitSaat))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"olmayan müşteri için boş liste değil NotFound dönmeli")
}

// TestGrupSuzgeciyleListeleme grup üyeliğine göre süzmeyi kanıtlar.
func TestGrupSuzgeciyleListeleme(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	uye := yeniMusteri(ctx, t, svc, "uye2@example.com")
	yeniMusteri(ctx, t, svc, "uyesiz@example.com")
	grup := yeniGrup(ctx, t, svc, "VIP")
	require.NoError(t, svc.AddToGroup(ctx, uye.ID, grup.ID))

	page, err := svc.ListCustomers(ctx, ListCustomersInput{GroupID: &grup.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Count)
	require.Len(t, page.Items, 1)
	assert.Equal(t, uye.ID, page.Items[0].ID)
}

// TestModullerArasiYuzey ilkel imzalı modüller arası metotları kanıtlar.
//
// İmzalar YALNIZCA ilkel tip kullanır; tüketici modül customer'ı import
// edemediği için ancak böyle bir imzayı kendi paketinde tekrarlayabilir
// (ADR 0001).
func TestModullerArasiYuzey(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	kimlik, err := svc.RegisterGuestCustomer(ctx, "Interop@Example.com", "Ali", "Veli", "555")
	require.NoError(t, err)
	assert.True(t, len(kimlik) > len(models.CustomerIDPrefix))

	misafir, err := svc.GetCustomer(ctx, kimlik)
	require.NoError(t, err)
	assert.False(t, misafir.HasAccount, "modüller arası kayıt da MİSAFİR açmalı")

	eposta, err := svc.CustomerEmail(ctx, kimlik)
	require.NoError(t, err)
	assert.Equal(t, "interop@example.com", eposta)

	kimlikler, err := svc.CustomerGroupIDs(ctx, kimlik)
	require.NoError(t, err)
	assert.NotNil(t, kimlikler, "grubu olmayan müşteri için boş dilim dönmeli")
	assert.Empty(t, kimlikler)

	grup := yeniGrup(ctx, t, svc, "VIP")
	require.NoError(t, svc.AddToGroup(ctx, kimlik, grup.ID))

	kimlikler, err = svc.CustomerGroupIDs(ctx, kimlik)
	require.NoError(t, err)
	assert.Equal(t, []string{grup.ID}, kimlikler)

	_, err = svc.CustomerEmail(ctx, models.NewCustomerID(sabitSaat))
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

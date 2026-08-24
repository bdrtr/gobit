package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestCreateTaxRegionUlkeKoku kök bölge oluşturmanın mutlu yolunu doğrular.
func TestCreateTaxRegionUlkeKoku(t *testing.T) {
	svc, _ := newTestService(t)

	region, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
		CountryCode: " tr ",
		Metadata:    map[string]any{"kaynak": "test"},
	})
	require.NoError(t, err)

	assert.Equal(t, "TR", region.CountryCode, "ülke kodu kırpılıp BÜYÜK harfe çevrilmeli")
	assert.True(t, region.IsRoot())
	assert.Nil(t, region.ProvinceCode)
	assert.True(t, strings.HasPrefix(region.ID, models.TaxRegionIDPrefix))
	assert.Len(t, region.ID, len(models.TaxRegionIDPrefix)+models.IDBodyLength())
	assert.Equal(t, testNow, region.CreatedAt)
	assert.Equal(t, map[string]any{"kaynak": "test"}, region.Metadata)
}

// TestCreateTaxRegionIkinciKokReddedilir ülke başına tek kök kuralını
// doğrular.
func TestCreateTaxRegionIkinciKokReddedilir(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "TR"})
	require.NoError(t, err)

	_, err = svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "tr"})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeRootExists, errors.CodeOf(err))
}

// TestCreateTaxRegionSaglayiciDogrulanir sağlayıcı kimliğinin bölge YAZILMADAN
// ÖNCE kayda karşı doğrulandığını gösterir.
//
// Doğrulanmayan bir kimliğin bedeli gecikmeli ve büyüktür: yazım hatası yazma
// anında değil, o ülkedeki İLK sepet hesabında KindInternal (500) olarak çıkar
// ve o ana kadar ülkedeki her sepet kapanmaz.
func TestCreateTaxRegionSaglayiciDogrulanir(t *testing.T) {
	t.Run("kayıtlı olmayan sağlayıcı yazılmadan reddedilir", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProviderID: "  bo yle bir saglayici yok  ",
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "yönetici yazım hatası 500 değil 422 olmalı")
		assert.Equal(t, CodeProviderNotFound, errors.CodeOf(err))
		assert.Contains(t, err.Error(), LocalProviderID, "mesaj kayıtlı kimlikleri yazmalı")
		assert.Zero(t, repo.callCount("CreateTaxRegion"), "hiçbir satır yazılmamalı")
	})

	t.Run("sınırsız sağlayıcı kimliği reddedilir", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProviderID: strings.Repeat("a", maxIDLen+1),
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Zero(t, repo.callCount("CreateTaxRegion"))
	})

	t.Run("kimlik kırpılarak saklanır", func(t *testing.T) {
		svc, _ := newTestService(t)

		region, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProviderID: "  " + LocalProviderID + "  ",
		})
		require.NoError(t, err)
		assert.Equal(t, LocalProviderID, region.ProviderID,
			"saklanan değer, hesapta uygulanan değerden AYRIŞMAMALI")
	})

	t.Run("boş kimlik serbesttir", func(t *testing.T) {
		svc, _ := newTestService(t)

		region, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "DE"})
		require.NoError(t, err)
		assert.Empty(t, region.ProviderID, "boş kimlik devralma/yerel demektir ve serbesttir")
	})
}

// TestCreateTaxRegionEyalet eyalet bölgesinin köke bağlandığını doğrular.
func TestCreateTaxRegionEyalet(t *testing.T) {
	svc, _ := newTestService(t)
	root, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)

	province, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
		CountryCode:  "US",
		ProvinceCode: "ca",
		ParentID:     root.ID,
	})
	require.NoError(t, err)

	assert.False(t, province.IsRoot())
	assert.Equal(t, "CA", province.Province(), "eyalet kodu BÜYÜK harfe çevrilmeli")
	assert.Equal(t, root.ID, province.Parent())
}

// TestCreateTaxRegionYarimHiyerarsiReddedilir ebeveyn/eyalet ikilisinin
// birlikte verilmesi şartını doğrular.
func TestCreateTaxRegionYarimHiyerarsiReddedilir(t *testing.T) {
	svc, _ := newTestService(t)
	root, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)

	t.Run("eyalet kodu var ebeveyn yok", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ProvinceCode: "CA",
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})

	t.Run("ebeveyn var eyalet kodu yok", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ParentID: root.ID,
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})
}

// TestCreateTaxRegionEbeveynDogrulanir kökün varlığı, türü ve ÜLKESİ
// denetimlerini doğrular.
func TestCreateTaxRegionEbeveynDogrulanir(t *testing.T) {
	svc, _ := newTestService(t)
	usRoot, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)
	province, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
		CountryCode: "US", ProvinceCode: "CA", ParentID: usRoot.ID,
	})
	require.NoError(t, err)

	t.Run("ebeveyn yok", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ProvinceCode: "NY",
			ParentID: models.TaxRegionIDPrefix + "YOK000000000000000000000000",
		})
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
	})

	t.Run("ebeveyn eyalet olamaz", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "US", ProvinceCode: "NY", ParentID: province.ID,
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Equal(t, CodeParentInvalid, errors.CodeOf(err))
	})

	t.Run("ebeveynin ülkesi farklı olamaz", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProvinceCode: "BY", ParentID: usRoot.ID,
		})
		require.Error(t, err)
		assert.Equal(t, CodeParentInvalid, errors.CodeOf(err))
	})

	t.Run("ebeveyn kimliği yanlış türde olamaz", func(t *testing.T) {
		_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
			CountryCode: "DE", ProvinceCode: "BY", ParentID: rateA,
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err), "önek denetimi 404 değil doğrulama hatası vermeli")
	})
}

// TestDeleteTaxRegionAgaciKapsar silmenin alt bölgeleri, oranları ve
// kuralları da kapsadığını doğrular.
func TestDeleteTaxRegionAgaciKapsar(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
	repo.seedDefaultRate(rateA, usRegionID, 2000)
	repo.seedRuledRate(rateB, trIstanbul, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	require.NoError(t, svc.DeleteTaxRegion(context.Background(), usRegionID))

	_, err := svc.GetTaxRegion(context.Background(), usRegionID)
	assert.True(t, errors.IsNotFound(err))
	_, err = svc.GetTaxRegion(context.Background(), trIstanbul)
	assert.True(t, errors.IsNotFound(err), "alt bölge de silinmeli")
	_, err = svc.GetTaxRate(context.Background(), rateA)
	assert.True(t, errors.IsNotFound(err), "kök bölgenin oranı da silinmeli")
	_, err = svc.GetTaxRate(context.Background(), rateB)
	assert.True(t, errors.IsNotFound(err), "alt bölgenin oranı da silinmeli")

	rules, err := repo.ListTaxRateRules(context.Background(), rateB)
	require.NoError(t, err)
	assert.Empty(t, rules, "oranın kuralları da silinmeli")

	// Silme sonrası aynı ülkeye yeni bir kök açılabilmelidir; aksi hâlde
	// silme, ülkeyi kalıcı olarak yapılandırılamaz bırakırdı.
	_, err = svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{CountryCode: "US"})
	require.NoError(t, err)
}

// TestDeleteTaxRegionOlmayanKayit silinmiş/bulunmayan bölgede NotFound
// döndüğünü doğrular.
func TestDeleteTaxRegionOlmayanKayit(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.DeleteTaxRegion(context.Background(), models.TaxRegionIDPrefix+"YOK")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestListTaxRegionsSuzerVeSayfalar süzgeç ve sayfalama sözleşmesini
// doğrular.
func TestListTaxRegionsSuzerVeSayfalar(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "TR", "34", trRegionID)

	all, err := svc.ListTaxRegions(context.Background(), "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), all.Count)
	assert.Equal(t, DefaultLimit, all.Limit, "limit verilmezse varsayılan uygulanmalı")

	tr, err := svc.ListTaxRegions(context.Background(), "tr", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), tr.Count, "ülke süzgeci küçük harfle de çalışmalı")

	capped, err := svc.ListTaxRegions(context.Background(), "", MaxLimit+1000, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, capped.Limit, "kırpılan limit sonuçta bildirilmeli")

	_, err = svc.ListTaxRegions(context.Background(), "TUR", 0, 0)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "biçimsiz süzgeç sessizce yok sayılmamalı")

	_, err = svc.ListTaxRegions(context.Background(), "", 0, -1)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestCreateTaxRateMutluYol oran oluşturmayı doğrular.
func TestCreateTaxRateMutluYol(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	rate, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID,
		Name:        "  KDV  ",
		Code:        " KDV20 ",
		RateBps:     2000,
		IsDefault:   true,
	})
	require.NoError(t, err)

	assert.Equal(t, "KDV", rate.Name, "ad kırpılmalı")
	assert.Equal(t, "KDV20", rate.RateCode())
	assert.True(t, strings.HasPrefix(rate.ID, models.TaxRateIDPrefix))

	percent, remainder := rate.RatePercent()
	assert.Equal(t, int32(20), percent)
	assert.Equal(t, int32(0), remainder)
}

// TestCreateTaxRateIkinciVarsayilanReddedilir bölge başına tek varsayılan
// kuralını doğrular.
func TestCreateTaxRateIkinciVarsayilanReddedilir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "İkinci", RateBps: 1000, IsDefault: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeDefaultExists, errors.CodeOf(err))

	// Varsayılan OLMAYAN ikinci oran serbesttir.
	_, err = svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "İndirimli", RateBps: 100,
	})
	require.NoError(t, err)
}

// TestCreateTaxRateGecersizGirdi oran doğrulamalarını doğrular.
func TestCreateTaxRateGecersizGirdi(t *testing.T) {
	tests := map[string]CreateTaxRateInput{
		"bölge kimliği boş":          {Name: "KDV", RateBps: 100},
		"bölge kimliği yanlış türde": {TaxRegionID: rateA, Name: "KDV", RateBps: 100},
		"ad boş":                     {TaxRegionID: trRegionID, RateBps: 100},
		"ad kontrol karakteri":       {TaxRegionID: trRegionID, Name: "KDV\nyeni", RateBps: 100},
		"kod boşluklu":               {TaxRegionID: trRegionID, Name: "KDV", Code: "KDV 20", RateBps: 100},
		"oran negatif":               {TaxRegionID: trRegionID, Name: "KDV", RateBps: -1},
		"oran yüzde yüzü aşar":       {TaxRegionID: trRegionID, Name: "KDV", RateBps: models.MaxRateBps + 1},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")

			_, err := svc.CreateTaxRate(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata: %v", err)
			assert.Zero(t, repo.callCount("CreateTaxRate"), "geçersiz girdi depoya ulaşmamalı")
		})
	}
}

// TestCreateTaxRateBolgeYoksa bölgesi olmayan oranın reddedildiğini doğrular.
func TestCreateTaxRateBolgeYoksa(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "KDV", RateBps: 2000,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestUpdateTaxRateKismidir yamanın yalnızca verilen alanlara dokunduğunu
// doğrular.
func TestUpdateTaxRateKismidir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	code := "KDV20"
	repo.seedRate(models.TaxRate{
		ID: rateA, TaxRegionID: trRegionID, Name: "KDV", Code: &code,
		RateBps: 2000, IsDefault: true, Metadata: map[string]any{"a": "b"},
	})

	yeniOran := int32(1800)
	updated, err := svc.UpdateTaxRate(context.Background(), rateA, UpdateTaxRateInput{RateBps: &yeniOran})
	require.NoError(t, err)

	assert.Equal(t, int32(1800), updated.RateBps)
	assert.Equal(t, "KDV", updated.Name, "dokunulmayan ad değişmemeli")
	assert.Equal(t, "KDV20", updated.RateCode(), "dokunulmayan kod değişmemeli")
	assert.True(t, updated.IsDefault, "dokunulmayan bayrak değişmemeli")
	assert.Equal(t, map[string]any{"a": "b"}, updated.Metadata)
}

// TestUpdateTaxRateKodKaldirilir boş dizenin kodu SİLDİĞİNİ doğrular.
func TestUpdateTaxRateKodKaldirilir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	code := "KDV20"
	repo.seedRate(models.TaxRate{ID: rateA, TaxRegionID: trRegionID, Name: "KDV", Code: &code, RateBps: 2000})

	bos := ""
	updated, err := svc.UpdateTaxRate(context.Background(), rateA, UpdateTaxRateInput{Code: &bos})
	require.NoError(t, err)
	assert.Nil(t, updated.Code)
	assert.Empty(t, updated.RateCode())
}

// TestUpdateTaxRateBosYamaReddedilir sessiz başarıya izin verilmediğini
// doğrular.
func TestUpdateTaxRateBosYamaReddedilir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.UpdateTaxRate(context.Background(), rateA, UpdateTaxRateInput{})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, repo.callCount("UpdateTaxRate"))
}

// TestUpdateTaxRateKuralliOranVarsayilanYapilamaz kapsam çakışmasının
// engellendiğini doğrular.
func TestUpdateTaxRateKuralliOranVarsayilanYapilamaz(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	varsayilan := true
	_, err := svc.UpdateTaxRate(context.Background(), rateB, UpdateTaxRateInput{IsDefault: &varsayilan})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestUpdateTaxRateIkinciVarsayilanYapilamaz güncelleme yolunun da tekillik
// kısıtına tabi olduğunu doğrular.
func TestUpdateTaxRateIkinciVarsayilanYapilamaz(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)

	varsayilan := true
	_, err := svc.UpdateTaxRate(context.Background(), rateB, UpdateTaxRateInput{IsDefault: &varsayilan})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestDeleteTaxRateKurallariDaSiler oranla birlikte kurallarının silindiğini
// doğrular.
func TestDeleteTaxRateKurallariDaSiler(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	require.NoError(t, svc.DeleteTaxRate(context.Background(), rateB))

	rules, err := repo.ListTaxRateRules(context.Background(), rateB)
	require.NoError(t, err)
	assert.Empty(t, rules)

	err = svc.DeleteTaxRate(context.Background(), rateB)
	require.Error(t, err, "ikinci silme NotFound dönmeli")
	assert.True(t, errors.IsNotFound(err))
}

// TestCreateRateRuleMutluYol kural oluşturmayı doğrular.
func TestCreateRateRuleMutluYol(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)

	rule, err := svc.CreateRateRule(context.Background(), CreateRateRuleInput{
		TaxRateID:   rateB,
		Reference:   "product_type",
		ReferenceID: "ptyp_gida",
	})
	require.NoError(t, err)

	assert.Equal(t, models.ReferenceProductType, rule.Reference)
	assert.Equal(t, "ptyp_gida", rule.ReferenceID)
	assert.True(t, strings.HasPrefix(rule.ID, models.TaxRateRuleIDPrefix))
}

// TestCreateRateRuleVarsayilanOranaEklenemez kapsam kuralını doğrular.
func TestCreateRateRuleVarsayilanOranaEklenemez(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.CreateRateRule(context.Background(), CreateRateRuleInput{
		TaxRateID: rateA, Reference: "product", ReferenceID: "prod_1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestCreateRateRuleGecersizGirdi kural doğrulamalarını doğrular.
func TestCreateRateRuleGecersizGirdi(t *testing.T) {
	tests := map[string]CreateRateRuleInput{
		"oran kimliği boş":          {Reference: "product", ReferenceID: "prod_1"},
		"oran kimliği yanlış tür":   {TaxRateID: trRegionID, Reference: "product", ReferenceID: "prod_1"},
		"referans tanımsız":         {TaxRateID: rateB, Reference: "variant", ReferenceID: "var_1"},
		"referans boş":              {TaxRateID: rateB, ReferenceID: "prod_1"},
		"referans kimliği boş":      {TaxRateID: rateB, Reference: "product"},
		"referans kimliği boşluklu": {TaxRateID: rateB, Reference: "product", ReferenceID: " prod_1"},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")
			repo.seedRuledRate(rateB, trRegionID, 100)

			_, err := svc.CreateRateRule(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata: %v", err)
			assert.Zero(t, repo.callCount("CreateTaxRateRule"))
		})
	}
}

// TestDeleteRateRuleOranVarsayilanYapmaz son kuralın silinmesinin oranı
// SESSİZCE genişletmediğini doğrular.
func TestDeleteRateRuleOranVarsayilanYapmaz(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

	require.NoError(t, svc.DeleteRateRule(context.Background(), ruleA))

	rate, err := svc.GetTaxRate(context.Background(), rateB)
	require.NoError(t, err)
	assert.False(t, rate.IsDefault, "kuralsız kalan oran varsayılan OLMAMALI")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 10_000}},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.TaxTotal, "kuralsız kalan oran hiçbir kaleme uygulanmamalı")
}

// TestListRateRulesOranYoksa bulunmayan oranın NotFound döndüğünü doğrular.
func TestListRateRulesOranYoksa(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.ListRateRules(context.Background(), rateB)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestKurulmamisServisPanikUretmez depo verilmeden kurulan servisin tipli
// hata döndüğünü doğrular.
func TestKurulmamisServisPanikUretmez(t *testing.T) {
	svc := New(nil, Options{})

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{CountryCode: "TR"})
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))

	_, err = svc.GetTaxRegion(context.Background(), trRegionID)
	require.Error(t, err)

	_, _, err = svc.DefaultRateForCountry(context.Background(), "TR")
	require.Error(t, err)
}

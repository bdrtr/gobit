package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

func TestKurulmamisServisTipliHataDoner(t *testing.T) {
	var svc *Service

	_, err := svc.ComputeDiscounts(context.Background(), ComputeInput{CurrencyCode: "TRY"})

	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))
}

func TestCreatePromotionKoduBuyukHarfeCevirir(t *testing.T) {
	repo := newMemRepo()

	promo, err := newTestService(repo).CreatePromotion(context.Background(), PromotionInput{
		Code: " yaz-20 ",
	})
	require.NoError(t, err)

	assert.Equal(t, "YAZ-20", promo.Code, "kupon kodu büyük harfe normalleştirilerek saklanır")
	assert.Equal(t, models.PromotionDraft, promo.Status,
		"durum verilmezse TASLAK olur; eksik bir istek kazara yayına girmemeli")
	assert.Equal(t, models.PromotionStandard, promo.Type)
	assert.Equal(t, map[string]string{}, promo.Metadata)
}

func TestCreatePromotionAyniKodIkinciKezAlinamaz(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	_, err := svc.CreatePromotion(context.Background(), PromotionInput{Code: "YAZ20"})
	require.NoError(t, err)

	_, err = svc.CreatePromotion(context.Background(), PromotionInput{Code: "yaz20"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"kod büyük/küçük harften bağımsız BENZERSİZDİR")
}

func TestBuygetPromosyonuEtkinlestirilemez(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	_, err := svc.CreatePromotion(context.Background(), PromotionInput{
		Code: "BUYGET", Type: models.PromotionBuyGet, Status: models.PromotionActive,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, CodeBuyGetNotActivatable, errors.CodeOf(err),
		"mekanik yokken aktif buyget, hiçbir şey yapmayan bir promosyon bırakırdı")

	taslak, err := svc.CreatePromotion(context.Background(), PromotionInput{
		Code: "BUYGET", Type: models.PromotionBuyGet,
	})
	require.NoError(t, err, "buyget promosyonu TASLAK olarak hazırlanabilir")
	assert.Equal(t, models.PromotionBuyGet, taslak.Type)
}

func TestPromotionGirdiDogrulamasi(t *testing.T) {
	uzunDeger := strings.Repeat("x", MaxMetadataValueLen+1)

	testler := []struct {
		ad      string
		in      PromotionInput
		gerekce string
	}{
		{ad: "kısa kod", in: PromotionInput{Code: "AB"}, gerekce: "kod en az üç karakter"},
		{ad: "boşluklu kod", in: PromotionInput{Code: "YAZ 20"}, gerekce: "kod boşluk içeremez"},
		{ad: "uzun kod", in: PromotionInput{Code: strings.Repeat("A", MaxCodeLen+1)}, gerekce: "kod sınırı"},
		{
			ad:      "tanımsız tür",
			in:      PromotionInput{Code: "YAZ20", Type: "olmayan"},
			gerekce: "tanımsız tür reddedilir",
		},
		{
			ad:      "tanımsız durum",
			in:      PromotionInput{Code: "YAZ20", Status: "olmayan"},
			gerekce: "tanımsız durum reddedilir",
		},
		{
			ad:      "yanlış önekli campaign id",
			in:      PromotionInput{Code: "YAZ20", CampaignID: ptr("promo_yanlis")},
			gerekce: "önek kontrolü yanlış türde kimliği yakalar",
		},
		{
			ad:      "negatif kullanım sınırı",
			in:      PromotionInput{Code: "YAZ20", UsageLimit: ptr(int64(-1))},
			gerekce: "negatif sınır anlamsızdır",
		},
		{
			ad:      "uzun üstveri değeri",
			in:      PromotionInput{Code: "YAZ20", Metadata: map[string]string{"not": uzunDeger}},
			gerekce: "üstveri değeri sınırı",
		},
		{
			ad:      "boş üstveri anahtarı",
			in:      PromotionInput{Code: "YAZ20", Metadata: map[string]string{"": "x"}},
			gerekce: "boş anahtar anlamsızdır",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			_, err := newTestService(newMemRepo()).CreatePromotion(context.Background(), tt.in)
			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.gerekce)
		})
	}
}

func TestUpdatePromotionKullanimSayaciniKorur(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	promo, err := svc.CreatePromotion(context.Background(), PromotionInput{Code: "YAZ20"})
	require.NoError(t, err)

	kayit := repo.promotions[promo.ID]
	kayit.UsageCount = 7
	repo.promotions[promo.ID] = kayit

	guncel, err := svc.UpdatePromotion(context.Background(), promo.ID, PromotionInput{
		Code: "KIS20", Status: models.PromotionActive,
	})
	require.NoError(t, err)

	assert.Equal(t, "KIS20", guncel.Code)
	assert.Equal(t, int64(7), guncel.UsageCount,
		"sayacı yalnızca kullanım akışı yazar; yönetim güncellemesi onu sıfırlayamaz")
}

func TestUpdatePromotionYerineKoymadir(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	promo, err := svc.CreatePromotion(context.Background(), PromotionInput{
		Code: "YAZ20", CampaignID: ptr("camp_1"), UsageLimit: ptr(int64(5)),
	})
	require.NoError(t, err)
	require.NotNil(t, promo.CampaignID)

	guncel, err := svc.UpdatePromotion(context.Background(), promo.ID, PromotionInput{Code: "YAZ20"})
	require.NoError(t, err)

	assert.Nil(t, guncel.CampaignID, "verilmeyen alan SIFIRLANIR; kısmi güncelleme değildir")
	assert.Nil(t, guncel.UsageLimit)
}

func TestCampaignButceDogrulamasi(t *testing.T) {
	temel := CampaignInput{Name: "Yaz", CampaignIdentifier: "YAZ-2026"}

	testler := []struct {
		ad       string
		degistir func(in *CampaignInput)
		gerekce  string
	}{
		{
			ad:       "bütçesizde sınır verilemez",
			degistir: func(in *CampaignInput) { in.BudgetLimit = ptr(int64(100)) },
			gerekce:  "önce bütçe türü seçilmeli",
		},
		{
			ad: "bütçesizde para birimi verilemez",
			degistir: func(in *CampaignInput) {
				in.BudgetCurrencyCode = "TRY"
			},
			gerekce: "bütçesiz kampanyanın para birimi yoktur",
		},
		{
			ad: "spend bütçesi sınır ister",
			degistir: func(in *CampaignInput) {
				in.BudgetType = models.BudgetSpend
				in.BudgetCurrencyCode = "TRY"
			},
			gerekce: "sınırsız bütçe için tür 'none' olmalı",
		},
		{
			ad: "spend bütçesi para birimi ister",
			degistir: func(in *CampaignInput) {
				in.BudgetType = models.BudgetSpend
				in.BudgetLimit = ptr(int64(100))
			},
			gerekce: "para ölçülü bütçe para birimi olmadan anlamsızdır",
		},
		{
			ad: "usage bütçesinde para birimi verilemez",
			degistir: func(in *CampaignInput) {
				in.BudgetType = models.BudgetUsage
				in.BudgetLimit = ptr(int64(100))
				in.BudgetCurrencyCode = "TRY"
			},
			gerekce: "adet ölçülü bütçenin para birimi yoktur",
		},
		{
			ad: "negatif sınır",
			degistir: func(in *CampaignInput) {
				in.BudgetType = models.BudgetUsage
				in.BudgetLimit = ptr(int64(-1))
			},
			gerekce: "negatif bütçe anlamsızdır",
		},
		{
			ad: "sınır azami tutarı aşamaz",
			degistir: func(in *CampaignInput) {
				in.BudgetType = models.BudgetUsage
				in.BudgetLimit = ptr(models.MaxAmount + 1)
			},
			gerekce: "taşma koruması",
		},
		{
			ad:       "boş ad",
			degistir: func(in *CampaignInput) { in.Name = "  " },
			gerekce:  "ad boş olamaz",
		},
		{
			ad: "başlangıç bitişten sonra",
			degistir: func(in *CampaignInput) {
				in.StartsAt = ptr(testNow.Add(time.Hour))
				in.EndsAt = ptr(testNow)
			},
			gerekce: "ters pencere anlamsızdır",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			in := temel
			tt.degistir(&in)

			_, err := newTestService(newMemRepo()).CreateCampaign(context.Background(), in)
			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.gerekce)
		})
	}
}

func TestCreateCampaignGecerliButce(t *testing.T) {
	repo := newMemRepo()

	campaign, err := newTestService(repo).CreateCampaign(context.Background(), CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: "YAZ-2026",
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(100_000)),
		BudgetCurrencyCode: "try",
	})
	require.NoError(t, err)

	assert.Equal(t, "TRY", campaign.BudgetCurrencyCode, "para birimi büyük harfe normalleştirilir")
	assert.Zero(t, campaign.BudgetUsed, "sayaç sıfırdan başlar ve girdiden okunmaz")
}

func TestCreateCampaignIsKimligiBenzersizdir(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)
	in := CampaignInput{Name: "Yaz", CampaignIdentifier: "YAZ-2026"}

	_, err := svc.CreateCampaign(context.Background(), in)
	require.NoError(t, err)

	_, err = svc.CreateCampaign(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

func TestSetApplicationMethodDogrulamasi(t *testing.T) {
	testler := []struct {
		ad      string
		in      ApplicationMethodInput
		gerekce string
	}{
		{
			ad:      "tanımsız tür",
			in:      ApplicationMethodInput{Type: "olmayan", TargetType: models.TargetItems},
			gerekce: "tanımsız tür reddedilir",
		},
		{
			ad:      "tanımsız hedef",
			in:      ApplicationMethodInput{Type: models.MethodPercentage, TargetType: "olmayan"},
			gerekce: "tanımsız hedef reddedilir",
		},
		{
			ad: "sabit indirimde para birimi zorunlu",
			in: ApplicationMethodInput{
				Type: models.MethodFixed, TargetType: models.TargetItems, Value: 100,
			},
			gerekce: "sabit tutar para birimsiz uygulanamaz",
		},
		{
			ad: "yüzde indirimde para birimi verilemez",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems,
				Value: 2000, CurrencyCode: "TRY",
			},
			gerekce: "yüzde para birimi taşımaz",
		},
		{
			ad: "yüzde %100'ü aşamaz",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 10001,
			},
			gerekce: "baz puan üst sınırı 10000'dir",
		},
		{
			ad: "negatif değer",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems, Value: -1,
			},
			gerekce: "negatif indirim anlamsızdır",
		},
		{
			ad: "sipariş hedefinde each reddedilir",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetOrder,
				Allocation: models.AllocationEach, Value: 1000,
			},
			gerekce: "sessiz düzeltme, operatörün yanılgısını sürdürürdü",
		},
		{
			ad: "azami adet sınırı",
			in: ApplicationMethodInput{
				Type: models.MethodFixed, TargetType: models.TargetItems,
				Value: 100, CurrencyCode: "TRY", MaxQuantity: ptr(models.MaxQuantity + 1),
			},
			gerekce: "adet üst sınırı aşılamaz",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := newMemRepo()
			repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

			_, err := newTestService(repo).SetApplicationMethod(context.Background(), "promo_1", tt.in)
			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.gerekce)
		})
	}
}

func TestSetApplicationMethodSiparisHedefindeAcrossaZorlanir(t *testing.T) {
	repo := newMemRepo()
	repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

	method, err := newTestService(repo).SetApplicationMethod(context.Background(), "promo_1",
		ApplicationMethodInput{
			Type: models.MethodPercentage, TargetType: models.TargetOrder, Value: 1000,
		})
	require.NoError(t, err)

	assert.Equal(t, models.AllocationAcross, method.Allocation,
		"tahsis verilmediğinde sipariş hedefi across'a zorlanır")
}

func TestSetApplicationMethodOlmayanPromosyonNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).SetApplicationMethod(context.Background(), "promo_yok",
		ApplicationMethodInput{Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 1000})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"foreign key ihlali 'kısıt hatası' olarak giderdi; erken kontrol ne olduğunu söyler")
}

func TestAddPromotionRuleDogrulamasi(t *testing.T) {
	testler := []struct {
		ad      string
		in      RuleInput
		gerekce string
	}{
		{
			ad:      "tanımsız kural türü",
			in:      RuleInput{RuleType: "olmayan", Attribute: "a", Operator: models.OpEq, Values: []string{"x"}},
			gerekce: "tanımsız tür reddedilir",
		},
		{
			ad:      "boş alan adı",
			in:      RuleInput{RuleType: models.RuleContext, Operator: models.OpEq, Values: []string{"x"}},
			gerekce: "alan adı boş olamaz",
		},
		{
			ad:      "tanımsız işleç",
			in:      RuleInput{RuleType: models.RuleContext, Attribute: "a", Operator: "olmayan", Values: []string{"x"}},
			gerekce: "tanımsız işleç reddedilir",
		},
		{
			ad:      "değersiz kural",
			in:      RuleInput{RuleType: models.RuleContext, Attribute: "a", Operator: models.OpEq},
			gerekce: "kural en az bir değer ister",
		},
		{
			ad: "tek değerli işlece iki değer",
			in: RuleInput{
				RuleType: models.RuleContext, Attribute: "a", Operator: models.OpEq,
				Values: []string{"x", "y"},
			},
			gerekce: "eq tam bir değer alır",
		},
		{
			ad: "sayısal işlece sayı olmayan değer",
			in: RuleInput{
				RuleType: models.RuleContext, Attribute: "a", Operator: models.OpGt,
				Values: []string{"abc"},
			},
			gerekce: "sayıya çevrilemeyen kural sessizce ölü kalırdı",
		},
		{
			ad: "boş değer",
			in: RuleInput{
				RuleType: models.RuleContext, Attribute: "a", Operator: models.OpIn,
				Values: []string{"x", ""},
			},
			gerekce: "boş değer anlamsızdır",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := newMemRepo()
			repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

			_, err := newTestService(repo).AddPromotionRule(context.Background(), "promo_1", tt.in)
			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.gerekce)
		})
	}
}

func TestAddPromotionRuleDegerleriKopyalar(t *testing.T) {
	repo := newMemRepo()
	repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

	degerler := []string{"vip", "b2b"}
	rule, err := newTestService(repo).AddPromotionRule(context.Background(), "promo_1", RuleInput{
		RuleType: models.RuleContext, Attribute: "customer_group_id",
		Operator: models.OpIn, Values: degerler,
	})
	require.NoError(t, err)

	degerler[0] = "degistirildi"
	assert.Equal(t, []string{"vip", "b2b"}, rule.Values,
		"çağıranın dilimini sonradan değiştirmek yazılmış kuralı bozmamalı")
}

func TestListPromotionRulesOlmayanPromosyonNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).ListPromotionRules(context.Background(), "promo_yok")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"boş dilim dönseydi istemci 'kuralı yok' sanardı")
}

func TestSayfalamaSinirlariUygulanir(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)
	for i := range 5 {
		_, err := svc.CreateCampaign(context.Background(), CampaignInput{
			Name: "K", CampaignIdentifier: string(rune('A' + i)),
		})
		require.NoError(t, err)
	}

	page, err := svc.ListCampaigns(context.Background(), 0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, page.Limit, "limit verilmezse varsayılan uygulanır")
	assert.Equal(t, int64(5), page.Count)

	page, err = svc.ListCampaigns(context.Background(), MaxLimit+50, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, page.Limit, "azami sayfa boyu aşılamaz ve UYGULANAN değer bildirilir")

	_, err = svc.ListCampaigns(context.Background(), 10, -1)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

func TestLookupStoreCouponYalnizcaKullanilabilirKuponuDoner(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YAZ20"},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	coupon, err := newTestService(repo).LookupStoreCoupon(context.Background(), "yaz20")
	require.NoError(t, err)

	assert.Equal(t, "YAZ20", coupon.Code)
	assert.Equal(t, models.MethodPercentage, coupon.MethodType)
	assert.Equal(t, int64(2000), coupon.Value)
	assert.Empty(t, coupon.CurrencyCode)
}

func TestLookupStoreCouponSizdirmaz(t *testing.T) {
	kapaliKampanya := models.Campaign{
		ID: "camp_1", Name: "Bitmiş", CampaignIdentifier: "BITMIS",
		BudgetType: models.BudgetNone, EndsAt: ptr(testNow.Add(-time.Hour)),
	}
	tukenmisKampanya := models.Campaign{
		ID: "camp_2", Name: "Tükenmiş", CampaignIdentifier: "TUKENMIS",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(1)), BudgetUsed: 1,
	}

	testler := []struct {
		ad      string
		hazirla func(repo *memRepo)
		kod     string
		gerekce string
	}{
		{
			ad:      "var olmayan kod",
			hazirla: func(*memRepo) {},
			kod:     "HICYOK",
			gerekce: "olmayan kod bulunamadı döner",
		},
		{
			ad: "taslak promosyon",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TASLAK", Status: models.PromotionDraft,
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			kod:     "TASLAK",
			gerekce: "taslak kupon müşteriye VAR görünmemeli",
		},
		{
			ad: "pasif promosyon",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "PASIF", Status: models.PromotionInactive,
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			kod:     "PASIF",
			gerekce: "pasif kupon müşteriye VAR görünmemeli",
		},
		{
			ad: "kampanyası kapanmış",
			hazirla: func(repo *memRepo) {
				repo.campaigns[kapaliKampanya.ID] = kapaliKampanya
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "KAPALI", CampaignID: ptr(kapaliKampanya.ID),
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			kod:     "KAPALI",
			gerekce: "kampanya takvimi ele verilmemeli",
		},
		{
			ad: "bütçesi tükenmiş",
			hazirla: func(repo *memRepo) {
				repo.campaigns[tukenmisKampanya.ID] = tukenmisKampanya
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TUKENMIS", CampaignID: ptr(tukenmisKampanya.ID),
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			kod:     "TUKENMIS",
			gerekce: "bütçe durumu ele verilmemeli",
		},
		{
			ad: "kullanım hakkı bitmiş",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "BITTI", UsageLimit: ptr(int64(1)), UsageCount: 1,
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			kod:     "BITTI",
			gerekce: "kullanım sayacı ele verilmemeli",
		},
		{
			ad: "uygulama yöntemi yok",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YONTEMSIZ"}, nil)
			},
			kod:     "YONTEMSIZ",
			gerekce: "yöntemsiz kupon indirim üretmez",
		},
		{
			ad:      "biçimsel olarak geçersiz kod",
			hazirla: func(*memRepo) {},
			kod:     "a b",
			gerekce: "biçim hatası da 'yok' sayılır; biçim doğrulaması arama alanını daraltırdı",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := newMemRepo()
			tt.hazirla(repo)

			_, err := newTestService(repo).LookupStoreCoupon(context.Background(), tt.kod)

			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindNotFound, errors.KindOf(err), tt.gerekce)
			assert.Equal(t, CodePromotionNotUsable, errors.CodeOf(err),
				"tüm sebepler AYNI kodu dönmeli; ayrım sızıntı olurdu")
		})
	}
}

func TestGetPromotionByCodeYonetimTaslagiGorur(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "TASLAK", Status: models.PromotionDraft,
	}, nil)

	promo, err := newTestService(repo).GetPromotionByCode(context.Background(), "taslak")
	require.NoError(t, err)

	assert.Equal(t, models.PromotionDraft, promo.Status,
		"operatör taslak promosyonu görebilmeli; süzgeç yalnızca müşteri yüzeyindedir")
}

// TestLookupStoreCouponAcikKampanyaliKuponuDoner [storeCandidate]'in kampanya
// okumasının OLUMLU yönünü pinler.
//
// Diğer store testleri yalnızca RET yönünü sınar (kapalı kampanya, tükenmiş
// bütçe -> 404). Okumayı tamamen silen bir değişiklik candidate.Campaign'i nil
// bırakır, [campaignUsable] false döner ve kampanyaya bağlı HER kupon müşteriye
// "yok" görünürdü — tek bir RET testi bunu yakalayamaz.
func TestLookupStoreCouponAcikKampanyaliKuponuDoner(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		StartsAt:   ptr(testNow.Add(-time.Hour)),
		EndsAt:     ptr(testNow.Add(time.Hour)),
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetUsed: 25_000, BudgetCurrencyCode: "TRY",
	}
	repo := newMemRepo()
	repo.campaigns[campaign.ID] = campaign
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "YAZ20", CampaignID: ptr(campaign.ID),
	}, fixedMethod("promo_1", 1500, models.TargetItems, models.AllocationEach))

	coupon, err := newTestService(repo).LookupStoreCoupon(context.Background(), "yaz20")
	require.NoError(t, err, "penceresi açık ve bütçesi kalan kampanyanın kuponu müşteriye GÖRÜNÜR")

	assert.Equal(t, "YAZ20", coupon.Code)
	assert.Equal(t, models.MethodFixed, coupon.MethodType)
	assert.Equal(t, models.TargetItems, coupon.TargetType)
	assert.Equal(t, int64(1500), coupon.Value)
	assert.Equal(t, "TRY", coupon.CurrencyCode)
}

// TestUpdateCampaignSayacDoluykenButceBirimiDegistirilemez sayacın eski birimde
// kalmasından doğan sessiz muhasebe bozulmasını pinler (bkz.
// [Service.UpdateCampaign]).
func TestUpdateCampaignSayacDoluykenButceBirimiDegistirilemez(t *testing.T) {
	adetKampanyasi := models.Campaign{
		ID: "camp_1", Name: "Adet", CampaignIdentifier: "ADET",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(100)), BudgetUsed: 30,
	}
	paraKampanyasi := models.Campaign{
		ID: "camp_1", Name: "Para", CampaignIdentifier: "PARA",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetUsed: 30_000, BudgetCurrencyCode: "TRY",
	}

	testler := []struct {
		ad      string
		mevcut  models.Campaign
		istek   CampaignInput
		gerekce string
	}{
		{
			ad:     "tür adetten paraya",
			mevcut: adetKampanyasi,
			istek: CampaignInput{
				Name: "Adet", CampaignIdentifier: "ADET",
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
				BudgetCurrencyCode: "TRY",
			},
			gerekce: "sayaçtaki 30 ADET, tür değişince 30 KURUŞ olarak okunurdu",
		},
		{
			ad:     "para birimi değişiyor",
			mevcut: paraKampanyasi,
			istek: CampaignInput{
				Name: "Para", CampaignIdentifier: "PARA",
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
				BudgetCurrencyCode: "USD",
			},
			gerekce: "önceki TRY harcaması USD sayılır ve TRY kullanımları reddedilmeye başlardı",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := newMemRepo()
			repo.campaigns[tt.mevcut.ID] = tt.mevcut

			_, err := newTestService(repo).UpdateCampaign(context.Background(), tt.mevcut.ID, tt.istek)

			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err), tt.gerekce)
			assert.Equal(t, repository.CodeBudgetUnitLocked, errors.CodeOf(err))
			assert.Equal(t, tt.mevcut.BudgetType, repo.campaigns[tt.mevcut.ID].BudgetType,
				"reddedilen güncelleme hiçbir alanı değiştirmez")
			assert.Equal(t, tt.mevcut.BudgetCurrencyCode, repo.campaigns[tt.mevcut.ID].BudgetCurrencyCode)
		})
	}
}

// TestUpdateCampaignSayacDoluykenTanimGuncellenebilir kilidin DAR olduğunu
// pinler: donan yalnızca bütçenin birimidir, kampanyanın tanımı değil.
func TestUpdateCampaignSayacDoluykenTanimGuncellenebilir(t *testing.T) {
	repo := newMemRepo()
	repo.campaigns["camp_1"] = models.Campaign{
		ID: "camp_1", Name: "Eski", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetUsed: 30_000, BudgetCurrencyCode: "TRY",
	}

	campaign, err := newTestService(repo).UpdateCampaign(context.Background(), "camp_1", CampaignInput{
		Name: "Yeni", CampaignIdentifier: "YAZ", Description: "güncellendi",
		EndsAt:     ptr(testNow.Add(48 * time.Hour)),
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(250_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err, "ad, açıklama, pencere ve bütçe SINIRI sayaçtan bağımsızdır")

	assert.Equal(t, "Yeni", campaign.Name)
	assert.Equal(t, int64(250_000), *campaign.BudgetLimit)
	assert.Equal(t, int64(30_000), campaign.BudgetUsed, "sayaç bu yoldan değişmez")
}

// TestUpdateCampaignSayacSifirkenButceBirimiDegistirilebilir kilidin yalnızca
// sayaç doluyken bağladığını pinler; hiç kullanılmamış bir kampanya serbestçe
// yeniden tanımlanabilmelidir.
func TestUpdateCampaignSayacSifirkenButceBirimiDegistirilebilir(t *testing.T) {
	repo := newMemRepo()
	repo.campaigns["camp_1"] = models.Campaign{
		ID: "camp_1", Name: "Adet", CampaignIdentifier: "ADET",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(100)),
	}

	campaign, err := newTestService(repo).UpdateCampaign(context.Background(), "camp_1", CampaignInput{
		Name: "Para", CampaignIdentifier: "ADET",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, models.BudgetSpend, campaign.BudgetType)
	assert.Equal(t, "TRY", campaign.BudgetCurrencyCode)
}

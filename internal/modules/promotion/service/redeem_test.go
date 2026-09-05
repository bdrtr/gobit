package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

// kuponluDepo sayaç testleri için bir promosyon (ve isteğe bağlı kampanya)
// hazırlanmış bir depo üretir.
func kuponluDepo(campaign *models.Campaign, usageLimit *int64) *memRepo {
	repo := newMemRepo()
	promo := models.Promotion{
		ID: "promo_1", Code: "YAZ20", Status: models.PromotionActive,
		Type: models.PromotionStandard, UsageLimit: usageLimit,
	}
	if campaign != nil {
		repo.campaigns[campaign.ID] = *campaign
		promo.CampaignID = ptr(campaign.ID)
	}
	seedPromotion(repo, promo, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
	return repo
}

func TestRedeemPromotionSayaciArtirir(t *testing.T) {
	repo := kuponluDepo(nil, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "try",
	})
	require.NoError(t, err)

	assert.Equal(t, "order_1", redemption.Reference)
	assert.Equal(t, int64(2500), redemption.Amount)
	assert.Equal(t, "TRY", redemption.CurrencyCode, "para birimi büyük harfe normalleştirilir")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)
}

func TestRedeemPromotionIdempotenttir(t *testing.T) {
	repo := kuponluDepo(nil, nil)
	svc := newTestService(repo)
	in := RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY"}

	ilk, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err)

	ikinci, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err, "aynı referansla ikinci çağrı hata VERMEZ")

	assert.Equal(t, ilk.ID, ikinci.ID, "ikinci çağrı var olan kaydı döner")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount,
		"saga bir adımı yeniden çalıştırabilir; sayaç ikinci kez artmamalı")
}

func TestRedeemPromotionKullanimSinirinaTakilir(t *testing.T) {
	repo := kuponluDepo(nil, ptr(int64(1)))
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	_, err = svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_2", Amount: 100, CurrencyCode: "TRY",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeUsageLimitReached, errors.CodeOf(err))
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount, "reddedilen kullanım sayacı artırmaz")
}

func TestRedeemPromotionParaOlculuButceyiTuketir(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := kuponluDepo(&campaign, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2500), redemption.BudgetDelta, "para ölçülü bütçe TUTAR kadar tükenir")
	assert.Equal(t, int64(2500), repo.campaigns["camp_1"].BudgetUsed)
}

func TestRedeemPromotionAdetOlculuButceyiBirTuketir(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(3)),
	}
	repo := kuponluDepo(&campaign, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), redemption.BudgetDelta, "adet ölçülü bütçe kullanım başına BİR tükenir")
	assert.Equal(t, int64(1), repo.campaigns["camp_1"].BudgetUsed)
}

func TestRedeemPromotionButceAsilirsaReddedilir(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(1000)),
		BudgetUsed: 900, BudgetCurrencyCode: "TRY",
	}
	repo := kuponluDepo(&campaign, nil)

	_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 500, CurrencyCode: "TRY",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeBudgetExceeded, errors.CodeOf(err))
	assert.Equal(t, int64(900), repo.campaigns["camp_1"].BudgetUsed, "reddedilen kullanım bütçeyi değiştirmez")
	assert.Zero(t, repo.promotions["promo_1"].UsageCount, "hiçbir sayaç yarım kalmaz")
}

func TestRedeemPromotionButceParaBirimiUyusmalidir(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := kuponluDepo(&campaign, nil)

	_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "USD",
	})

	require.Error(t, err)
	assert.Equal(t, repository.CodeBudgetCurrencyMismatch, errors.CodeOf(err),
		"iki para birimi aynı sayaçta toplanamaz")
}

func TestReleasePromotionSayaclariGeriAlir(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := kuponluDepo(&campaign, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	released, err := svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	assert.True(t, released, "telafi gerçekten iş yapmalı")
	assert.Zero(t, repo.promotions["promo_1"].UsageCount)
	assert.Zero(t, repo.campaigns["camp_1"].BudgetUsed, "bütçe TAHMİN edilmez, defterdeki pay düşülür")
}

func TestReleasePromotionIdempotenttir(t *testing.T) {
	repo := kuponluDepo(nil, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	in := ReleaseInput{PromotionID: "promo_1", Reference: "order_1"}
	ilk, err := svc.ReleasePromotion(context.Background(), in)
	require.NoError(t, err)
	require.True(t, ilk)

	ikinci, err := svc.ReleasePromotion(context.Background(), in)
	require.NoError(t, err, "telafi tekrar çalıştırılabilir olmalı (plan Bölüm 5.5)")

	assert.False(t, ikinci, "ikinci çağrı bir şey geri almaz")
	assert.Zero(t, repo.promotions["promo_1"].UsageCount, "sayaç İKİNCİ kez düşmemeli")
}

func TestReleasePromotionHicKullanimYoksaHataVermez(t *testing.T) {
	repo := kuponluDepo(nil, nil)

	released, err := newTestService(repo).ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_yok",
	})

	require.NoError(t, err, "yazmadan patlamış bir adımın telafisi de çalışabilmeli")
	assert.False(t, released)
}

func TestReleasePromotionOlmayanPromosyonNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_yok", Reference: "order_1",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"yanlış kimlikle çağrılmış bir telafi sessizce yutulmamalı")
}

func TestSerbestBirakilanReferansYenidenKullanilabilir(t *testing.T) {
	repo := kuponluDepo(nil, nil)
	svc := newTestService(repo)
	redeem := RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY"}

	ilk, err := svc.RedeemPromotion(context.Background(), redeem)
	require.NoError(t, err)

	_, err = svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	ikinci, err := svc.RedeemPromotion(context.Background(), redeem)
	require.NoError(t, err)

	assert.NotEqual(t, ilk.ID, ikinci.ID, "bırakılan referans yeniden kullanılabilir")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)
}

func TestRedeemPromotionKodlaCozulebilir(t *testing.T) {
	repo := kuponluDepo(nil, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		Code: "yaz20", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, "promo_1", redemption.PromotionID)
}

func TestRedeemPromotionKimlikVeKodCakisirsaReddedilir(t *testing.T) {
	repo := kuponluDepo(nil, nil)

	_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Code: "BASKAKOD", Reference: "order_1",
		Amount: 100, CurrencyCode: "TRY",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
		"iki farklı promosyonu adlandıran bir istekte sayaç yanlış yere yazılırdı")
}

func TestRedeemPromotionGirdiDogrulamasi(t *testing.T) {
	testler := []struct {
		ad      string
		in      RedeemInput
		gerekce string
	}{
		{
			ad:      "promosyon adlandırılmamış",
			in:      RedeemInput{Reference: "order_1", Amount: 100, CurrencyCode: "TRY"},
			gerekce: "kimlik ya da kod verilmeli",
		},
		{
			ad:      "referans boş",
			in:      RedeemInput{PromotionID: "promo_1", Amount: 100, CurrencyCode: "TRY"},
			gerekce: "the idempotency key is required",
		},
		{
			ad:      "negatif tutar",
			in:      RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: -1, CurrencyCode: "TRY"},
			gerekce: "negatif indirim anlamsızdır",
		},
		{
			ad:      "para birimi yok",
			in:      RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 100},
			gerekce: "para birimi zorunludur",
		},
		{
			ad: "tutar azami sınırı aşıyor",
			in: RedeemInput{
				PromotionID: "promo_1", Reference: "order_1",
				Amount: models.MaxAmount + 1, CurrencyCode: "TRY",
			},
			gerekce: "taşma koruması",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := kuponluDepo(nil, nil)

			_, err := newTestService(repo).RedeemPromotion(context.Background(), tt.in)
			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.gerekce)
		})
	}
}

func TestListRedemptionsOlmayanPromosyonNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).ListRedemptions(context.Background(), "promo_yok", 10, 0)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

func TestListRedemptionsBirakilanlariDaDoner(t *testing.T) {
	repo := kuponluDepo(nil, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	_, err = svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	page, err := svc.ListRedemptions(context.Background(), "promo_1", 10, 0)
	require.NoError(t, err)

	require.Len(t, page.Items, 1)
	assert.True(t, page.Items[0].Released(), "defter bir geçmiştir; geri alınmış kullanımın izi silinmez")
}

// TestRedeemPromotionYayindaOlmayanPromosyonReddedilir taslak ve pasif
// promosyonların kullanılamadığını pinler.
//
// Denetim olmasaydı yayına HİÇ alınmamış bir promosyonun sayacı artar,
// kampanyasının bütçesi sessizce yenirdi; hata yönetim yüzeyinde ancak defter
// okunarak fark edilirdi. Yüzey hem /admin/v1/promotions/{id}/redeem hem de
// "promotion.interop" üzerinden çağırana açıktır.
func TestRedeemPromotionYayindaOlmayanPromosyonReddedilir(t *testing.T) {
	for _, durum := range []models.PromotionStatus{models.PromotionDraft, models.PromotionInactive} {
		t.Run(string(durum), func(t *testing.T) {
			campaign := models.Campaign{
				ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
			}
			repo := kuponluDepo(&campaign, nil)
			promo := repo.promotions["promo_1"]
			promo.Status = durum
			repo.promotions["promo_1"] = promo

			_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
				PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
			})

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, repository.CodePromotionNotActive, errors.CodeOf(err))
			assert.Zero(t, repo.promotions["promo_1"].UsageCount, "reddedilen kullanım sayacı artırmaz")
			assert.Zero(t, repo.campaigns["camp_1"].BudgetUsed,
				"yayına alınmamış bir promosyon kampanya bütçesini YEMEZ")
		})
	}
}

// TestRedeemPromotionKampanyaPenceresiKapaliysaReddedilir kullanım ANININ
// kampanyanın tarih penceresinde olmasını pinler.
//
// Pencere hesapta zaten eleniyor ([Service.ComputeDiscounts]); ama hesap yan
// etkisizdir ve sepetle sipariş tamamlama arasında pencere kapanabilir. Sayaca
// yazma anının hakemi burasıdır.
func TestRedeemPromotionKampanyaPenceresiKapaliysaReddedilir(t *testing.T) {
	testler := []struct {
		ad       string
		campaign models.Campaign
	}{
		{
			ad: "pencere kapanmış",
			campaign: models.Campaign{
				ID: "camp_1", Name: "Bitmiş", CampaignIdentifier: "BITMIS",
				BudgetType: models.BudgetNone, EndsAt: ptr(testNow.Add(-time.Hour)),
			},
		},
		{
			ad: "pencere henüz açılmamış",
			campaign: models.Campaign{
				ID: "camp_1", Name: "Gelecek", CampaignIdentifier: "GELECEK",
				BudgetType: models.BudgetNone, StartsAt: ptr(testNow.Add(time.Hour)),
			},
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := kuponluDepo(&tt.campaign, nil)

			_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
				PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
			})

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, repository.CodeCampaignWindowClosed, errors.CodeOf(err))
			assert.Zero(t, repo.promotions["promo_1"].UsageCount, "reddedilen kullanım sayacı artırmaz")
		})
	}
}

// TestRedeemPromotionAcikPencereliKampanyaKullanilabilir denetimin RET yönünü
// değil OLUMLU yönünü pinler: penceresi açık bir kampanyanın promosyonu normal
// biçimde kullanılabilmelidir.
func TestRedeemPromotionAcikPencereliKampanyaKullanilabilir(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		StartsAt:   ptr(testNow.Add(-time.Hour)),
		EndsAt:     ptr(testNow.Add(time.Hour)),
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := kuponluDepo(&campaign, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2500), redemption.BudgetDelta)
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)
}

// TestRedeemPromotionIdempotencyDurumDenetimindenOnceGelir sıranın bilinçli
// olduğunu pinler: kullanımı yazıldıktan SONRA durdurulan bir promosyonun saga
// adımı yeniden çalıştırılabilmelidir.
//
// Sıra ters olsaydı telafi edilemeyen bir sipariş kalırdı: adım hata döner ama
// kullanım defterde durur.
func TestRedeemPromotionIdempotencyDurumDenetimindenOnceGelir(t *testing.T) {
	repo := kuponluDepo(nil, nil)
	svc := newTestService(repo)
	in := RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY"}

	ilk, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err)

	promo := repo.promotions["promo_1"]
	promo.Status = models.PromotionInactive
	repo.promotions["promo_1"] = promo

	ikinci, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err, "yazılmış bir kullanımın tekrarı, promosyon durdurulmuş olsa da okunabilmeli")

	assert.Equal(t, ilk.ID, ikinci.ID)
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount, "sayaç ikinci kez artmaz")
}

// TestReleasePromotionDurdurulmusPromosyonuDaGeriAlir telafinin hiçbir uygunluk
// denetimi yapmadığını pinler: durdurulmuş bir promosyonun kullanımı da geri
// alınabilmelidir, aksi hâlde saga telafisi tıkanırdı.
func TestReleasePromotionDurdurulmusPromosyonuDaGeriAlir(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := kuponluDepo(&campaign, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := repo.promotions["promo_1"]
	promo.Status = models.PromotionInactive
	repo.promotions["promo_1"] = promo

	released, err := svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	assert.True(t, released, "durdurulmuş promosyonun kullanımı da telafi edilebilmeli")
	assert.Zero(t, repo.campaigns["camp_1"].BudgetUsed)
}

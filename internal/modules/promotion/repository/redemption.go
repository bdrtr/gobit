package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// Redeem promosyonu bir referans için kullanır ve sayaçları artırır.
//
// req'ten YALNIZCA ID, PromotionID, Reference, Amount ve CurrencyCode okunur;
// CampaignID, BudgetDelta ve zaman damgaları YOK SAYILIR çünkü onlar kilit
// altında okunan kampanyadan türetilir. Çağıranın gönderdiği bir bütçe payı
// kabul edilseydi, defterle sayacın ayrışması istemcinin elinde olurdu.
//
// İkinci dönüş değeri kaydın BU ÇAĞRIDA oluşturulup oluşturulmadığını bildirir;
// false ise kullanım zaten vardı ve hiçbir sayaç değişmedi.
//
// # Neden tek işlem ve iki kilit
//
// Adımlar tek bir işlemde ve HER ZAMAN aynı kilit sırasıyla (önce promosyon,
// sonra kampanya) yürür:
//
//  1. Promosyon satırı FOR UPDATE ile kilitlenir. Bu, aynı promosyonun tüm
//     eşzamanlı kullanımlarını SERİ hâle getirir; "önce oku sonra yaz" yarışı
//     oluşamaz.
//  2. Aynı referans için GEÇERLİ bir kullanım varsa o kayıt dönür ve sayaçlara
//     DOKUNULMAZ — idempotency budur. Kilit altında bakıldığı için iki
//     eşzamanlı çağrıdan yalnızca biri kaydı oluşturur.
//  3. Promosyonun DURUMU denetlenir; yayında değilse errors.Conflict döner.
//  4. Kampanya varsa satırı kilitlenir; tarih penceresi kullanım anını
//     kapsamalıdır ve bütçe para biriminde ölçülüyorsa kullanımın para
//     birimiyle eşleşmesi ŞARTTIR (yoksa iki para birimi aynı sayaçta
//     toplanırdı).
//  5. Sayaçlar KOŞULLU UPDATE ile artırılır: sınır aşılacaksa satır
//     güncellenmez ve işlem errors.Conflict ile geri alınır.
//  6. Defter satırı yazılır; sayaç ile defter ya birlikte yazılır ya hiç
//     yazılmaz.
//
// Kilit sırasının sabit olması zorunludur: aynı kampanyaya bağlı iki promosyon
// eşzamanlı kullanıldığında ikisi de aynı kampanya satırını ister ve ters sıra
// kilitlenme (deadlock) demektir.
//
// # Uygunluk denetimi İDEMPOTENCY'DEN SONRA gelir
//
// Durum ve pencere denetimleri BİLEREK 2. adımdan sonradır. Sıra ters olsaydı,
// kullanımı yazıldıktan sonra durdurulan bir promosyonun saga adımı yeniden
// çalıştığında hata dönerdi; oysa o kullanım zaten defterdedir ve tekrar,
// yalnızca var olan kaydın okunması demektir. Telafi (bkz. [Repo.Release])
// aynı sebeple hiçbir uygunluk denetimi yapmaz: durdurulmuş bir promosyonun
// kullanımı da geri alınabilmelidir.
func (r *Repo) Redeem(ctx context.Context, req models.Redemption, now time.Time) (models.Redemption, bool, error) {
	var (
		out     models.Redemption
		created bool
	)

	err := r.inTx(ctx, func(q *promotiondb.Queries) error {
		promoRow, err := q.LockPromotion(ctx, req.PromotionID)
		if err != nil {
			return notFoundOr(err, CodePromotionNotFound, "promosyon bulunamadı: %s", req.PromotionID)
		}
		promo := toPromotion(promoRow)

		existing, err := q.GetActiveRedemption(ctx, promotiondb.GetActiveRedemptionParams{
			PromotionID: req.PromotionID,
			Reference:   req.Reference,
		})
		switch {
		case err == nil:
			out, created = toRedemption(existing), false
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return wrapDB(err, "kullanım kaydı okunamadı: %s/%s", req.PromotionID, req.Reference)
		}

		if promo.Status != models.PromotionActive {
			return errors.Conflict(CodePromotionNotActive,
				"promosyon yayında değil: %s (durum: %s)", req.PromotionID, promo.Status)
		}

		delta, campaignID, err := lockBudget(ctx, q, promo, req, now)
		if err != nil {
			return err
		}

		if _, err := q.IncrementPromotionUsage(ctx, promotiondb.IncrementPromotionUsageParams{
			ID:  req.PromotionID,
			Now: fromTime(now),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.Conflict(CodeUsageLimitReached,
					"promosyonun kullanım hakkı bitti: %s", req.PromotionID)
			}
			return wrapDB(err, "kullanım sayacı artırılamadı: %s", req.PromotionID)
		}

		if delta > 0 && campaignID != nil {
			if _, err := q.IncrementCampaignBudget(ctx, promotiondb.IncrementCampaignBudgetParams{
				ID:    *campaignID,
				Delta: delta,
				Now:   fromTime(now),
			}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errors.Conflict(CodeBudgetExceeded,
						"kampanya bütçesi yetmiyor: %s (istenen: %d)", *campaignID, delta)
				}
				return wrapDB(err, "kampanya bütçesi artırılamadı: %s", *campaignID)
			}
		}

		row, err := q.InsertRedemption(ctx, promotiondb.InsertRedemptionParams{
			ID:           req.ID,
			PromotionID:  req.PromotionID,
			CampaignID:   campaignID,
			Reference:    req.Reference,
			Amount:       req.Amount,
			CurrencyCode: req.CurrencyCode,
			BudgetDelta:  delta,
			CreatedAt:    fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "kullanım kaydı yazılamadı: %s/%s", req.PromotionID, req.Reference)
		}

		out, created = toRedemption(row), true
		return nil
	})
	if err != nil {
		return models.Redemption{}, false, err
	}
	return out, created, nil
}

// lockBudget kampanyayı kilitler, kullanım anında kullanılabilir olduğunu
// doğrular ve bütçeden ne kadar tüketeceğini hesaplar.
//
// Kampanyasız promosyonda sıfır ve nil döner. Kampanya kimliği dolu ama satır
// bulunamıyorsa kampanya işlem sırasında SİLİNMİŞTİR ve çakışma dönülür:
// silinmiş bir kampanyanın bütçesine yazmak, defteri sahipsiz bırakırdı.
func lockBudget(
	ctx context.Context,
	q *promotiondb.Queries,
	promo models.Promotion,
	req models.Redemption,
	now time.Time,
) (delta int64, campaignID *string, err error) {
	if promo.CampaignID == nil {
		return 0, nil, nil
	}

	campRow, err := q.LockCampaign(ctx, *promo.CampaignID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, campaignGone(*promo.CampaignID)
		}
		return 0, nil, wrapDB(err, "kampanya kilitlenemedi: %s", *promo.CampaignID)
	}
	campaign := toCampaign(campRow)

	// Penceresi kapalı bir kampanyanın bütçesi yenemez. Denetim kilidin ARDINDAN
	// yapılır ki pencere ile sayaç aynı anın kaydı olsun; bütçe sınırının hakemi
	// zaten burasıdır ve pencerenin başka bir yere bırakılması, hesapta elenen
	// ama kullanımda kabul edilen bir promosyon bırakırdı.
	if !campaign.WindowContains(now) {
		return 0, nil, errors.Conflict(CodeCampaignWindowClosed,
			"kampanyanın tarih penceresi kullanım anını kapsamıyor: %s", campaign.ID)
	}

	// Para birimi ölçülü bir bütçe yalnızca KENDİ para biriminde tüketilebilir.
	// Aksi hâlde 100 TRY ile 100 USD aynı sayaçta toplanır ve bütçe anlamını
	// yitirirdi.
	if campaign.BudgetType == models.BudgetSpend && campaign.BudgetCurrencyCode != req.CurrencyCode {
		return 0, nil, errors.Conflict(CodeBudgetCurrencyMismatch,
			"kampanya bütçesi %s para biriminde; kullanım %s para biriminde geldi (kampanya: %s)",
			campaign.BudgetCurrencyCode, req.CurrencyCode, campaign.ID)
	}

	id := campaign.ID
	return campaign.BudgetDeltaFor(req.Amount), &id, nil
}

// Release bir kullanımı serbest bırakır ve sayaçları geri alır.
//
// İkinci dönüş değeri BU ÇAĞRIDA bir şeyin geri alınıp alınmadığını bildirir.
//
// # İDEMPOTENTTİR
//
// Saga telafisi olarak çağrıldığı için tekrar çalıştırılabilir olmak zorundadır
// (plan Bölüm 5.5). İki savunma vardır:
//
//   - Satır kilidi: geçerli kullanım FOR UPDATE ile kilitlenir, iki eşzamanlı
//     Release'ten yalnızca biri satırı bırakabilir.
//   - Koşullu UPDATE: işaretleme yalnızca released_at IS NULL iken yazar.
//
// Hiç kullanım YOKSA (ya da zaten bırakılmışsa) çağrı hata VERMEZ: telafi,
// yazılmadan patlamış bir adımın ardından da çalışabilmelidir. Promosyonun
// KENDİSİ yoksa errors.NotFound döner — bu, telafinin sessizce yutmaması
// gereken bir kurulum hatasıdır.
//
// Kampanya bu arada silinmişse bütçe düşümü ATLANIR ve serbest bırakma yine de
// tamamlanır: bir kampanyanın silinmesi, telafiyi düşürmek için yeterli bir
// sebep değildir.
func (r *Repo) Release(
	ctx context.Context,
	promotionID, reference string,
	now time.Time,
) (models.Redemption, bool, error) {
	var (
		out      models.Redemption
		released bool
	)

	err := r.inTx(ctx, func(q *promotiondb.Queries) error {
		if _, err := q.LockPromotion(ctx, promotionID); err != nil {
			return notFoundOr(err, CodePromotionNotFound, "promosyon bulunamadı: %s", promotionID)
		}

		locked, err := q.LockActiveRedemption(ctx, promotiondb.LockActiveRedemptionParams{
			PromotionID: promotionID,
			Reference:   reference,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Geri alınacak bir şey yok; telafi zaten çalışmış ya da hiç
				// kullanım yazılmamış demektir.
				return nil
			}
			return wrapDB(err, "kullanım kaydı kilitlenemedi: %s/%s", promotionID, reference)
		}
		redemption := toRedemption(locked)

		if redemption.CampaignID != nil && redemption.BudgetDelta > 0 {
			if _, err := q.DecrementCampaignBudget(ctx, promotiondb.DecrementCampaignBudgetParams{
				ID:    *redemption.CampaignID,
				Delta: redemption.BudgetDelta,
				Now:   fromTime(now),
			}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return wrapDB(err, "kampanya bütçesi düşürülemedi: %s", *redemption.CampaignID)
			}
		}

		if _, err := q.DecrementPromotionUsage(ctx, promotiondb.DecrementPromotionUsageParams{
			ID:  promotionID,
			Now: fromTime(now),
		}); err != nil {
			return wrapDB(err, "kullanım sayacı düşürülemedi: %s", promotionID)
		}

		row, err := q.MarkRedemptionReleased(ctx, promotiondb.MarkRedemptionReleasedParams{
			ID:         redemption.ID,
			ReleasedAt: fromTime(now),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Kayıt, biz kilidi tutarken bırakılmış demektir — satır kilidi
				// altında bu OLAMAZ. Buraya düşmek kilidin çalışmadığını
				// gösterir ve durum sessizce yutulmaz; ama sınıf Conflict'tir
				// (Internal değil), çünkü istek geçerliydi ve yeniden
				// denenebilir. Sayaçlar bu dalda işlemle birlikte geri alınır.
				return errors.Conflict(CodeRedemptionRaced,
					"kullanım kaydı bırakılırken başka bir çağrı araya girdi: %s", redemption.ID)
			}
			return wrapDB(err, "kullanım kaydı serbest bırakılamadı: %s", redemption.ID)
		}

		out, released = toRedemption(row), true
		return nil
	})
	if err != nil {
		return models.Redemption{}, false, err
	}
	return out, released, nil
}

// GetRedemption bir referansın GEÇERLİ kullanımını döner; yoksa errors.NotFound.
func (r *Repo) GetRedemption(ctx context.Context, promotionID, reference string) (models.Redemption, error) {
	if err := r.ready(); err != nil {
		return models.Redemption{}, err
	}

	row, err := r.q.GetActiveRedemption(ctx, promotiondb.GetActiveRedemptionParams{
		PromotionID: promotionID,
		Reference:   reference,
	})
	if err != nil {
		return models.Redemption{}, notFoundOr(err, CodePromotionNotFound,
			"kullanım kaydı bulunamadı: %s/%s", promotionID, reference)
	}
	return toRedemption(row), nil
}

// ListRedemptions bir promosyonun kullanım defterini sayfalanmış olarak döner.
//
// Serbest bırakılmış kayıtlar da DÖNER: defter bir geçmiştir ve geri alınmış
// bir kullanımın izi silinmemelidir.
func (r *Repo) ListRedemptions(
	ctx context.Context,
	promotionID string,
	limit, offset int32,
) ([]models.Redemption, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListRedemptions(ctx, promotiondb.ListRedemptionsParams{
		PromotionID: promotionID,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "kullanım defteri okunamadı: %s", promotionID)
	}
	total, err := r.q.CountRedemptions(ctx, promotionID)
	if err != nil {
		return nil, 0, wrapDB(err, "kullanım sayısı alınamadı: %s", promotionID)
	}

	out := make([]models.Redemption, 0, len(rows))
	for i := range rows {
		out = append(out, toRedemption(rows[i]))
	}
	return out, total, nil
}

// toRedemption üretilen satırı domain modeline çevirir.
func toRedemption(row promotiondb.PromotionRedemption) models.Redemption {
	return models.Redemption{
		ID:           row.ID,
		PromotionID:  row.PromotionID,
		CampaignID:   copyString(row.CampaignID),
		Reference:    row.Reference,
		Amount:       row.Amount,
		CurrencyCode: row.CurrencyCode,
		BudgetDelta:  row.BudgetDelta,
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
		ReleasedAt:   toTimePtr(row.ReleasedAt),
	}
}

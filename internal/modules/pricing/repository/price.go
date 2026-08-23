package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// ListPrices bir price set'in canlı fiyatlarını kurallarıyla birlikte döner.
func (r *Repo) ListPrices(ctx context.Context, priceSetID string) ([]models.Price, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListPricesBySet(ctx, priceSetID)
	if err != nil {
		return nil, wrapDB(err, "fiyatlar alınamadı: %s", priceSetID)
	}

	prices := make([]models.Price, 0, len(rows))
	for i := range rows {
		prices = append(prices, toPrice(rows[i]))
	}
	if err := r.attachRules(ctx, prices); err != nil {
		return nil, err
	}
	return prices, nil
}

// ListPricesBySets birden çok price set'in fiyatlarını TEK sorguda döner ve
// kap kimliğine göre gruplar.
//
// Toplu olması Query katmanının N+1 yasağı içindir (ADR 0004): product'ın store
// listelemesi yüz varyantın fiyatını tek çağrıda okur. Kurallar da ikinci ve
// SON bir sorguyla toplu getirilir.
func (r *Repo) ListPricesBySets(ctx context.Context, priceSetIDs []string) (map[string][]models.Price, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(priceSetIDs) == 0 {
		return map[string][]models.Price{}, nil
	}

	rows, err := r.q.ListPricesBySets(ctx, priceSetIDs)
	if err != nil {
		return nil, wrapDB(err, "fiyatlar toplu alınamadı")
	}

	prices := make([]models.Price, 0, len(rows))
	for i := range rows {
		prices = append(prices, toPrice(rows[i]))
	}
	if err := r.attachRules(ctx, prices); err != nil {
		return nil, err
	}

	grouped := make(map[string][]models.Price, len(priceSetIDs))
	for i := range prices {
		setID := prices[i].PriceSetID
		grouped[setID] = append(grouped[setID], prices[i])
	}
	return grouped, nil
}

// ListPriceCandidates hesaplamaya girecek fiyatları, bağlı oldukları listenin
// üstverisi ve kurallarıyla birlikte döner.
//
// Eleme YAPILMAZ: para birimi, adet aralığı ve liste geçerliliği süzgeci servis
// katmanındaki saf seçim fonksiyonundadır.
func (r *Repo) ListPriceCandidates(ctx context.Context, priceSetID string) ([]models.PriceCandidate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListPriceCandidates(ctx, priceSetID)
	if err != nil {
		return nil, wrapDB(err, "fiyat adayları alınamadı: %s", priceSetID)
	}

	candidates := make([]models.PriceCandidate, 0, len(rows))
	prices := make([]models.Price, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		price := models.Price{
			ID:           row.ID,
			PriceSetID:   row.PriceSetID,
			PriceListID:  row.PriceListID,
			CurrencyCode: row.CurrencyCode,
			Amount:       row.Amount,
			MinQuantity:  row.MinQuantity,
			MaxQuantity:  row.MaxQuantity,
			CreatedAt:    toTime(row.CreatedAt),
			UpdatedAt:    toTime(row.UpdatedAt),
		}
		prices = append(prices, price)
		candidates = append(candidates, models.PriceCandidate{List: toPriceListInfo(row)})
	}

	if err := r.attachRules(ctx, prices); err != nil {
		return nil, err
	}
	for i := range candidates {
		candidates[i].Price = prices[i]
	}
	return candidates, nil
}

// ReplacePrices bir price set'in fiyatlarını TOPLUCA ve ATOMİK olarak yazar.
//
// Eski fiyatlar soft delete edilir, verilen fiyatlar (ve kuralları) eklenir;
// hepsi tek işlemdedir. Herhangi bir fiyat ya da kural reddedilirse HİÇBİRİ
// yazılmaz ve kap eski fiyat kümesiyle kalır.
//
// Verilen dilim boşsa çağrı kabın tüm fiyatlarını silmek anlamına gelir; bu
// geçerli bir istektir (fiyatı kaldırılmış varyant).
func (r *Repo) ReplacePrices(
	ctx context.Context,
	priceSetID string,
	prices []models.Price,
	now time.Time,
) ([]models.Price, error) {
	written := make([]models.Price, 0, len(prices))

	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		// Kabın varlığı işlem İÇİNDE doğrulanır: aksi hâlde eşzamanlı bir
		// silme ile yazma arasında fiyatlar yetim kalabilirdi.
		if _, err := q.GetPriceSet(ctx, priceSetID); err != nil {
			return notFoundOr(err, CodePriceSetNotFound, "price set bulunamadı: %s", priceSetID)
		}

		if err := q.SoftDeletePricesBySet(ctx, pricingdb.SoftDeletePricesBySetParams{
			PriceSetID: priceSetID,
			DeletedAt:  fromTime(now),
		}); err != nil {
			return wrapDB(err, "eski fiyatlar silinemedi: %s", priceSetID)
		}

		written = written[:0]
		for i := range prices {
			price := &prices[i]
			row, err := q.InsertPrice(ctx, pricingdb.InsertPriceParams{
				ID:           price.ID,
				PriceSetID:   priceSetID,
				PriceListID:  price.PriceListID,
				CurrencyCode: price.CurrencyCode,
				Amount:       price.Amount,
				MinQuantity:  price.MinQuantity,
				MaxQuantity:  price.MaxQuantity,
				CreatedAt:    fromTime(now),
			})
			if err != nil {
				return wrapDB(err, "fiyat eklenemedi (%s %d)", price.CurrencyCode, price.Amount)
			}

			created := toPrice(row)
			created.Rules = make([]models.PriceRule, 0, len(price.Rules))
			for j := range price.Rules {
				rule := &price.Rules[j]
				ruleRow, err := q.InsertPriceRule(ctx, pricingdb.InsertPriceRuleParams{
					ID:         rule.ID,
					PriceID:    created.ID,
					Attribute:  rule.Attribute,
					Operator:   string(rule.Operator),
					RuleValues: rule.Values,
					CreatedAt:  fromTime(now),
				})
				if err != nil {
					return wrapDB(err, "fiyat kuralı eklenemedi (%s %s)", rule.Attribute, rule.Operator)
				}
				created.Rules = append(created.Rules, toPriceRule(ruleRow))
			}
			written = append(written, created)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

// GetPrice kimliğe göre fiyatı kurallarıyla döner; yoksa errors.NotFound.
func (r *Repo) GetPrice(ctx context.Context, id string) (models.Price, error) {
	if err := r.ready(); err != nil {
		return models.Price{}, err
	}

	row, err := r.q.GetPrice(ctx, id)
	if err != nil {
		return models.Price{}, notFoundOr(err, CodePriceNotFound, "fiyat bulunamadı: %s", id)
	}

	price := toPrice(row)
	prices := []models.Price{price}
	if err := r.attachRules(ctx, prices); err != nil {
		return models.Price{}, err
	}
	return prices[0], nil
}

// attachRules verilen fiyatların kurallarını TEK sorguda getirip yerine yazar.
//
// Fiyat başına sorgu açılmaz; maliyet fiyat sayısıyla değil, sabit bir gidiş
// dönüşle sınırlıdır.
func (r *Repo) attachRules(ctx context.Context, prices []models.Price) error {
	if len(prices) == 0 {
		return nil
	}

	ids := make([]string, 0, len(prices))
	for i := range prices {
		ids = append(ids, prices[i].ID)
	}

	rows, err := r.q.ListPriceRulesByPrices(ctx, ids)
	if err != nil {
		return wrapDB(err, "fiyat kuralları alınamadı")
	}

	byPrice := make(map[string][]models.PriceRule, len(prices))
	for i := range rows {
		priceID := rows[i].PriceID
		byPrice[priceID] = append(byPrice[priceID], toPriceRule(rows[i]))
	}
	for i := range prices {
		rules := byPrice[prices[i].ID]
		if rules == nil {
			rules = []models.PriceRule{}
		}
		prices[i].Rules = rules
	}
	return nil
}

// toPrice üretilen satırı domain modeline çevirir. Kurallar ayrı doldurulur.
func toPrice(row pricingdb.Price) models.Price {
	return models.Price{
		ID:           row.ID,
		PriceSetID:   row.PriceSetID,
		PriceListID:  row.PriceListID,
		CurrencyCode: row.CurrencyCode,
		Amount:       row.Amount,
		MinQuantity:  row.MinQuantity,
		MaxQuantity:  row.MaxQuantity,
		Rules:        []models.PriceRule{},
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
	}
}

// toPriceListInfo aday satırındaki liste üstverisini çevirir.
//
// Fiyat bir listeye bağlı DEĞİLSE ya da bağlı olduğu liste silinmişse nil
// döner; ikinci durumu servis katmanı fiyatı eleyerek yorumlar.
func toPriceListInfo(row *pricingdb.ListPriceCandidatesRow) *models.PriceListInfo {
	if row.ListID == nil || row.ListType == nil || row.ListStatus == nil {
		return nil
	}
	return &models.PriceListInfo{
		ID:       *row.ListID,
		Type:     models.PriceListType(*row.ListType),
		Status:   models.PriceListStatus(*row.ListStatus),
		StartsAt: toTimePtr(row.ListStartsAt),
		EndsAt:   toTimePtr(row.ListEndsAt),
	}
}

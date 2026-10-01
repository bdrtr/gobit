package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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

// ListPriceCandidatesBySets birden çok price set'in fiyat adaylarını TEK
// sorguda döner ve kap kimliğine göre gruplar.
//
// Toplu olması Query katmanının N+1 yasağı içindir (ADR 0004): product'ın store
// listelemesi yüz varyantın fiyatını tek çağrıda okur. Kurallar da ikinci ve
// SON bir sorguyla toplu getirilir.
//
// Fiyat yerine ADAY dönmesi bilinçlidir: liste üstverisi taşınmasaydı okuma
// yüzeyi yayınlanmamış bir kampanyanın fiyatını taban fiyattan ayırt edemez ve
// hesaplamanın elediği bir fiyatı vitrine sızdırırdı.
func (r *Repo) ListPriceCandidatesBySets(
	ctx context.Context,
	priceSetIDs []string,
) (map[string][]models.PriceCandidate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(priceSetIDs) == 0 {
		return map[string][]models.PriceCandidate{}, nil
	}

	rows, err := r.q.ListPriceCandidatesBySets(ctx, priceSetIDs)
	if err != nil {
		return nil, wrapDB(err, "fiyat adayları toplu alınamadı")
	}

	candidates := make([]models.PriceCandidate, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		candidates = append(candidates, models.PriceCandidate{
			Price: models.Price{
				ID:           row.ID,
				PriceSetID:   row.PriceSetID,
				PriceListID:  row.PriceListID,
				CurrencyCode: row.CurrencyCode,
				Amount:       row.Amount,
				MinQuantity:  row.MinQuantity,
				MaxQuantity:  row.MaxQuantity,
				CreatedAt:    toTime(row.CreatedAt),
				UpdatedAt:    toTime(row.UpdatedAt),
			},
			List: toPriceListInfo(row.ListID, row.ListType, row.ListStatus, row.ListStartsAt, row.ListEndsAt),
		})
	}
	if err := r.attachCandidateRules(ctx, candidates); err != nil {
		return nil, err
	}

	grouped := make(map[string][]models.PriceCandidate, len(priceSetIDs))
	for i := range candidates {
		setID := candidates[i].Price.PriceSetID
		grouped[setID] = append(grouped[setID], candidates[i])
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
	for i := range rows {
		row := &rows[i]
		candidates = append(candidates, models.PriceCandidate{
			Price: models.Price{
				ID:           row.ID,
				PriceSetID:   row.PriceSetID,
				PriceListID:  row.PriceListID,
				CurrencyCode: row.CurrencyCode,
				Amount:       row.Amount,
				MinQuantity:  row.MinQuantity,
				MaxQuantity:  row.MaxQuantity,
				CreatedAt:    toTime(row.CreatedAt),
				UpdatedAt:    toTime(row.UpdatedAt),
			},
			List: toPriceListInfo(row.ListID, row.ListType, row.ListStatus, row.ListStartsAt, row.ListEndsAt),
		})
	}
	if err := r.attachCandidateRules(ctx, candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}

// ReplacePrices writes a price set's prices WHOLESALE and ATOMICALLY.
//
// The old prices are DELETED — not stamped, removed from the row — and the given
// prices (and their rules) are inserted, all in one transaction. If any price or
// rule is refused NONE is written and the set keeps its old prices.
//
// The hard delete is ADR 0047's decision. The rows a stamp left behind were not
// a price history: each replacement made a new id, so there was no thread to
// follow between generations, the row did not say why it was retired (a replaced
// price and the price of a deleted set look alike) and both of the table's
// indexes are partial on deleted_at IS NULL, so no index held those rows. The
// amount a customer PAID is kept by the cart and the order line in their own
// copy; what piled up here was the price of a day nobody shopped on.
//
// The delete KEEPS the liveness condition the stamp carried. A partial index can
// only serve an expression that implies its own condition, and price_set_id_idx
// is partial on exactly that condition: measured, the conditional delete plans
// an index scan over it and the unconditional one a sequential scan of the whole
// table. The cost is that rows an old version of the code already stamped are
// out of reach; cleaning them is an operator's one-off job.
//
// A price's rules go with it through the ON DELETE CASCADE on
// price_rule.price_id; the stamp never fired that cascade, so every old
// generation's rules stayed alive behind a parent nobody could read any more.
//
// An empty slice means deleting every price of the set, which is a valid request
// (a variant whose price was removed).
//
// The transaction's first step LOCKS the set's row, so concurrent writes to one
// set are serialized and the promise of a "replacement" holds (see
// GetPriceSetForUpdate). With an unlocked existence check the prices of two
// writes would merge in the set and both would report success.
//
// The clock is read AFTER that lock (ADR 0242): the prices' creation and the
// snapshot are stamped with the moment of the write, so a write that waited for
// the lock is recorded after the one that held it, and the history's latest
// snapshot holds the set's prices.
func (r *Repo) ReplacePrices(
	ctx context.Context,
	priceSetID string,
	prices []models.Price,
	clock func() time.Time,
) ([]models.Price, error) {
	var written []models.Price

	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		// The set's existence is checked INSIDE the transaction and UNDER THE
		// LOCK: otherwise a concurrent delete could orphan the prices between the
		// check and the write.
		if _, err := q.GetPriceSetForUpdate(ctx, priceSetID); err != nil {
			return notFoundOr(err, CodePriceSetNotFound, "price set not found: %s", priceSetID)
		}

		var err error
		written, err = replaceLocked(ctx, q, priceSetID, prices, clock())

		return err
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

// insertPrices verilen fiyatları ve kurallarını AÇIK BİR İŞLEM İÇİNDE ekler.
//
// Çağıranın işlemini paylaşır (q, tx'e bağlı Queries'tir); bu yüzden kabın
// oluşturulmasıyla fiyatlarının yazılması tek bir atomik adım olabilir.
func insertPrices(
	ctx context.Context,
	q *pricingdb.Queries,
	priceSetID string,
	prices []models.Price,
	now time.Time,
) ([]models.Price, error) {
	written := make([]models.Price, 0, len(prices))
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
			return nil, wrapDB(err, "fiyat eklenemedi (%s %d)", price.CurrencyCode, price.Amount)
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
				return nil, wrapDB(err, "fiyat kuralı eklenemedi (%s %s)", rule.Attribute, rule.Operator)
			}
			created.Rules = append(created.Rules, toPriceRule(ruleRow))
		}
		written = append(written, created)
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
	return attachRulesWith(ctx, r.q, prices)
}

// attachRulesWith attaches the prices' rules read through q, so a read inside a
// transaction sees the transaction's rows (D193).
func attachRulesWith(ctx context.Context, q *pricingdb.Queries, prices []models.Price) error {
	if len(prices) == 0 {
		return nil
	}

	ids := make([]string, 0, len(prices))
	for i := range prices {
		ids = append(ids, prices[i].ID)
	}

	rows, err := q.ListPriceRulesByPrices(ctx, ids)
	if err != nil {
		return wrapDB(err, "the price rules could not be read")
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

// attachCandidateRules adayların fiyat kurallarını TEK sorguda getirip yerine
// yazar.
//
// Aday listesi doğrudan [Repo.attachRules]'a verilemez (o []models.Price
// bekler); dönüşüm burada bir kez yapılır ki iki aday sorgusu aynı yolu
// paylaşsın.
func (r *Repo) attachCandidateRules(ctx context.Context, candidates []models.PriceCandidate) error {
	prices := make([]models.Price, 0, len(candidates))
	for i := range candidates {
		prices = append(prices, candidates[i].Price)
	}
	if err := r.attachRules(ctx, prices); err != nil {
		return err
	}
	for i := range candidates {
		candidates[i].Price = prices[i]
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
//
// Satır tipi değil ALANLAR alınır: tekil ve toplu aday sorguları sqlc'de ayrı
// satır tipleri üretir, aynı beş sütunu taşısalar da. Alanları geçmek tek bir
// dönüşümün iki sorguya da hizmet etmesini sağlar.
func toPriceListInfo(
	id, listType, status *string,
	startsAt, endsAt pgtype.Timestamptz,
) *models.PriceListInfo {
	if id == nil || listType == nil || status == nil {
		return nil
	}
	return &models.PriceListInfo{
		ID:       *id,
		Type:     models.PriceListType(*listType),
		Status:   models.PriceListStatus(*status),
		StartsAt: toTimePtr(startsAt),
		EndsAt:   toTimePtr(endsAt),
	}
}

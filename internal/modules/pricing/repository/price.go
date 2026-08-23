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

// ReplacePrices bir price set'in fiyatlarını TOPLUCA ve ATOMİK olarak yazar.
//
// Eski fiyatlar soft delete edilir, verilen fiyatlar (ve kuralları) eklenir;
// hepsi tek işlemdedir. Herhangi bir fiyat ya da kural reddedilirse HİÇBİRİ
// yazılmaz ve kap eski fiyat kümesiyle kalır.
//
// Verilen dilim boşsa çağrı kabın tüm fiyatlarını silmek anlamına gelir; bu
// geçerli bir istektir (fiyatı kaldırılmış varyant).
//
// İşlemin ilk adımı kabın satırını KİLİTLER: aynı kaba yapılan eşzamanlı
// yazımlar böylece seri hâle gelir ve "yerine koyma" sözü korunur (bkz.
// GetPriceSetForUpdate). Kilitsiz bir varlık denetiminde iki yazımın fiyatları
// kapta birleşir ve ikisi de başarı dönerdi.
func (r *Repo) ReplacePrices(
	ctx context.Context,
	priceSetID string,
	prices []models.Price,
	now time.Time,
) ([]models.Price, error) {
	var written []models.Price

	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		// Kabın varlığı işlem İÇİNDE ve KİLİTLE doğrulanır: aksi hâlde
		// eşzamanlı bir silme ile yazma arasında fiyatlar yetim kalabilirdi.
		if _, err := q.GetPriceSetForUpdate(ctx, priceSetID); err != nil {
			return notFoundOr(err, CodePriceSetNotFound, "price set bulunamadı: %s", priceSetID)
		}

		if err := q.SoftDeletePricesBySet(ctx, pricingdb.SoftDeletePricesBySetParams{
			PriceSetID: priceSetID,
			DeletedAt:  fromTime(now),
		}); err != nil {
			return wrapDB(err, "eski fiyatlar silinemedi: %s", priceSetID)
		}

		var err error
		written, err = insertPrices(ctx, q, priceSetID, prices, now)
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

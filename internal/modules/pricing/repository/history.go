package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// The snapshots the ladder's past is rebuilt from (ADR 0167).
//
// Every write that changes what the ladder reads calls one of the two recorders
// below INSIDE its own transaction, after the change: a set's prices and rules
// through [recordSetHistory], a list's header through [recordListHistory]. An
// arch gate holds the pairing — every function that calls a query writing
// price, price_rule, price_set or price_list records a snapshot, or is only
// called by functions that do — because a writer that skipped it would leave
// the history silently wrong from that moment on, and nothing else would say so.

// historyPrice is one price in a set snapshot, in the JSON the migration's seed
// writes too. The two shapes are one contract; the integration test decodes the
// seed with this type.
type historyPrice struct {
	ID           string        `json:"id"`
	PriceListID  *string       `json:"price_list_id"`
	CurrencyCode string        `json:"currency_code"`
	Amount       int64         `json:"amount"`
	MinQuantity  int32         `json:"min_quantity"`
	MaxQuantity  *int32        `json:"max_quantity"`
	Rules        []historyRule `json:"rules"`
}

// historyRule is one rule of a snapshot price.
type historyRule struct {
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"`
	Values    []string `json:"values"`
}

// recordSetHistory appends the set's live prices, with their rules, as they
// stand now inside q's transaction.
func recordSetHistory(ctx context.Context, q *pricingdb.Queries, priceSetID string, now time.Time) error {
	rows, err := q.ListPricesBySet(ctx, priceSetID)
	if err != nil {
		return wrapDB(err, "the prices of %s could not be read for its history", priceSetID)
	}

	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	rules := map[string][]historyRule{}
	if len(ids) > 0 {
		ruleRows, err := q.ListPriceRulesByPrices(ctx, ids)
		if err != nil {
			return wrapDB(err, "the rules of %s could not be read for its history", priceSetID)
		}
		for i := range ruleRows {
			rule := ruleRows[i]
			rules[rule.PriceID] = append(rules[rule.PriceID], historyRule{
				Attribute: rule.Attribute,
				Operator:  rule.Operator,
				Values:    rule.RuleValues,
			})
		}
	}

	prices := make([]historyPrice, 0, len(rows))
	for i := range rows {
		row := rows[i]
		ruleList := rules[row.ID]
		if ruleList == nil {
			ruleList = []historyRule{}
		}
		prices = append(prices, historyPrice{
			ID:           row.ID,
			PriceListID:  row.PriceListID,
			CurrencyCode: row.CurrencyCode,
			Amount:       row.Amount,
			MinQuantity:  row.MinQuantity,
			MaxQuantity:  row.MaxQuantity,
			Rules:        ruleList,
		})
	}

	raw, err := json.Marshal(prices)
	if err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeHistoryUnreadable,
			"the history of %s could not be encoded", priceSetID)
	}

	if err := q.InsertPriceSetHistory(ctx, pricingdb.InsertPriceSetHistoryParams{
		ID:         models.NewPriceSetHistoryID(now),
		PriceSetID: priceSetID,
		RecordedAt: fromTime(now),
		Prices:     raw,
	}); err != nil {
		return wrapDB(err, "the history of %s could not be written", priceSetID)
	}

	return nil
}

// recordListHistory appends a list's header as it stands after a write.
func recordListHistory(
	ctx context.Context, q *pricingdb.Queries, list pricingdb.PriceList, deleted bool, now time.Time,
) error {
	if err := q.InsertPriceListHistory(ctx, pricingdb.InsertPriceListHistoryParams{
		ID:          models.NewPriceListHistoryID(now),
		PriceListID: list.ID,
		RecordedAt:  fromTime(now),
		Type:        list.Type,
		Status:      list.Status,
		StartsAt:    list.StartsAt,
		EndsAt:      list.EndsAt,
		Deleted:     deleted,
	}); err != nil {
		return wrapDB(err, "the history of the price list %s could not be written", list.ID)
	}

	return nil
}

// PriceSetHistory returns every snapshot of the given sets, oldest first.
func (r *Repo) PriceSetHistory(ctx context.Context, priceSetIDs []string) ([]models.PriceSetSnapshot, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(priceSetIDs) == 0 {
		return []models.PriceSetSnapshot{}, nil
	}

	rows, err := r.q.ListPriceSetHistory(ctx, priceSetIDs)
	if err != nil {
		return nil, wrapDB(err, "the price history could not be read")
	}

	out := make([]models.PriceSetSnapshot, 0, len(rows))
	for i := range rows {
		row := rows[i]
		var prices []historyPrice
		if err := json.Unmarshal(row.Prices, &prices); err != nil {
			return nil, errors.Wrap(err, errors.KindInternal, CodeHistoryUnreadable,
				"the history snapshot %s could not be decoded", row.ID)
		}

		snapshot := models.PriceSetSnapshot{
			PriceSetID: row.PriceSetID,
			RecordedAt: toTime(row.RecordedAt),
			Prices:     make([]models.Price, 0, len(prices)),
		}
		for j := range prices {
			snapshot.Prices = append(snapshot.Prices, fromHistoryPrice(row.PriceSetID, prices[j]))
		}
		out = append(out, snapshot)
	}

	return out, nil
}

// PriceListHistory returns every snapshot of the given lists, oldest first,
// keyed by list.
func (r *Repo) PriceListHistory(
	ctx context.Context, priceListIDs []string,
) (map[string][]models.PriceListSnapshot, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	out := map[string][]models.PriceListSnapshot{}
	if len(priceListIDs) == 0 {
		return out, nil
	}

	rows, err := r.q.ListPriceListHistory(ctx, priceListIDs)
	if err != nil {
		return nil, wrapDB(err, "the price list history could not be read")
	}
	for i := range rows {
		row := rows[i]
		out[row.PriceListID] = append(out[row.PriceListID], models.PriceListSnapshot{
			RecordedAt: toTime(row.RecordedAt),
			Info: models.PriceListInfo{
				ID:       row.PriceListID,
				Type:     models.PriceListType(row.Type),
				Status:   models.PriceListStatus(row.Status),
				StartsAt: toTimePtr(row.StartsAt),
				EndsAt:   toTimePtr(row.EndsAt),
			},
			Deleted: row.Deleted,
		})
	}

	return out, nil
}

// fromHistoryPrice turns a snapshot price back into the model the ladder reads.
func fromHistoryPrice(priceSetID string, price historyPrice) models.Price {
	rules := make([]models.PriceRule, 0, len(price.Rules))
	for i := range price.Rules {
		rules = append(rules, models.PriceRule{
			PriceID:   price.ID,
			Attribute: price.Rules[i].Attribute,
			Operator:  models.RuleOperator(price.Rules[i].Operator),
			Values:    price.Rules[i].Values,
		})
	}

	return models.Price{
		ID:           price.ID,
		PriceSetID:   priceSetID,
		PriceListID:  price.PriceListID,
		CurrencyCode: price.CurrencyCode,
		Amount:       price.Amount,
		MinQuantity:  price.MinQuantity,
		MaxQuantity:  price.MaxQuantity,
		Rules:        rules,
	}
}

// recordSetOfPrice records the set a price belongs to, when the price is live.
//
// A rule can be written to a deleted price — the foreign key looks at the row,
// not at its deleted_at, and TestSilinmisFiyataKuralYazilabilirAmaUlasilamaz
// keeps that — and such a rule changes nothing the ladder reads, so it leaves
// no snapshot.
func recordSetOfPrice(ctx context.Context, q *pricingdb.Queries, priceID string, now time.Time) error {
	price, err := q.GetPrice(ctx, priceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return wrapDB(err, "the price %s could not be read for its history", priceID)
	}

	return recordSetHistory(ctx, q, price.PriceSetID, now)
}

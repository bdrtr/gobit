package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// The bounds of a price list comparison (ADR 0220).
const (
	// MaxCompareEntries is how many purchases one comparison prices: the
	// orders of a trial.
	MaxCompareEntries = 5_000
	// MaxCompareItems is how many lines one comparison prices across them.
	MaxCompareItems = 100_000
)

// compareListRequest is the body of [Service.CompareListJSON]:
//
//	{"entries": [{"reference": "order_1", "currency_code": "TRY",
//	  "attributes": {"region_id": "reg_1"}, "items": [{"price_set_id": "pset_1", "quantity": 2}]}]}
type compareListRequest struct {
	Entries []compareListEntry `json:"entries"`
}

// compareListEntry is one purchase: its currency, its rule context and its
// lines.
type compareListEntry struct {
	Reference    string                 `json:"reference"`
	CurrencyCode string                 `json:"currency_code"`
	Attributes   map[string]string      `json:"attributes"`
	Items        []calculateAmountsItem `json:"items"`
}

// compareListResponse answers each entry's lines twice, in the request's order.
type compareListResponse struct {
	Entries []compareListAnswer `json:"entries"`
}

// compareListAnswer is one purchase's lines.
type compareListAnswer struct {
	Reference string          `json:"reference"`
	Items     []comparedPrice `json:"items"`
}

// comparedPrice is one line's price without the list and with it.
type comparedPrice struct {
	Baseline calculatedAmount `json:"baseline"`
	Trial    calculatedAmount `json:"trial"`
}

// CompareListJSON prices purchases twice, as the ladder would today: once as if
// the list did not exist, and once as if it were active with no window (ADR
// 0220). Nothing is written.
//
// The list may be a draft, active or expired; its type, its prices and their
// rules and quantities count as they are. The rest of the ladder is today's:
// every other list, the base prices and the moment the windows are read at.
// Each entry carries its own currency and rule context, so the purchases of
// many customers are priced in one read of their sets' candidates.
func (s *Service) CompareListJSON(ctx context.Context, listID string, request json.RawMessage) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(listID, models.PriceListIDPrefix, "price list id"); err != nil {
		return nil, err
	}
	list, err := s.repo.GetPriceList(ctx, listID)
	if err != nil {
		return nil, err
	}
	var req compareListRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput, "the comparison request could not be decoded")
	}
	if len(req.Entries) > MaxCompareEntries {
		return nil, errors.Invalid(CodeInvalidInput, "a comparison prices at most %d purchases, %d given",
			MaxCompareEntries, len(req.Entries))
	}

	var setIDs []string
	seen := map[string]bool{}
	items := 0
	for e := range req.Entries {
		entry := &req.Entries[e]
		currency, err := normalizeCurrency(entry.CurrencyCode)
		if err != nil {
			return nil, err
		}
		entry.CurrencyCode = currency
		for i := range entry.Items {
			item := &entry.Items[i]
			label := fmt.Sprintf("item %d of purchase %d", i, e)
			if err := requireID(item.PriceSetID, models.PriceSetIDPrefix, label); err != nil {
				return nil, err
			}
			quantity, err := normalizeQuantity(item.Quantity)
			if err != nil {
				return nil, errors.Wrap(err, errors.KindOf(err), errors.CodeOf(err), "%s was rejected", label)
			}
			item.Quantity = quantity
			if !seen[item.PriceSetID] {
				seen[item.PriceSetID] = true
				setIDs = append(setIDs, item.PriceSetID)
			}
		}
		items += len(entry.Items)
	}
	if items > MaxCompareItems {
		return nil, errors.Invalid(CodeInvalidInput, "a comparison prices at most %d lines, %d given",
			MaxCompareItems, items)
	}

	candidatesBySet, err := s.repo.ListPriceCandidatesBySets(ctx, setIDs)
	if err != nil {
		return nil, err
	}
	at := s.clock()
	baseline, trial := map[string][]models.PriceCandidate{}, map[string][]models.PriceCandidate{}
	for setID, candidates := range candidatesBySet {
		baseline[setID], trial[setID] = withAndWithoutList(candidates, list)
	}

	out := compareListResponse{Entries: make([]compareListAnswer, 0, len(req.Entries))}
	for e := range req.Entries {
		entry := &req.Entries[e]
		answer := compareListAnswer{Reference: entry.Reference, Items: make([]comparedPrice, 0, len(entry.Items))}
		for _, item := range entry.Items {
			answer.Items = append(answer.Items, comparedPrice{
				Baseline: comparedAmount(selectPrice(baseline[item.PriceSetID], entry.CurrencyCode, item.Quantity, entry.Attributes, at)),
				Trial:    comparedAmount(selectPrice(trial[item.PriceSetID], entry.CurrencyCode, item.Quantity, entry.Attributes, at)),
			})
		}
		out.Entries = append(out.Entries, answer)
	}

	payload, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput, "the comparison could not be encoded")
	}
	return payload, nil
}

// withAndWithoutList returns a set's candidates without the list's prices, and
// with them offered as an active list with no window.
func withAndWithoutList(
	candidates []models.PriceCandidate, list models.PriceList,
) (without, with []models.PriceCandidate) {
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.List == nil || candidate.List.ID != list.ID {
			without = append(without, *candidate)
			with = append(with, *candidate)

			continue
		}
		offered := *candidate
		offered.List = &models.PriceListInfo{ID: list.ID, Type: list.Type, Status: models.PriceListActive}
		with = append(with, offered)
	}
	return without, with
}

// comparedAmount turns a selection into the bulk answer's shape.
func comparedAmount(selected models.CalculatedPrice, ok bool) calculatedAmount {
	if !ok {
		return calculatedAmount{}
	}
	return calculatedAmount{
		Amount: selected.Amount, Priced: true, PriceID: selected.PriceID,
		PriceListID: selected.PriceListID, PriceListType: string(selected.PriceListType),
	}
}

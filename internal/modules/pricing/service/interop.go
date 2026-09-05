package service

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// This file is pricing's CROSS-MODULE surface (ADR 0001).
//
// The signatures here use ONLY primitive and stdlib types. The reason is Go's
// structural conformance rule: since the consuming module cannot import
// pricing, it cannot name a type such as [models.PriceSet] in its signature; the
// moment it names one, that becomes ANOTHER type defined in its own package and
// the concrete service does not satisfy the consumer's interface. A signature
// written with primitive types, on the other hand, can be repeated verbatim in
// the consumer's own package and resolved from the container by name.
//
// The rich in-module surface (with the models types) is in service.go and
// calculate.go; only pricing's own API layer and query provider call it.

// CreateEmptyPriceSet creates a price set with no prices and returns ITS ID.
//
// The product module calls this while creating a variant and writes the returned
// id into the "product_variant_price_set" link; pricing never sees that link and
// is unaware that the variant exists (Principle 2.1/2.3).
//
// The counterpart on the consumer side:
//
//	type PriceSetCreator interface {
//	    CreateEmptyPriceSet(ctx context.Context) (string, error)
//	}
func (s *Service) CreateEmptyPriceSet(ctx context.Context) (string, error) {
	set, err := s.CreatePriceSet(ctx, nil)
	if err != nil {
		return "", err
	}
	return set.ID, nil
}

// SetBasePrices writes the BASE prices of a container in bulk from a currency ->
// amount mapping.
//
// "Base" means without a list and without rules; campaign and segment prices
// cannot be written through this surface (those are the job of pricing's own
// admin API). The write is a REPLACE like [Service.SetPrices]: the prices of
// currencies absent from the mapping are deleted.
//
// Because a map's iteration order is random, the currencies are SORTED; the same
// input is written in the same order on every call and the index in an error
// message becomes meaningful.
//
// The counterpart on the consumer side:
//
//	type BasePriceWriter interface {
//	    SetBasePrices(ctx context.Context, priceSetID string, amountsByCurrency map[string]int64) error
//	}
func (s *Service) SetBasePrices(ctx context.Context, priceSetID string, amountsByCurrency map[string]int64) error {
	inputs := make([]PriceInput, 0, len(amountsByCurrency))
	for _, currency := range slices.Sorted(maps.Keys(amountsByCurrency)) {
		inputs = append(inputs, PriceInput{
			CurrencyCode: currency,
			Amount:       amountsByCurrency[currency],
			MinQuantity:  models.MinQuantity,
		})
	}

	_, err := s.SetPrices(ctx, priceSetID, inputs)
	return err
}

// CalculateAmount returns the UNIT amount of the selected price in minor units.
//
// The selection rule is exactly the same as [Service.CalculatePrice]; this is
// merely a narrow signature that can cross module boundaries. The moment of
// calculation is "now": a consuming module asking for a price in the past is a
// reporting need and is served from pricing's own API.
//
// If quantity is given as 0 it is taken as 1. attributes may be nil; in that case
// ruled prices are eliminated and the base price is selected.
//
// The counterpart on the consumer side (cart will define it in Phase 5):
//
//	type PriceCalculator interface {
//	    CalculateAmount(ctx context.Context, priceSetID, currencyCode string,
//	        quantity int32, attributes map[string]string) (int64, error)
//	}
func (s *Service) CalculateAmount(
	ctx context.Context,
	priceSetID, currencyCode string,
	quantity int32,
	attributes map[string]string,
) (int64, error) {
	calculated, err := s.CalculatePrice(ctx, priceSetID, CalculateParams{
		CurrencyCode: currencyCode,
		Quantity:     quantity,
		Attributes:   attributes,
	})
	if err != nil {
		return 0, err
	}
	return calculated.Amount, nil
}

// MaxCalculateItems is the number of items a single bulk price request may
// carry; if it is exceeded the request is REJECTED with errors.Invalid and no
// item is priced.
//
// The limit is NOT SILENT: there is no truncation, the error message writes both
// the limit and the number of items that arrived. Truncating would mean leaving
// part of the caller's cart unpriced and presenting the result as "successful".
//
// The value is TEN TIMES the ceiling of the cart total calculation, its only
// consumer (MaxLineItems in workflows/cart, 100 today). The two not being equal
// is deliberate: a cart opened BEFORE that ceiling was put in place, and carrying
// more lines than it, must still be calculable — rejecting the calculation would
// render the customer's existing cart unpayable. The gap covers those old carts;
// above 1000 lines too, a single request is a read that pulls the price
// candidates of 1000 containers into memory, and one has to stop there.
//
// # The ceiling and the point where the plan TURNS are not the same place
//
// 1000 is the legal upper bound, not the bound of what is CHEAP: as the id array
// grows the planner at some point abandons the partial index and scans the price
// table from the start. Measured (gobit_load, 58,000 price rows, same query,
// warmed up, best of five):
//
//	id count   plan                time
//	     280   Bitmap Index Scan   0.73 ms
//	     300   Seq Scan on price   4.69 ms
//	   1,000   Seq Scan on price   5.30 ms
//
// The turn lies between 280 and 300, that is THREE TIMES below the ceiling.
// Today it is unreachable (the only path that opens a line is subject to the
// cart's own ceiling of 100) and it is not something to be fixed either: the
// single query that falls to a scan is still far below asking for the same
// containers one by one (300 × ~0.1 ms). The reason it is written here is the
// EXPECTATION of whoever raises the ceiling — the cost is not linear up to 1000,
// and the ceiling must not be raised without seeing this jump.
const MaxCalculateItems = 1_000

// calculateAmountsRequest is the body of the bulk price request.
//
// The currency and the rule context are carried PER REQUEST rather than PER
// ITEM: all the lines of one cart are in the same currency and the same region,
// and repeating the field per item would give the impression that two lines
// could be priced with different contexts.
type calculateAmountsRequest struct {
	// CurrencyCode is the requested currency (ISO 4217); it is required.
	CurrencyCode string `json:"currency_code"`
	// Attributes is the rule context (e.g. {"region_id": "reg_1"}); it may be empty.
	Attributes map[string]string `json:"attributes"`
	// Items are the items to be priced; the ORDER is preserved and the response is
	// in the same order.
	Items []calculateAmountsItem `json:"items"`
}

// calculateAmountsItem is a single item in the bulk request.
type calculateAmountsItem struct {
	// PriceSetID is the container whose price is asked for.
	PriceSetID string `json:"price_set_id"`
	// Quantity is the quantity to be purchased; if 0 is given it is taken as 1.
	Quantity int32 `json:"quantity"`
}

// calculateAmountsResponse is the body of the bulk price response.
type calculateAmountsResponse struct {
	// Items are the results, IN THE SAME ORDER and OF THE SAME LENGTH as the items
	// in the request.
	Items []calculatedAmount `json:"items"`
}

// calculatedAmount is the result of a single item.
type calculatedAmount struct {
	// Amount is the unit amount of the selected price (minor unit); if Priced is
	// false it is meaningless and zero.
	Amount int64 `json:"amount"`
	// Priced reports whether a valid price WAS FOUND for the item.
	//
	// A separate flag is A MUST: zero is a VALID price (the price table's
	// constraint is amount >= 0 and a free item is a real scenario), and therefore
	// "amount 0" cannot be told apart from "no price" by the amount itself. Without
	// the flag a variant with no price would enter the cart FOR FREE.
	Priced bool `json:"priced"`
}

// CalculateAmountsJSON returns the unit amounts of several containers in a
// SINGLE round.
//
// [Service.CalculateAmount] is for a single container and opens two queries per
// container (price candidates + their rules). This method does the same work in
// two queries INDEPENDENTLY of the number of containers; the bulk read itself
// already existed (ListPriceCandidatesBySets on [Repository]) and had not been
// carried this far.
//
// Measurement (gobit_load, 54,000 containers, localhost TCP, best of seven
// rounds): for 50 containers the per-container path is 4.93 ms, the bulk path
// 0.25 ms (20 times); for 100 containers 9.88 ms and 0.33 ms (30 times). For a
// single container the bulk path has NO advantage — the candidate query itself
// is 66 µs against 77 µs on the median of 500 rounds — which is why the singular
// method stays and a caller asking for a single price uses it. The difference is
// not a plan difference; EXPLAIN shows that the same partial index
// (price_set_id_idx) is scanned for a single-id array as well, the array query
// adds one sort step on top. At fifty ids the plan switches to a bitmap scan and
// the server side is measured at 0.35 ms for the single query; asking the same
// fifty containers one by one costs 50 × 0.17 ms on the server.
//
// # The selection rule is THE SAME
//
// The winning price is again chosen by [selectPrice] — the same pure function,
// the same elimination and ordering criteria. The candidate rows the two paths
// see are the same too: the two SQL queries carry the same columns, the same
// LEFT JOIN and the same deleted_at condition, the bulk one merely looks the
// container id up with ANY(...). The order within a container is the same as
// well (p.id), but the result is independent of order anyway: [better] looks at
// the price ID as its last criterion and the id is the primary key, that is, the
// ordering is total.
//
// The only difference is HOW MANY TIMES the clock is read: on the per-container
// path every call takes its own moment, here all the items are evaluated with a
// SINGLE moment. The difference is IN FAVOR of the bulk path — a campaign ending
// at exactly that instant cannot price two lines of the same cart from different
// worlds.
//
// # An item with no price is NOT AN ERROR
//
// If there is no valid price for the container, that item is returned with
// Priced=false and the request counts as successful. The singular method returns
// errors.NotFound in this situation; the distinction is deliberate, because
// throwing away the price of the whole cart over a single priceless line would
// mean the caller going back to the per-item path in order to learn which line
// was the problem. The caller decides which line to reject.
//
// This is also why the case where the container DOES NOT EXIST AT ALL is not
// asked separately (the singular path, on seeing empty candidates, tells "no
// container" apart from "empty container" with GetPriceSet through
// [Repository]): both situations are "this item has no price" and both fall to
// the same flag.
func (s *Service) CalculateAmountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	req, err := decodeCalculateAmounts(request)
	if err != nil {
		return nil, err
	}
	currency, err := normalizeCurrency(req.CurrencyCode)
	if err != nil {
		return nil, err
	}

	quantities := make([]int32, len(req.Items))
	setIDs := make([]string, 0, len(req.Items))
	seen := make(map[string]struct{}, len(req.Items))
	for i := range req.Items {
		item := req.Items[i]
		if err := requireID(item.PriceSetID, models.PriceSetIDPrefix, itemLabel(i)); err != nil {
			return nil, err
		}
		quantity, err := normalizeQuantity(item.Quantity)
		if err != nil {
			// The kind and the CODE are preserved; the only thing added is
			// which item was rejected. Rewriting the code would break the
			// caller's branching on quantity validation.
			return nil, errors.Wrap(err, errors.KindOf(err), errors.CodeOf(err),
				"%s was rejected", itemLabel(i))
		}
		quantities[i] = quantity

		if _, dup := seen[item.PriceSetID]; !dup {
			seen[item.PriceSetID] = struct{}{}
			setIDs = append(setIDs, item.PriceSetID)
		}
	}

	candidatesBySet, err := s.repo.ListPriceCandidatesBySets(ctx, setIDs)
	if err != nil {
		return nil, err
	}

	// The clock is read ONCE; the rationale is under the "The selection rule is
	// THE SAME" heading in the godoc.
	at := s.clock()

	out := calculateAmountsResponse{Items: make([]calculatedAmount, 0, len(req.Items))}
	for i := range req.Items {
		selected, ok := selectPrice(
			candidatesBySet[req.Items[i].PriceSetID], currency, quantities[i], req.Attributes, at)
		if !ok {
			out.Items = append(out.Items, calculatedAmount{})
			continue
		}
		out.Items = append(out.Items, calculatedAmount{Amount: selected.Amount, Priced: true})
	}

	payload, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the bulk price response could not be converted to JSON")
	}
	return payload, nil
}

// decodeCalculateAmounts decodes the bulk request body and checks ITS SIZE.
func decodeCalculateAmounts(request json.RawMessage) (calculateAmountsRequest, error) {
	if len(request) == 0 {
		return calculateAmountsRequest{}, errors.Invalid(CodeInvalidInput,
			"the bulk price request cannot be empty")
	}

	var req calculateAmountsRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return calculateAmountsRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput,
			"the bulk price request could not be decoded")
	}
	if len(req.Items) > MaxCalculateItems {
		return calculateAmountsRequest{}, errors.Invalid(CodeInvalidInput,
			"a bulk price request can carry at most %d items, %d given",
			MaxCalculateItems, len(req.Items))
	}
	return req, nil
}

// itemLabel is the name of an item in the bulk request as it appears in error
// messages.
//
// The index is written because nothing else tells items apart in a bulk request:
// the same container may be asked for twice, with different quantities.
func itemLabel(index int) string {
	return "item " + strconv.Itoa(index) + " of the batch price request"
}

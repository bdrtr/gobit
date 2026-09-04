package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Identity and code constants repeated across the tests.
const (
	testCartID     = "cart_1"
	testRegionID   = "reg_tr"
	testCurrency   = "TRY"
	testCustomerID = "cust_1"
	testVariantA   = "var_a"
	testVariantB   = "var_b"
	testPriceSetA  = "pset_a"
	testPriceSetB  = "pset_b"
	testLineA      = "li_a"
	testLineB      = "li_b"
)

// errUnexpected is the error of an unscripted fake call.
//
// The fake DOES NOT STAY SILENT when an unscripted surface is touched: had it
// returned the zero value, the claim "this workflow must never call that
// module" could rot silently while the test stayed green.
func errUnexpected(what string) error {
	return errors.Internal("test_unexpected_call", "unexpected fake call: %s", what)
}

// stubCarts is the implementation of the [Carts] interface that the tests can
// script.
type stubCarts struct {
	openCartFn  func(ctx context.Context, regionID, currencyCode, customerID, email string, metadata json.RawMessage) (string, error)
	snapshotFn  func(ctx context.Context, cartID string) (json.RawMessage, error)
	addLineFn   func(ctx context.Context, cartID, variantID, title string, quantity, unitPrice int64, metadata json.RawMessage) (string, error)
	setQtyFn    func(ctx context.Context, cartID, lineItemID string, quantity int64) error
	removeFn    func(ctx context.Context, cartID, lineItemID string) error
	setTotalsFn func(ctx context.Context, cartID string, totals json.RawMessage) error

	// written holds, in order, the decoded totals passed to SetCartTotalsJSON.
	written []Totals
	// snapshotCalls counts how many times the snapshot was read; the retry
	// claim is proven with it.
	snapshotCalls int
	// removed and quantities record which of the write paths was chosen.
	removed    []string
	quantities map[string]int64
}

// newStubCarts produces an empty fake cart service.
func newStubCarts() *stubCarts {
	return &stubCarts{quantities: map[string]int64{}}
}

// OpenCart applies the scripted cart-opening behavior.
func (s *stubCarts) OpenCart(
	ctx context.Context,
	regionID, currencyCode, customerID, email string,
	metadata json.RawMessage,
) (string, error) {
	if s.openCartFn == nil {
		return "", errUnexpected("OpenCart")
	}
	return s.openCartFn(ctx, regionID, currencyCode, customerID, email, metadata)
}

// CartSnapshotJSON returns the scripted snapshot and counts the call.
func (s *stubCarts) CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	s.snapshotCalls++
	if s.snapshotFn == nil {
		return nil, errUnexpected("CartSnapshotJSON")
	}
	return s.snapshotFn(ctx, cartID)
}

// AddCartLineItem applies the scripted line-item adding behavior.
func (s *stubCarts) AddCartLineItem(
	ctx context.Context,
	cartID, variantID, title string,
	quantity, unitPrice int64,
	metadata json.RawMessage,
) (string, error) {
	if s.addLineFn == nil {
		return "", errUnexpected("AddCartLineItem")
	}
	return s.addLineFn(ctx, cartID, variantID, title, quantity, unitPrice, metadata)
}

// SetCartLineItemQuantity applies the scripted quantity-writing behavior.
func (s *stubCarts) SetCartLineItemQuantity(ctx context.Context, cartID, lineItemID string, quantity int64) error {
	s.quantities[lineItemID] = quantity
	if s.setQtyFn == nil {
		return nil
	}
	return s.setQtyFn(ctx, cartID, lineItemID, quantity)
}

// RemoveLineItem applies the scripted line-item removing behavior.
func (s *stubCarts) RemoveLineItem(ctx context.Context, cartID, lineItemID string) error {
	s.removed = append(s.removed, lineItemID)
	if s.removeFn == nil {
		return nil
	}
	return s.removeFn(ctx, cartID, lineItemID)
}

// SetCartTotalsJSON decodes the incoming body, records it and returns the
// scripted result.
func (s *stubCarts) SetCartTotalsJSON(ctx context.Context, cartID string, totals json.RawMessage) error {
	var decoded Totals
	if err := json.Unmarshal(totals, &decoded); err != nil {
		return err
	}
	s.written = append(s.written, decoded)

	if s.setTotalsFn == nil {
		return nil
	}
	return s.setTotalsFn(ctx, cartID, totals)
}

// stubPrices is the fake implementation of the [Prices] interface.
type stubPrices struct {
	// amounts is a price set -> unit amount mapping.
	amounts map[string]int64
	// fn is used instead of amounts when it is given.
	fn func(ctx context.Context, priceSetID, currencyCode string, quantity int32, attrs map[string]string) (int64, error)
	// seen holds, in order, the context of the SINGLE price calls.
	seen []priceCall
	// requests holds, in order, the decoded bodies of the BATCH price calls;
	// the claim that the pricing pass makes a single round trip is proven with
	// it.
	requests []priceRequest
	// batchFn, when given, produces the batch response entirely; it is there
	// for out-of-contract response scenarios.
	batchFn func(request priceRequest) (priceResponse, error)
	// batchErr, when given, makes the batch call fail with this error.
	batchErr error
}

// priceCall is the record of a single price call.
type priceCall struct {
	priceSetID   string
	currencyCode string
	quantity     int32
	attributes   map[string]string
}

// CalculateAmount returns the scripted unit price.
func (s *stubPrices) CalculateAmount(
	ctx context.Context,
	priceSetID, currencyCode string,
	quantity int32,
	attributes map[string]string,
) (int64, error) {
	s.seen = append(s.seen, priceCall{
		priceSetID:   priceSetID,
		currencyCode: currencyCode,
		quantity:     quantity,
		attributes:   attributes,
	})
	if s.fn != nil {
		return s.fn(ctx, priceSetID, currencyCode, quantity, attributes)
	}
	amount, ok := s.amounts[priceSetID]
	if !ok {
		return 0, errors.NotFound("price_not_calculable",
			"no price for %s in the %s currency", priceSetID, currencyCode)
	}
	return amount, nil
}

// CalculateAmountsJSON decodes the batch request, records it and returns a
// schema-conforming response.
//
// The fake imitates the RESPONSE INVARIANTS of the real pricing module: one
// record IN THE SAME ORDER for every item in the request, and a flag rather
// than an error for an item that has no price. Otherwise the tests would pass
// with a body that never comes about in production.
func (s *stubPrices) CalculateAmountsJSON(_ context.Context, request json.RawMessage) (json.RawMessage, error) {
	var req priceRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	s.requests = append(s.requests, req)

	if s.batchErr != nil {
		return nil, s.batchErr
	}
	if s.batchFn != nil {
		resp, err := s.batchFn(req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(resp)
	}

	resp := priceResponse{Items: make([]priceResponseItem, 0, len(req.Items))}
	for i := range req.Items {
		amount, ok := s.amounts[req.Items[i].PriceSetID]
		if !ok {
			resp.Items = append(resp.Items, priceResponseItem{})
			continue
		}
		resp.Items = append(resp.Items, priceResponseItem{Amount: amount, Priced: true})
	}
	return json.Marshal(resp)
}

// stubRegions is the fake implementation of the [Regions] interface.
type stubRegions struct {
	regionByCountry map[string]string
	currency        string
	decimalDigits   int32
	rateBps         int32
	automatic       bool
	regionErr       error
	currencyErr     error
	taxErr          error
}

// newStubRegions produces a default region fake with 20% automatic tax.
func newStubRegions() *stubRegions {
	return &stubRegions{
		regionByCountry: map[string]string{"TR": testRegionID},
		currency:        testCurrency,
		decimalDigits:   2,
		rateBps:         2000,
		automatic:       true,
	}
}

// RegionIDForCountry returns the region ID for the country code.
func (s *stubRegions) RegionIDForCountry(_ context.Context, countryCode string) (string, error) {
	if s.regionErr != nil {
		return "", s.regionErr
	}
	id, ok := s.regionByCountry[countryCode]
	if !ok {
		return "", errors.NotFound("region_not_found", "country %q has no region", countryCode)
	}
	return id, nil
}

// RegionCurrency returns the region's currency and decimal digits.
func (s *stubRegions) RegionCurrency(_ context.Context, _ string) (code string, decimalDigits int32, err error) {
	if s.currencyErr != nil {
		return "", 0, s.currencyErr
	}
	return s.currency, s.decimalDigits, nil
}

// RegionTax returns the region's tax rate and its automatic flag.
func (s *stubRegions) RegionTax(_ context.Context, _ string) (rateBps int32, automatic bool, err error) {
	if s.taxErr != nil {
		return 0, false, s.taxErr
	}
	return s.rateBps, s.automatic, nil
}

// stubCustomers is the fake implementation of the [Customers] interface.
type stubCustomers struct {
	emails map[string]string
	calls  int
}

// CustomerEmail returns the customer's e-mail; NotFound when there is no such
// customer.
func (s *stubCustomers) CustomerEmail(_ context.Context, customerID string) (string, error) {
	s.calls++
	email, ok := s.emails[customerID]
	if !ok {
		return "", errors.NotFound("customer_not_found", "no such customer: %s", customerID)
	}
	return email, nil
}

// stubLinks is the fake implementation of the [Links] interface.
type stubLinks struct {
	// links is a variant -> price set IDs mapping.
	links map[string][]string
	err   error
	// batches holds the source IDs asked for on every call; the batched-query
	// claim is proven with it.
	batches [][]string
}

// ListMany returns the links of the given source IDs.
func (s *stubLinks) ListMany(_ context.Context, _ string, fromIDs []string) (map[string][]string, error) {
	s.batches = append(s.batches, append([]string(nil), fromIDs...))
	if s.err != nil {
		return nil, s.err
	}

	out := make(map[string][]string, len(fromIDs))
	for _, id := range fromIDs {
		if targets, ok := s.links[id]; ok {
			out[id] = targets
		}
	}
	return out, nil
}

// stubCatalog is the fake implementation of the [Catalog] interface.
//
// It answers two entities: variants (title) and regions (country codes). The
// two sit in a single fake because in reality they are a single surface too
// ("core.query"); splitting them would give the impression that the workflow
// has two separate dependencies.
type stubCatalog struct {
	// titles is a variant -> title mapping.
	titles map[string]string
	// countries is a region -> country codes mapping.
	countries map[string][]string
	// scopedOut holds the variants for which NO RECORD IS RETURNED on a query
	// that CARRIES the channel filter.
	//
	// The fake DOES NOT re-IMPLEMENT the sales channel rule — that rule lives
	// in the product module's SQL, and repeating it here would make the test
	// exercise a second definition that can drift away from the real surface.
	// Instead the fake scripts the ANSWER THE PROVIDER WOULD GIVE: the answer
	// "this variant is not within the scope of this request" means an empty
	// result at the Query layer.
	scopedOut map[string]bool
	// regionErr, when given, makes the region query fail with this error.
	regionErr error
	err       error
	specs     []query.GraphSpec
}

// Graph returns variant or region records.
func (s *stubCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	s.specs = append(s.specs, spec)
	if spec.Entity == EntityRegion {
		return s.regionRecords(spec)
	}
	if s.err != nil {
		return nil, s.err
	}

	id, ok := spec.Filters[query.IDField].(string)
	if !ok {
		return nil, errors.Invalid("test_bad_filter", "the ID filter is not a string")
	}
	if _, scoped := spec.Filters[FilterSalesChannelIDs]; scoped && s.scopedOut[id] {
		return []query.Record{}, nil
	}
	title, ok := s.titles[id]
	if !ok {
		return []query.Record{}, nil
	}
	return []query.Record{{query.IDField: id, FieldTitle: title}}, nil
}

// regionRecords returns the region record along with its country sub-records.
func (s *stubCatalog) regionRecords(spec query.GraphSpec) ([]query.Record, error) {
	if s.regionErr != nil {
		return nil, s.regionErr
	}

	id, ok := spec.Filters[query.IDField].(string)
	if !ok {
		return nil, errors.Invalid("test_bad_filter", "the ID filter is not a string")
	}
	codes, ok := s.countries[id]
	if !ok {
		return []query.Record{}, nil
	}

	records := make([]map[string]any, 0, len(codes))
	for _, code := range codes {
		records = append(records, map[string]any{FieldCode: code, "name": code})
	}
	return []query.Record{{query.IDField: id, FieldCountries: records}}, nil
}

// harness carries a test's fake dependencies and its constructed workflows.
type harness struct {
	carts     *stubCarts
	prices    *stubPrices
	regions   *stubRegions
	customers *stubCustomers
	discounts *stubDiscounts
	taxes     *stubTaxes
	links     *stubLinks
	catalog   *stubCatalog
	wf        *Workflows
}

// newHarness builds a harness with the promotion and tax surfaces UNREGISTERED.
//
// The default being the degraded path is deliberate: every one of the tests
// written in Phase 5 runs on this harness and none of them changes, that is,
// the takeover not breaking the existing behavior is proven anew on every run.
// For the two-module harness see [newModuleHarness].
func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, nil, nil)
}

// newModuleHarness builds a harness with the promotion and tax surfaces
// REGISTERED.
func newModuleHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, &stubDiscounts{perLine: map[string]int64{}}, newStubTaxes())
}

// newHarnessWith builds a harness with the given optional surfaces; a surface
// given as nil counts as unregistered.
func newHarnessWith(t *testing.T, discounts *stubDiscounts, taxes *stubTaxes) *harness {
	t.Helper()

	h := &harness{
		carts:     newStubCarts(),
		prices:    &stubPrices{amounts: map[string]int64{testPriceSetA: 1000, testPriceSetB: 250}},
		regions:   newStubRegions(),
		customers: &stubCustomers{emails: map[string]string{testCustomerID: "registered@example.com"}},
		discounts: discounts,
		taxes:     taxes,
		links: &stubLinks{links: map[string][]string{
			testVariantA: {testPriceSetA},
			testVariantB: {testPriceSetB},
		}},
		catalog: &stubCatalog{
			titles: map[string]string{
				testVariantA: "Red T-Shirt / M",
				testVariantB: "Blue Socks",
			},
			countries: map[string][]string{testRegionID: {"TR"}},
		},
	}

	deps := Deps{
		Carts:     h.carts,
		Prices:    h.prices,
		Regions:   h.regions,
		Customers: h.customers,
		Links:     h.links,
		Catalog:   h.catalog,
	}
	// The difference between a nil interface VALUE and a nil interface TYPE:
	// if h.discounts is a nil *stubDiscounts, once it is put into the
	// Deps.Discounts field the interface is NO LONGER nil and the degradation
	// path would never run.
	if discounts != nil {
		deps.Discounts = discounts
	}
	if taxes != nil {
		deps.Taxes = taxes
	}

	wf, err := New(deps)
	require.NoError(t, err)
	h.wf = wf
	return h
}

// snapshotOf produces a cart snapshot with the given fields.
func snapshotOf(revision int64, items []SnapshotItem, methods []SnapshotShippingMethod) Snapshot {
	return Snapshot{
		ID:              testCartID,
		RegionID:        testRegionID,
		CurrencyCode:    testCurrency,
		Revision:        revision,
		Items:           items,
		ShippingMethods: methods,
	}
}

// serveSnapshot scripts the fake cart so that it returns the given snapshots
// IN ORDER; once the last snapshot is used up it keeps returning that last one.
func serveSnapshot(carts *stubCarts, snaps ...Snapshot) {
	carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		index := carts.snapshotCalls - 1
		if index >= len(snaps) {
			index = len(snaps) - 1
		}
		snap := snaps[index]
		snap.ID = cartID
		return json.Marshal(snap)
	}
}

// stubDiscounts is the fake implementation of the [Discounts] interface.
//
// Its default behavior is "no promotion at all": it returns a zero discount
// for every line. The fake imitates the RESPONSE INVARIANTS of the real
// promotion module (one record in the same order for every line in the
// request, consistent totals); otherwise the tests would pass with a body that
// never comes about in production.
type stubDiscounts struct {
	// perLine is a line ID -> discount mapping.
	perLine map[string]int64
	// fn, when given, produces the body entirely; it is there for malformed
	// response scenarios.
	fn func(request discountRequest) (discountResponse, error)
	// err, when given, makes the call fail with this error.
	err error
	// requests holds, in order, the decoded bodies of the calls that were made.
	requests []discountRequest
	// calls is the number of calls.
	calls int
}

// ComputeDiscountsJSON decodes the request, applies the scripted discount and
// returns a schema-conforming response.
func (s *stubDiscounts) ComputeDiscountsJSON(_ context.Context, request json.RawMessage) (json.RawMessage, error) {
	s.calls++

	var req discountRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	s.requests = append(s.requests, req)

	if s.err != nil {
		return nil, s.err
	}

	var resp discountResponse
	if s.fn != nil {
		var err error
		if resp, err = s.fn(req); err != nil {
			return nil, err
		}
		return json.Marshal(resp)
	}

	resp = discountResponse{
		CurrencyCode:    req.CurrencyCode,
		Items:           make([]discountLine, 0, len(req.Items)),
		ShippingMethods: []discountLine{},
	}
	for i := range req.Items {
		amount := s.perLine[req.Items[i].ID]
		resp.Items = append(resp.Items, discountLine{ID: req.Items[i].ID, Amount: amount})
		resp.ItemsDiscountTotal += amount
	}
	resp.DiscountTotal = resp.ItemsDiscountTotal
	return json.Marshal(resp)
}

// stubTaxes is the fake implementation of the [Taxes] interface.
//
// Its default behavior is to apply a single basis-point rate PER ITEM and
// rounding DOWN, without looking at the country; the local provider of the
// real tax module uses the same arithmetic as well.
type stubTaxes struct {
	// rateBps is the rate to apply (basis points).
	rateBps int32
	// regionFound reports whether the country's tax region was found.
	regionFound bool
	// fn, when given, produces the body entirely.
	fn func(request taxRequest) (taxResponse, error)
	// err, when given, makes the call fail with this error.
	err error
	// requests holds, in order, the decoded bodies of the calls that were made.
	requests []taxRequest
	// calls is the number of calls.
	calls int
}

// newStubTaxes produces a tax fake with a 20% rate whose region was found.
func newStubTaxes() *stubTaxes {
	return &stubTaxes{rateBps: 2000, regionFound: true}
}

// CalculateTaxJSON decodes the request, applies the scripted rate and returns
// a schema-conforming response.
func (s *stubTaxes) CalculateTaxJSON(_ context.Context, request json.RawMessage) (json.RawMessage, error) {
	s.calls++

	var req taxRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	s.requests = append(s.requests, req)

	if s.err != nil {
		return nil, s.err
	}

	var resp taxResponse
	if s.fn != nil {
		var err error
		if resp, err = s.fn(req); err != nil {
			return nil, err
		}
		return json.Marshal(resp)
	}

	resp = taxResponse{
		RegionFound: s.regionFound,
		ProviderID:  "test",
		Items:       make([]taxResponseLine, 0, len(req.Items)),
		Shipping:    taxResponseLine{ID: "_shipping"},
	}
	rate := s.rateBps
	if !s.regionFound {
		rate = 0
	}
	for i := range req.Items {
		base := req.Items[i].Amount
		amount := base * int64(rate) / BpsScale
		resp.Items = append(resp.Items, taxResponseLine{
			ID:            req.Items[i].ID,
			RateBps:       rate,
			TaxableAmount: base,
			TaxAmount:     amount,
		})
		resp.TaxTotal += amount
	}
	return json.Marshal(resp)
}

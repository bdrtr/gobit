package checkout

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Identifier and code constants repeated across the tests.
const (
	testCartID       = "cart_1"
	testRegionID     = "reg_tr"
	testCurrency     = "TRY"
	testCustomerID   = "cus_1"
	testLocationID   = "sloc_1"
	testProviderID   = "manual"
	testVariantA     = "var_a"
	testVariantB     = "var_b"
	testLineA        = "li_a"
	testLineB        = "li_b"
	testItemA        = "inv_a"
	testItemB        = "inv_b"
	testPriceSetA    = "pset_a"
	testPriceSetB    = "pset_b"
	testTitleA       = "Red T-Shirt"
	testTitleB       = "Blue Hat"
	testOrderID      = "order_1"
	testCollectionID = "pcol_1"
	testSessionID    = "pses_1"
	testPaymentID    = "pay_1"
	testRevision     = int64(7)
	testAmount       = int64(3000)
)

// Warehouses of the tests that exercise per-line location selection.
//
// There are three of them because two warehouses are not enough: the chosen
// candidate must be able to be neither FIRST nor LAST in the list, otherwise an
// implementation saying "take the first candidate" or "take the last candidate"
// would pass the test too.
const (
	testLocationEast  = "sloc_east"
	testLocationNorth = "sloc_north"
	testLocationWest  = "sloc_west"
)

// TestMain silences the tests' default logger.
//
// [FromContainer] takes its setup logger from slog.Default (the application
// installs it at startup); the default is discarded here so that the test output
// stays readable.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// errUnexpected is the error of an unscripted fake call.
//
// The fake DOES NOT STAY SILENT when an unscripted surface is touched: had it
// returned the zero value, the claim "this workflow must never call that module"
// could rot silently while the test stayed green.
func errUnexpected(what string) error {
	return errors.Internal("test_unexpected_call", "unexpected fake call: %s", what)
}

// hasCode reports whether the given code is found in the error CHAIN.
//
// The engine wraps the step's error with its own code (workflow_step_failed),
// while errors.CodeOf sees only the OUTERMOST code. To be able to assert on the
// step's own code the chain is walked, and branches joined by errors.Join are
// followed too.
func hasCode(err error, code string) bool {
	if err == nil {
		return false
	}
	if errors.CodeOf(err) == code {
		return true
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, branch := range multi.Unwrap() {
			if hasCode(branch, code) {
				return true
			}
		}
		return false
	}
	return hasCode(errors.Unwrap(err), code)
}

// recorder records the module calls IN ARRIVAL ORDER.
//
// The claim that compensation runs in REVERSE ORDER can only be proven by the
// order; keeping a counter answers the question "did it run" but it does not
// answer the question "when".
type recorder struct {
	mu    sync.Mutex
	calls []string
}

// add records one call.
func (r *recorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

// snapshot returns a copy of the recorded calls.
func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// count returns how many times the given call was made.
func (r *recorder) count(call string) int {
	var n int
	for _, seen := range r.snapshot() {
		if seen == call {
			n++
		}
	}
	return n
}

// stubCarts is the implementation of the [Carts] interface that the tests can
// script.
//
// The type also satisfies the surface of the cart workflows ([cartwf.Carts]), so
// that the [FromContainer] test can register a SINGLE value under the name
// "cart.interop".
type stubCarts struct {
	rec *recorder

	snapshotFn      func(ctx context.Context, cartID string) (json.RawMessage, error)
	markCompletedFn func(ctx context.Context, cartID string) error
	setTotalsFn     func(ctx context.Context, cartID string) error
}

// AddShippingMethod is never called on this path and says so.
//
// It exists because the cart workflows resolve the whole cart.interop surface
// by name, so this stub has to satisfy it; the checkout saga does not add
// shipping methods — the shopper does, before completing.
func (s *stubCarts) AddShippingMethod(
	_ context.Context, _, _, _ string, _ int64, _ json.RawMessage,
) (string, error) {
	return "", errors.New("AddShippingMethod is not part of the checkout path")
}

// CartSnapshotJSON returns the scripted snapshot.
func (s *stubCarts) CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	s.rec.add("cart:snapshot")
	if s.snapshotFn == nil {
		return nil, errUnexpected("CartSnapshotJSON")
	}
	return s.snapshotFn(ctx, cartID)
}

// MarkCompleted stamps the cart as completed.
func (s *stubCarts) MarkCompleted(ctx context.Context, cartID string) error {
	s.rec.add("cart:complete")
	if s.markCompletedFn == nil {
		return nil
	}
	return s.markCompletedFn(ctx, cartID)
}

// OpenCart completes the surface of the cart workflows; this package never calls
// it.
func (s *stubCarts) OpenCart(
	_ context.Context, _, _, _, _ string, _ json.RawMessage,
) (string, error) {
	return "", errUnexpected("OpenCart")
}

// AddCartLineItem completes the surface of the cart workflows; this package
// never calls it.
func (s *stubCarts) AddCartLineItem(
	_ context.Context, _, _, _ string, _, _ int64, _ json.RawMessage,
) (string, error) {
	return "", errUnexpected("AddCartLineItem")
}

// SetCartLineItemQuantity completes the surface of the cart workflows.
func (s *stubCarts) SetCartLineItemQuantity(_ context.Context, _, _ string, _ int64) error {
	return errUnexpected("SetCartLineItemQuantity")
}

// RemoveLineItem completes the surface of the cart workflows.
func (s *stubCarts) RemoveLineItem(_ context.Context, _, _ string) error {
	return errUnexpected("RemoveLineItem")
}

// SetCartTotalsJSON completes the surface of the cart workflows.
//
// It is scripted only in the test where the REAL cart calculation runs; this
// package does not write totals onto the cart.
func (s *stubCarts) SetCartTotalsJSON(ctx context.Context, cartID string, _ json.RawMessage) error {
	s.rec.add("cart:set_totals")
	if s.setTotalsFn == nil {
		return errUnexpected("SetCartTotalsJSON")
	}
	return s.setTotalsFn(ctx, cartID)
}

// stubTotals is the implementation of the [CartTotals] interface that the tests
// can script.
type stubTotals struct {
	rec *recorder

	calculateFn func(ctx context.Context, cartID string) (cartwf.Totals, error)
}

// CalculateTotals returns the scripted calculation.
func (s *stubTotals) CalculateTotals(ctx context.Context, cartID string) (cartwf.Totals, error) {
	s.rec.add("totals:calculate")
	if s.calculateFn == nil {
		return cartwf.Totals{}, errUnexpected("CalculateTotals")
	}
	return s.calculateFn(ctx, cartID)
}

// stubInventory is the implementation of the [Inventory] interface that the
// tests can script.
type stubInventory struct {
	rec *recorder

	locationsFn func(ctx context.Context, itemID string, quantity int64) ([]string, error)
	reserveFn   func(ctx context.Context, itemID, locationID string, quantity int64, lineItemID string) (string, error)
	releaseFn   func(ctx context.Context, reservationID string) error
	confirmFn   func(ctx context.Context, reservationID string) error

	// reserved keeps the ARGUMENTS of the Reserve calls in arrival order.
	//
	// recorder answers only the question "which call, when"; per-line location
	// selection, on the other hand, raises the question "which line, from WHICH
	// warehouse", and the answer lives only in the arguments.
	reserved []reservedCall
}

// reservedCall holds the arguments of a single Reserve call.
type reservedCall struct {
	LineItemID string
	ItemID     string
	LocationID string
	Quantity   int64
}

// LocationsWithStock returns the scripted candidate location list.
//
// It has NO default: a flow whose location the caller declares must NEVER touch
// this surface, and a silent default would rot that claim while the test stayed
// green.
func (s *stubInventory) LocationsWithStock(
	ctx context.Context,
	itemID string,
	quantity int64,
) ([]string, error) {
	s.rec.add("inventory:locations:" + itemID)
	if s.locationsFn == nil {
		return nil, errUnexpected("LocationsWithStock")
	}
	return s.locationsFn(ctx, itemID, quantity)
}

// Reserve applies the scripted reservation behavior.
func (s *stubInventory) Reserve(
	ctx context.Context,
	itemID, locationID string,
	quantity int64,
	lineItemID string,
) (string, error) {
	s.rec.add("inventory:reserve:" + lineItemID)
	s.reserved = append(s.reserved, reservedCall{
		LineItemID: lineItemID,
		ItemID:     itemID,
		LocationID: locationID,
		Quantity:   quantity,
	})
	if s.reserveFn == nil {
		return "res_" + lineItemID, nil
	}
	return s.reserveFn(ctx, itemID, locationID, quantity, lineItemID)
}

// ReleaseReservation applies the scripted release behavior.
func (s *stubInventory) ReleaseReservation(ctx context.Context, reservationID string) error {
	s.rec.add("inventory:release:" + reservationID)
	if s.releaseFn == nil {
		return nil
	}
	return s.releaseFn(ctx, reservationID)
}

// ConfirmReservation applies the scripted confirmation behavior.
func (s *stubInventory) ConfirmReservation(ctx context.Context, reservationID string) error {
	s.rec.add("inventory:confirm:" + reservationID)
	if s.confirmFn == nil {
		return nil
	}
	return s.confirmFn(ctx, reservationID)
}

// stubFulfillment is the implementation of the [Fulfillment] interface that the
// tests can script.
type stubFulfillment struct {
	rec *recorder

	// listOptionsFn is the scripted shipping quote.
	//
	// The checkout saga does not quote shipping — the cart flows do, before the
	// cart is completed. It is here because the container registers ONE
	// "fulfillment.interop" and the cart workflows resolve the whole of it, so a
	// stub missing this method makes the wiring test fail with a type mismatch
	// rather than with anything about checkout.
	listOptionsFn func(ctx context.Context, request json.RawMessage) (json.RawMessage, error)

	rankFn func(ctx context.Context, destinationRegionID string, candidateLocationIDs []string) ([]string, error)

	// offered keeps, in order, the candidate lists passed to RankLocations.
	//
	// That the candidates are handed over exactly as they COME from the
	// inventory module can only be proven this way: if checkout filtered or
	// sorted the list, the workflow would still look like it "works", yet at
	// that point it would be checkout that had decided the preference order.
	offered [][]string

	// offeredRegions keeps, in order, the destination regions passed to
	// RankLocations.
	//
	// That the policy's input comes from the PLAN can only be proven this way:
	// if an empty region were passed, the real module would reject the request,
	// but the fake module does not, and the workflow would stay green.
	offeredRegions []string
}

// ListOptionsJSON applies the scripted shipping quote.
func (s *stubFulfillment) ListOptionsJSON(
	ctx context.Context, request json.RawMessage,
) (json.RawMessage, error) {
	if s.listOptionsFn == nil {
		return nil, errors.New("ListOptionsJSON is not part of the checkout path")
	}

	return s.listOptionsFn(ctx, request)
}

// RankLocations applies the scripted ranking behavior.
func (s *stubFulfillment) RankLocations(
	ctx context.Context,
	destinationRegionID string,
	candidateLocationIDs []string,
) ([]string, error) {
	s.rec.add("fulfillment:rank_locations")
	s.offered = append(s.offered, append([]string(nil), candidateLocationIDs...))
	s.offeredRegions = append(s.offeredRegions, destinationRegionID)
	if s.rankFn == nil {
		return nil, errUnexpected("RankLocations")
	}
	return s.rankFn(ctx, destinationRegionID, candidateLocationIDs)
}

// rankByGreatestID is a fulfillment surface behavior that orders the candidates
// so that the one with the GREATEST identifier comes first.
//
// The real module's TIE-BREAKING rule (smallest identifier first) is reversed
// DELIBERATELY: only this way can it be proven that the fulfillment module is
// the one that establishes the order. With a fake imitating the real rule, a
// checkout that ordered the candidates on its own would stay green too.
//
// The destination region is NOT USED: this fake does not imitate the policy, it
// proves that the policy is NOT in checkout.
func rankByGreatestID(_ context.Context, _ string, candidateLocationIDs []string) ([]string, error) {
	sorted := slices.Clone(candidateLocationIDs)
	slices.SortFunc(sorted, func(a, b string) int { return strings.Compare(b, a) })
	return sorted, nil
}

// stubOrders is the implementation of the [Orders] interface that the tests can
// script.
type stubOrders struct {
	rec *recorder

	placeFn  func(ctx context.Context, snapshot json.RawMessage) (string, error)
	cancelFn func(ctx context.Context, orderID, reason string) error

	// placed keeps, in order, the decoded snapshots passed to PlaceOrderJSON.
	placed []orderSnapshot
	// canceled holds the identifiers of the canceled orders.
	canceled []string
}

// PlaceOrderJSON decodes the incoming body, records it and returns the scripted
// result.
func (s *stubOrders) PlaceOrderJSON(ctx context.Context, snapshot json.RawMessage) (string, error) {
	s.rec.add("order:place")

	var decoded orderSnapshot
	if err := json.Unmarshal(snapshot, &decoded); err != nil {
		return "", err
	}
	s.placed = append(s.placed, decoded)

	if s.placeFn == nil {
		return testOrderID, nil
	}
	return s.placeFn(ctx, snapshot)
}

// CancelOrder applies the scripted cancellation behavior.
func (s *stubOrders) CancelOrder(ctx context.Context, orderID, reason string) error {
	s.rec.add("order:cancel")
	s.canceled = append(s.canceled, orderID)
	if s.cancelFn == nil {
		return nil
	}
	return s.cancelFn(ctx, orderID, reason)
}

// stubPayments is the implementation of the [Payments] interface that the tests
// can script.
type stubPayments struct {
	rec *recorder

	createCollectionFn func(ctx context.Context, reference, currencyCode string, amount int64) (string, error)
	openSessionFn      func(ctx context.Context, collectionID, providerID, key string, data json.RawMessage) (string, error)
	authorizeFn        func(ctx context.Context, sessionID string) (string, int64, error)
	captureFn          func(ctx context.Context, sessionID string, amount int64) (string, error)
	cancelFn           func(ctx context.Context, sessionID string) error
	collectionFn       func(ctx context.Context, collectionID string) (string, int64, int64, int64, int64, error)

	// captureAmounts keeps, in order, the amounts passed to Capture.
	captureAmounts []int64
	// sessionData keeps, in order, the bodies passed to OpenSessionWithData.
	sessionData []string
}

// CreateCollection applies the scripted collection-opening behavior.
func (s *stubPayments) CreateCollection(
	ctx context.Context,
	reference, currencyCode string,
	amount int64,
) (string, error) {
	s.rec.add("payment:collection")
	if s.createCollectionFn == nil {
		return testCollectionID, nil
	}
	return s.createCollectionFn(ctx, reference, currencyCode, amount)
}

// OpenSessionWithData applies the scripted session-opening behavior.
func (s *stubPayments) OpenSessionWithData(
	ctx context.Context,
	collectionID, providerID, idempotencyKey string,
	data json.RawMessage,
) (string, error) {
	s.rec.add("payment:session")
	s.sessionData = append(s.sessionData, string(data))
	if s.openSessionFn == nil {
		return testSessionID, nil
	}
	return s.openSessionFn(ctx, collectionID, providerID, idempotencyKey, data)
}

// Authorize applies the scripted authorization behavior.
func (s *stubPayments) Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error) {
	s.rec.add("payment:authorize")
	if s.authorizeFn == nil {
		return "authorized", testAmount, nil
	}
	return s.authorizeFn(ctx, sessionID)
}

// Capture applies the scripted capture behavior.
func (s *stubPayments) Capture(ctx context.Context, sessionID string, amount int64) (string, error) {
	s.rec.add("payment:capture")
	s.captureAmounts = append(s.captureAmounts, amount)
	if s.captureFn == nil {
		return testPaymentID, nil
	}
	return s.captureFn(ctx, sessionID, amount)
}

// Cancel applies the scripted cancellation behavior.
func (s *stubPayments) Cancel(ctx context.Context, sessionID string) error {
	s.rec.add("payment:cancel")
	if s.cancelFn == nil {
		return nil
	}
	return s.cancelFn(ctx, sessionID)
}

// Collection applies the scripted collection read.
//
//nolint:gocritic // The result count comes from the [Payments.Collection] signature; the fake has to match it exactly.
func (s *stubPayments) Collection(ctx context.Context, collectionID string) (
	status string,
	amount, authorized, captured, refunded int64,
	err error,
) {
	s.rec.add("payment:read_collection")
	if s.collectionFn == nil {
		return "captured", testAmount, 0, testAmount, 0, nil
	}
	return s.collectionFn(ctx, collectionID)
}

// stubLinks is the implementation of the [Links] interface that the tests can
// script.
type stubLinks struct {
	rec *recorder

	listManyFn func(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
}

// ListMany applies the scripted link read.
func (s *stubLinks) ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error) {
	s.rec.add("link:list_many:" + name)
	if s.listManyFn == nil {
		return nil, errUnexpected("ListMany")
	}
	return s.listManyFn(ctx, name, fromIDs)
}

// stubCatalog is the implementation of the [Catalog] interface that the tests
// can script.
type stubCatalog struct {
	rec *recorder

	graphFn func(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Graph applies the scripted catalog read.
func (s *stubCatalog) Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error) {
	s.rec.add("catalog:graph")
	if s.graphFn == nil {
		return nil, errUnexpected("Graph")
	}
	return s.graphFn(ctx, spec)
}

// harness carries every fake a test needs together with the workflow built on
// them.
type harness struct {
	rec         *recorder
	carts       *stubCarts
	totals      *stubTotals
	inventory   *stubInventory
	fulfillment *stubFulfillment
	orders      *stubOrders
	payments    *stubPayments
	links       *stubLinks
	catalog     *stubCatalog
	wf          *Workflows
}

// newHarness builds a workflow with the fakes tuned to the HAPPY PATH.
//
// Each test re-scripts only the behavior it wants to change; everything else is
// in working order. The engine is in-process (workflow.NewInMemory): idempotency
// protection holds for the duration of the test and no database is needed.
func newHarness(t *testing.T) *harness {
	t.Helper()

	rec := &recorder{}
	h := &harness{
		rec:         rec,
		carts:       &stubCarts{rec: rec, snapshotFn: defaultSnapshot},
		totals:      &stubTotals{rec: rec, calculateFn: defaultTotals},
		inventory:   &stubInventory{rec: rec},
		fulfillment: &stubFulfillment{rec: rec},
		orders:      &stubOrders{rec: rec},
		payments:    &stubPayments{rec: rec},
		links:       &stubLinks{rec: rec, listManyFn: defaultLinks},
		catalog:     &stubCatalog{rec: rec, graphFn: defaultCatalog},
	}

	wf, err := New(Deps{
		Carts:       h.carts,
		Totals:      h.totals,
		Inventory:   h.inventory,
		Fulfillment: h.fulfillment,
		Orders:      h.orders,
		Payments:    h.payments,
		Links:       h.links,
		Catalog:     h.catalog,
		Executor:    workflow.NewInMemory(slog.New(slog.DiscardHandler)),
		Logger:      slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)

	h.wf = wf
	return h
}

// input returns the happy path's input.
//
// The location is FILLED IN: the happy path preserves the behavior from before
// the field became optional, and the tests that exercise per-line selection
// empty it one by one.
func (h *harness) input() CompleteCartInput {
	return CompleteCartInput{
		CartID:            testCartID,
		LocationID:        testLocationID,
		PaymentProviderID: testProviderID,
		Email:             "customer@example.com",
	}
}

// defaultSnapshot returns the snapshot of a two-line cart.
func defaultSnapshot(_ context.Context, cartID string) (json.RawMessage, error) {
	return json.Marshal(Snapshot{
		ID:           cartID,
		RegionID:     testRegionID,
		CustomerID:   testCustomerID,
		CurrencyCode: testCurrency,
		Revision:     testRevision,
		Items: []SnapshotItem{
			{ID: testLineA, VariantID: testVariantA, Quantity: 2},
			{ID: testLineB, VariantID: testVariantB, Quantity: 1},
		},
	})
}

// defaultTotals returns a calculation CONSISTENT with the snapshot.
//
// The totals satisfy the cart identity: 2500 - 0 + 500 + 0 = 3000.
func defaultTotals(_ context.Context, _ string) (cartwf.Totals, error) {
	return cartwf.Totals{
		Revision:      testRevision,
		Subtotal:      2500,
		DiscountTotal: 0,
		TaxTotal:      500,
		ShippingTotal: 0,
		Total:         testAmount,
		Lines: []cartwf.LineTotals{
			{LineItemID: testLineA, UnitPrice: 1000, Subtotal: 2000, TaxTotal: 400, Total: 2400},
			{LineItemID: testLineB, UnitPrice: 500, Subtotal: 500, TaxTotal: 100, Total: 600},
		},
	}, nil
}

// linkVariantPriceSet is the name of the price link the cart calculation uses.
//
// This package does NOT resolve it; the constant exists only so that the link
// provider sees the right name in the test where the real cart calculation runs.
const linkVariantPriceSet = "product_variant_price_set"

// defaultLinks links the variants to inventory items and to price sets.
func defaultLinks(_ context.Context, name string, _ []string) (map[string][]string, error) {
	switch name {
	case LinkVariantInventory:
		return map[string][]string{
			testVariantA: {testItemA},
			testVariantB: {testItemB},
		}, nil
	case linkVariantPriceSet:
		return map[string][]string{
			testVariantA: {testPriceSetA},
			testVariantB: {testPriceSetB},
		}, nil
	default:
		return nil, errUnexpected("ListMany: " + name)
	}
}

// defaultCatalog returns the titles of the variants.
func defaultCatalog(_ context.Context, _ query.GraphSpec) ([]query.Record, error) {
	return []query.Record{
		{query.IDField: testVariantA, FieldTitle: testTitleA},
		{query.IDField: testVariantB, FieldTitle: testTitleB},
	}, nil
}

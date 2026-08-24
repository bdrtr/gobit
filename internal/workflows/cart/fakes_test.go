package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Testlerde tekrarlanan kimlik ve kod sabitleri.
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

// errUnexpected betiklenmemiş bir sahte çağrısının hatasıdır.
//
// Sahte, betiklenmemiş bir yüzeye dokunulduğunda SESSİZ KALMAZ: sıfır değer
// dönseydi, "bu akış o modülü hiç çağırmamalı" iddiası test yeşilken sessizce
// çürüyebilirdi.
func errUnexpected(what string) error {
	return errors.Internal("test_unexpected_call", "beklenmeyen sahte çağrısı: %s", what)
}

// stubCarts [Carts] arayüzünün testlerde betiklenebilen uygulamasıdır.
type stubCarts struct {
	openCartFn  func(ctx context.Context, regionID, currencyCode, customerID, email string) (string, error)
	snapshotFn  func(ctx context.Context, cartID string) (json.RawMessage, error)
	addLineFn   func(ctx context.Context, cartID, variantID, title string, quantity, unitPrice int64) (string, error)
	setQtyFn    func(ctx context.Context, cartID, lineItemID string, quantity int64) error
	removeFn    func(ctx context.Context, cartID, lineItemID string) error
	setTotalsFn func(ctx context.Context, cartID string, totals json.RawMessage) error

	// written SetCartTotalsJSON'a geçen çözülmüş toplamları sırayla tutar.
	written []Totals
	// snapshotCalls anlık görüntünün kaç kez okunduğunu sayar; yeniden deneme
	// iddiası bununla kanıtlanır.
	snapshotCalls int
	// removed ve quantities yazma yolunun hangisinin seçildiğini kaydeder.
	removed    []string
	quantities map[string]int64
}

// newStubCarts boş bir sahte sepet servisi üretir.
func newStubCarts() *stubCarts {
	return &stubCarts{quantities: map[string]int64{}}
}

// OpenCart betiklenen sepet açma davranışını uygular.
func (s *stubCarts) OpenCart(ctx context.Context, regionID, currencyCode, customerID, email string) (string, error) {
	if s.openCartFn == nil {
		return "", errUnexpected("OpenCart")
	}
	return s.openCartFn(ctx, regionID, currencyCode, customerID, email)
}

// CartSnapshotJSON betiklenen anlık görüntüyü döner ve çağrıyı sayar.
func (s *stubCarts) CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	s.snapshotCalls++
	if s.snapshotFn == nil {
		return nil, errUnexpected("CartSnapshotJSON")
	}
	return s.snapshotFn(ctx, cartID)
}

// AddCartLineItem betiklenen satır ekleme davranışını uygular.
func (s *stubCarts) AddCartLineItem(
	ctx context.Context,
	cartID, variantID, title string,
	quantity, unitPrice int64,
) (string, error) {
	if s.addLineFn == nil {
		return "", errUnexpected("AddCartLineItem")
	}
	return s.addLineFn(ctx, cartID, variantID, title, quantity, unitPrice)
}

// SetCartLineItemQuantity betiklenen adet yazma davranışını uygular.
func (s *stubCarts) SetCartLineItemQuantity(ctx context.Context, cartID, lineItemID string, quantity int64) error {
	s.quantities[lineItemID] = quantity
	if s.setQtyFn == nil {
		return nil
	}
	return s.setQtyFn(ctx, cartID, lineItemID, quantity)
}

// RemoveLineItem betiklenen satır kaldırma davranışını uygular.
func (s *stubCarts) RemoveLineItem(ctx context.Context, cartID, lineItemID string) error {
	s.removed = append(s.removed, lineItemID)
	if s.removeFn == nil {
		return nil
	}
	return s.removeFn(ctx, cartID, lineItemID)
}

// SetCartTotalsJSON gelen gövdeyi çözer, kaydeder ve betiklenen sonucu döner.
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

// stubPrices [Prices] arayüzünün sahte uygulamasıdır.
type stubPrices struct {
	// amounts fiyat kümesi -> birim tutar eşlemesidir.
	amounts map[string]int64
	// fn verilirse amounts yerine bu kullanılır.
	fn func(ctx context.Context, priceSetID, currencyCode string, quantity int32, attrs map[string]string) (int64, error)
	// seen yapılan çağrıların bağlamını sırayla tutar.
	seen []priceCall
}

// priceCall tek bir fiyat çağrısının kaydıdır.
type priceCall struct {
	priceSetID   string
	currencyCode string
	quantity     int32
	attributes   map[string]string
}

// CalculateAmount betiklenen birim fiyatı döner.
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
			"%s için %s para biriminde fiyat yok", priceSetID, currencyCode)
	}
	return amount, nil
}

// stubRegions [Regions] arayüzünün sahte uygulamasıdır.
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

// newStubRegions %20 otomatik vergili varsayılan bir bölge sahtesi üretir.
func newStubRegions() *stubRegions {
	return &stubRegions{
		regionByCountry: map[string]string{"TR": testRegionID},
		currency:        testCurrency,
		decimalDigits:   2,
		rateBps:         2000,
		automatic:       true,
	}
}

// RegionIDForCountry ülke kodundan bölge kimliğini döner.
func (s *stubRegions) RegionIDForCountry(_ context.Context, countryCode string) (string, error) {
	if s.regionErr != nil {
		return "", s.regionErr
	}
	id, ok := s.regionByCountry[countryCode]
	if !ok {
		return "", errors.NotFound("region_not_found", "%q ülkesinin bölgesi yok", countryCode)
	}
	return id, nil
}

// RegionCurrency bölgenin para birimini ve ondalık basamağını döner.
func (s *stubRegions) RegionCurrency(_ context.Context, _ string) (code string, decimalDigits int32, err error) {
	if s.currencyErr != nil {
		return "", 0, s.currencyErr
	}
	return s.currency, s.decimalDigits, nil
}

// RegionTax bölgenin vergi oranını ve otomatiklik bayrağını döner.
func (s *stubRegions) RegionTax(_ context.Context, _ string) (rateBps int32, automatic bool, err error) {
	if s.taxErr != nil {
		return 0, false, s.taxErr
	}
	return s.rateBps, s.automatic, nil
}

// stubCustomers [Customers] arayüzünün sahte uygulamasıdır.
type stubCustomers struct {
	emails map[string]string
	calls  int
}

// CustomerEmail müşterinin e-postasını döner; müşteri yoksa NotFound.
func (s *stubCustomers) CustomerEmail(_ context.Context, customerID string) (string, error) {
	s.calls++
	email, ok := s.emails[customerID]
	if !ok {
		return "", errors.NotFound("customer_not_found", "müşteri yok: %s", customerID)
	}
	return email, nil
}

// stubLinks [Links] arayüzünün sahte uygulamasıdır.
type stubLinks struct {
	// links varyant -> fiyat kümesi kimlikleri eşlemesidir.
	links map[string][]string
	err   error
	// batches her çağrıda istenen kaynak kimlikleri tutar; toplu sorgu
	// iddiası bununla kanıtlanır.
	batches [][]string
}

// ListMany verilen kaynak kimliklerin bağlarını döner.
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

// stubCatalog [Catalog] arayüzünün sahte uygulamasıdır.
type stubCatalog struct {
	// titles varyant -> başlık eşlemesidir.
	titles map[string]string
	err    error
	specs  []query.GraphSpec
}

// Graph varyant kayıtlarını döner.
func (s *stubCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	s.specs = append(s.specs, spec)
	if s.err != nil {
		return nil, s.err
	}

	id, ok := spec.Filters[query.IDField].(string)
	if !ok {
		return nil, errors.Invalid("test_bad_filter", "kimlik filtresi dizge değil")
	}
	title, ok := s.titles[id]
	if !ok {
		return []query.Record{}, nil
	}
	return []query.Record{{query.IDField: id, FieldTitle: title}}, nil
}

// harness bir testin sahte bağımlılıklarını ve kurulmuş akışlarını taşır.
type harness struct {
	carts     *stubCarts
	prices    *stubPrices
	regions   *stubRegions
	customers *stubCustomers
	links     *stubLinks
	catalog   *stubCatalog
	wf        *Workflows
}

// newHarness varsayılan sahtelerle çalışan bir test düzeneği kurar.
func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		carts:     newStubCarts(),
		prices:    &stubPrices{amounts: map[string]int64{testPriceSetA: 1000, testPriceSetB: 250}},
		regions:   newStubRegions(),
		customers: &stubCustomers{emails: map[string]string{testCustomerID: "kayitli@example.com"}},
		links: &stubLinks{links: map[string][]string{
			testVariantA: {testPriceSetA},
			testVariantB: {testPriceSetB},
		}},
		catalog: &stubCatalog{titles: map[string]string{
			testVariantA: "Kırmızı Tişört / M",
			testVariantB: "Mavi Çorap",
		}},
	}

	wf, err := New(Deps{
		Carts:     h.carts,
		Prices:    h.prices,
		Regions:   h.regions,
		Customers: h.customers,
		Links:     h.links,
		Catalog:   h.catalog,
	})
	require.NoError(t, err)
	h.wf = wf
	return h
}

// snapshotOf verilen alanlarla bir sepet anlık görüntüsü üretir.
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

// serveSnapshot sahte sepeti, verilen görüntüleri SIRAYLA dönecek biçimde
// betikler; son görüntü tükendikten sonra yine sonuncusu döner.
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

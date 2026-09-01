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
	openCartFn  func(ctx context.Context, regionID, currencyCode, customerID, email string, metadata json.RawMessage) (string, error)
	snapshotFn  func(ctx context.Context, cartID string) (json.RawMessage, error)
	addLineFn   func(ctx context.Context, cartID, variantID, title string, quantity, unitPrice int64, metadata json.RawMessage) (string, error)
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
	metadata json.RawMessage,
) (string, error) {
	if s.addLineFn == nil {
		return "", errUnexpected("AddCartLineItem")
	}
	return s.addLineFn(ctx, cartID, variantID, title, quantity, unitPrice, metadata)
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
//
// İki entity'ye cevap verir: varyantlar (başlık) ve bölgeler (ülke kodları).
// İkisi tek sahtede durur çünkü gerçekte de tek bir yüzeydir ("core.query");
// ayırmak, akışın iki ayrı bağımlılığı varmış izlenimi verirdi.
type stubCatalog struct {
	// titles varyant -> başlık eşlemesidir.
	titles map[string]string
	// countries bölge -> ülke kodları eşlemesidir.
	countries map[string][]string
	// regionErr verilirse bölge sorgusu bu hatayla düşer.
	regionErr error
	err       error
	specs     []query.GraphSpec
}

// Graph varyant ya da bölge kayıtlarını döner.
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
		return nil, errors.Invalid("test_bad_filter", "kimlik filtresi dizge değil")
	}
	title, ok := s.titles[id]
	if !ok {
		return []query.Record{}, nil
	}
	return []query.Record{{query.IDField: id, FieldTitle: title}}, nil
}

// regionRecords bölge kaydını ülke alt kayıtlarıyla döner.
func (s *stubCatalog) regionRecords(spec query.GraphSpec) ([]query.Record, error) {
	if s.regionErr != nil {
		return nil, s.regionErr
	}

	id, ok := spec.Filters[query.IDField].(string)
	if !ok {
		return nil, errors.Invalid("test_bad_filter", "kimlik filtresi dizge değil")
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

// harness bir testin sahte bağımlılıklarını ve kurulmuş akışlarını taşır.
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

// newHarness promotion ve tax yüzeyleri KAYITSIZ bir düzenek kurar.
//
// Varsayılanın degrade yol olması bilinçlidir: Faz 5'te yazılmış testlerin
// tamamı bu düzenekle koşar ve hiçbiri değişmez, yani devralmanın var olan
// davranışı bozmadığı her koşuda yeniden kanıtlanır. İki modüllü düzenek için
// bkz. [newModulHarness].
func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, nil, nil)
}

// newModulHarness promotion ve tax yüzeyleri KAYITLI bir düzenek kurar.
func newModulHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, &stubDiscounts{perLine: map[string]int64{}}, newStubTaxes())
}

// newHarnessWith verilen opsiyonel yüzeylerle bir düzenek kurar; nil verilen
// yüzey kayıtsız sayılır.
func newHarnessWith(t *testing.T, discounts *stubDiscounts, taxes *stubTaxes) *harness {
	t.Helper()

	h := &harness{
		carts:     newStubCarts(),
		prices:    &stubPrices{amounts: map[string]int64{testPriceSetA: 1000, testPriceSetB: 250}},
		regions:   newStubRegions(),
		customers: &stubCustomers{emails: map[string]string{testCustomerID: "kayitli@example.com"}},
		discounts: discounts,
		taxes:     taxes,
		links: &stubLinks{links: map[string][]string{
			testVariantA: {testPriceSetA},
			testVariantB: {testPriceSetB},
		}},
		catalog: &stubCatalog{
			titles: map[string]string{
				testVariantA: "Kırmızı Tişört / M",
				testVariantB: "Mavi Çorap",
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
	// nil arayüz DEĞERİ ile nil arayüz TİPİ farkı: h.discounts nil bir
	// *stubDiscounts ise Deps.Discounts alanına konduğunda arayüz artık nil
	// OLMAZ ve degradasyon yolu hiç çalışmazdı.
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

// stubDiscounts [Discounts] arayüzünün sahte uygulamasıdır.
//
// Varsayılan davranışı "hiç promosyon yok"tur: her satır için sıfır indirim
// döner. Sahte, gerçek promotion modülünün YANIT DEĞİŞMEZLERİNİ taklit eder
// (istekteki her satır için aynı sırada bir kayıt, tutarlı toplamlar); aksi
// hâlde testler, üretimde asla oluşmayacak bir gövdeyle geçerdi.
type stubDiscounts struct {
	// perLine satır kimliği -> indirim eşlemesidir.
	perLine map[string]int64
	// fn verilirse gövde tamamen bu işlevce üretilir; bozuk yanıt senaryoları
	// için vardır.
	fn func(request discountRequest) (discountResponse, error)
	// err verilirse çağrı bu hatayla düşer.
	err error
	// requests yapılan çağrıların çözülmüş gövdelerini sırayla tutar.
	requests []discountRequest
	// calls çağrı sayısıdır.
	calls int
}

// ComputeDiscountsJSON isteği çözer, betiklenen indirimi uygular ve şemaya
// uygun bir yanıt döner.
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

// stubTaxes [Taxes] arayüzünün sahte uygulamasıdır.
//
// Varsayılan davranışı, ülkeye bakmaksızın tek bir baz puan oranını KALEM
// BAŞINA ve AŞAĞI yuvarlayarak uygulamaktır; gerçek tax modülünün yerel
// sağlayıcısı da aynı aritmetiği kullanır.
type stubTaxes struct {
	// rateBps uygulanacak orandır (baz puan).
	rateBps int32
	// regionFound ülkenin vergi bölgesinin bulunup bulunmadığını bildirir.
	regionFound bool
	// fn verilirse gövde tamamen bu işlevce üretilir.
	fn func(request taxRequest) (taxResponse, error)
	// err verilirse çağrı bu hatayla düşer.
	err error
	// requests yapılan çağrıların çözülmüş gövdelerini sırayla tutar.
	requests []taxRequest
	// calls çağrı sayısıdır.
	calls int
}

// newStubTaxes %20 oranlı, bölgesi bulunmuş bir vergi sahtesi üretir.
func newStubTaxes() *stubTaxes {
	return &stubTaxes{rateBps: 2000, regionFound: true}
}

// CalculateTaxJSON isteği çözer, betiklenen oranı uygular ve şemaya uygun bir
// yanıt döner.
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

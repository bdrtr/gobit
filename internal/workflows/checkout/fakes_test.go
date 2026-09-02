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

// Testlerde tekrarlanan kimlik ve kod sabitleri.
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
	testTitleA       = "Kırmızı Tişört"
	testTitleB       = "Mavi Şapka"
	testOrderID      = "order_1"
	testCollectionID = "pcol_1"
	testSessionID    = "pses_1"
	testPaymentID    = "pay_1"
	testRevision     = int64(7)
	testAmount       = int64(3000)
)

// Satır başına lokasyon seçimini sınayan testlerin depoları.
//
// Üç tanedir çünkü iki depo yetmez: seçilen aday listede ne İLK ne SON sırada
// olabilmelidir, aksi hâlde "ilk adayı al" ya da "son adayı al" diyen bir
// uygulama da testi geçerdi.
const (
	testLocationEast  = "sloc_east"
	testLocationNorth = "sloc_north"
	testLocationWest  = "sloc_west"
)

// TestMain testlerin varsayılan logger'ını susturur.
//
// [FromContainer] kurulum logger'ını slog.Default'tan alır (uygulama açılışta
// onu kurar); test çıktısının okunabilir kalması için varsayılan burada
// atılır.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// errUnexpected betiklenmemiş bir sahte çağrısının hatasıdır.
//
// Sahte, betiklenmemiş bir yüzeye dokunulduğunda SESSİZ KALMAZ: sıfır değer
// dönseydi, "bu akış o modülü hiç çağırmamalı" iddiası test yeşilken sessizce
// çürüyebilirdi.
func errUnexpected(what string) error {
	return errors.Internal("test_unexpected_call", "beklenmeyen sahte çağrısı: %s", what)
}

// hasCode hata ZİNCİRİNDE verilen kodun bulunup bulunmadığını söyler.
//
// Motor, adımın hatasını kendi koduyla (workflow_step_failed) sarar;
// errors.CodeOf ise yalnızca EN DIŞTAKİ kodu görür. Adımın kendi kodunu
// sınayabilmek için zincir dolaşılır ve errors.Join ile birleşmiş dallar da
// izlenir.
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

// recorder modül çağrılarını GELİŞ SIRASINDA kaydeder.
//
// Telafinin TERS SIRADA çalıştığı iddiası ancak sırayla kanıtlanabilir; sayaç
// tutmak "çalıştı mı" sorusunu yanıtlar ama "ne zaman" sorusunu yanıtlamaz.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

// add bir çağrıyı kaydeder.
func (r *recorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

// snapshot kaydedilen çağrıların kopyasını döner.
func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// count verilen çağrının kaç kez yapıldığını döner.
func (r *recorder) count(call string) int {
	var n int
	for _, seen := range r.snapshot() {
		if seen == call {
			n++
		}
	}
	return n
}

// stubCarts [Carts] arayüzünün testlerde betiklenebilen uygulamasıdır.
//
// Tip aynı zamanda sepet akışlarının ([cartwf.Carts]) yüzeyini de karşılar;
// [FromContainer] testi "cart.interop" adına TEK bir değer koyabilsin diye.
type stubCarts struct {
	rec *recorder

	snapshotFn      func(ctx context.Context, cartID string) (json.RawMessage, error)
	markCompletedFn func(ctx context.Context, cartID string) error
	setTotalsFn     func(ctx context.Context, cartID string) error
}

// CartSnapshotJSON betiklenen anlık görüntüyü döner.
func (s *stubCarts) CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	s.rec.add("cart:snapshot")
	if s.snapshotFn == nil {
		return nil, errUnexpected("CartSnapshotJSON")
	}
	return s.snapshotFn(ctx, cartID)
}

// MarkCompleted sepeti tamamlanmış damgalar.
func (s *stubCarts) MarkCompleted(ctx context.Context, cartID string) error {
	s.rec.add("cart:complete")
	if s.markCompletedFn == nil {
		return nil
	}
	return s.markCompletedFn(ctx, cartID)
}

// OpenCart sepet akışlarının yüzeyini tamamlar; bu paket onu çağırmaz.
func (s *stubCarts) OpenCart(
	_ context.Context, _, _, _, _ string, _ json.RawMessage,
) (string, error) {
	return "", errUnexpected("OpenCart")
}

// AddCartLineItem sepet akışlarının yüzeyini tamamlar; bu paket onu çağırmaz.
func (s *stubCarts) AddCartLineItem(
	_ context.Context, _, _, _ string, _, _ int64, _ json.RawMessage,
) (string, error) {
	return "", errUnexpected("AddCartLineItem")
}

// SetCartLineItemQuantity sepet akışlarının yüzeyini tamamlar.
func (s *stubCarts) SetCartLineItemQuantity(_ context.Context, _, _ string, _ int64) error {
	return errUnexpected("SetCartLineItemQuantity")
}

// RemoveLineItem sepet akışlarının yüzeyini tamamlar.
func (s *stubCarts) RemoveLineItem(_ context.Context, _, _ string) error {
	return errUnexpected("RemoveLineItem")
}

// SetCartTotalsJSON sepet akışlarının yüzeyini tamamlar.
//
// Yalnızca GERÇEK sepet hesabının koştuğu testte betiklenir; bu paket sepete
// toplam yazmaz.
func (s *stubCarts) SetCartTotalsJSON(ctx context.Context, cartID string, _ json.RawMessage) error {
	s.rec.add("cart:set_totals")
	if s.setTotalsFn == nil {
		return errUnexpected("SetCartTotalsJSON")
	}
	return s.setTotalsFn(ctx, cartID)
}

// stubTotals [CartTotals] arayüzünün testlerde betiklenebilen uygulamasıdır.
type stubTotals struct {
	rec *recorder

	calculateFn func(ctx context.Context, cartID string) (cartwf.Totals, error)
}

// CalculateTotals betiklenen hesabı döner.
func (s *stubTotals) CalculateTotals(ctx context.Context, cartID string) (cartwf.Totals, error) {
	s.rec.add("totals:calculate")
	if s.calculateFn == nil {
		return cartwf.Totals{}, errUnexpected("CalculateTotals")
	}
	return s.calculateFn(ctx, cartID)
}

// stubInventory [Inventory] arayüzünün testlerde betiklenebilen uygulamasıdır.
type stubInventory struct {
	rec *recorder

	locationsFn func(ctx context.Context, itemID string, quantity int64) ([]string, error)
	reserveFn   func(ctx context.Context, itemID, locationID string, quantity int64, lineItemID string) (string, error)
	releaseFn   func(ctx context.Context, reservationID string) error
	confirmFn   func(ctx context.Context, reservationID string) error

	// reserved Reserve çağrılarının ARGÜMANLARINI geliş sırasında tutar.
	//
	// recorder yalnızca "hangi çağrı ne zaman" sorusunu yanıtlar; satır başına
	// lokasyon seçimi ise "hangi satır HANGİ depodan" sorusunu sordurur ve
	// cevabı yalnızca argümanlarda vardır.
	reserved []reservedCall
}

// reservedCall tek bir Reserve çağrısının argümanlarıdır.
type reservedCall struct {
	LineItemID string
	ItemID     string
	LocationID string
	Quantity   int64
}

// LocationsWithStock betiklenen aday lokasyon listesini döner.
//
// Varsayılanı YOKTUR: lokasyonu çağıranın bildirdiği akış bu yüzeye HİÇ
// dokunmamalıdır ve sessiz bir varsayılan, o iddiayı test yeşilken çürütürdü.
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

// Reserve betiklenen ayırma davranışını uygular.
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

// ReleaseReservation betiklenen geri bırakma davranışını uygular.
func (s *stubInventory) ReleaseReservation(ctx context.Context, reservationID string) error {
	s.rec.add("inventory:release:" + reservationID)
	if s.releaseFn == nil {
		return nil
	}
	return s.releaseFn(ctx, reservationID)
}

// ConfirmReservation betiklenen kesinleştirme davranışını uygular.
func (s *stubInventory) ConfirmReservation(ctx context.Context, reservationID string) error {
	s.rec.add("inventory:confirm:" + reservationID)
	if s.confirmFn == nil {
		return nil
	}
	return s.confirmFn(ctx, reservationID)
}

// stubFulfillment [Fulfillment] arayüzünün testlerde betiklenebilen
// uygulamasıdır.
type stubFulfillment struct {
	rec *recorder

	rankFn func(ctx context.Context, destinationRegionID string, candidateLocationIDs []string) ([]string, error)

	// offered RankLocations'a geçen aday listelerini sırayla tutar.
	//
	// Adayların stok modülünden GELDİĞİ gibi geçtiği ancak böyle
	// kanıtlanabilir: checkout listeyi süzse ya da sıralasa akış yine
	// "çalışır" görünürdü, oysa o an tercih sırasını checkout belirlemiş
	// olurdu.
	offered [][]string

	// offeredRegions RankLocations'a geçen hedef bölgeleri sırayla tutar.
	//
	// Politikanın girdisinin PLANDAN geldiği ancak böyle kanıtlanabilir: boş
	// bir bölge geçirilse gerçek modül isteği düşürürdü, ama sahte modül
	// düşürmez ve akış yeşil kalırdı.
	offeredRegions []string
}

// RankLocations betiklenen sıralama davranışını uygular.
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

// rankByGreatestID adayları kimliği EN BÜYÜK olan başta olacak şekilde dizen
// bir kargo yüzeyi davranışıdır.
//
// Gerçek modülün EŞİTLİK BOZMA kuralı (en küçük kimlik önce) BİLİNÇLİ OLARAK
// tersine çevrilmiştir: sırayı kargo modülünün kurduğu ancak böyle
// kanıtlanabilir. Gerçek kuralı taklit eden bir sahteyle, adayları kendi sıraya
// dizen bir checkout da yeşil kalırdı.
//
// Hedef bölge KULLANILMAZ: bu sahte politikayı taklit etmez, politikanın
// checkout'ta OLMADIĞINI kanıtlar.
func rankByGreatestID(_ context.Context, _ string, candidateLocationIDs []string) ([]string, error) {
	sirali := slices.Clone(candidateLocationIDs)
	slices.SortFunc(sirali, func(a, b string) int { return strings.Compare(b, a) })
	return sirali, nil
}

// stubOrders [Orders] arayüzünün testlerde betiklenebilen uygulamasıdır.
type stubOrders struct {
	rec *recorder

	placeFn  func(ctx context.Context, snapshot json.RawMessage) (string, error)
	cancelFn func(ctx context.Context, orderID, reason string) error

	// placed PlaceOrderJSON'a geçen çözülmüş görüntüleri sırayla tutar.
	placed []orderSnapshot
	// canceled iptal edilen sipariş kimlikleridir.
	canceled []string
}

// PlaceOrderJSON gelen gövdeyi çözer, kaydeder ve betiklenen sonucu döner.
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

// CancelOrder betiklenen iptal davranışını uygular.
func (s *stubOrders) CancelOrder(ctx context.Context, orderID, reason string) error {
	s.rec.add("order:cancel")
	s.canceled = append(s.canceled, orderID)
	if s.cancelFn == nil {
		return nil
	}
	return s.cancelFn(ctx, orderID, reason)
}

// stubPayments [Payments] arayüzünün testlerde betiklenebilen uygulamasıdır.
type stubPayments struct {
	rec *recorder

	createCollectionFn func(ctx context.Context, reference, currencyCode string, amount int64) (string, error)
	openSessionFn      func(ctx context.Context, collectionID, providerID, key string, data json.RawMessage) (string, error)
	authorizeFn        func(ctx context.Context, sessionID string) (string, int64, error)
	captureFn          func(ctx context.Context, sessionID string, amount int64) (string, error)
	cancelFn           func(ctx context.Context, sessionID string) error
	collectionFn       func(ctx context.Context, collectionID string) (string, int64, int64, int64, int64, error)

	// captureAmounts Capture'a geçen tutarları sırayla tutar.
	captureAmounts []int64
	// sessionData OpenSessionWithData'ya geçen gövdeleri sırayla tutar.
	sessionData []string
}

// CreateCollection betiklenen koleksiyon açma davranışını uygular.
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

// OpenSessionWithData betiklenen oturum açma davranışını uygular.
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

// Authorize betiklenen yetkilendirme davranışını uygular.
func (s *stubPayments) Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error) {
	s.rec.add("payment:authorize")
	if s.authorizeFn == nil {
		return "authorized", testAmount, nil
	}
	return s.authorizeFn(ctx, sessionID)
}

// Capture betiklenen tahsilat davranışını uygular.
func (s *stubPayments) Capture(ctx context.Context, sessionID string, amount int64) (string, error) {
	s.rec.add("payment:capture")
	s.captureAmounts = append(s.captureAmounts, amount)
	if s.captureFn == nil {
		return testPaymentID, nil
	}
	return s.captureFn(ctx, sessionID, amount)
}

// Cancel betiklenen iptal davranışını uygular.
func (s *stubPayments) Cancel(ctx context.Context, sessionID string) error {
	s.rec.add("payment:cancel")
	if s.cancelFn == nil {
		return nil
	}
	return s.cancelFn(ctx, sessionID)
}

// Collection betiklenen koleksiyon okumasını uygular.
//
//nolint:gocritic // Sonuç sayısı [Payments.Collection] imzasından gelir; sahte onu birebir karşılamak zorunda.
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

// stubLinks [Links] arayüzünün testlerde betiklenebilen uygulamasıdır.
type stubLinks struct {
	rec *recorder

	listManyFn func(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
}

// ListMany betiklenen bağ okumasını uygular.
func (s *stubLinks) ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error) {
	s.rec.add("link:list_many:" + name)
	if s.listManyFn == nil {
		return nil, errUnexpected("ListMany")
	}
	return s.listManyFn(ctx, name, fromIDs)
}

// stubCatalog [Catalog] arayüzünün testlerde betiklenebilen uygulamasıdır.
type stubCatalog struct {
	rec *recorder

	graphFn func(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Graph betiklenen katalog okumasını uygular.
func (s *stubCatalog) Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error) {
	s.rec.add("catalog:graph")
	if s.graphFn == nil {
		return nil, errUnexpected("Graph")
	}
	return s.graphFn(ctx, spec)
}

// harness bir testin ihtiyaç duyduğu tüm sahteleri ve kurulu akışı taşır.
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

// newHarness MUTLU YOL'a ayarlanmış sahtelerle bir akış kurar.
//
// Her test yalnızca değiştirmek istediği davranışı yeniden betikler; geri kalan
// her şey çalışır durumdadır. Motor süreç içidir (workflow.NewInMemory):
// idempotency koruması testin süresince geçerlidir ve veritabanı gerekmez.
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

// input mutlu yolun girdisini döner.
//
// Lokasyon DOLUDUR: mutlu yol, alanın opsiyonel hâle gelmesinden önceki
// davranışı korur ve satır başına seçimi sınayan testler onu tek tek boşaltır.
func (h *harness) input() CompleteCartInput {
	return CompleteCartInput{
		CartID:            testCartID,
		LocationID:        testLocationID,
		PaymentProviderID: testProviderID,
		Email:             "musteri@example.com",
	}
}

// defaultSnapshot iki satırlı bir sepetin anlık görüntüsünü döner.
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

// defaultTotals anlık görüntüyle TUTARLI bir hesap döner.
//
// Toplamlar sepetin kimliğini sağlar: 2500 - 0 + 500 + 0 = 3000.
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

// linkVariantPriceSet sepet hesabının kullandığı fiyat bağının adıdır.
//
// Bu paket onu ÇÖZMEZ; sabit yalnızca gerçek sepet hesabının koştuğu testte
// bağ sağlayıcısının doğru adı görmesi için vardır.
const linkVariantPriceSet = "product_variant_price_set"

// defaultLinks varyantları stok kalemlerine ve fiyat kümelerine bağlar.
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

// defaultCatalog varyantların başlıklarını döner.
func defaultCatalog(_ context.Context, _ query.GraphSpec) ([]query.Record, error) {
	return []query.Record{
		{query.IDField: testVariantA, FieldTitle: testTitleA},
		{query.IDField: testVariantB, FieldTitle: testTitleB},
	}, nil
}

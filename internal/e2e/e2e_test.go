//go:build integration

// Package e2e verifies the plan's Phase 5, Phase 6 and Phase 7 DoDs end to end
// with the REAL modules.
//
// The Phase 5 DoD in one sentence: "Create a cart -> add a product -> update the
// quantity -> subtotal / discount / tax / grand total are computed CORRECTLY;
// GUEST and REGISTERED CUSTOMER scenarios are tested."
//
// The Phase 6 DoD in one sentence: "The end-to-end cart -> order flow works with
// the test provider; while the payment step fails the STOCK RESERVATION AND THE
// ORDER ARE ROLLED BACK (saga test); the order.placed event is published."
//
// The Phase 7 DoD in one sentence: "A fulfillment can be created for an order; a
// discount can be applied to a cart and the total is updated CORRECTLY; tax is
// computed per region."
//
// # Why the Phase 7 handover does not change the Phase 5/6 amounts
//
// In Phase 7 the two stopgaps in internal/workflows/cart were handed over: the
// discount now comes from promotion and the tax from the tax module. Even so, the
// hand-written amounts of the Phase 5 and Phase 6 scenarios stayed THE SAME, and
// that is not a coincidence but a requirement of the fixture.
//
// On the tax side: [setUpTaxFixtures] installs a DEFAULT rate of 20% in the tax
// module for [taxedCountry], so the new authority gives the same answer as the old
// one. Had the rate not been installed, tax would have said "this country has no
// tax region", the tax would have dropped to zero and every Phase 5/6 amount
// would have shifted — the old tests are thereby also an audit of the handover
// being wired correctly.
//
// On the discount side the promotion's TARGET RULE provides the same protection:
// the Phase 7 scenario's automatic promotion only lands on its own variants
// (see indirim_test.go), so the discount of the other scenarios stays zero. Had
// the rule not been set, a single automatic promotion would have lowered other
// scenarios' totals too, depending on the order in which the tests run.
//
// # Why not under internal/workflows
//
// ADR 0006 does not let ANY package under internal/workflows import
// internal/modules, and TestWorkflowsDoNotImportModules in internal/arch audits
// that on the file system — test files included. This package's job is the exact
// opposite: to set up the real modules, apply the real migrations and run the
// workflows on top of that ground. The two cannot live in the same tree, so the
// package sits under internal/e2e and is outside the scope of ADR 0006.
//
// # Setup
//
// The tests share a single PostgreSQL container (testcontainers) and the setup
// FOLLOWS the order in cmd/server/main.go: the core services are registered in
// the container by name (core.db, core.link, core.query, core.eventbus,
// core.workflow), the core migrations are applied, the modules are brought up
// with [module.Registry] and the workflows are built from surfaces resolved BY
// NAME out of the container. The setup being real is the entire value of the
// test: a computation that passes with a fake dependency does not prove it will
// make the same computation in production.
//
// The ground also installs the SEARCH PLUGIN in the production order (Install ->
// Bootstrap -> Start): the module the plugin brings goes through the same
// lifecycle as the core modules and its subscriptions are wired before the first
// product. The whole decision, and why the payment plugin is NOT installed on the
// ground, is at the top of arama_test.go.
//
// The saga engine does not run IN MEMORY but on pgstore, as in production
// (core.workflow.store). The difference changes what the test sees: the
// idempotency key and the execution state really are written to the database, so
// the claim "the same cart cannot be completed twice" exercises the behaviour of
// a durable record rather than that of an in-process map.
//
// # Why the expected amounts are written by hand
//
// The subtotal, tax and grand total in every scenario are CONSTANTS computed by
// hand INSIDE the test. Repeating the production code's formula in the test (for
// example computing the tax again as "base × rate / 10000") would be making the
// same mistake in two places at once, and the test would stay blind.
//
// # Money
//
// All amounts are INTEGER minor units (plan Section 8); there are no floats in
// the test either.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/eventbus/outbox"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	authmod "github.com/bdrtr/gobit/internal/modules/auth"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	b2bmod "github.com/bdrtr/gobit/internal/modules/b2b"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	cartmod "github.com/bdrtr/gobit/internal/modules/cart"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	customermod "github.com/bdrtr/gobit/internal/modules/customer"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	filemod "github.com/bdrtr/gobit/internal/modules/file"
	fulfillmentmod "github.com/bdrtr/gobit/internal/modules/fulfillment"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	inventorymod "github.com/bdrtr/gobit/internal/modules/inventory"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	notificationmod "github.com/bdrtr/gobit/internal/modules/notification"
	ordermod "github.com/bdrtr/gobit/internal/modules/order"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmod "github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	pricingmod "github.com/bdrtr/gobit/internal/modules/pricing"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmod "github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	promotionmod "github.com/bdrtr/gobit/internal/modules/promotion"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	regionmod "github.com/bdrtr/gobit/internal/modules/region"
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
	taxmod "github.com/bdrtr/gobit/internal/modules/tax"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// postgresImage is the database image the tests share; the SAME version is used
// as in the module integration tests, so that schema behaviour does not diverge
// between the two places.
const postgresImage = "postgres:16-alpine"

// The names of the core services in the container.
//
// The names are THE SAME as the ones in cmd/server/main.go and repeating them is
// deliberate: the cart workflows resolve their dependencies not at compile time
// but with exactly these strings (ADR 0006). A typo here has the same effect as a
// typo in production, and the test must see it.
const (
	svcDB       = "core.db"
	svcLink     = "core.link"
	svcQuery    = "core.query"
	svcEventBus = "core.eventbus"
	// svcWorkflow is the saga executor; the order completion workflow resolves
	// it under this name (checkoutwf.ServiceWorkflow).
	svcWorkflow = "core.workflow"
	// svcWorkflowStore is the DURABLE store of the execution state.
	svcWorkflowStore = "core.workflow.store"
	// svcAuthInterop is the authenticator's name in the container; the core
	// resolves it BY THAT NAME and does not import the auth module (ADR 0001).
	svcAuthInterop = "auth.interop"
)

// The constants of the Phase 8 identity fixture.
//
// The secret is LONGER than 32 characters: the auth module does not reject a
// short secret but logs a warning, and the test's output must stay assertions.
const (
	// testJWTSecret is the signing secret of the end-to-end tests.
	testJWTSecret = "e2e-test-signing-secret-longer-than-32-bytes"
	// adminEmail is the e-mail address of the fixture administrator.
	adminEmail = "admin@gobit.test"
	// adminPassword is the password of the fixture administrator.
	adminPassword = "very-secret-password-42"
	// testChannelName is the sales channel the publishable key is bound to.
	testChannelName = "e2e-storefront"
	// testRateLimit is the shared router's per-minute request limit.
	//
	// It is deliberately HIGHER than the production default (600): the shape of
	// the stack stays the same as in production, but the limit must not fire in
	// the middle of a scenario and take unrelated tests down. The limit's OWN
	// behaviour is exercised on its own router (see sertlestirme_test.go).
	testRateLimit = 1_000_000
)

// The fixture constants of the region whose tax is applied automatically.
//
// The country and currency codes come from the region module's SEED data
// (000002_region_seed); the test only creates the region and binds the country to
// it.
const (
	// taxedCountry is the country bound to the taxed region (ISO 3166-1 alpha-2).
	taxedCountry = "TR"
	// taxedCurrency is the currency of the taxed region (ISO 4217).
	taxedCurrency = "TRY"
	// taxRateBps is the taxed region's basis-point rate: 2000 = 20%.
	taxRateBps int32 = 2000
)

// The fixture constants of the region whose tax is NOT applied automatically.
//
// The rate is deliberately NOT ZERO: the tax has to come out zero not because the
// region carries a zero rate, but because it switches automatic tax off. Had it
// been set up with a zero rate, the test could not have told the two cases apart.
const (
	// untaxedCountry is the country bound to the untaxed region.
	untaxedCountry = "DE"
	// untaxedCurrency is the currency of the untaxed region.
	untaxedCurrency = "EUR"
	// untaxedRateBps is the rate the untaxed region carries but that must NOT be
	// applied: 1900 = 19%.
	untaxedRateBps int32 = 1900
)

// The fixture constants of the second region whose tax comes from the TAX module
// (Phase 7).
//
// The region differs from the [taxedCountry] region ONLY in its tax rate: the
// currency is deliberately the same ([taxedCurrency]). Had it differed, the price
// of the same product would change too and it would be impossible to tell whether
// the difference between the two regions' taxes came from the rate or from the
// price.
const (
	// secondTaxCountry is the country bound to the second tax region.
	secondTaxCountry = "FR"
	// secondTaxRateBps is the country's rate in the TAX module: 1000 = 10%.
	secondTaxRateBps int32 = 1000
	// secondRegionRateBps is the region's OWN (Phase 5) rate: 5000 = 50%, and
	// automatic tax is ON in the region.
	//
	// The value is deliberately DIFFERENT from tax's: were the computation still
	// using region's rate, the tax would come out five times higher and the
	// handover not having been made would show up in a single number.
	secondRegionRateBps int32 = 5000
)

// The fixture constants of the country whose tax region is NOT CONFIGURED in the
// TAX module (Phase 7).
//
// The region keeps automatic tax ON and carries a non-zero rate; that is
// deliberate. Had the computation said "tax could not answer, let me fall back to
// region", the tax would have come out with that rate. Coming out zero proves
// that tax's authoritative answer (this country has no tax region) is taken AS
// IT IS.
const (
	// unconfiguredCountry is the country for which no tax region is created.
	unconfiguredCountry = "IT"
	// multiCountryRateBps is the REGION rate of the multi-country region: 30%.
	// It was chosen DIFFERENT from the other fixture rates so that the amount
	// alone gives away which source it came from.
	multiCountryRateBps int32 = 3000
	// unconfiguredRegionRateBps is the rate the region carries but that
	// must NOT be applied: 1800 = 18%.
	unconfiguredRegionRateBps int32 = 1800
)

// The ground the tests share. TestMain fills it, the tests only read it.
var (
	// testPool is the connection pool all modules share.
	testPool *db.Pool
	// testDSN is the connection address the migration calls use.
	testDSN string
	// ctr is the DI container the modules and the workflows are resolved from.
	ctr *container.Container
	// links is the core's Module Links service; it is handed to the container and
	// to the Query engine, because extensions traverse the links through it.
	links link.LinkService
	// testAuthn is the authenticator bound to the guard middleware.
	//
	// The router has to be built BEFORE the modules come up (chi refuses r.Use
	// being called after the routes), while the authenticator is born when the
	// auth module registers. Production has the same gap and closes it with the
	// same type (see cmd/server/main.go).
	testAuthn = &corehttp.DeferredAuthenticator{}
	// testRouter is the router that carries the modules' routes.
	//
	// The Phase 5 and Phase 6 scenarios call the workflows directly and never
	// touch the router; the "store surface" scenario of Phase 7 exercises exactly
	// the behaviour of the HTTP edge (see kargo_test.go). An admin_only option not
	// showing up in the storefront is not a SERVICE decision but a trust decision
	// pinned down by that edge, and it can only be proven by going through the
	// edge.
	testRouter chi.Router
	// testModules is the FULL list of the modules bound to the router (the ones
	// plugins bring included). The schema tests can answer the question "which
	// endpoints were described" only from this list; a second, hand-maintained
	// list would silently leave a newly added module out of the description.
	testModules []module.Module
	// testDoc is THE VERY document the /openapi.json endpoint serves.
	//
	// The test not building a separate copy is deliberate: a copy would verify not
	// the generated schema but the schema the test built itself, and it would stay
	// green when the two diverged. The reason the variable is also kept around is
	// [openapi.Doc.UnmatchedDescriptions]: descriptions that match no route are
	// INVISIBLE in the JSON body and can only be read off the document.
	testDoc *openapi.Doc
)

// The identities the Phase 8 fixture produces; the tests only read them.
var (
	// authSvc is the auth module's service; the fixture creates the user and the
	// keys with it.
	authSvc *authsvc.Service
	// adminID is the identifier of the fixture administrator.
	adminID string
	// secretKey is the PLAIN secret key usable on the admin surface.
	secretKey string
	// publishableKey is the storefront surface's PLAIN publishable key.
	publishableKey string
	// testChannelID is the sales channel the publishable key is bound to.
	testChannelID string
)

// The module services; all of them are resolved from the container BY NAME, none
// is built by hand.
var (
	productSvc   *productsvc.Service
	pricingSvc   *pricingsvc.Service
	regionSvc    *regionsvc.Service
	customerSvc  *customersvc.Service
	cartSvc      *cartsvc.Service
	inventorySvc *inventorysvc.Service
	orderSvc     *ordersvc.Service
	paymentSvc   *paymentsvc.Service
	// The Phase 7 modules.
	shippingSvc  *fulfillmentsvc.Service
	promotionSvc *promotionsvc.Service
	taxSvc       *taxsvc.Service
	// The Section 10 module.
	b2bSvc *b2bsvc.Service
)

// shippingSurface is the fulfillment module's cross-module surface
// ("fulfillment.interop", ADR 0006).
//
// The interface is redefined HERE rather than the module's concrete type being
// used. The reason is that the surface has no consumer today: the order saga does
// not run the shipping step yet, which means the signatures written in the
// module's interop.go as "the counterpart on the consumer side" are pinned down
// by no package at compile time. Defining the narrow interface here fills that
// gap: if a signature drifts, the container resolution FAILS and the drift shows
// up in the test.
type shippingSurface interface {
	// ListOptionsJSON returns the options eligible for a cart context together
	// with their prices.
	ListOptionsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
	// CreateFulfillment opens a shipment for an order and returns ITS ID.
	CreateFulfillment(ctx context.Context, reference, optionID, idempotencyKey string) (string, error)
	// CancelFulfillment cancels the shipment; this is the saga compensation.
	CancelFulfillment(ctx context.Context, fulfillmentID string) error
	// FulfillmentStatus returns the shipment's current status.
	FulfillmentStatus(ctx context.Context, fulfillmentID string) (string, error)
}

// taxSurface is the tax module's cross-module surface ("tax.interop", ADR 0006).
//
// Both methods are written out, even though the cart computation only uses
// [taxSurface.CalculateTaxJSON] (see the Taxes interface in the cartwf package).
// RateForCountry being here is deliberate: it is the exact counterpart of the
// region module's stopgap RegionTax method, and no production package pins down
// the "new surface replacing the old one" side of the handover.
type taxSurface interface {
	// CalculateTaxJSON computes the tax for the given country and line items.
	CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
	// RateForCountry returns a country's DEFAULT rate in basis points; the second
	// return value is whether the configuration exists at all.
	RateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error)
}

// The Phase 7 surfaces; both are resolved from the container BY NAME.
var (
	shippingInterop shippingSurface
	taxInterop      taxSurface
)

// workflows is the cart workflows' instance built with the PRODUCTION wiring
// (cartwf.FromContainer). There is no bridge and no fake in the test.
var workflows *cartwf.Workflows

// orderWorkflows is the order completion workflow's instance built with the
// PRODUCTION wiring (checkoutwf.FromContainer).
//
// Being a separate variable is deliberate: the two workflow sets are built on the
// same container but UNAWARE OF EACH OTHER, and checkout builds the cart
// computation again inside itself (see checkoutwf.FromContainer). The test taking
// both of them from the same container verifies that production uses the same
// container too.
var orderWorkflows *checkoutwf.Workflows

// The identifiers of the fixture regions.
var (
	taxedRegionID   string
	untaxedRegionID string
	// secondTaxRegionID is the second region whose tax comes from the tax module.
	secondTaxRegionID string
	// unconfiguredRegionID is the region whose country HAS NO tax region in the
	// tax module.
	unconfiguredRegionID string
	// multiCountryRegionID is the region that carries two countries and triggers
	// the path where the tax is computed FROM REGION: the cart computation reads
	// the tax country off the region, and when the region carries more than one
	// country it cannot be known which one to ask, so tax is NOT asked AT ALL. It
	// is an utterly ordinary configuration in production (a multi-country "Europe"
	// region) and it is the only e2e proof of the fallback path.
	multiCountryRegionID string
	// multiCountryCountries are the two countries the region carries.
	multiCountryCountries = []string{"ES", "PT"}
)

// stockLocationID is the stock location the scenarios SHARE.
//
// The location is created once in TestMain and all tests share it; because every
// test creates ITS OWN stock item, the levels still do not bleed between tests.
//
// The scenarios that share the warehouse DECLARE the location to the workflow
// (checkoutwf.CompleteCartInput.LocationID): the field is optional now and when
// left empty the warehouse is chosen per line, but when declared the old
// behaviour is preserved exactly — and what these tests exercise is not warehouse
// selection. The multi-warehouse path has its own separate proof and sets up its
// own warehouses (see coklu_depo_test.go).
var stockLocationID string

// eventLog is the test-side record of the published "order.placed" events.
var eventLog = &orderEventLog{}

// fileRoot is the root directory the uploaded files are written to (the FILE_ROOT
// counterpart).
//
// It is a TEMPORARY directory and is deleted when the run ends. In production a
// temporary directory is FORBIDDEN — it would mean silent data loss on a restart,
// and the file module therefore never falls back to one (see the file/local
// package). In the test the exact opposite is right: nothing must be left on disk
// when the run ends, because the files here are test data and their surviving
// would only pollute the next run.
//
// The directory is created once in TestMain and all tests share it; sharing is
// safe because the storage key is produced by the provider and two uploads never
// get the same name.
var fileRoot string

// TestMain brings up a single Postgres container, brings the modules up and runs
// all the tests on top of that ground.
func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings the container up, performs the setup and returns the
// exit code.
//
// It lives in a separate function because os.Exit skips the defers: the container
// and the pool can only be closed safely here.
func runWithPostgres(m *testing.M) int {
	// The modules use slog.Default() at startup; the logs are discarded so that
	// the test's output stays the computation assertions.
	slog.SetDefault(slog.New(slog.DiscardHandler))

	ctx := context.Background()

	// The upload root is created BEFORE the container and deleted on exit; because
	// os.Exit skips the defers, the cleanup is only safe in this function (the same
	// reasoning as for the container and the pool).
	var rootErr error
	if fileRoot, rootErr = os.MkdirTemp("", "gobit-e2e-uploads-"); rootErr != nil {
		fmt.Fprintf(os.Stderr, "could not create the upload root directory: %v\n", rootErr)

		return 1
	}
	defer func() {
		if rmErr := os.RemoveAll(fileRoot); rmErr != nil {
			fmt.Fprintf(os.Stderr, "could not delete the upload root directory: %v\n", rmErr)
		}
	}()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_e2e"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "could not stop the postgres ctr: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start the postgres ctr: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not obtain the connection address: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open the connection pool: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := setUpHarness(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "could not set up the ground: %v\n", err)
		return 1
	}

	return m.Run()
}

// setUpHarness prepares the container, the modules, the workflows and the region
// fixtures.
//
// The order is THE SAME as cmd/server/main.go's and it has to be: the modules
// resolve core.db, core.link and core.query during Register, so those three must
// be registered BEFORE Bootstrap. If the order changes, a setup that would blow
// up in production too blows up here — which is what we want.
func setUpHarness(ctx context.Context) error {
	ctr = container.New(nil)

	if err := ctr.Provide(svcDB, testPool); err != nil {
		return err
	}

	// The core migrations are applied BEFORE the module migrations; even though
	// the cart workflows do not use the workflow engine, the setup order must stay
	// the same as in production.
	if err := db.Migrate(ctx, testDSN, pgstore.Migrations(), pgstore.MigrationOwner); err != nil {
		return err
	}
	// The outbox is a core schema too, and the order module writes into it
	// inside its own transaction — so an order cannot be placed without it.
	if err := db.Migrate(ctx, testDSN, outbox.Migrations(), outbox.MigrationOwner); err != nil {
		return err
	}

	links = link.New(testPool, nil)
	if err := ctr.Provide(svcLink, links); err != nil {
		return err
	}
	if err := ctr.Provide(svcQuery, query.New(links, ctr, nil)); err != nil {
		return err
	}

	// The saga engine is built on the DURABLE store (as in main.go). The in-memory
	// engine (workflow.NewInMemory) leaves the idempotency guard at the process
	// boundary; Phase 6's claim "the same cart cannot be completed twice" has to
	// exercise exactly that guard in its database form.
	persistentStore := pgstore.New(testPool, nil)
	if err := ctr.Provide(svcWorkflowStore, persistentStore); err != nil {
		return err
	}
	if err := ctr.Provide(svcWorkflow, workflow.New(persistentStore, nil)); err != nil {
		return err
	}

	// The bus is kept in a separate variable: the "order.placed" subscriber has to
	// be wired BEFORE the modules come up, otherwise an event published during
	// bootstrap would be missed.
	bus := eventbus.NewInMemory(nil)
	if err := ctr.Provide(svcEventBus, bus); err != nil {
		return err
	}
	if err := eventLog.abone(bus); err != nil {
		return err
	}

	registry := module.NewRegistry(nil, func(ctx context.Context, src fs.FS, owner string) error {
		return db.Migrate(ctx, testDSN, src, owner)
	})
	// The module set and its order are the same as cmd/server/main.go's. The whole
	// setup must be exercised: pruning a module for the test would hide from the
	// test a conflict that production would only see at startup.
	registry.Add(productmod.New(productmod.Options{}))
	registry.Add(pricingmod.New(nil))
	registry.Add(inventorymod.New())
	registry.Add(regionmod.New(nil))
	registry.Add(customermod.New(nil))
	registry.Add(cartmod.New())
	registry.Add(paymentmod.New())
	registry.Add(ordermod.New())
	// Phase 7: fulfillment, promotion, tax. All three are added in the ORDER of
	// main.go.
	registry.Add(fulfillmentmod.New())
	registry.Add(promotionmod.New(nil))
	registry.Add(taxmod.New(nil))
	// Notification. That the "order.placed" subscriber REALLY is wired and that it
	// can read the order's contact details off the real order module can only be
	// exercised here — in the module's own integration test the order surface is a
	// FAKE and the schemas of the two sides cannot be audited by the compiler
	// (Principle 2.4).
	//
	// The provider chosen is NOT the out-of-the-box "log" one but a SPY (see
	// bildirim_test.go). There is a single reason for it: the claim "the
	// notification's recipient is the order's e-mail address" requires a place that
	// SEES the address, and the address is deliberately stored nowhere — the
	// delivery log has no column for it, and the "log" provider does not log it
	// either. One place is left: the provider itself. The spy stands where a real
	// plugin provider would stand; the rest of the chain is production code, and
	// the out-of-the-box provider stays in the registry too.
	registry.Add(notificationmod.New(notificationmod.Options{ProviderID: notificationSpyID}))
	// File. That the address the upload produces REALLY can be used as a product
	// image can only be exercised here: the two ends of the chain are in two
	// separate modules (file uploads, product stores) and the two do not import
	// each other, which means no unit test can see both at once (see
	// dosya_test.go).
	//
	// The limit and the allow list are the PRODUCTION DEFAULTS (config constants);
	// the test making up its own values would lead to e2e one day "proving" that it
	// accepts a file production does not. The root directory, on the other hand,
	// necessarily diverges (see [fileRoot]).
	registry.Add(filemod.New(filemod.Options{
		Root:           fileRoot,
		MaxUploadBytes: config.DefaultFileMaxUploadBytes,
		AllowedTypes:   strings.Split(config.DefaultFileAllowedTypes, ","),
	}))
	// Phase 8: identity.
	registry.Add(authmod.New(authmod.Options{
		JWTSecret: testJWTSecret,
		JWTTTL:    time.Hour,
		JWTIssuer: "gobit-e2e",
		// The bcrypt cost is LOWERED for the test: the default cost adds ~100ms to
		// every login call and the identity scenarios perform dozens of logins. The
		// cost parameter ITSELF is not exercised here; the behaviour of password
		// verification is.
		BcryptCost: bcrypt.MinCost,
	}))
	// Section 10: B2B. It is registered as in PRODUCTION, because what is exercised
	// is not the module's own endpoints but the fact that its being registered
	// CHANGES the order module's behaviour: order resolves the spend rule from the
	// container under the name "b2b.interop", and without the registration it
	// counts every customer as unlimited.
	registry.Add(b2bmod.New(nil))

	// The router is built as in PRODUCTION: the guard stack (rate limit -> identity
	// -> idempotency) comes from the single definition in the core, the test has no
	// copy of its own. Had there been a copy, the test would still verify the old
	// order once production's order changed, and it would stay green.
	testRouter = corehttp.NewRouter(corehttp.RouterOptions{
		Version: "e2e",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator:    testAuthn,
			AdminExempt:      []string{authapi.LoginPath},
			Limiter:          corehttp.NewMemoryLimiter(testRateLimit, time.Minute),
			LimitKey:         corehttp.ClientIPKey,
			IdempotencyStore: corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
			// The exemption list has to be the same as in PRODUCTION, otherwise
			// this file exercises not an end-to-end setup but some other
			// configuration it built itself. The difference bit exactly here: when
			// cart creation was taken out of the ring in production, the tests
			// here went on passing as before and started documenting a behaviour
			// that no longer existed.
			IdempotencyExempt: []string{graph.Path, cartapi.StoreCartsPath},
		}),
	})

	// The plugins are installed BEFORE the modules (the same order as main.go): the
	// module a plugin BRINGS must go through the Register/migration/route cycle
	// too. The rationale, and why installing them on the ground does not break the
	// existing tests, is at the top of arama_test.go.
	if err := setUpPlugins(ctx, registry, bus); err != nil {
		return fmt.Errorf("could not install the plugins: %w", err)
	}

	if err := registry.Bootstrap(ctx, ctr, testRouter); err != nil {
		return err
	}

	// The authenticator is only in the container after Bootstrap.
	authenticator, err := container.Resolve[corehttp.Authenticator](ctr, svcAuthInterop)
	if err != nil {
		return fmt.Errorf("could not resolve the authenticator: %w", err)
	}
	testAuthn.Bind(authenticator)

	// The plugins' subscriptions and routes are applied AFTER the modules have come
	// up; the provider registrations and the subscriptions can only be resolved at
	// that moment.
	if err := startPlugins(ctx); err != nil {
		return fmt.Errorf("could not start the plugins: %w", err)
	}

	// The notification spy is registered at the SAME stage as the plugin providers,
	// that is AFTER the modules have come up: "notification.providers" is put into
	// the container in the module's Register, and trying earlier would take the
	// ground down with an error where nothing is actually missing.
	if err := setUpNotificationSpy(); err != nil {
		return fmt.Errorf("could not set up the notification spy: %w", err)
	}

	// The OpenAPI endpoint is set up as in production too (Phase 9): the path, the
	// method and the security are read off the router tree, while the BODY schemas
	// come from the modules' descriptions. Running the description hook here as
	// well is mandatory — had openapi.New been wired on its own, the e2e schema
	// would stay bodyless and the claim "the schema the server serves is filled in"
	// would have exercised a setup that never exists in production.
	//
	// The module list is READ from the registry (the same reasoning as main.go):
	// the modules the plugins bring show up only there.
	testModules = registry.Modules()
	testDoc = describeDocument("gobit API", "e2e", testModules)
	testRouter.Get(schemaPath, testDoc.Handler(testRouter))

	if err := resolveModuleServices(); err != nil {
		return err
	}

	var setupErr error
	if workflows, setupErr = setUpCartWorkflows(); setupErr != nil {
		return fmt.Errorf("could not set up the cart workflows: %w", setupErr)
	}
	if orderWorkflows, setupErr = setUpCheckoutWorkflows(); setupErr != nil {
		return fmt.Errorf("could not set up the order completion workflow: %w", setupErr)
	}

	if err := setUpRegionFixtures(ctx); err != nil {
		return err
	}
	if err := setUpTaxFixtures(ctx); err != nil {
		return err
	}
	if err := setUpIdentityFixture(ctx); err != nil {
		return err
	}
	return setUpStockLocation(ctx)
}

// setUpIdentityFixture produces the identities the Phase 8 scenarios share.
//
// The identities are created from the SERVICE and not over HTTP, and that is
// deliberate: the admin endpoints themselves are guarded now, which means there
// is no way to create the first administrator over HTTP. In a real setup the
// first administrator is born from a seed step as well; the test imitates that
// step.
//
// What is produced:
//   - an admin user with a password (the login scenarios),
//   - a fully privileged SECRET key (tokenless admin access),
//   - a sales channel and a PUBLISHABLE key bound to it (the storefront surface).
func setUpIdentityFixture(ctx context.Context) error {
	admin, err := authSvc.CreateUser(ctx, authsvc.CreateUserInput{
		Email:     adminEmail,
		FirstName: "E2E",
		LastName:  "Admin",
	}, adminPassword)
	if err != nil {
		return fmt.Errorf("could not set up the admin user: %w", err)
	}
	adminID = admin.ID

	_, secretKey, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeySecret,
		Title:     "e2e secret key",
		CreatedBy: adminID,
	})
	if err != nil {
		return fmt.Errorf("could not set up the secret api key: %w", err)
	}

	channel, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        testChannelName,
		Description: "end-to-end test storefront",
	})
	if err != nil {
		return fmt.Errorf("could not set up the sales channel: %w", err)
	}
	testChannelID = channel.ID

	_, publishableKey, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:            models.APIKeyPublishable,
		Title:           "e2e publishable key",
		CreatedBy:       adminID,
		SalesChannelIDs: []string{testChannelID},
	})
	if err != nil {
		return fmt.Errorf("could not set up the publishable api key: %w", err)
	}

	return nil
}

// resolveModuleServices resolves the module services the fixtures will use from
// the container BY NAME.
//
// The services are taken from the container and NOT from the module objects (e.g.
// cartmod.Module.Service()): the service the test uses must be THE VERY service
// the workflows use. Were there two separate instances, the test would read back
// what it wrote itself and could not prove that the workflow really touched the
// same cart.
func resolveModuleServices() error {
	var err error
	if productSvc, err = container.Resolve[*productsvc.Service](ctr, productmod.ServiceName); err != nil {
		return err
	}
	if pricingSvc, err = container.Resolve[*pricingsvc.Service](ctr, pricingmod.ServiceName); err != nil {
		return err
	}
	if regionSvc, err = container.Resolve[*regionsvc.Service](ctr, regionmod.ServiceName); err != nil {
		return err
	}
	if customerSvc, err = container.Resolve[*customersvc.Service](ctr, customermod.ServiceName); err != nil {
		return err
	}
	if cartSvc, err = container.Resolve[*cartsvc.Service](ctr, cartmod.ServiceName); err != nil {
		return err
	}
	if inventorySvc, err = container.Resolve[*inventorysvc.Service](ctr, inventorymod.ServiceName); err != nil {
		return err
	}
	if orderSvc, err = container.Resolve[*ordersvc.Service](ctr, ordermod.ServiceName); err != nil {
		return err
	}
	if paymentSvc, err = container.Resolve[*paymentsvc.Service](ctr, paymentmod.ServiceName); err != nil {
		return err
	}
	if shippingSvc, err = container.Resolve[*fulfillmentsvc.Service](ctr, fulfillmentmod.ServiceName); err != nil {
		return err
	}
	if promotionSvc, err = container.Resolve[*promotionsvc.Service](ctr, promotionmod.ServiceName); err != nil {
		return err
	}
	if taxSvc, err = container.Resolve[*taxsvc.Service](ctr, taxmod.ServiceName); err != nil {
		return err
	}
	if authSvc, err = container.Resolve[*authsvc.Service](ctr, authmod.ServiceName); err != nil {
		return err
	}
	if b2bSvc, err = container.Resolve[*b2bsvc.Service](ctr, b2bmod.ServiceName); err != nil {
		return err
	}

	// The surfaces are resolved with the NARROW INTERFACE (see [shippingSurface],
	// [taxSurface]); resolving with the concrete type would not exercise signature
	// compatibility at all.
	if shippingInterop, err = container.Resolve[shippingSurface](ctr, fulfillmentmod.InteropName); err != nil {
		return err
	}
	taxInterop, err = container.Resolve[taxSurface](ctr, taxmod.InteropName)
	return err
}

// setUpCartWorkflows builds the cart workflows with the PRODUCTION wiring and
// REGISTERS them in the container.
//
// [cartwf.FromContainer] resolves all six surfaces from the container by name;
// the cart side is the primitive surface registered under the name "cart.interop"
// (ADR 0006). There is no bridge and no fake in the test: if an incompatibility
// turns up here, it turns up in production too.
//
// # Why the registration is MANDATORY
//
// Had the workflow only been written into this file's variable, the tests could
// call it but the STORE ENDPOINTS could not: the cart module's storefront line
// endpoints resolve the workflow from the container under the name
// [cartwf.InteropName] and fail CLOSED when they cannot find it. The registration
// is the same as registerWorkflows in cmd/server; without it e2e would exercise
// not a setup that works in production but only the workflow itself.
func setUpCartWorkflows() (*cartwf.Workflows, error) {
	workflows, err := cartwf.FromContainer(ctr)
	if err != nil {
		return nil, err
	}
	if err := ctr.Provide(cartwf.InteropName, cartwf.NewInterop(workflows)); err != nil {
		return nil, err
	}
	return workflows, nil
}

// setUpCheckoutWorkflows builds the order completion workflow with the PRODUCTION
// wiring and REGISTERS it in the container.
//
// [checkoutwf.FromContainer] resolves seven surfaces from the container by name
// (cart.interop, inventory.interop, order.interop, payment.interop, core.link,
// core.query, core.workflow) and ALSO builds the cart computation itself on the
// same container. There is no bridge and no fake in the test: an incompatibility
// here blows up at startup in production too.
//
// The rationale for the registration is the same as [setUpCartWorkflows]'s: the
// POST /store/v1/carts/{id}/complete endpoint resolves the workflow under the
// name [checkoutwf.InteropName].
func setUpCheckoutWorkflows() (*checkoutwf.Workflows, error) {
	flows, err := checkoutwf.FromContainer(ctr)
	if err != nil {
		return nil, err
	}
	if err := ctr.Provide(checkoutwf.InteropName, checkoutwf.NewInterop(flows)); err != nil {
		return nil, err
	}
	return flows, nil
}

// setUpStockLocation prepares the single stock location the scenarios share.
//
// The location is created in TestMain rather than per test: sharing it is safe
// because the stock LEVEL is written against the (item, location) pair and every
// test creates its own item. The country code was chosen the same as the taxed
// region's so that the fixture stays realistic; the workflow does not use the
// location's country today.
func setUpStockLocation(ctx context.Context) error {
	location, err := inventorySvc.CreateStockLocation(ctx, inventorysvc.CreateStockLocationInput{
		Name:        "E2E Main Warehouse",
		CountryCode: taxedCountry,
	})
	if err != nil {
		return err
	}
	stockLocationID = location.ID
	return nil
}

// setUpRegionFixtures prepares the four regions the scenarios share.
//
// The regions are created once in TestMain because a country can be bound to only
// one region at a time; setting them up again per test would conflict on the
// second call.
//
// Every region is bound to a SINGLE country and in Phase 7 that is a requirement:
// the cart computation reads the tax country off the region, and if the region
// carries more than one country it cannot be known which one to ask, so tax is NOT
// asked AT ALL (see countryForRegion in cartwf). A multi-country fixture would
// silently drop every scenario that exercises the tax module's answer onto the
// region path.
//
// The last two regions are for Phase 7 and both of them keep automatic tax ON:
// this way the question "does the tax come from region or from tax" can be
// answered by the amount itself.
func setUpRegionFixtures(ctx context.Context) error {
	taxed, err := regionSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Taxed Region",
		CurrencyCode:   taxedCurrency,
		AutomaticTaxes: true,
		TaxRate:        taxRateBps,
	})
	if err != nil {
		return err
	}
	if _, err := regionSvc.AddCountryToRegion(ctx, taxed.ID, taxedCountry); err != nil {
		return err
	}
	taxedRegionID = taxed.ID

	untaxed, err := regionSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Untaxed Region",
		CurrencyCode:   untaxedCurrency,
		AutomaticTaxes: false,
		TaxRate:        untaxedRateBps,
	})
	if err != nil {
		return err
	}
	if _, err := regionSvc.AddCountryToRegion(ctx, untaxed.ID, untaxedCountry); err != nil {
		return err
	}
	untaxedRegionID = untaxed.ID

	second, err := regionSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Second Tax Region",
		CurrencyCode:   taxedCurrency,
		AutomaticTaxes: true,
		TaxRate:        secondRegionRateBps,
	})
	if err != nil {
		return err
	}
	if _, err := regionSvc.AddCountryToRegion(ctx, second.ID, secondTaxCountry); err != nil {
		return err
	}
	secondTaxRegionID = second.ID

	unconfigured, err := regionSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Region With Unconfigured Tax",
		CurrencyCode:   taxedCurrency,
		AutomaticTaxes: true,
		TaxRate:        unconfiguredRegionRateBps,
	})
	if err != nil {
		return err
	}
	if _, err := regionSvc.AddCountryToRegion(ctx, unconfigured.ID, unconfiguredCountry); err != nil {
		return err
	}
	unconfiguredRegionID = unconfigured.ID

	// The multi-country region: the region rate is 30%, and there is NOTHING in the
	// tax module. Because it carries two countries the country cannot be resolved
	// and the computation falls back to region.
	multiCountry, err := regionSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Multi-Country Region",
		CurrencyCode:   untaxedCurrency,
		AutomaticTaxes: true,
		TaxRate:        multiCountryRateBps,
	})
	if err != nil {
		return fmt.Errorf("could not create the multi-country region: %w", err)
	}
	for _, country := range multiCountryCountries {
		if _, err := regionSvc.AddCountryToRegion(ctx, multiCountry.ID, country); err != nil {
			return fmt.Errorf("could not add country %s to the multi-country region: %w", country, err)
		}
	}
	multiCountryRegionID = multiCountry.ID

	return nil
}

// setUpTaxFixtures prepares the tax regions and the rates in the tax module
// (Phase 7).
//
// # Why one root and one default rate PER country
//
// The tax module refuses a second root region being written for a country or a
// second default rate for a region; the fixture is therefore created once in
// TestMain. Had it been created per test, the second call would have got
// errors.Conflict.
//
// # What is set up for which country
//
//   - [taxedCountry] -> 20%. Every amount of the Phase 5 and Phase 6 scenarios
//     rests on this rate; for the SAME numbers to come out after the handover, the
//     new authority has to give the same answer as the old one.
//   - [secondTaxCountry] -> 10%. Two countries producing DIFFERENT tax is visible
//     from here.
//   - [unconfiguredCountry] -> NOTHING. What a country without a region does can
//     only be exercised through the absence of configuration.
//
// The regions' provider is left empty: an empty provider on a root region means
// "local computation", and an external tax service is not the subject of this
// test.
func setUpTaxFixtures(ctx context.Context) error {
	taxedRoot, err := taxSvc.CreateTaxRegion(ctx, taxsvc.CreateTaxRegionInput{
		CountryCode: taxedCountry,
	})
	if err != nil {
		return err
	}
	if _, err := taxSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: taxedRoot.ID,
		Name:        "E2E VAT",
		RateBps:     taxRateBps,
		IsDefault:   true,
	}); err != nil {
		return err
	}

	secondRoot, err := taxSvc.CreateTaxRegion(ctx, taxsvc.CreateTaxRegionInput{
		CountryCode: secondTaxCountry,
	})
	if err != nil {
		return err
	}
	if _, err := taxSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: secondRoot.ID,
		Name:        "E2E TVA",
		RateBps:     secondTaxRateBps,
		IsDefault:   true,
	}); err != nil {
		return err
	}

	return nil
}

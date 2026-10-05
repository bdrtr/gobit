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
// (see discount_test.go), so the discount of the other scenarios stays zero. Had
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
// The tests share a single PostgreSQL container (testcontainers), and the ground
// opens the installation through app.Open, the function the server's assembly
// and App.InProcess share (ADR 0398), under the shared profile (APP_ENV=staging).
// Before it sets its own environment it clears every variable the configuration
// reads, so a developer's shell or a runner cannot change it. It adds only what
// an embedder adds: a module providing the storefront identity and two plugins
// carrying the spies and the event logs. It builds no flow that subscribes and
// provides no name outside a module's Register.
//
// The plugins the ground runs are named in PLUGINS, as on a server, and the
// saga engine runs on pgstore, as in production (core.workflow.store). The
// difference changes what the test sees: the idempotency key and the execution
// state really are written to the database, so the claim "the same cart cannot
// be completed twice" exercises the behavior of a durable record rather than
// that of an in-process map.
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
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/app"
	"github.com/bdrtr/gobit/internal/core/config"
	authmod "github.com/bdrtr/gobit/internal/modules/auth"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	b2bmod "github.com/bdrtr/gobit/internal/modules/b2b"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	cartmod "github.com/bdrtr/gobit/internal/modules/cart"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	customermod "github.com/bdrtr/gobit/internal/modules/customer"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	fulfillmentmod "github.com/bdrtr/gobit/internal/modules/fulfillment"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	inventorymod "github.com/bdrtr/gobit/internal/modules/inventory"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	ordermod "github.com/bdrtr/gobit/internal/modules/order"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmod "github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	pricingmod "github.com/bdrtr/gobit/internal/modules/pricing"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmod "github.com/bdrtr/gobit/internal/modules/product"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	promotionmod "github.com/bdrtr/gobit/internal/modules/promotion"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	regionmod "github.com/bdrtr/gobit/internal/modules/region"
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
	taxmod "github.com/bdrtr/gobit/internal/modules/tax"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	segmentwf "github.com/bdrtr/gobit/internal/workflows/segment"
	stockalertwf "github.com/bdrtr/gobit/internal/workflows/stockalert"
)

// postgresImage is the database image the tests share; the SAME version is used
// as in the module integration tests, so that schema behavior does not diverge
// between the two places.
const postgresImage = "postgres:16-alpine"

// The names of the core services in the container.
//
// The names are THE SAME as the ones in internal/app/app.go and repeating them is
// deliberate: the cart workflows resolve their dependencies not at compile time
// but with exactly these strings (ADR 0006). A typo here has the same effect as a
// typo in production, and the test must see it.
const (
	svcDB       = "core.db"
	svcLink     = "core.link"
	svcQuery    = "core.query"
	svcEventBus = "core.eventbus"
	// svcAuthInterop is the authenticator's name in the container; the core
	// resolves it BY THAT NAME and does not import the auth module (ADR 0001).
	svcAuthInterop = "auth.interop"
)

// The constants of the Phase 8 identity fixture.
//
// The secret is LONGER than 32 characters: under the shared profile the
// configuration refuses a shorter one.
const (
	// testJWTSecret is the signing secret of the end-to-end tests.
	testJWTSecret = "e2e-test-signing-secret-longer-than-32-bytes"
	// testMFASecretKey seals the TOTP secrets of the administrators these tests
	// enroll. It is SEPARATE from the signing secret for the reason production
	// keeps them apart: they are rotated at different hours.
	testMFASecretKey = "e2e-mfa-sealing-key-longer-than-32-bytes"
	// adminEmail is the e-mail address of the fixture administrator, the one the
	// seed step creates (ADMIN_BOOTSTRAP_EMAIL).
	adminEmail = "admin@gobit.test"
	// adminPassword is the password of the fixture administrator. It is longer
	// than the shared profile's floor for a bootstrap password.
	adminPassword = "very-secret-password-42"
	// testChannelName is the sales channel the publishable key is bound to.
	testChannelName = "e2e-storefront"
	// testRateLimit is the ground's per-minute request limit
	// (RATE_LIMIT_PER_MINUTE).
	//
	// It is deliberately HIGHER than the production default (600): the stack is
	// production's, but the limit must not fire in the middle of a scenario and
	// take unrelated tests down. The limit's OWN behavior is exercised on its own
	// router (see hardening_test.go).
	testRateLimit = 1_000_000

	// loyaltyEarnBasisPoints is the payment module's earn rate in the harness:
	// the ceiling, one point per minor unit.
	loyaltyEarnBasisPoints = paymentsvc.MaxLoyaltyEarnBasisPoints
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

// The fixture constants of the market whose prices INCLUDE their tax
// (ADR 0086, ADR 0246).
//
// The region's own rate is zero and its automatic tax is on, so a cart the tax
// module was not asked about comes out with no tax at all, and the currency is
// [taxedCurrency] so the catalog's prices apply.
const (
	// inclusiveTaxCountry is the country bound to the tax-inclusive region.
	inclusiveTaxCountry = "AT"
	// inclusiveTaxRateBps is the country's rate in the TAX module: 20%.
	inclusiveTaxRateBps int32 = 2000
)

// The ground the tests share. TestMain fills it, the tests only read it.
var (
	// installation is what app.Open brought up; the variables below are its
	// parts under the names the scenarios read.
	installation *app.Installation
	// testPool is the connection pool all modules share.
	testPool *db.Pool
	// ctr is the DI container the modules and the workflows are resolved from.
	ctr *container.Container
	// links is the core's Module Links service; it is handed to the container and
	// to the Query engine, because extensions traverse the links through it.
	links link.LinkService
	// testRouter is the router the server would serve, guard stack, panel and
	// schema included.
	//
	// The Phase 5 and Phase 6 scenarios call the workflows directly and never
	// touch the router; the "store surface" scenario of Phase 7 exercises exactly
	// the behavior of the HTTP edge (see shipping_test.go). An admin_only option not
	// showing up in the storefront is not a SERVICE decision but a trust decision
	// pinned down by that edge, and it can only be proven by going through the
	// edge.
	testRouter chi.Router
	// testModules is the FULL list of the modules bound to the router (the ones
	// plugins bring included), read from the installation's registry. The schema
	// tests can answer the question "which endpoints were described" only from
	// this list; a second, hand-maintained list would silently leave a newly
	// added module out of the description.
	testModules []module.Module
	// testDoc is THE VERY document the /openapi.json endpoint serves.
	//
	// The test not building a separate DOCUMENT is deliberate: a second document
	// would verify not the generated schema but the schema the test built itself,
	// and it would stay green when the two diverged. The reason the variable is
	// also kept around is [openapi.Doc.UnmatchedDescriptions]: descriptions that
	// match no route are INVISIBLE in the JSON body and can only be read off the
	// document.
	testDoc *openapi.Doc
	// groundJobs are the scheduled jobs the plugins registered. Nothing runs
	// them on their own: a scenario that needs a pass runs it.
	groundJobs []coreplugin.Job
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
	// CreateFulfillment opens a shipment for an order and returns ITS ID;
	// destination is the order's shipping address as JSON, or nil (ADR 0194).
	CreateFulfillment(
		ctx context.Context, reference, optionID, idempotencyKey string, destination json.RawMessage,
	) (string, error)
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

// stockAlerts is a stock alert flow built on the installation's container (ADR
// 0215); a scenario runs its pass where production's job would.
var stockAlerts *stockalertwf.Workflow

// segments is a segment flow built on the installation's container (ADR 0217);
// a scenario runs a pass through it as the customer-segments job does.
var segments *segmentwf.Workflow

// The identifiers of the fixture regions.
var (
	taxedRegionID   string
	untaxedRegionID string
	// secondTaxRegionID is the second region whose tax comes from the tax module.
	secondTaxRegionID string
	// inclusiveRegionID is the region whose prices include their tax
	// ([inclusiveTaxCountry]).
	inclusiveRegionID string
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
// behavior is preserved exactly — and what these tests exercise is not warehouse
// selection. The multi-warehouse path has its own separate proof and sets up its
// own warehouses (see multi_warehouse_test.go).
var stockLocationID string

// eventLog is the test-side record of the published "order.placed" events.
var eventLog = &orderEventLog{topic: ordersvc.EventOrderPlaced}

// cancelLog is the test-side record of the published "order.canceled" events.
var cancelLog = &orderEventLog{topic: ordersvc.EventOrderCanceled}

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
// and the installation can only be closed safely here.
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

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not obtain the connection address: %v\n", err)
		return 1
	}

	stop, err := setUpHarness(ctx, dsn)
	if stop != nil {
		defer stop()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not set up the ground: %v\n", err)
		return 1
	}

	return m.Run()
}

// clearConfigEnvironment unsets every variable the configuration reads.
//
// The names are read off config.Config's env tags rather than listed here: a
// list would miss the next setting, and the setting it missed is the one a
// developer's shell or a runner's environment would then decide for the ground.
// Plugin settings need nothing: the plugins on the ground read none.
func clearConfigEnvironment() error {
	names := envTagNames(reflect.TypeFor[config.Config]())
	if len(names) == 0 {
		return fmt.Errorf("config.Config carries no env tag; the clearing has gone blind")
	}
	for _, name := range names {
		if err := os.Unsetenv(name); err != nil {
			return fmt.Errorf("could not unset %s: %w", name, err)
		}
	}

	return nil
}

// envTagNames returns the env tags of a struct type, nested structs included.
func envTagNames(t reflect.Type) []string {
	var names []string
	for i := range t.NumField() {
		field := t.Field(i)
		if name, ok := field.Tag.Lookup("env"); ok {
			names = append(names, strings.Split(name, ",")[0])

			continue
		}
		if field.Type.Kind() == reflect.Struct {
			names = append(names, envTagNames(field.Type)...)
		}
	}

	return names
}

// groundEnvironment is the configuration the ground runs under, on top of a
// cleared environment.
func groundEnvironment(dsn string) map[string]string {
	return map[string]string{
		"DATABASE_URL": dsn,
		// The shared profile: what staging and production run, less the one
		// branch that is production's alone (the manual payment provider).
		"APP_ENV":       "staging",
		"EVENT_BUS":     "inmemory",
		"GUARD_BACKEND": "memory",
		"LOG_LEVEL":     "error",
		"JWT_SECRET":    testJWTSecret,
		// The key that lets an administrator hold a second factor. Without one
		// the module REFUSES to enroll, and the demand at login (ADR 0147) can
		// only be exercised end to end by an account that really enrolled.
		"MFA_SECRET_KEY":        testMFASecretKey,
		"RATE_LIMIT_PER_MINUTE": strconv.Itoa(testRateLimit),
		"PLUGINS":               strings.Join(groundPlugins, ","),
		// The spy stands where a real plugin provider would (notification_test.go).
		"NOTIFICATION_PROVIDER":   notificationSpyID,
		"FILE_ROOT":               fileRoot,
		"PAYMENT_OFFLINE_METHODS": offlineMethod,
		// The earn rate is the CEILING — one point per minor unit — so that one
		// paid order earns enough to pay for the next one entirely, which is what
		// a spend scenario needs at a storefront that takes one tender for the
		// whole order (ADR 0165).
		"PAYMENT_LOYALTY_EARN_BASIS_POINTS": strconv.FormatInt(loyaltyEarnBasisPoints, 10),
		// The first administrator is born from the seed step, as on a fresh
		// server.
		"ADMIN_BOOTSTRAP_EMAIL":    adminEmail,
		"ADMIN_BOOTSTRAP_PASSWORD": adminPassword,
	}
}

// setUpHarness opens the installation and prepares the fixtures.
//
// The returned function closes the installation; it is non-nil once the
// installation is open, even when a later step fails.
func setUpHarness(ctx context.Context, dsn string) (func(), error) {
	if err := clearConfigEnvironment(); err != nil {
		return nil, err
	}
	for name, value := range groundEnvironment(dsn) {
		if err := os.Setenv(name, value); err != nil {
			return nil, fmt.Errorf("could not set %s: %w", name, err)
		}
	}

	opened, stop, err := app.Open(ctx, app.Options{
		Version: "e2e",
		Modules: []module.Module{identityModule{}},
		Plugins: []coreplugin.Plugin{carrierSpyPlugin{}, observersPlugin{}},
	})
	if err != nil {
		return nil, fmt.Errorf("could not open the installation: %w", err)
	}
	installation = opened
	ctr, testRouter, testModules = opened.Container, opened.Router, opened.Modules
	testDoc, groundJobs = opened.Schema, opened.Jobs

	if testPool, err = container.Resolve[*db.Pool](ctr, svcDB); err != nil {
		return stop, err
	}
	if links, err = container.Resolve[link.LinkService](ctr, svcLink); err != nil {
		return stop, err
	}
	if err := resolveModuleServices(); err != nil {
		return stop, err
	}

	// The flows below are built on the installation's container, and each one
	// is a second instance of what production wired: none of them subscribes,
	// and none is provided here.
	if workflows, err = cartwf.FromContainer(ctr); err != nil {
		return stop, fmt.Errorf("could not build the cart workflows: %w", err)
	}
	if orderWorkflows, err = checkoutwf.FromContainer(ctr); err != nil {
		return stop, fmt.Errorf("could not build the order completion workflow: %w", err)
	}
	if stockAlerts, err = stockalertwf.FromContainer(ctr, nil); err != nil {
		return stop, fmt.Errorf("could not build the stock alert workflow: %w", err)
	}
	if segments, err = segmentwf.FromContainer(ctr, nil); err != nil {
		return stop, fmt.Errorf("could not build the segment workflow: %w", err)
	}

	if err := setUpRegionFixtures(ctx); err != nil {
		return stop, err
	}
	if err := setUpTaxFixtures(ctx); err != nil {
		return stop, err
	}
	if err := setUpIdentityFixture(ctx); err != nil {
		return stop, err
	}

	return stop, setUpStockLocation(ctx)
}

// setUpIdentityFixture produces the identities the Phase 8 scenarios share.
//
// The administrator is the one the seed step created (ADMIN_BOOTSTRAP_EMAIL),
// read back rather than made: the admin endpoints are guarded, so a fresh
// installation's first administrator can only come from that step. The keys
// and the channel are created from the SERVICE.
//
// What is produced:
//   - the seeded admin user and its password (the login scenarios),
//   - a fully privileged SECRET key (tokenless admin access),
//   - a sales channel and a PUBLISHABLE key bound to it (the storefront surface).
func setUpIdentityFixture(ctx context.Context) error {
	admin, err := authSvc.GetUserByEmail(ctx, adminEmail)
	if err != nil {
		return fmt.Errorf("the seed step created no administrator: %w", err)
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

	inclusive, err := regionSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Tax-Inclusive Region",
		CurrencyCode:   taxedCurrency,
		AutomaticTaxes: true,
	})
	if err != nil {
		return fmt.Errorf("could not create the tax-inclusive region: %w", err)
	}
	if _, err := regionSvc.AddCountryToRegion(ctx, inclusive.ID, inclusiveTaxCountry); err != nil {
		return fmt.Errorf("could not add country %s to the tax-inclusive region: %w", inclusiveTaxCountry, err)
	}
	inclusiveRegionID = inclusive.ID

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
//   - [inclusiveTaxCountry] -> 20%, with prices that INCLUDE it (ADR 0246).
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

	included := true
	inclusiveRoot, err := taxSvc.CreateTaxRegion(ctx, taxsvc.CreateTaxRegionInput{
		CountryCode:      inclusiveTaxCountry,
		PricesIncludeTax: &included,
	})
	if err != nil {
		return err
	}
	if _, err := taxSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: inclusiveRoot.ID,
		Name:        "E2E USt",
		RateBps:     inclusiveTaxRateBps,
		IsDefault:   true,
	}); err != nil {
		return err
	}

	return nil
}

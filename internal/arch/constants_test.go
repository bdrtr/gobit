package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/auth"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/internal/modules/file"
	"github.com/bdrtr/gobit/internal/modules/file/local"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
)

// TestTheProviderRegistryNamesAgree verifies that the plugin package and the
// modules use the same provider registry names.
//
// The core CANNOT import modules (Principle 2.4), so
// [coreplugin.PaymentProvidersName] cannot be bound to the module's constant; it
// repeats the value by hand. Every hand-repeated constant is open to silent
// drift: if one of the names changes, the plugin provider finds nothing in the
// container and an installation believed to have "stripe installed" takes no
// payment at all.
//
// This test moves that binding to COMPILE time. The reason it lives here is that
// the arch package is test-only and can import both the core and the modules;
// the core itself could not host this test.
//
// The cost of the notification assertion is DIFFERENT from the others and it is
// quieter: if payment drifts the customer cannot pay and says so right away, if
// notification drifts the plugin provider can never be registered and the
// installation goes on writing order confirmations to the log only — without
// producing a single error.
//
// The FOURTH provider's assertion ([coreplugin.FileProvidersName]) stayed
// MISSING for a while: the contract and the registration point were written
// before the module that would consume them, and importing a package that does
// not exist would break the build. The line was added when the file module
// arrived — because a missing assertion, unlike a wrong one, does not blow the
// test up, it makes no sound at all.
//
// The cost of drift in file is even more insidious than in notification: the
// plugin provider (S3, say) can never be registered, the installation goes on
// writing to the local disk, and it is noticed only once the container is
// restarted and the uploaded images are gone — and at that moment the addresses
// in the product records are still right where they were.
func TestTheProviderRegistryNamesAgree(t *testing.T) {
	t.Parallel()

	assert.Equal(t, payment.ProvidersName, coreplugin.PaymentProvidersName,
		"the payment provider registry name in the plugin package must match the payment module")
	assert.Equal(t, fulfillment.ProvidersName, coreplugin.FulfillmentProvidersName,
		"the shipping provider registry name in the plugin package must match the fulfillment module")
	assert.Equal(t, notification.ProvidersName, coreplugin.NotificationProvidersName,
		"the notification provider registry name in the plugin package must match the notification module")
	assert.Equal(t, file.ProvidersName, coreplugin.FileProvidersName,
		"the file provider registry name in the plugin package must match the file module")
}

// TestTheDefaultFileProviderAgreesWithTheConfig verifies that the config's
// default file provider REALLY corresponds to a registered provider.
//
// The two constants live in two separate packages and there is NO compiler
// binding between them: the core cannot import modules (Principle 2.4), so
// [config.DefaultFileProvider] cannot be bound to local.ID and repeats the value
// by hand.
//
// The cost of drift would be that an installation given no environment variable
// at all DOES NOT COME UP: cmd/server halts startup when it cannot find the
// selected provider in the registry (see verifyFileProvider). So the drift is
// not silent, but it would blow up at the worst possible moment — on the first
// attempt of someone who has configured nothing.
func TestTheDefaultFileProviderAgreesWithTheConfig(t *testing.T) {
	t.Parallel()

	assertDefaultProvider(t, "file", local.ID, config.DefaultFileProvider, file.DefaultProviderID)
}

// TestTheFileAllowListAgreesWithTheCoreConstants verifies that the config's
// default allow list describes the same set as the core's content type
// constants.
//
// The value sits in config as a single STRING (envDefault tags do not accept
// constant references) and in the core as four separate constants. Drift is
// silent in both directions: a type that made it into the list with a typo lets
// no file through (nobody notices, it is taken for "that format is not
// supported"), while a type dropped from the list means yesterday's uploads are
// rejected today.
//
// That SVG is NOT on the list is asserted separately: this is not a forgotten
// omission but a decision that was MADE (an SVG is a document, it carries script
// and served from the same origin it becomes stored XSS). If a decision has no
// test, one day it is "completed" as though something were missing.
func TestTheFileAllowListAgreesWithTheCoreConstants(t *testing.T) {
	t.Parallel()

	types := strings.Split(config.DefaultFileAllowedTypes, ",")

	assert.ElementsMatch(t, []string{
		coreprovider.ContentTypeJPEG,
		coreprovider.ContentTypePNG,
		coreprovider.ContentTypeGIF,
		coreprovider.ContentTypeWebP,
	}, types, "the config's default allow list must match the core's constants")

	assert.NotContains(t, types, "image/svg+xml",
		"SVG must NOT be on the default allow list; being a document it carries stored XSS")
}

// TestTheDefaultNotificationProviderAgreesWithTheConfig verifies that the
// config's default provider name REALLY corresponds to a registered provider.
//
// The two constants live in two separate packages and there is NO compiler
// binding between them: the core cannot import modules (Principle 2.4), so
// [config.DefaultNotificationProvider] cannot be bound to logonly.ID and repeats
// the value by hand.
//
// The cost of drift would be that an installation given no environment variable
// at all DOES NOT COME UP: cmd/server halts startup when it cannot find the
// selected provider in the registry. So the drift is not silent, but it would
// blow up at the worst possible moment — on the first attempt of someone who has
// configured nothing.
func TestTheDefaultNotificationProviderAgreesWithTheConfig(t *testing.T) {
	t.Parallel()

	assertDefaultProvider(t, "notification", logonly.ID,
		config.DefaultNotificationProvider, notification.DefaultProviderID)
}

// TestThePoolDefaultsAgreeWithTheDbPackage verifies that the PostgreSQL pool's
// defaults in TWO separate places are the same.
//
// The side that BUILDS the pool is core/db and it carries its own
// defaults (db.DefaultConfig); the side that READS is config, and it repeats by
// hand the numbers it will use when no environment variable is given. The
// repetition is mandatory: envDefault is a struct tag and Go does not accept
// constant references in tags.
//
// The cost of drift is silent and its direction MATTERS. cmd/server now WRITES
// the config's numbers into the pool on every startup (see dbConfig), so db's
// default is dead code for the server: if the two drift apart, whoever reads
// db.DefaultConfig — integration tests, an installation that embeds product in
// its own binary — runs with a pool OTHER than the server's, and nothing says
// so. The equality assertion keeps in place the shared origin of the two
// numbers, which is "the behavior from before it was made configurable".
//
// db.DefaultConfig wants a DSN; the string here only fills the signature, no
// connection is opened and the limits are independent of the DSN.
func TestThePoolDefaultsAgreeWithTheDbPackage(t *testing.T) {
	t.Parallel()

	pool := db.DefaultConfig("postgres://gobit@localhost:5432/gobit")

	assert.Equal(t, pool.MaxConns, config.DefaultDBMaxConns,
		"the config's DB_MAX_CONNS default must match db.DefaultConfig's")
	assert.Equal(t, pool.MinConns, config.DefaultDBMinConns,
		"the config's DB_MIN_CONNS default must match db.DefaultConfig's")
}

// TestTheGraphQLLimitDefaultsAgreeWithTheConfig verifies that the GraphQL
// hardening limits' defaults in TWO separate places are the same.
//
// The side that ENFORCES the limits is the product module's graph package; the
// reading side is the core's configuration, and because the core CANNOT import
// modules (Principle 2.4) it cannot bind to the constants and repeats their
// values by hand.
//
// The cost of drift, unlike that of its neighbors in this file, does not blow up
// at startup — it never blows up. An installation that gives no environment
// variable runs with the config's number, embedded use (someone who takes
// product into their own binary) with graph's; the day the two drift apart the
// same document passes in one installation and is rejected in the other, and
// both sides believe what their own documentation says.
//
// The introspection default belongs here too, because on the graph side the
// field is named NEGATIVELY (IntrospectionDisabled): its zero value means "on",
// so the config's default must be true, that is, on, as well. If the two drift,
// the surface closes although nobody wanted it to.
func TestTheGraphQLLimitDefaultsAgreeWithTheConfig(t *testing.T) {
	t.Parallel()

	// The mapping goes from graph.Options' NUMERIC field name to the core's
	// default. The list is written by hand but it CANNOT STAY INCOMPLETE: the
	// reflection audit below fails every limit that is added to Options and not
	// added here.
	expected := map[string]int{
		"MaxDepth":              config.DefaultGraphQLMaxDepth,
		"MaxComplexity":         config.DefaultGraphQLMaxComplexity,
		"MaxFieldRepetition":    config.DefaultGraphQLMaxFieldRepetition,
		"MaxResponseBytes":      config.DefaultGraphQLMaxResponseBytes,
		"MaxIntrospectionRoots": config.DefaultGraphQLMaxIntrospectionRoots,
		"MaxIntrospectionDepth": config.DefaultGraphQLMaxIntrospectionDepth,
		"MaxSelections":         config.DefaultGraphQLMaxSelections,
	}
	enforced := map[string]int{
		"MaxDepth":              graph.DefaultMaxDepth,
		"MaxComplexity":         graph.DefaultMaxComplexity,
		"MaxFieldRepetition":    graph.DefaultMaxFieldRepetition,
		"MaxResponseBytes":      graph.DefaultMaxResponseBytes,
		"MaxIntrospectionRoots": graph.DefaultMaxIntrospectionRoots,
		"MaxIntrospectionDepth": graph.DefaultMaxIntrospectionDepth,
		"MaxSelections":         graph.DefaultMaxSelections,
	}

	// The reflection audit forces the rule ITSELF: "every limit has an
	// environment variable and a matching default". A hand-written comparison
	// list applies that rule for today only; the eighth limit added tomorrow
	// silently stays outside it and the operator notices only in production that
	// they cannot tune it. This is exactly what HAPPENED once in the GRAPHQL
	// hardening.
	typ := reflect.TypeOf(graph.Options{})
	limitsWalked := 0
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !strings.HasPrefix(field.Name, "Max") {
			continue
		}
		limitsWalked++
		require.Contains(t, expected, field.Name,
			"graph.Options.%s is a limit but has NO default in the core; "+
				"every limit must have an environment variable and a matching default, "+
				"otherwise the operator cannot tune it", field.Name)
	}

	// The reflection audit depends ENTIRELY on the "Max" prefix. The day the
	// prefix goes away (when the fields move to names like DepthLimit and
	// ComplexityLimit) the loop enters no field at all and the "cannot stay
	// incomplete" assertion silently becomes able to stay incomplete; the
	// hand-written matches below, meanwhile, go on comparing all seven, so the
	// test stays green — that is, what is lost is COVERAGE.
	//
	// The counter only separates "none" from "at least one": when SOME of the
	// fields leave the prefix this gate passes and those fields fall out of
	// coverage. A stronger one (requiring the field count to match the match list
	// one to one) could have been written, but it adds a new assertion to the
	// audit; the question here is whether the audit has gone blind.
	require.Positive(t, limitsWalked,
		"graph.Options has not a SINGLE field with the \"Max\" prefix; the reflection "+
			"audit must have gone BLIND.\n"+
			"If the limits moved to names like DepthLimit and ComplexityLimit the loop "+
			"enters no field and the assertion \"every limit has an environment variable\" "+
			"becomes unverifiable on its own: the hand-written matches below go on "+
			"comparing seven, what is lost is COVERAGE and the limit added tomorrow "+
			"silently stays untunable. If the prefix changed, this audit must change too.")

	for name, cfgValue := range expected {
		assert.Equal(t, enforced[name], cfgValue,
			"the config's %s default must match the package that enforces the limit", name)
	}

	zero := graph.Options{}
	assert.Equal(t, config.DefaultGraphQLIntrospection, !zero.IntrospectionDisabled,
		"if graph's zero value leaves introspection on, the config's default must be on too")
}

// TestTheSalesChannelEntityNameAgrees verifies that the sales channel entity
// name product writes into its link definition is the same as the name under
// which auth REGISTERS its provider.
//
// product CANNOT import auth (Principle 2.4, ADR 0001), so
// [productsvc.EntitySalesChannel] cannot be bound to auth's constant; it repeats
// the value by hand. The rationale is the same as in
// [TestTheProviderRegistryNamesAgree] and is not repeated — here the concrete
// cost of drift is this: Query finds the target of the expansion from the entity
// name at the link's To end and looks the provider up as "<name>.query". If the
// names drift the lookup comes up empty and the product ↔ sales channel
// expansion returns errors.NotFound at run time.
//
// The second assertion closes the chain: the name auth writes into the container
// must really derive from the entity name; if auth one day registered its
// provider under the module name, the first assertion would still pass but the
// lookup would come up empty all the same.
func TestTheSalesChannelEntityNameAgrees(t *testing.T) {
	t.Parallel()

	assert.Equal(t, authsvc.Entity, productsvc.EntitySalesChannel,
		"the entity name product writes at the link end must match the entity name auth serves")
	assert.Equal(t, productsvc.EntitySalesChannel+query.ProviderSuffix, auth.ProviderName,
		"auth must register its provider under the name derived from the entity name; Query looks it up under that name")
}

// TestPluginsDoNotImportModules forces Phase 9's claim that a plugin "can be
// plugged in and selected without touching the core".
//
// The godoc of the core/plugin package says that a plugin WILL NOT
// import any commerce module, and plugins/paymentstripe honors this today. But
// no rule ENFORCES it: the depguard rules were written for the files under
// internal/modules, and the plugins/ tree falls within none of them.
//
// The cost of a violation is concrete: a plugin that imports the payment module
// is bound at compile time to that module's concrete type. From that moment on,
// pulling the module out into a separate service breaks the plugin, and testing
// the plugin requires standing up the whole payment chain. The right path for a
// plugin is to take the contract from core/provider and the
// registration point from coreplugin.Host.
func TestPluginsDoNotImportModules(t *testing.T) {
	t.Parallel()

	// If the tree is missing the audit is NOT SKIPPED, it FAILS: the only input of
	// this test is the plugin source, and a skipped run gives the impression that
	// the ban is still being enforced.
	root := filepath.Join(repoRoot, pluginsPath)
	require.DirExists(t, root,
		"the %s tree is MISSING; no file is left to walk for whether plugins import modules.\n"+
			"The depguard rules DO NOT COVER this tree, so once the scan goes quiet "+
			"nothing else is left enforcing the rule.", pluginsPath)

	pluginFiles := goFiles(t, root)
	require.NotEmpty(t, pluginFiles,
		"there is NO Go file at all under %s; the directory is still there but it has left "+
			"nothing to walk. A violation can only be found in a file's import list.", pluginsPath)

	prefix := modulePrefix(t)

	for _, file := range pluginFiles {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s could not be parsed: %v", file, err)
		}

		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, prefix) {
				t.Errorf("%s: the plugin imports the %q module.\n"+
					"A plugin must take the contract from core/provider and the "+
					"registration point from coreplugin.Host; it must not bind to the module's "+
					"concrete type.",
					file, strings.TrimPrefix(path, prefix))
			}
		}
	}
}

// TestTheBootstrapPasswordFloorIsAboveAuths verifies that the password length
// the config demands for the first administrator stays ABOVE the floor auth
// applies to EVERYONE.
//
// The two constants live in two separate packages and there is no compiler
// binding between them. If auth's floor one day catches up with the config's,
// the gate in config is SILENTLY neutralized: rejecting a second time a password
// auth already rejects adds nothing, and the claim that "a longer password is
// demanded in a shared environment" loses its truth. Silent neutralization is
// worse than a protection that was removed: the protection still looks as though
// it were there.
//
// The reason the test lives here is that the arch package is test-only and can
// import both the core and the modules; neither of the two packages can import
// the other (Principle 2.4).
func TestTheBootstrapPasswordFloorIsAboveAuths(t *testing.T) {
	t.Parallel()

	assert.Greater(t, config.MinBootstrapPasswordLen, authsvc.MinPasswordLen,
		"the first administrator's password must be STRICTLY longer than auth's general floor; "+
			"if they are equalized the gate in config adds nothing")
}

// The trees the symmetry audit walks, and its own name.
const (
	// configDirName is the core's configuration package. Its source is PARSED,
	// not imported: Go constants cannot be walked by reflection, so the only
	// structural answer to "which constants does this package have" is the source
	// itself.
	configDirName = "internal/core/config"

	// archDirName is the test package where the symmetry assertions live; coverage
	// is read from the Test* bodies in this tree.
	//
	// Not just this file but the WHOLE package is scanned: when an assertion is
	// written in a neighboring file (the configuration audits import config too)
	// the audit must see that one as well. If it did not, it would fail because of
	// an assertion written in the right place, and people would MOVE the assertion
	// into this file to silence the test.
	archDirName = "internal/arch"

	// symmetryAuditName is the name of the test that enforces the rule, and it is
	// kept OUT of the scan.
	//
	// If the audit one day read a constant from config (to show an example in an
	// error message, say), it would count that constant as "asserted" by its own
	// hand: the rule turns into a self-satisfying sentence and enforces nothing.
	//
	// This constant is itself a hand repetition and is subject to the same rule:
	// that such a test exists is VERIFIED inside [constantsInSymmetryAssertions],
	// because once the name drifts the exclusion would silently fall on nothing.
	symmetryAuditName = "TestEveryConfigConstantIsBoundToASymmetryAssertion"
)

// asymmetricConfigConstants lists, with their rationales, the config constants
// that have NO counterpart on the module side.
//
// An exemption is a debt: when a constant is written here, it is ASSERTED that
// "this value is not repeated in any other package". Even if the assertion is
// true today it may turn false tomorrow — the day someone writes the value on the
// module side as well, the line here HIDES exactly the drift this file exists to
// prevent. That is why a rationale is mandatory and its staleness is audited
// (see [checkStaleExemptions]).
var asymmetricConfigConstants = map[string]string{
	"BackendRedis": `the "redis" name lives in the core only: config's enum ` +
		`lists and internal/app's decision to set up the Redis client. No module ` +
		`repeats this string in a constant of its own, so there is no end to drift.`,

	"DefaultRedisKeyPrefix": `redisguard, which consumes the prefix, has NO ` +
		`default of its own; the prefix is a mandatory constructor parameter and ` +
		`is rejected if given empty (redisguard.validatePrefix). The value is not ` +
		`a repetition but a single source; the backward compatibility claim, too, ` +
		`lives in the constant's godoc rather than in a second copy of the value.`,

	"DefaultFileRoot": `the local provider's Options.Root field is MANDATORY and ` +
		`has no default; the root directory is chosen here only. Since there is no ` +
		`value repeated on the module side, there is no second end to compare with.`,

	"DefaultFileMaxUploadBytes": `the file service IS NOT BUILT without a ` +
		`positive limit (service.New rejects MaxUploadBytes <= 0), so there is no ` +
		`default on the module side — this is the limit's single source.`,

	"DefaultDatabaseURL": `its counterpart is not a Go package but ` +
		`deploy/docker-compose.yml and .env.example. The chain is already closed: ` +
		`the config package's TestDefaultTagsMatchConstants binds the constant to ` +
		`the envDefault tag, and TestTheEnvExampleAgreesWithTheConfigDefaults binds ` +
		`the tag. Writing a second copy here would not strengthen the assertion, ` +
		`it would only keep it in one more place.`,

	"DefaultRedisURL": `the rationale is the same as DefaultDatabaseURL's: its ` +
		`counterpart is the compose file and .env.example, not a module constant ` +
		`on the Go side.`,

	"MinIdempotencyMemoryBytes": `its counterpart is corehttp's UNEXPORTED ` +
		`maxIdempotentBodyBytes constant (the largest buffered response body) and ` +
		`internal/arch cannot see it. The chain was closed over there: corehttp's ` +
		`TestBudgetDefaultAgreesWithConfiguration test COMPARES the two constants, ` +
		`so drift fails that test. Copying the limit here would not strengthen the ` +
		`assertion, it would only produce a third copy that could drift.`,
}

// TestEveryConfigConstantIsBoundToASymmetryAssertion forces EVERY exported
// constant of config either to appear in a symmetry assertion or to be exempted
// with a rationale.
//
// # Which failure class
//
// All the tests in this file are the same rule applied one instance at a time:
// "because the core CANNOT import modules (Principle 2.4) it repeats the value by
// hand; if the repetition drifts no compiler warns". The rule had been applied to
// six cases ONE BY ONE — and even though the sixth one's INSIDE had been made
// structural ([TestTheGraphQLLimitDefaultsAgreeWithTheConfig] walks
// graph.Options), that assertion's EXISTING at all still depended on someone
// writing it by hand. And a rule applied by hand does not cover the SEVENTH case:
// a default added to config tomorrow would stay untested even if its counterpart
// sat in a module — and silently at that, because a MISSING assertion, unlike a
// wrong one, does not fail the test, it makes no sound. This HAPPENED once, in
// the fourth-provider case recounted in the godoc of
// [TestTheProviderRegistryNamesAgree].
//
// This audit takes the place of that habit: what enforces the rule is no longer
// an attentiveness repeated in six places but a single walk that cannot stay
// incomplete.
//
// # Why the scope is "every exported constant" and not "Default*"
//
// Filtering by name would be applying the rule BY HAND again: constants that do
// not start with Default, such as [config.BackendRedis] or
// [config.MinBootstrapPasswordLen], may perfectly well be repetitions of a value
// in another package (the second one really is, see
// [TestTheBootstrapPasswordFloorIsAboveAuths]). If a constant named
// "SeedAdminEmail" were added tomorrow, a prefix rule would silently leave it
// out. Being exported is a sufficient and correct criterion: a constant is
// exported only if ANOTHER package is going to read it, so exportedness already
// means "it has an end outside the package".
//
// var declarations are walked too. There is no exported var in config today;
// they are still looked at, because writing a variable instead of a constant is
// the cheapest way out of the rule, and that escape route would not be a
// deliberate decision but a lapse of attention.
//
// # How coverage is measured
//
// "Asserted" means the name appears in the form config.<Name> in a Test* BODY
// under internal/arch. The criterion is the AST, not text: MENTIONING
// [config.DefaultFileProvider] in a godoc does not count as coverage. If it did,
// anyone who mentioned the constant in a comment could silence the audit — and a
// value mentioned in a comment is never compared.
//
// The criterion is NOT as narrow as "is the value really compared"; it is
// technically possible for a test to read the constant and assert nothing. A
// narrower one (requiring it to be an argument of an assert call, say) would fix
// the FORM of the assertion and turn the test into expression recognition; the
// criterion here looks not for intent but for the BINDING: it is enough that the
// constant be in some audit's line of sight — because what will fail at the
// moment of drift is that audit.
func TestEveryConfigConstantIsBoundToASymmetryAssertion(t *testing.T) {
	t.Parallel()

	constants := exportedConfigConstants(t)
	assertions := constantsInSymmetryAssertions(t)

	for _, name := range slices.Sorted(maps.Keys(constants)) {
		if _, asserted := assertions[name]; asserted {
			continue
		}
		if rationale, exempt := asymmetricConfigConstants[name]; exempt {
			t.Logf("config.%s has no counterpart on the module side: %s", name, rationale)
			continue
		}
		t.Errorf("config.%s (%s) DOES NOT APPEAR in any symmetry assertion.\n"+
			"The core cannot import modules (Principle 2.4), so an exported config "+
			"constant is either a BY-HAND repetition of a value in another package or a "+
			"single source with no counterpart anywhere. If the former, its drift is "+
			"invisible at compile time and silent at run time most of the time as well; "+
			"if the latter, that needs to be WRITTEN DOWN.\n"+
			"To do: either compare it with its counterpart in a Test* body under %s/, "+
			"or add it to asymmetricConfigConstants WITH ITS RATIONALE.",
			name, repoPath(constants[name].String()), archDirName)
	}

	checkStaleExemptions(t, asymmetricConfigConstants, constants,
		"an exported constant of the config package", assertions, archDirName)
}

// exportedConfigConstants reads the exported const and var names of the config
// package from the source and returns them with their declaration positions.
//
// The source is parsed because in Go constants DO NOT EXIST at run time: reflect
// cannot give a package's constant list, the compiler inlines them at the point
// of use. Since there is no way to enforce the rule by reflection, the walk looks
// at the only place the compiler looks, the source itself.
func exportedConfigConstants(t *testing.T) map[string]token.Position {
	t.Helper()

	fset := token.NewFileSet()
	constants := map[string]token.Position{}

	for _, file := range parseDir(t, fset, filepath.Join(repoRoot, configDirName), false) {
		for _, decl := range file.tree.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					if !name.IsExported() {
						continue
					}
					constants[name.Name] = fset.Position(name.Pos())
				}
			}
		}
	}

	require.NotEmpty(t, constants,
		"no exported constant at all was found in %s; the walk must be broken — "+
			"an audit that finds nothing stays green in a vacuum", configDirName)

	return constants
}

// constantsInSymmetryAssertions returns, with their positions, the exported
// names that appear in the form config.<Name> in Test* bodies under
// internal/arch.
//
// Only Test* bodies are looked at: a package-level declaration (a table variable,
// say) may not be evaluated in any run, whereas the value of an assertion lies in
// its RUNNING. Helper functions are out of scope too; a helper asserts something
// only when it is called, and following the call chain would drag the audit
// toward writing a type checker — the helpers here already take the constants
// from their caller as PARAMETERS (see [assertDefaultProvider]), so the name is
// visible at the call site.
func constantsInSymmetryAssertions(t *testing.T) map[string]token.Position {
	t.Helper()

	fset := token.NewFileSet()
	configImportPath := modulePath + "/" + configDirName
	assertions := map[string]token.Position{}
	auditFound := false

	for _, file := range parseDir(t, fset, filepath.Join(repoRoot, archDirName), true) {
		alias := ""
		for name, path := range file.imports {
			if path == configImportPath {
				alias = name
				break
			}
		}

		for _, decl := range file.tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv != nil {
				continue
			}
			if !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			// That the excluded test REALLY exists is searched for in the files
			// that do not import config as well: the correctness of the exclusion
			// must not depend on an import.
			if fn.Name.Name == symmetryAuditName {
				auditFound = true
				continue
			}
			if alias == "" {
				continue
			}

			ast.Inspect(fn.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != alias || !selector.Sel.IsExported() {
					return true
				}
				if _, seen := assertions[selector.Sel.Name]; !seen {
					assertions[selector.Sel.Name] = fset.Position(selector.Sel.Pos())
				}

				return true
			})
		}
	}

	require.NotEmpty(t, assertions,
		"no config.<Name> use at all was found in %s; the scan must be broken — "+
			"an empty coverage set would show ALL the constants as unasserted and "+
			"drown the audit in noise", archDirName)

	require.True(t, auditFound,
		"there is NO test named %q in %s; symmetryAuditName must have gone stale.\n"+
			"The constant is a by-hand repetition of the audit's OWN name — that is, an "+
			"instance of the very class this file tries to close. Once the name drifts the "+
			"exclusion falls on nothing and the audit starts silently approving itself by "+
			"counting the config uses in its own body as coverage.", symmetryAuditName, archDirName)

	return assertions
}

// assertDefaultProvider verifies that the THREE ends of a provider family point
// at the same identity: the config's default, the module's default and the
// provider's own identity.
//
// The third end closes the chain. Comparing only the config with the module would
// stay silent if the two slid TOGETHER (to the same wrong value); the provider's
// own identity, on the other hand, is the registration key itself, that is, the
// name looked up at run time.
func assertDefaultProvider(t *testing.T, family, providerID, configDefault, moduleDefault string) {
	t.Helper()

	assert.Equal(t, providerID, configDefault,
		"the config's default %s provider must be the module's out-of-the-box provider", family)
	assert.Equal(t, providerID, moduleDefault,
		"the %s module's default must be the same provider", family)
}

// TestThePanelCatalogNamesAgree verifies that the catalog names the panel writes
// BY HAND are the same as the modules' own constants.
//
// The panel IMPORTS no module (ADR 0011) and reaches the read layer BY NAME like
// everyone else (ADR 0004). The cost is that these names are repeated by hand in
// two places and that their drift is SILENT: the day a link name changes the
// panel compiles, returns 200, and only the price column goes empty. The
// difference is not seen until someone looking at the catalog screen takes it for
// "this product has no price".
//
// This test moves that binding to COMPILE time; the rationale here is exactly the
// same as [TestTheProviderRegistryNamesAgree]'s, and the arch package is again the
// only suitable place, because it is the only package that can import both the
// panel and the modules.
//
// # The names this test DOES NOT COVER
//
// Most of the FILTER and FIELD names the panel uses (product's "id"/"product_id"
// filters, pricing's "prices"/"amount"/"currency_code" fields) are NOT exported
// in the module that owns them; they cannot be compared at compile time. Their
// only protection is the read layer's own answer: an unrecognized filter or field
// is rejected with errors.Invalid (ADR 0004) and the panel turns that into a 500
// saying "the screen asked the read layer for something it does not serve". So
// the failure is NOISY, but it is seen on the first request, not at build time.
//
// The sales report's date filters ("placed_from"/"placed_to") are that same hole
// with the sides swapped: the order module DOES export those two names, and it is
// the PANEL's copies that are unexported, so the pair still cannot be bound at
// compile time. It is tolerable for the same reason as the rest — an unrecognized
// filter is REFUSED by the provider (ADR 0004) rather than ignored, so a drift
// makes the report answer 500 instead of quietly reporting a different period.
// The entity NAME is the one that had to be pinned here: it is the string the
// whole request is addressed to, and the panel repeats it by hand.
func TestThePanelCatalogNamesAgree(t *testing.T) {
	t.Parallel()

	assert.Equal(t, productsvc.EntityProduct, adminui.EntityProduct,
		"the panel's product entity name must match the product module")
	assert.Equal(t, productsvc.EntityVariant, adminui.EntityVariant,
		"the panel's variant entity name must match the product module")
	assert.Equal(t, regionsvc.Entity, adminui.EntityRegion,
		"the panel's region entity name must match the region module")
	assert.Equal(t, ordersvc.LineItemEntity, adminui.EntityOrderLineItem,
		"the panel's order line entity name must match the order module")

	assert.Equal(t, productsvc.LinkVariantPriceSet, adminui.LinkVariantPriceSet,
		"the panel's price link name must match the product module")
	assert.Equal(t, productsvc.LinkVariantInventory, adminui.LinkVariantInventory,
		"the panel's inventory link name must match the product module")

	assert.Equal(t, inventorysvc.FieldAvailableQuantity, adminui.FieldAvailableQuantity,
		"the panel's available quantity field name must match the inventory module")

	assert.Equal(t, product.AdminName, adminui.ServiceProductAdmin,
		"the panel's product write surface name must match the product module")
	assert.Equal(t, pricing.AdminName, adminui.ServicePricingAdmin,
		"the panel's price write surface name must match the pricing module")
	assert.Equal(t, inventory.AdminName, adminui.ServiceInventoryAdmin,
		"the panel's inventory surface name must match the inventory module")
}

// TestThePanelStatusOptionsAgreeWithTheModules verifies that the statuses the
// panel offers in its edit form are the same as the ones the module ACCEPTS.
//
// It can drift in both directions and each direction fails in a different way:
//
//   - A status that IS in the panel and not in the module: the operator picks it,
//     the write surface rejects it, the form comes back with an error message.
//     Noisy and fixable.
//   - A status that IS in the module and not in the panel: the form never shows
//     it, the operator cannot move to that status and sees NO error at all. They
//     cannot even know the capability exists.
//
// The second one is this test's real reason: it is the silent one.
func TestThePanelStatusOptionsAgreeWithTheModules(t *testing.T) {
	t.Parallel()

	moduleStatuses := []string{
		productmodels.StatusDraft.String(),
		productmodels.StatusPublished.String(),
		productmodels.StatusArchived.String(),
	}

	assert.ElementsMatch(t, moduleStatuses, adminui.ProductStatuses(),
		"the panel's status list must match the module's accepted ones exactly")

	for _, status := range adminui.ProductStatuses() {
		assert.True(t, productmodels.Status(status).Valid(),
			"the panel offers the %q status but the module does not consider it valid", status)
	}
}

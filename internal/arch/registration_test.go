package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Paths of the composition roots and of the core contract.
const (
	// compositionRoot is the package where the production binary builds the
	// module registry. Its source is PARSED, not IMPORTED: a main package
	// cannot be imported.
	compositionRoot = "cmd/server"
	// e2eHarness is the package where the end-to-end tests bring the modules up
	// with real migrations. That package sits behind the "integration" build
	// tag; parsing does not care about the tag, so this audit runs even without
	// an integration run.
	e2eHarness = "internal/e2e"
	// coreModulePackage is the core package where the
	// [github.com/bdrtr/gobit/core/module.Module] contract and the
	// [github.com/bdrtr/gobit/core/module.Registry] registry live.
	//
	// The godocs below name those same two names SHORT (module.Module,
	// module.Registry) and do not write them as links: this package does not
	// import core/module, so a short name would resolve nowhere. The full path
	// is given exactly once, here, where the name is defined.
	coreModulePackage = modulePath + "/core/module"
	// workflowsDirName is the tree where the cross-module workflows live (ADR
	// 0006). It is neither core nor a module: the depguard rules are for
	// internal/modules, and the module registration audit walks below
	// internal/modules — so until today NO wiring rule covered this tree.
	workflowsDirName = "internal/workflows"
	// adminUIDirName is the tree where the admin panel lives (ADR 0011) and it
	// shares the SAME gap as [workflowsDirName]: because it is neither core nor
	// a module, no wiring rule covers it on its own.
	//
	// The tree was opened this round and the gap was closed THE MOMENT it
	// opened: a capability with no registration is this repository's most
	// expensive class of defect, and reopening it for a new tree would mean
	// paying a second time the price ADR 0006 already paid once.
	adminUIDirName = "internal/adminui"
	// coreContainerPackage is the core package where the container lives. The
	// mark that a workflow package is "designed to be built from the container"
	// is that one of its exported functions takes this type as a PARAMETER.
	coreContainerPackage = modulePath + "/core/container"
	// setupMarkerName is the conventional name of the workflow constructors.
	//
	// The forward audit does NOT USE this name (it looks at the shape, see
	// [workflowPackagesBuiltFromContainer]); the name is only needed in the
	// REVERSE direction, to catch a package that the composition root builds
	// but that the audit does not SEE as a constructor. Its staleness is
	// verified inside [TestEveryWorkflowIsSetUpInTheCompositionRoot].
	setupMarkerName = "FromContainer"
)

// moduleContract is the method set of the module.Module interface: from the
// method name to the expected parameter and result types.
//
// Signatures are compared as SOURCE TEXT (go/types.ExprString), because this
// audit does not need to run the type checker and if it did it would pull a new
// dependency into go.mod. The price of that is that the match escapes if the
// package is imported under a different alias; the price is paid inside
// [packagesImplementingModule]: if all four method NAMES are found and a
// signature does not hold, the test does NOT PASS SILENTLY, it errors.
var moduleContract = map[string]struct {
	parameters []string
	results    []string
}{
	"Name": {results: []string{"string"}},
	"Register": {
		parameters: []string{"context.Context", "*container.Container"},
		results:    []string{"error"},
	},
	"Migrations": {results: []string{"fs.FS"}},
	"Routes":     {parameters: []string{"chi.Router"}},
}

// unregisteredModules holds the rationales of the module packages that are
// DELIBERATELY left out of the registry in the composition root; the key is the
// package's import path.
//
// # Why the exemption exists
//
// A module that is written but not yet wired is a real situation (an unfinished
// phase, a module meant only for embedded use). Making it impossible to leave
// such a module OUT of the registry would push the developer to look for another
// way of silencing the test, and that path would always be quieter.
//
// # Why the exemption is HERE
//
// The mark is NOT a silent file sitting in a directory like ".gitkeep", it is a
// line of code with a written rationale: the rationale shows up in code review,
// it gets a date and an owner in git blame, it cannot be added without a
// rationale (the value is mandatory) and thanks to the STALE EXEMPTION audit
// below it cannot rot — if an exempted module is registered one day, or if the
// package is deleted, the test still fails. So an exemption is a debt, and the
// debt stays visible.
//
// Today it is empty: fifteen of the repository's fifteen modules are registered.
var unregisteredModules = map[string]string{}

// setupsOffTheStartupPath maps the import path of the workflow packages that are
// DELIBERATELY exempt from the goroutine audit to their rationale.
//
// # Why there is a gate
//
// The audit is a syntactic PROXY (see
// [TestEveryWorkflowIsSetUpInTheCompositionRoot]) and it gives a FALSE POSITIVE
// in one direction: a parallel setup that startup REALLY waits for with a
// sync.WaitGroup or an errgroup, and whose failure REALLY stops startup, also
// fails as "not on the startup path". It was measured. Parallel startup is an
// ordinary pattern in Go, and when that day comes there are two options: silence
// the audit or write a rationale. Without the gate silencing would be the only
// path, and a silenced audit is worse than one that was never written — it stays
// in place and looks as if it gives confidence.
//
// # What must be written when entering the gate
//
// The rationale must say concretely HOW startup waits for the setup and HOW the
// failure stops startup. "We build in parallel" is not enough; the property
// being looked for is not parallelism, it is that the failure COMES BACK to
// startup.
//
// Today it is EMPTY: setup is synchronous and no exemption is needed.
var setupsOffTheStartupPath = map[string]string{}

// modulesOutsideE2EHarness holds the rationales of the module packages that are
// DELIBERATELY not set up on the e2e harness; the key is the package's import
// path.
//
// The reason a separate map is kept is that the two exemptions say DIFFERENT
// things: [unregisteredModules] says "this module is not in production", this
// map says "this module is in production but cannot be run on the end-to-end
// harness" (for example a module with a hard dependency on an external service).
// The second is a far lighter debt than the first, and standing in the same list
// would make the heavy one look light.
//
// Today it is empty: the harness sets up every module production registers.
var modulesOutsideE2EHarness = map[string]string{}

// workflowsNotSetUp holds the rationales of the workflow packages that are
// DELIBERATELY not set up in the composition root; the key is the package's
// import path.
//
// Why the exemption exists and why it is HERE is told in the
// [unregisteredModules] godoc and is not repeated. The reason this map stands
// apart is that the SIZE of the debt is different: an unregistered module only
// loses its own surface, whereas a workflow that is not set up also closes the
// module endpoints that resolve it BY NAME (see linePricing in the cart module).
// Keeping the two debts in the same list would make the heavy one look light.
//
// Today it is empty: both packages of internal/workflows are set up in the
// composition root.
var workflowsNotSetUp = map[string]string{}

// parsedFile carries the parsed form of a Go file and its import aliases.
type parsedFile struct {
	path    string
	tree    *ast.File
	imports map[string]string
}

// workflowSetup is the place of a workflow setup found in the composition root.
type workflowSetup struct {
	// position is the source position of the reference made to the constructor.
	position token.Position
	// enclosing is the name of the function containing the reference; it is
	// empty if the reference is in a package-level declaration.
	enclosing string
	// constructor is the name of the called constructor.
	constructor string
	// notCounted says that the reference was found but is NOT counted as a VALID
	// setup (dead code, or a reference that is not called). Its reason was
	// reported where it was found; the calling side does not produce a second
	// error.
	notCounted bool
	// inGoroutineForm says that the setup was found under or behind a go
	// statement. It is marked whether or not there is an exemption: only this
	// field can show that an exemption has GONE STALE.
	inGoroutineForm bool
}

// TestEveryModuleIsRegisteredInTheCompositionRoot audits the invariant "every
// module that is written is registered IN THE COMPOSITION ROOT".
//
// # Which class of defect
//
// This repository's most expensive class of defect is A CAPABILITY WITH NO
// CONSUMER: the whole of Phase 8 and Phase 9 was written, their tests were
// green, and NONE of the /admin/v1/** endpoints were mounted — the admin surface
// never existed in any deployment. The same thing repeated in the b2b module:
// the module, its migration, its endpoints and its tests were ready but there
// was no registration for it in cmd/server, so the spending limit was enforced
// in NO deployment and order counted every customer as unlimited.
//
// What both failures have in common is that no test failed: a module's own tests
// set the module up themselves, so they are green. What is missing is not in the
// module, it is BETWEEN the module and the composition root, and nobody was
// auditing that place.
//
// # Why it keeps no list
//
// The audit WALKS the module directories and derives who satisfies the
// module.Module contract from the method set. A hand-written module list would
// enforce the rule only for TODAY: when the person writing the sixteenth module
// forgot to update the list the test would stay green — that is, it would miss
// exactly the defect it is supposed to catch.
//
// # Why it parses instead of importing
//
// The composition root is a main package and CANNOT BE IMPORTED. That is why the
// registration is read from source: the module.Registry variable is found, the
// Add calls on it are collected, and which package each call's argument goes to
// is resolved from the file's import list.
func TestEveryModuleIsRegisteredInTheCompositionRoot(t *testing.T) {
	t.Parallel()

	modules := packagesImplementingModule(t)
	require.NotEmpty(t, modules,
		"no package implementing [module.Module] was found below internal/modules; "+
			"the audit must have gone BLIND (did the contract change?)")

	registered := registeredModulePackages(t, compositionRoot, false)

	for _, path := range slices.Sorted(maps.Keys(modules)) {
		if _, isRegistered := registered[path]; isRegistered {
			continue
		}
		if rationale, exempt := unregisteredModules[path]; exempt {
			t.Logf("%s is deliberately NOT REGISTERED in the composition root: %s", path, rationale)
			continue
		}
		t.Errorf("package %s implements [module.Module] (%s) but is NOT ADDED to the registry in %s/.\n"+
			"A module that is not registered exists in no deployment: its migration is not "+
			"applied, its service does not enter the container, its endpoints are not mounted "+
			"and because the module's own tests stay green this shows up nowhere.\n"+
			"Either add the registry.Add(...) line in cmd/server/main.go, or write the module "+
			"into the unregisteredModules map together with its rationale.",
			path, strings.Join(modules[path], ", "), compositionRoot)
	}

	// The reverse direction closes the audit's OWN blind spot: if the audit does
	// NOT SEE as a module an internal/modules package that the composition root
	// registers, then the contract reading has drifted. From that moment on this
	// test would say not "every module is registered" but "the modules I see are
	// registered" — that is, it would silently leave tomorrow's module out of
	// scope and stay green.
	for _, path := range slices.Sorted(maps.Keys(filterToModulePackages(registered))) {
		if _, seen := modules[path]; !seen {
			t.Errorf("package %s is registered in %s/ but the audit does NOT SEE it as a "+
				"package implementing [module.Module].\n"+
				"The registration proves that it is a module; the audit not seeing it means "+
				"that the contract reading (moduleContract) has drifted from reality, and that "+
				"leaves this test blind from here on.", path, compositionRoot)
		}
	}

	checkStaleExemptions(t, unregisteredModules, modules,
		"a package implementing [module.Module]", registered, compositionRoot)
}

// TestEveryRegisteredModuleIsSetUpInTheE2EHarness audits that every module
// registered in the composition root is set up on the end-to-end harness as
// well.
//
// # Why the second half of the invariant is needed
//
// The first half ([TestEveryModuleIsRegisteredInTheCompositionRoot]) asks "is
// the module that was written registered in production". On its own that is not
// enough, because REGISTRATION and RUNNING are not the same thing: the
// registry.Add line compiles, it runs at startup, and that module's real
// migration, real routes and cross-module link may still never have been tried
// together in any test. b2b was exactly like this — the registration itself was
// one line, and the only place that shows that line changing the order module's
// behavior is the e2e harness.
//
// # Why the e2e harness and not some other harness
//
// The harness is the only copy of what the deployment looks like in production:
// the same core service names, the same module.Registry, a real PostgreSQL, real
// migrations and tests that do the authorization audit by walking the router
// TREE (see the authorization test in internal/e2e, 196 admin endpoints). The
// moment a module enters that harness it also enters the scope of the existing
// tree-walking tests; until it does, no matter how many tests are written it
// stays in its own bubble.
//
// # Would this test have seen the Phase 8/9 failure
//
// Not directly, the first half would have. But the requirement that the harness
// and production hold the SAME set would have prevented the failure from
// forming: the moment the Phase 8/9 modules were added to the harness this test
// would have demanded the registration that was missing in production; had they
// not been added to the harness either, the first half would already have
// failed. The two halves together close the case "the module exists but is in no
// composition root".
//
// # Why internal/e2e is not touched
//
// e2e_test.go already SAYS "The module set and its order are the same as the one
// in cmd/server/main.go". That is a comment's promise, and this repository's
// third class of defect is exactly that: the godoc's promise drifting from the
// code's behavior. What enforces a promise is not another line written next to
// it but a test that audits it from the outside; the test does NOT CHANGE that
// file, it only reads it.
func TestEveryRegisteredModuleIsSetUpInTheE2EHarness(t *testing.T) {
	t.Parallel()

	production := filterToModulePackages(registeredModulePackages(t, compositionRoot, false))
	harness := filterToModulePackages(registeredModulePackages(t, e2eHarness, true))

	// Both ends are guarded separately. If the production end empties out the
	// loop never turns and this test — even though its name says "every
	// registered module" — passes without saying anything about any module.
	// [TestEveryModuleIsRegisteredInTheCompositionRoot] fails loudly in that
	// case, but RELYING on the neighbor failing is not a guard: the two tests
	// can change independently of each other, and on that day the silence here
	// would go unnoticed.
	require.NotEmpty(t, production,
		"no module registration was found in %s/; the audit must have gone BLIND — the "+
			"registration form (Add on module.Registry) may have changed", compositionRoot)
	require.NotEmpty(t, harness,
		"no module registration was found on the e2e harness; the audit must have gone BLIND")

	for _, path := range slices.Sorted(maps.Keys(production)) {
		if _, isSetUp := harness[path]; isSetUp {
			continue
		}
		if rationale, exempt := modulesOutsideE2EHarness[path]; exempt {
			t.Logf("%s is deliberately NOT SET UP on the e2e harness: %s", path, rationale)
			continue
		}
		t.Errorf("module %s is registered in %s/ (%s) but is NOT SET UP on the %s/ harness.\n"+
			"The production wiring of a module that does not enter the harness is tried end "+
			"to end nowhere: its migration does not run on a real database, its endpoints do "+
			"not enter the scope of the authorization audit that walks the router tree, and "+
			"its cross-module link is only exercised against FAKE counterparts.\n"+
			"Either add it to the harness, or write the module with its rationale into the "+
			"modulesOutsideE2EHarness map.",
			path, compositionRoot, production[path], e2eHarness)
	}

	// The reverse direction is NOT audited HERE, and that is not a gap: a module
	// that is set up on the harness but NOT registered in production is exactly
	// the Phase 8/9 failure, and
	// [TestEveryModuleIsRegisteredInTheCompositionRoot] already fails it. Two
	// tests saying the same thing twice would mean that when one of them changes
	// the other quietly becomes redundant.
	checkStaleExemptions(t, modulesOutsideE2EHarness, production,
		"a module registered in the composition root", harness, e2eHarness)
}

// TestEveryWorkflowIsSetUpInTheCompositionRoot audits the invariant "every
// workflow written to be built from the container really is set up IN THE
// COMPOSITION ROOT".
//
// # Which class of defect
//
// The very class the two tests above close — but with an instance that fell
// OUTSIDE their scope. internal/workflows/cart and internal/workflows/checkout
// were written, their unit tests were green, they were proven on the end-to-end
// harness and they were wired into the composition root NOT AT ALL: cmd/server
// only registered the saga ENGINE, and nobody in production code called the two
// workflows' FromContainer. That is, in the running binary there was no path
// turning a cart into an order — payment, shipping, the checkout promotion, the
// order.placed notification and the b2b spending limit were unreachable — while
// the README described it as a capability that was offered.
//
// [TestEveryModuleIsRegisteredInTheCompositionRoot] could not have seen this:
// that audit walks below internal/modules and looks for the module.Module
// contract. Workflows are NOT modules (they do not carry the four methods) and
// they do not stand below internal/modules, so the SCOPE of the invariant had
// left an instance of the class it was supposed to close outside. This test
// widens that scope.
//
// # Why the mark is the SHAPE and not the name "FromContainer"
//
// Looking at the name would enforce the rule only for TODAY: a third workflow
// package that names its constructor Build or New would not exist at all in the
// audit's eyes, and on the day it was not wired the test would still stay green
// — that is, it would miss exactly the defect it is supposed to catch. What
// makes a package "built from the container" is not its constructor's NAME but
// its SHAPE: an exported function taking a *container.Container is the mark that
// the package resolves its dependencies from the registry BY NAME and that it
// can only do so after the Register loop has finished.
//
// The conventional name is still good for something, and it is used in the
// REVERSE direction: if the composition root calls FromContainer while the audit
// does NOT SEE a constructor taking a container in that package, then the shape
// reading has drifted from reality (for example the signature has started taking
// an interface instead of the container) and from that moment on the audit would
// silently leave the package out of scope.
//
// # Why DEAD CODE does not count as a setup either
//
// The shape of the failure that was found was exactly this: FromContainer WAS
// being called — but only from inside internal/e2e. In the production binary it
// is not enough for the setup to EXIST, it has to be REACHABLE from main(); a
// setup function that is not reached is the same thing as a setup function that
// was never written, and neither the compiler nor the module's own tests know
// about it. That is why the audit walks the composition root's call graph from
// main() and does not count a setup it cannot reach.
//
// The graph is built FROM NAMES and is deliberately kept WIDE: if a name occurs
// in the body of another name there is an edge (even without a call, for example
// when it is passed as a value), and the nodes are not only functions but also
// the package-level var/const declarations — taking the setup into a function
// variable does not move it out of the graph (see [compositionRootNodes]). An
// overly wide graph at worst takes a dead setup for a live one; a narrow graph
// would declare a live setup dead, fail the test in the wrong place, and people
// would stop trusting the audit.
//
// # Why BEHIND A GO STATEMENT does not count as a setup either
//
// Taking the setup into a go statement does NOT MAKE it dead: it runs, it writes
// to the container and the store works. It was measured — when the setup was
// moved inside "go func() { ... }()" both this audit's reachability question and
// the smoke run PASSED, and the real process produced an order end to end. Both
// layers ask "is the setup running" and both RIGHTLY say yes.
//
// What silently disappears is STARTUP FAILING CLOSED. The same setup FAILURE was
// measured in two forms: in the synchronous call the process did NOT come up at
// all ("workflow_setup_failed", exit code 1); with the go statement the process
// came up healthy, /health returned 200, the admin surface worked completely, a
// single ERROR line was left in the log and the failure blew up as a 500 on the
// first cart line. That is, the go form turns a STARTUP failure into a RUNTIME
// failure and nothing reports it.
//
// The property being looked for is therefore not "is the call synchronous" but
// "CAN A MISCONFIGURATION STOP STARTUP". Testing that property at runtime is not
// possible today: a configuration that makes the workflow setup fail cannot be
// written, because FromContainer only resolves module interops that are ALWAYS
// registered. What is left is the structural question the static side can
// answer, and that is the third question added next to the reachability the
// graph already computes: does the setup's path to main() pass through a GO
// STATEMENT?
//
// The question is asked in TWO places at once, because the form is of two kinds
// as well: the call ITSELF may be under a go statement (in which case no edge in
// the name graph changes), or the function containing the call may be reachable
// from main() only from behind a go statement (in which case there is no trace
// in the call's syntax). Had only one been asked, the other form would have been
// left uncovered.
//
// The rule is only for TRACKED SETUP CALLS and does NOT say "using goroutines is
// forbidden": the server itself, the event bus and the shutdown watchers use
// goroutines and must do so. Because none of them calls a workflow constructor,
// the audit never sees them.
//
// # What this invariant does NOT GUARANTEE
//
// It guarantees only THIS: somewhere in the composition root, reachable from
// main() without entering a go statement, every workflow package's constructor
// is called — that is, the setup call is ON THE STARTUP PATH and its failure can
// stop startup. That is the question static analysis can answer, and claiming
// more would be wrong.
//
// The difference between "can stop" and "does stop" is deliberate: the audit
// sees that the call stands on the startup path, it cannot see that the returned
// error is NOT SWALLOWED. A setup that logs the error and carries on also passes
// this test.
//
// # The goroutine question is a PROXY and it drifts in both directions
//
// The property being looked for is "can a misconfiguration stop startup". What
// is implemented is NOT that property but its syntactic proxy: "does the path to
// the setup call pass through a go statement". The two DO NOT OVERLAP, and both
// directions of the drift were MEASURED:
//
//   - FALSE POSITIVE: a setup built in parallel with a sync.WaitGroup or an
//     errgroup, which startup REALLY waits for with Wait() and whose failure
//     REALLY stops startup, FAILS this audit — even though the property being
//     looked for holds. Parallel startup is an ordinary pattern in Go, which is
//     why the [setupsOffTheStartupPath] gate exists: so that when that day comes
//     the option is not "silence the audit" but "write a rationale".
//
//   - FALSE NEGATIVE: when the go statement is hidden inside a one-line
//     indirection —
//
//     func inBackground(fn func()) { go fn() }
//     inBackground(func() { registerWorkflows(c) })
//
//     — the audit PASSES, even though the property does not hold: it was
//     measured in a real process, where on a setup failure the synchronous
//     binary exits with "fatal" and exit code 1 while the binary in this form
//     comes up healthy and reduces the failure to a single ERROR line.
//
// The proxy is kept anyway because the forms it DOES CATCH (a bare "go", a
// closure, a multi-link chain) are the forms somebody would write by accident;
// the form it misses has to be written on purpose. But the sentence "this
// invariant guarantees that startup fails closed" would be WRONG and is not
// built here.
//
// The question it cannot answer is whether the call RUNS. When the setup is
// taken behind a condition —
//
//	var enableWorkflows = false
//	if enableWorkflows { registerWorkflows(c) }
//
// — the call keeps standing in the graph and this test PASSES, even though in
// the running binary no line can be added to a cart and no cart can be turned
// into an order (both endpoints return 500: the cart module cannot resolve the
// workflow and fails closed). Reading and evaluating the flag would not have
// saved it either: the value can come from an environment variable, from a
// configuration or from another call's return, and at that point the audit turns
// into a bad imitation of running the application.
//
// # The missing half: RUNTIME PROOF
//
// That question is answered only by a run that REALLY USES the path, and the
// answer is in internal/smoke: TestStorefrontFromCartToOrderInARealProcess brings up
// the real binary, fills the cart with a line priced from the catalog and turns
// the cart into an order. A flag, a condition or a variable — EVERY mutation
// that closes the path fails there, because that test uses the path.
//
// The two layers do NOT REPLACE each other, they complete each other:
//
//   - The static invariant fails when the setup line is DELETED and when the
//     setup is TAKEN OFF THE STARTUP PATH; it does so without docker, in
//     seconds, naming the package that is not set up. The smoke run reports the
//     first only as "the line could not be added, 500"; for a diagnosis one has
//     to go down to the source. The second it cannot see AT ALL: a setup moved
//     into a go statement runs without trouble on the smoke harness.
//   - Smoke fails when the setup DOES NOT RUN. The static invariant cannot see
//     that case and does not claim that it can.
//
// If one of them is removed the other is ASSUMED to be enough; that is why what
// each of them closes is written down.
//
// # Why it keeps no list, why it parses
//
// The reasons are word for word the ones in the
// [TestEveryModuleIsRegisteredInTheCompositionRoot] godoc and are not repeated:
// a hand-written workflow list would miss the third workflow, and the
// composition root cannot be imported because it is a main package.
//
// # Why there is NO e2e twin
//
// On the module side there is a second half
// ([TestEveryRegisteredModuleIsSetUpInTheE2EHarness]) because there REGISTRATION
// and RUNNING are separate things. Workflows have no such distinction: a
// workflow only exists where it is set up, and if the harness does not set it up
// the store endpoints fail CLOSED — that is, a person who forgets to set a
// workflow up on the harness cannot see a green run; the storefront scenarios
// take a 500 right then. Writing the rule a second time would be repeating a
// requirement that already enforces itself.
func TestEveryWorkflowIsSetUpInTheCompositionRoot(t *testing.T) {
	t.Parallel()

	workflows := workflowPackagesBuiltFromContainer(t, workflowsDirName)
	require.NotEmpty(t, workflows,
		"no package built from the container was found below %s; the audit must have gone "+
			"BLIND (do the constructors no longer take *container.Container?)", workflowsDirName)

	conventionAlive := false
	for _, constructors := range workflows {
		if slices.Contains(constructors, setupMarkerName) {
			conventionAlive = true
			break
		}
	}
	require.True(t, conventionAlive,
		"NO workflow package has a constructor named %q; setupMarkerName must have gone "+
			"stale.\nThe constant is the only foothold of the reverse-direction audit: when it "+
			"goes stale, a package that the composition root builds but the audit cannot see "+
			"silently falls out of scope.",
		setupMarkerName)

	setUp := workflowsSetUpInCompositionRoot(t, workflows, workflowsDirName)
	checkStaleGoroutineExemptions(t, workflows, setUp)

	live := map[string]token.Position{}
	for path, setup := range setUp {
		if !setup.notCounted {
			live[path] = setup.position
		}
	}

	for _, path := range slices.Sorted(maps.Keys(workflows)) {
		if _, isSetUp := live[path]; isSetUp {
			continue
		}
		// The error for a setup that was found but not counted was already given
		// where the reason for not counting it is known; saying it a second time
		// here, in a cruder sentence, would bury the right diagnosis in noise.
		if _, found := setUp[path]; found {
			continue
		}
		if rationale, exempt := workflowsNotSetUp[path]; exempt {
			t.Logf("%s is deliberately NOT SET UP in the composition root: %s", path, rationale)
			continue
		}
		t.Errorf("package %s is written to be built from the container (%s) but is NOT SET UP "+
			"in %s/.\n"+
			"A workflow that is not set up exists in no deployment: the module endpoints that "+
			"resolve it from the container BY NAME fail closed, the whole cross-module chain "+
			"(price, discount, tax, payment, shipping, notification) becomes unreachable, and "+
			"because the workflow's own tests set the workflow up themselves this shows up "+
			"nowhere.\n"+
			"Either call the constructor in %s/ and leave the result in the container, or "+
			"write the package into the workflowsNotSetUp map together with its rationale.",
			path, strings.Join(workflows[path], ", "), compositionRoot, compositionRoot)
	}

	// The reverse direction closes the audit's OWN blind spot: if the composition
	// root calls a workflow package's constructor while the audit does not see a
	// constructor taking a container in that package, the shape reading has
	// drifted from reality. From that moment on this test would say not "every
	// workflow is set up" but "the workflows I see are set up".
	for _, path := range slices.Sorted(maps.Keys(setUp)) {
		if _, seen := workflows[path]; !seen {
			t.Errorf("package %s's %q constructor is called in %s/ (%s) but the audit does NOT "+
				"SEE a constructor built from the container in that package.\n"+
				"The call itself proves that the package is built from the container; the audit "+
				"not seeing it means that the shape reading (exported + a *container.Container "+
				"parameter) has drifted from reality, and that leaves this test blind from here "+
				"on.",
				path, setUp[path].constructor, compositionRoot, setUp[path].position)
		}
	}

	checkStaleExemptions(t, workflowsNotSetUp, workflows,
		"a workflow package built from the container", live, compositionRoot)
}

// checkStaleExemptions catches the rot of an exemption map.
//
// An exemption goes stale in two ways: the exempted package NO LONGER EXISTS (a
// module that was deleted or renamed) or the package IS NOW REGISTERED. If both
// stay silent the map turns into a pile of comments nobody reads and nobody
// verifies — and at that moment the whole justification for keeping the
// exemption inside the code falls away too.
func checkStaleExemptions[T any](
	t *testing.T,
	exemptions map[string]string,
	candidates map[string]T,
	candidateDescription string,
	registered map[string]token.Position,
	root string,
) {
	t.Helper()

	for _, path := range slices.Sorted(maps.Keys(exemptions)) {
		if _, isCandidate := candidates[path]; !isCandidate {
			t.Errorf("STALE exemption: %q is no longer %s.\n"+
				"If the package was deleted or renamed the exemption line must go too; if it "+
				"stays it will one day silently exempt a new module written under the same name.",
				path, candidateDescription)
			continue
		}
		if position, isRegistered := registered[path]; isRegistered {
			t.Errorf("STALE exemption: %q is now registered in %s/ (%s) but is still exempt.\n"+
				"An exemption is a debt; when the debt is paid the line must be deleted.", path, root, position)
		}
	}
}

// packagesImplementingModule returns the packages below internal/modules that
// satisfy the module.Module contract: the key is the package's import path, the
// value the names of the types that satisfy the contract.
//
// Helper packages that are not modules (api, service, repository, models…) are
// eliminated because the contract is read FROM THE METHOD SET, not from the
// directory name: a module's sub-package does not carry all four at once.
func packagesImplementingModule(t *testing.T) map[string][]string {
	t.Helper()

	found := map[string][]string{}
	for _, pkgDir := range slices.Sorted(maps.Keys(productionPackages(t, filepath.Join(repoRoot, modulesDir)))) {
		fset := token.NewFileSet()
		files := parseDir(t, fset, pkgDir, false)
		receiverMethods := map[string]map[string]*ast.FuncDecl{}

		for _, d := range files {
			for _, decl := range d.tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
					continue
				}
				typeName := receiverTypeName(fn.Recv.List[0].Type)
				if typeName == "" {
					continue
				}
				if receiverMethods[typeName] == nil {
					receiverMethods[typeName] = map[string]*ast.FuncDecl{}
				}
				receiverMethods[typeName][fn.Name.Name] = fn
			}
		}

		for _, typeName := range slices.Sorted(maps.Keys(receiverMethods)) {
			if satisfiesContract(t, fset, pkgDir, typeName, receiverMethods[typeName]) {
				path := packageImportPath(t, pkgDir)
				found[path] = append(found[path], typeName)
			}
		}
	}

	return found
}

// satisfiesContract says whether a type's method set satisfies module.Module.
//
// If all four method NAMES are present and one of the signatures does not hold,
// the answer is not "no" but an ERROR: that means either that the contract
// changed and the audit fell behind, or that the package is imported under a
// different alias. In both cases the right behavior is not to eliminate silently
// but to speak up — silent elimination takes the module out of the audit's scope
// and leaves the test blind exactly where it would have been useful.
func satisfiesContract(
	t *testing.T,
	fset *token.FileSet,
	pkgDir, typeName string,
	methods map[string]*ast.FuncDecl,
) bool {
	t.Helper()

	complete := true
	for name := range moduleContract {
		if _, exists := methods[name]; !exists {
			complete = false
			break
		}
	}
	if !complete {
		return false
	}

	matching := true
	for _, name := range slices.Sorted(maps.Keys(moduleContract)) {
		expected := moduleContract[name]
		fn := methods[name]
		parameters := fieldTypes(fn.Type.Params)
		results := fieldTypes(fn.Type.Results)
		if slices.Equal(parameters, expected.parameters) && slices.Equal(results, expected.results) {
			continue
		}
		matching = false
		t.Errorf("%s: the %s.%s signature does not match the [module.Module] contract.\n"+
			"expected: (%s) (%s) — found: (%s) (%s)\n"+
			"The type carries ALL FOUR of the four methods, so it is most likely a module. "+
			"If the contract changed, the moduleContract map must be updated as well; "+
			"otherwise this package silently drops out of the registration audit.",
			fset.Position(fn.Pos()), typeName, name,
			strings.Join(expected.parameters, ", "), strings.Join(expected.results, ", "),
			strings.Join(parameters, ", "), strings.Join(results, ", "))
	}

	if !matching {
		t.Logf("%s: type %s is COUNTED as satisfying the contract; the registration "+
			"requirement is applied anyway so that the audit does not go blind", pkgDir, typeName)
	}

	return true
}

// registeredModulePackages returns the import paths of the modules added to the
// module.Registry registry in the given package; the value is the position of
// the Add call.
//
// includeTestFiles is for the e2e harness: there the setup happens in the
// TestMain flow and there is no production file.
func registeredModulePackages(t *testing.T, root string, includeTestFiles bool) map[string]token.Position {
	t.Helper()

	dir := filepath.Join(repoRoot, root)
	fset := token.NewFileSet()
	files := parseDir(t, fset, dir, includeTestFiles)
	require.NotEmpty(t, files, "there is no Go file to parse in %s", root)

	// First the registry's VARIABLE names are collected: a method named "Add"
	// can be on any type (an atomic counter, a plugin registry, a slice wrapper)
	// and only this name set says that the receiver really is the module
	// registry.
	variables := registryVariableNames(files)
	require.NotEmpty(t, variables,
		"no [module.Registry] variable was found in %s.\n"+
			"If the registry is built in another form (a helper function, type embedding) then "+
			"this audit verifies NOTHING; registryVariableNames must be updated.", root)

	registered := map[string]token.Position{}
	for _, d := range files {
		ast.Inspect(d.tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Add" {
				return true
			}
			receiver, ok := sel.X.(*ast.Ident)
			if !ok || !variables[receiver.Name] {
				return true
			}

			position := fset.Position(call.Lparen)
			packageName, resolved := argumentPackage(call.Args)
			if !resolved {
				t.Errorf("%s: the argument of %s.Add(...) is in an unrecognized form.\n"+
					"The audit can only read the \"pkg.Constructor(...)\" form; any other form "+
					"HIDES from the audit which module the registration goes to. Either the "+
					"registration must be brought into this form or argumentPackage must be "+
					"widened.", position, receiver.Name)
				return true
			}
			path, known := d.imports[packageName]
			if !known {
				t.Errorf("%s: package %q could not be resolved in the import list of file %s",
					position, packageName, filepath.Base(d.path))
				return true
			}
			registered[path] = position

			return true
		})
	}

	return registered
}

// registryVariableNames returns the names of the identifiers holding
// module.Registry values in the package.
//
// Two sources are scanned: the variables the module.NewRegistry call is assigned
// to, and the declarations whose type is *module.Registry (function parameters
// included — the e2e harness passes the registry to a helper function as a
// PARAMETER).
func registryVariableNames(files []parsedFile) map[string]bool {
	names := map[string]bool{}

	for _, d := range files {
		ast.Inspect(d.tree, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range node.Rhs {
					call, ok := rhs.(*ast.CallExpr)
					if !ok || !isCoreModuleType(call.Fun, "NewRegistry", d.imports) {
						continue
					}
					if i < len(node.Lhs) {
						if ident, ok := node.Lhs[i].(*ast.Ident); ok {
							names[ident.Name] = true
						}
					}
				}
			case *ast.ValueSpec:
				if isCoreModuleType(node.Type, "Registry", d.imports) {
					for _, ident := range node.Names {
						names[ident.Name] = true
					}
					break
				}
				for i, value := range node.Values {
					call, ok := value.(*ast.CallExpr)
					if !ok || !isCoreModuleType(call.Fun, "NewRegistry", d.imports) {
						continue
					}
					if i < len(node.Names) {
						names[node.Names[i].Name] = true
					}
				}
			case *ast.Field:
				if isCoreModuleType(node.Type, "Registry", d.imports) {
					for _, ident := range node.Names {
						names[ident.Name] = true
					}
				}
			}

			return true
		})
	}

	return names
}

// isCoreModuleType says whether the expression corresponds to the given name in
// the core's module package; a pointer star is ignored.
//
// It is resolved through the alias map and does not look at the identifier's
// name: a file that imports the package as "coremodule" must be recognized
// correctly too.
func isCoreModuleType(expr ast.Expr, name string, imports map[string]string) bool {
	if expr == nil {
		return false
	}
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)

	return ok && imports[pkg.Name] == coreModulePackage
}

// argumentPackage returns the package name of the module constructor in the Add
// argument.
//
// The expected form is "pkg.Constructor(...)"; other forms come back as
// unresolved and become an ERROR on the calling side (see
// [registeredModulePackages]).
func argumentPackage(args []ast.Expr) (string, bool) {
	if len(args) != 1 {
		return "", false
	}
	call, ok := args[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}

	return pkg.Name, true
}

// filterToModulePackages reduces the registration set to the packages below
// internal/modules.
//
// The modules brought in by plugins (see plugins/searchpg) enter the registry
// through the core's plugin host, they have no Add line in the composition root
// and they are not this audit's subject: the invariant says "every module
// written below internal/modules".
func filterToModulePackages(registered map[string]token.Position) map[string]token.Position {
	prefix := modulePath + "/" + modulesDir + "/"
	filtered := make(map[string]token.Position, len(registered))
	for path, position := range registered {
		if strings.HasPrefix(path, prefix) {
			filtered[path] = position
		}
	}

	return filtered
}

// workflowPackagesBuiltFromContainer returns the packages below
// internal/workflows that are designed to be built from the container: the key
// is the package's import path, the value the names of the constructors.
//
// The criterion is "an exported function that takes a *container.Container".
// Both halves are needed: the composition root cannot call an unexported
// constructor anyway (so the rule could not cover it), and a function that does
// not take the container is not resolving its dependencies from the registry —
// whoever builds that package builds it directly by hand and no registration
// ordering problem arises.
//
// Helper packages that are not workflows (money, snapshot, catalog…) are thereby
// eliminated on their own: the criterion is the signature, not the directory
// name.
func workflowPackagesBuiltFromContainer(t *testing.T, dir string) map[string][]string {
	t.Helper()

	root := filepath.Join(repoRoot, dir)
	require.DirExists(t, root,
		"the %s tree does NOT EXIST; the wiring audit is left without a foothold. If the tree "+
			"moved the constant must move too, otherwise the audit stays green in a vacuum", dir)

	found := map[string][]string{}
	for _, pkgDir := range slices.Sorted(maps.Keys(productionPackages(t, root))) {
		fset := token.NewFileSet()

		var constructors []string
		for _, d := range parseDir(t, fset, pkgDir, false) {
			for _, decl := range d.tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !fn.Name.IsExported() {
					continue
				}
				if takesContainer(fn, d.imports) {
					constructors = append(constructors, fn.Name.Name)
				}
			}
		}

		if len(constructors) > 0 {
			slices.Sort(constructors)
			found[packageImportPath(t, pkgDir)] = constructors
		}
	}

	return found
}

// takesContainer says whether at least one of the function's parameters is the
// core's container.
func takesContainer(fn *ast.FuncDecl, imports map[string]string) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		if isCoreContainerType(field.Type, imports) {
			return true
		}
	}

	return false
}

// isCoreContainerType says whether the expression is the core's container type;
// a pointer star is ignored.
//
// It is resolved through the alias map and does not look at the identifier's
// name: a file that imports the package as "corecontainer" must be recognized
// correctly too.
func isCoreContainerType(expr ast.Expr, imports map[string]string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Container" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)

	return ok && imports[pkg.Name] == coreContainerPackage
}

// workflowsSetUpInCompositionRoot returns the workflow setups in the given
// package; the key is the import path of the workflow package that is set up,
// the value the place of the setup.
//
// What is looked for is "the constructor NAME being CALLED with its package
// qualifier". Every reference that falls outside this form is NOT left SILENT,
// because all the escape routes give the same result: which package the setup
// goes to is hidden from the audit, and a hidden link is the same thing as a
// link that does not exist.
//
//   - If the constructor is taken into a value and called that way (setup :=
//     pkg.FromContainer), or if it is put into a slice and called in a loop, the
//     name appears not at the CALL position but at the VALUE position; both are
//     caught here.
//   - If the qualifier is not a package name (the result of an expression, a
//     field) which package is being reached cannot be read, and the case is
//     reported as an error.
func workflowsSetUpInCompositionRoot(t *testing.T, constructors map[string][]string, dir string) map[string]workflowSetup {
	t.Helper()

	fset := token.NewFileSet()
	files := parseDir(t, fset, filepath.Join(repoRoot, compositionRoot), false)
	require.NotEmpty(t, files, "there is no Go file to parse in %s", compositionRoot)

	// The name set being looked for: the packages' REAL constructors and the
	// conventional name. The second is for the reverse-direction audit; see
	// [setupMarkerName].
	names := map[string]bool{setupMarkerName: true}
	for _, pkgConstructors := range constructors {
		for _, name := range pkgConstructors {
			names[name] = true
		}
	}

	// The root is the tree ITSELF; the prefix covers the sub-packages. Both are
	// needed: in the workflow tree the packages live in sub-directories
	// (…/workflows/cart), while in the panel tree the package itself is the root
	// (…/adminui). An audit that only looked at the prefix would NEVER see a
	// package living at the root and would stay silently green for that package
	// — it was measured.
	root := modulePath + "/" + dir
	prefix := root + "/"
	reach := reachFromMain(files)
	setUp := map[string]workflowSetup{}

	for _, d := range files {
		called := calledExpressions(d.tree)
		insideGo := insideGoStatements(d.tree)
		for _, decl := range d.tree.Decls {
			// A package-level declaration (a var initializer) has no enclosing
			// function; that code runs on every run, synchronously, and BEFORE
			// main(). The reachability questions are therefore only meaningful
			// for functions.
			enclosing, enclosingLive, enclosingOnStartupPath := "", true, true
			if fn, ok := decl.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
				enclosingLive = reach.reachable[enclosing]
				enclosingOnStartupPath = reach.onStartupPath[enclosing]
			}

			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || !names[sel.Sel.Name] {
					return true
				}
				path, resolved := resolveWorkflowPackage(t, fset.Position(sel.Sel.Pos()), sel, d, root, prefix)
				if !resolved {
					return true
				}
				// The name set is the UNION OF THE PACKAGES; here it is verified
				// that this is that package's OWN constructor. Without the
				// verification, a completely different function sharing a
				// workflow package's constructor name (a helper's New, for
				// example) would make that package look as if it were set up.
				if !slices.Contains(constructors[path], sel.Sel.Name) && sel.Sel.Name != setupMarkerName {
					return true
				}

				setup := workflowSetup{
					position:    fset.Position(sel.Sel.Pos()),
					enclosing:   enclosing,
					constructor: sel.Sel.Name,
				}
				switch {
				case !called[sel]:
					t.Errorf("%s: %s.%s is used as a VALUE, it is not called.\n"+
						"Taking the constructor into a variable, a slice or a table and calling "+
						"it that way HIDES from this audit which workflow the setup goes to: at "+
						"the call site there is no longer a package name. The setup must be "+
						"written in the \"pkg.Constructor(...)\" form, or "+
						"workflowsSetUpInCompositionRoot must be widened so that it reads this "+
						"form too.",
						setup.position, sel.X, sel.Sel.Name)
					setup.notCounted = true
				case !enclosingLive:
					t.Errorf("%s: the %s.%s call is in DEAD CODE — the %s() function is not "+
						"reachable from main().\n"+
						"A setup that compiles but does not run is the same thing as a setup that "+
						"was never written, and it is exactly the failure found this round: the "+
						"workflows' constructor was being called, but only from inside the "+
						"tests.\n"+
						"Either connect the call chain to main(), or, if the setup really is "+
						"unnecessary, delete the dead function.",
						setup.position, sel.X, sel.Sel.Name, enclosing)
					setup.notCounted = true
				case insideGo[sel] && goroutineExempt(path):
					// Exempt: startup declared with a rationale that it waits for
					// the setup.
					setup.inGoroutineForm = true
				case !enclosingOnStartupPath && goroutineExempt(path):
					// Exempt: the same rationale, for the form in a link of the
					// chain.
					setup.inGoroutineForm = true
				case insideGo[sel]:
					t.Errorf("%s: the %s.%s call is inside a GO STATEMENT — the setup is NOT ON "+
						"THE STARTUP PATH.\n"+
						"The failure of a workflow built in a separate goroutine CANNOT COME "+
						"BACK to startup: the process comes up healthy, /health returns 200, the "+
						"admin surface works completely and the failure blows up as a 500 on the "+
						"first cart line — a STARTUP failure has silently been turned into a "+
						"RUNTIME failure.\n"+
						"The rule is only for tracked SETUP calls; the goroutines of the server, "+
						"of the event bus and of the shutdown watchers are not this audit's "+
						"subject. The setup must be taken OUTSIDE the go statement — or, if "+
						"startup REALLY waits for it (WaitGroup/errgroup), it must be written "+
						"with its rationale into the setupsOffTheStartupPath map.",
						setup.position, sel.X, sel.Sel.Name)
					setup.notCounted = true
				case !enclosingOnStartupPath:
					t.Errorf("%s: the %s.%s call is NOT ON THE STARTUP PATH — the %s() function "+
						"is reached from main() only from behind a GO STATEMENT.\n"+
						"Even though the call itself looks synchronous, one link of the chain "+
						"runs in a separate goroutine: the setup failure CANNOT COME BACK to "+
						"startup, the process comes up healthy and the failure blows up as a 500 "+
						"on the first cart line.\n"+
						"Either take the %s() chain outside the go statement, or move the setup's "+
						"failure to a place startup WAITS for. If startup ALREADY waits for it, "+
						"write it with its rationale into the setupsOffTheStartupPath map.",
						setup.position, sel.X, sel.Sel.Name, enclosing, enclosing)
					setup.notCounted = true
				}

				// If a valid setup has already been found for the same package, a
				// flawed one does not take its place: the flaw was already
				// reported above, and the record's job is to answer the question
				// "is it set up".
				if existing, exists := setUp[path]; exists && !existing.notCounted {
					return true
				}
				setUp[path] = setup

				return true
			})
		}
	}

	return setUp
}

// resolveWorkflowPackage returns which workflow package the constructor
// reference goes to.
//
// References that go outside the workflow tree (a function of another package
// carrying the same name) are eliminated silently; forms that cannot be read do
// give an error, because there the audit cannot tell the difference between
// ELIMINATING and NOT SEEING.
func resolveWorkflowPackage(
	t *testing.T,
	position token.Position,
	sel *ast.SelectorExpr,
	d parsedFile,
	root, prefix string,
) (string, bool) {
	t.Helper()

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		t.Errorf("%s: the qualifier of the %s call is NOT a package name.\n"+
			"The audit can only read the \"pkg.Constructor(...)\" form; any other form HIDES "+
			"which workflow the setup goes to.", position, sel.Sel.Name)

		return "", false
	}

	path, known := d.imports[pkg.Name]
	if !known {
		t.Errorf("%s: %q could not be resolved in the import list of file %s.\n"+
			"If the constructor name is used through a VALUE rather than through a package, "+
			"the target of the setup cannot be read; the setup must be called directly through "+
			"the package.",
			position, pkg.Name, filepath.Base(d.path))

		return "", false
	}

	return path, path == root || strings.HasPrefix(path, prefix)
}

// calledExpressions collects the CALLED expression of every call in the file.
//
// Generic calls have an index expression in between, in the
// "pkg.Constructor[T](...)" form; the base expression is taken into the set as
// well, otherwise such a call would be taken for "used as a value".
func calledExpressions(tree *ast.File) map[ast.Expr]bool {
	called := map[ast.Expr]bool{}
	ast.Inspect(tree, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		called[call.Fun] = true
		switch typed := call.Fun.(type) {
		case *ast.IndexExpr:
			called[typed.X] = true
		case *ast.IndexListExpr:
			called[typed.X] = true
		}

		return true
	})

	return called
}

// compositionRootReach holds the reachability from main() of the package-level
// names in the composition root according to TWO separate questions.
type compositionRootReach struct {
	// reachable are the names reached from main() in any form; a name left
	// behind a go statement is in here too. Its question is "is this code
	// running".
	reachable map[string]bool
	// onStartupPath are the names connected to main() without entering a GO
	// STATEMENT. Its question is "does startup WAIT for this step", that is, can
	// its failure come back to startup.
	onStartupPath map[string]bool
}

// reachFromMain returns the reach sets from main() of the package-level names in
// the composition root.
//
// The edges are built FROM NAMES and are deliberately wide: if a name occurs in
// the body of another name in any form (a call, being passed as a value, a
// defer) it counts as an edge. The rationale is in the
// [TestEveryWorkflowIsSetUpInTheCompositionRoot] godoc.
//
// init is a root as well: package initialization runs on every run, and a setup
// called from there is not dead.
//
// # Why the two sets are separate
//
// A name left under a go statement RUNS — that is, it is not dead — but it is
// not a step that startup waits for: its failure stays in its own goroutine and
// startup carries on without seeing it. Had a single set been kept, only one of
// the two questions could have been answered, and which one was answered would
// have stayed unclear on the calling side; whereas the two failures need
// different diagnoses ("dead code" and "not on the startup path" are not the
// same thing).
//
// The narrow set looks for the name to have AT LEAST ONE path on the startup
// path: if the same function is reached both from behind a go statement and
// directly, then startup does wait for it. Accusing a live setup is the shortest
// way to lose trust in an audit.
func reachFromMain(files []parsedFile) compositionRootReach {
	bodies := compositionRootNodes(files)

	reach := compositionRootReach{
		reachable:     map[string]bool{},
		onStartupPath: map[string]bool{},
	}

	var (
		visit     func(name string, onStartup bool)
		visitBody func(node ast.Node, onStartup bool)
	)

	visitBody = func(node ast.Node, onStartup bool) {
		ast.Inspect(node, func(n ast.Node) bool {
			// UNDER a go statement is still walked — the names there do run —
			// but from that point on it does NOT COUNT as the startup path.
			if goStmt, isGo := n.(*ast.GoStmt); isGo && onStartup {
				visitBody(goStmt.Call, false)

				return false
			}
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if _, known := bodies[ident.Name]; known {
				visit(ident.Name, onStartup)
			}

			return true
		})
	}

	visit = func(name string, onStartup bool) {
		// The same name can be visited TWICE: first from behind a go statement,
		// then from the startup path. If the second pass is skipped the narrow
		// set stays dependent on the visit ORDER and a live setup could be
		// reported as "not on the startup path".
		if reach.reachable[name] && (!onStartup || reach.onStartupPath[name]) {
			return
		}
		reach.reachable[name] = true
		if onStartup {
			reach.onStartupPath[name] = true
		}
		for _, body := range bodies[name] {
			visitBody(body, onStartup)
		}
	}

	visit("main", true)
	if _, exists := bodies["init"]; exists {
		visit("init", true)
	}

	return reach
}

// insideGoStatements collects the expressions left under a GO STATEMENT in the
// file.
//
// The name graph cannot see this form: taking the setup's own CALL site into a
// go statement (go func() { pkg.Constructor(c) }()) does NOT TAKE the calling
// function off the startup path — that function is still reached synchronously
// from main(). The stain shows only here, at the call's syntactic place.
func insideGoStatements(tree *ast.File) map[ast.Expr]bool {
	inside := map[ast.Expr]bool{}
	ast.Inspect(tree, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		// Nested go statements are covered by this walk as well; the outer walk
		// does not need to descend into the same subtree a second time.
		ast.Inspect(goStmt.Call, func(inner ast.Node) bool {
			if expr, isExpr := inner.(ast.Expr); isExpr {
				inside[expr] = true
			}

			return true
		})

		return false
	})

	return inside
}

// compositionRootNodes maps every package-level name to the AST nodes counted as
// that name's "body": the body block for functions, the initializer expressions
// for var/const declarations.
//
// # Why declarations are nodes too
//
// As long as the graph only walks function bodies, taking the setup into a
// function VARIABLE would make it look dead in the audit's eyes:
//
//	var workflowSetupFn = registerWorkflows   // package level, in no body
//	func main() { workflowSetupFn(c) }     // the name "registerWorkflows" never occurs
//
// While the binary works completely the audit would say "IN DEAD CODE" — and an
// audit that declares the living dead is the worst kind of audit: it RIGHTLY
// convinces people that it deserves to be silenced. Making the declaration a
// node completes the chain (main -> workflowSetupFn -> registerWorkflows) and
// brings the graph to the width its godoc ALREADY PROMISES.
//
// # Why declarations are not ROOTS
//
// Package initialization runs on every run, that is, an initializer EXPRESSION
// runs unconditionally. But the fact that the expression "var f = Build" runs
// does NOT MEAN that Build runs: if nobody calls the variable, Build is still
// dead. Counting declarations as roots swallows that distinction and would make
// a setup that is "switched off with if false but held in a variable" look
// live. That is why a declaration is a node that is walked only WHEN IT IS
// REFERENCED.
//
// The distinction is consistent with [workflowsSetUpInCompositionRoot] as well:
// there a call INSIDE a package-level declaration counts as unconditionally
// live, because the question asked there is a different one — is the expression
// itself running? The question asked here is whether the function the NAME
// points to runs.
func compositionRootNodes(files []parsedFile) map[string][]ast.Node {
	bodies := map[string][]ast.Node{}
	for _, d := range files {
		for _, decl := range d.tree.Decls {
			switch typed := decl.(type) {
			case *ast.FuncDecl:
				if typed.Body != nil {
					bodies[typed.Name.Name] = append(bodies[typed.Name.Name], typed.Body)
				}
			case *ast.GenDecl:
				// import and type declarations carry no running code; only
				// var/const initializers give a name a body.
				if typed.Tok != token.VAR && typed.Tok != token.CONST {
					continue
				}
				for _, spec := range typed.Specs {
					value, isValue := spec.(*ast.ValueSpec)
					if !isValue {
						continue
					}
					// The names and the initializers may not match EXACTLY (in
					// the declaration counterpart of the "a, b := f()" form one
					// expression falls to two names); all of them are bound to
					// every name. The surplus only WIDENS the graph, and that is
					// the accepted direction.
					for _, name := range value.Names {
						for _, initializer := range value.Values {
							bodies[name.Name] = append(bodies[name.Name], initializer)
						}
					}
				}
			}
		}
	}

	return bodies
}

// productionPackages returns the directories below root that hold at least one
// production (non-_test.go) file.
func productionPackages(t *testing.T, root string) map[string]struct{} {
	t.Helper()

	dirs := map[string]struct{}{}
	for _, file := range goFiles(t, root) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		dirs[filepath.Dir(file)] = struct{}{}
	}

	return dirs
}

// parseDir parses the Go files in a directory (WITHOUT descending into
// sub-directories) and resolves the import aliases.
func parseDir(t *testing.T, fset *token.FileSet, dir string, includeTestFiles bool) []parsedFile {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s could not be read: %v", dir, err)
	}

	var files []parsedFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if !includeTestFiles && strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(dir, name)
		tree, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("%s could not be parsed: %v", path, parseErr)
		}
		files = append(files, parsedFile{path: path, tree: tree, imports: importAliases(tree)})
	}

	return files
}

// importAliases maps the local name of every import in the file to its import
// path.
//
// If no alias is given the last segment of the path is used. That is wrong in
// the case where the package name differs from the directory name; in all the
// packages this audit looks at the two are the same, and being wrong only means
// NOT SEEING a registration — and that does not stay silent on the calling side
// either, because an unresolvable package name gives an error.
func importAliases(tree *ast.File) map[string]string {
	names := make(map[string]string, len(tree.Imports))
	for _, imp := range tree.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = path
	}

	return names
}

// receiverTypeName returns the base type name of a method receiver; the empty
// string if it cannot be resolved.
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	// Generic receivers are in the "T[P]" form; the base name is on the left.
	switch typed := expr.(type) {
	case *ast.IndexExpr:
		expr = typed.X
	case *ast.IndexListExpr:
		expr = typed.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}

	return ""
}

// fieldTypes returns the types of a parameter or result list in source form;
// shared declarations such as "a, b int" are expanded by REPEATING the type.
func fieldTypes(list *ast.FieldList) []string {
	if list == nil {
		return nil
	}

	var collected []string
	for _, field := range list.List {
		for range max(len(field.Names), 1) {
			collected = append(collected, types.ExprString(field.Type))
		}
	}

	return collected
}

// packageImportPath produces the Go import path from a file system path.
func packageImportPath(t *testing.T, dir string) string {
	t.Helper()

	rel, err := filepath.Rel(repoRoot, dir)
	if err != nil {
		t.Fatalf("the relative path for %s could not be computed: %v", dir, err)
	}

	return modulePath + "/" + filepath.ToSlash(rel)
}

// goroutineExempt reports whether the given workflow package is exempt from the
// goroutine audit.
//
// The exemption silences ONLY the goroutine question. The questions "is this
// package set up in the composition root" and "is the call in dead code" keep
// being asked for an exempt package too: what the exemption declares is "startup
// waits for this", not "this setup is unnecessary".
func goroutineExempt(packagePath string) bool {
	_, exempt := setupsOffTheStartupPath[packagePath]
	return exempt
}

// checkStaleGoroutineExemptions fails the goroutine exemptions whose debt has
// been paid.
//
// There are two ways of going stale, and if both stay silent the exemption turns
// into a permanent blind spot: the exempt package is no longer a workflow
// package (it was deleted or renamed), or the setup is NO LONGER in goroutine
// form — that is, the situation the exemption protected has disappeared. The
// second is the main one: an exemption is a debt, and when the debt is paid it
// must not stay on the books.
func checkStaleGoroutineExemptions[T any](
	t *testing.T,
	workflows map[string]T,
	setUp map[string]workflowSetup,
) {
	t.Helper()

	for _, path := range slices.Sorted(maps.Keys(setupsOffTheStartupPath)) {
		if _, isWorkflow := workflows[path]; !isWorkflow {
			t.Errorf("STALE exemption: %q is no longer a workflow package built from the "+
				"container.\n"+
				"If the package was deleted or renamed the exemption line must go too; if it "+
				"stays it will one day silently exempt a new package written under the same "+
				"name.", path)
			continue
		}
		setup, found := setUp[path]
		if !found || !setup.inGoroutineForm {
			t.Errorf("STALE exemption: the setup of %q is NO LONGER in goroutine form, so the "+
				"exemption is not needed.\n"+
				"An exemption is a debt; when the debt is paid the line must be deleted. If it "+
				"stays, the audit will be silent the day the setup is taken back into a "+
				"goroutine.", path)
		}
	}
}

// TestTheAdminPanelIsSetUpInTheCompositionRoot verifies that the admin panel
// REALLY is set up in the composition root (ADR 0011).
//
// The audit is the twin of [TestEveryWorkflowIsSetUpInTheCompositionRoot] and
// uses the same helpers; the reason it is a separate test is that its name has
// to stay honest — the panel is not a workflow, and auditing it under the name
// of the workflow test would mean that the day the workflow tree is removed the
// panel too silently loses its audit.
//
// # The gap was closed the round it OPENED
//
// The panel tree is neither core nor a module: the module registration audits
// reduce their scope to the internal/modules prefix, and the depguard rules look
// there as well. So in this tree a capability that is "written but wired
// nowhere" would leave the arch run GREEN without this test — it was measured. A
// capability with no registration is this repository's most expensive class of
// defect; ADR 0006 closed it once for the workflow tree, and this test closes it
// a second time for the panel tree.
//
// # What it does NOT PROVE
//
// It does not prove that the setup RUNS, only that the setup line stands on the
// startup path. The distinction is written down, as measured, in the
// [TestEveryWorkflowIsSetUpInTheCompositionRoot] godoc and is not repeated here.
//
// One more end of the scope was measured: a bare REFERENCE to the setup function
// also counts as reach. If the call is deleted and a value reference is left in
// its place the audit PASSES — because the shape reading asks "can this function
// be reached", not "is this function called". The same class is already named in
// the workflow test's godoc as "a setup taken into a go statement", and the
// answer is the same again: runtime proof does not take the place of a static
// invariant.
func TestTheAdminPanelIsSetUpInTheCompositionRoot(t *testing.T) {
	t.Parallel()

	packages := workflowPackagesBuiltFromContainer(t, adminUIDirName)
	require.NotEmpty(t, packages,
		"no package built from the container was found below %s; the audit must have gone "+
			"BLIND (does the constructor no longer take *container.Container?)", adminUIDirName)

	conventionAlive := false
	for _, constructors := range packages {
		if slices.Contains(constructors, setupMarkerName) {
			conventionAlive = true
			break
		}
	}
	require.True(t, conventionAlive,
		"there is NO constructor named %q in the panel tree; that is the only foothold of "+
			"the reverse-direction audit, and when it goes stale a package that the "+
			"composition root builds but the audit cannot see silently falls out of scope",
		setupMarkerName)

	setUp := workflowsSetUpInCompositionRoot(t, packages, adminUIDirName)

	for _, path := range slices.Sorted(maps.Keys(packages)) {
		setup, isSetUp := setUp[path]
		if isSetUp && !setup.notCounted {
			continue
		}
		t.Errorf("package %s is written to be built from the container (%s) but is NOT SET UP "+
			"in %s/.\n"+
			"A panel that is not set up compiles, passes its tests and exists in NO "+
			"deployment: nothing called an admin surface is published, and no unit test can "+
			"see this, because the panel's own tests set the panel up themselves.",
			path, strings.Join(packages[path], ", "), compositionRoot)
	}

	for _, path := range slices.Sorted(maps.Keys(setUp)) {
		if _, seen := packages[path]; !seen {
			t.Errorf("the constructor of package %s is called in %s/ but the audit does NOT "+
				"SEE a constructor built from the container in that package; the shape reading "+
				"must have drifted from reality", path, compositionRoot)
		}
	}
}

// TestTheAdminPanelDoesNotImportModules enforces ADR 0011's import ban.
//
// Like the workflow tree, the panel tree falls under no existing rule: the
// depguard rules are for internal/modules, and the core ban does not bind it
// either. Had it imported the modules directly it would turn into a second node
// that knows every module, and the structure ADR 0006 rejected for the workflows
// would come back under the panel's name.
//
// Access must go through the narrow interface the panel defines in its OWN
// package and through resolution by name from the container.
func TestTheAdminPanelDoesNotImportModules(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRoot, adminUIDirName)
	require.DirExists(t, root,
		"the %s tree does NOT EXIST; no file is left to walk for ADR 0011's import ban and "+
			"the audit stays green in a vacuum", adminUIDirName)

	files := goFiles(t, root)
	require.NotEmpty(t, files,
		"there is NO Go file below %s; the directory is still there but has left nothing to "+
			"walk. An empty file set shows not that the rule has been lifted but that it has "+
			"gone BLIND",
		adminUIDirName)

	prefix := modulePath + "/internal/modules/"
	for _, file := range files {
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, err, "%s could not be parsed", file)

		for _, imp := range tree.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, prefix) {
				t.Errorf("%s: the panel imports the %q module (ADR 0011).\n"+
					"The panel does not know the modules: it defines the surface it needs as a "+
					"narrow interface in its OWN package and resolves it from the container BY "+
					"NAME.", file, path)
			}
		}
	}
}

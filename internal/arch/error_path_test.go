package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// coreHTTPDir is the directory of the core's HTTP package inside the repository.
const coreHTTPDir = "internal/core/http"

// coreHTTPPath is the import path of the core's HTTP package.
const coreHTTPPath = modulePath + "/" + coreHTTPDir

// coreWriterDefinition is the file where the error and success writers are DEFINED.
//
// This file is outside the scan and it MUST BE outside it: this is exactly the
// place that writes the body and the status code, so applying the rule to
// itself would mean "the single copy of the policy violates the policy".
// Instead of pinning the name down, it is verified that the file REALLY defines
// the writers (see [TestNonModuleHTTPSurfacesWriteThroughTheCore]); if the
// writers move to another file the exemption must move with them, otherwise
// this file would silently turn into a name that "may write anything".
const coreWriterDefinition = coreHTTPDir + "/response.go"

// netHTTPPath is the import path of the standard library's HTTP package.
const netHTTPPath = "net/http"

// errorWriterName is the name of the ONLY function in the core that writes an
// error body.
//
// The whole policy lives there: an unclassified error counts as KindInternal,
// its message is MASKED and the real text is logged (see corehttp.WriteError).
const errorWriterName = "WriteError"

// successWriterName is the name of the helper that writes the SUCCESS body in
// the core.
const successWriterName = "WriteJSON"

// htmlWriterName is the single gate for the HTML body (ADR 0011).
//
// Unlike [successWriterName] there is NO 2xx requirement: returning the login
// page with a 401 to an unauthenticated browser is more honest than sending it
// somewhere else. That is why the audit applies no status constraint to the
// HTML writer.
const htmlWriterName = "WriteHTML"

// writerTypeName is the type name looked for in order to recognize
// ResponseWriter parameters.
const writerTypeName = "ResponseWriter"

// successStatuses holds the names of net/http's 2xx constants.
//
// The set is CLOSED: the 2xx range is fixed in HTTP and net/http has no names
// beyond these ten. So the list kept here does not grow like "today's
// endpoints"; it does not need updating when a new endpoint is added.
//
// Why the name is what gets inspected: [successWriterName] writes the body
// without interpreting it at all, so if it is handed a 4xx/5xx status the error
// response goes to the client WITHOUT PASSING THROUGH THE CORE'S POLICY — both
// the masking and the logging are skipped. The result is the same as never
// calling WriteError at all.
var successStatuses = map[string]bool{
	"StatusOK":                   true,
	"StatusCreated":              true,
	"StatusAccepted":             true,
	"StatusNonAuthoritativeInfo": true,
	"StatusNoContent":            true,
	"StatusResetContent":         true,
	"StatusPartialContent":       true,
	"StatusMultiStatus":          true,
	"StatusAlreadyReported":      true,
	"StatusIMUsed":               true,
}

// writerSafeMethods are the methods on ResponseWriter that write NEITHER a body
// NOR a status.
//
// Header() only returns the map; writing begins with WriteHeader/Write and both
// of those are outside this set.
var writerSafeMethods = map[string]bool{
	"Header": true,
}

// safeWriterTakingCalls are the external calls that TAKE the writer but write
// NO response.
//
// The key has the form "importPath.Name"; the value is the rationale.
var safeWriterTakingCalls = map[string]string{
	netHTTPPath + ".MaxBytesReader": "takes the writer only to close the connection once the " +
		"limit is exceeded; it writes not a single byte to the body",
}

// errorPathExemption is a single call, with a discussed rationale, that writes
// the response body from outside the core.
//
// An exemption is NOT A SILENT SKIP: an unused exemption fails the test (see
// [TestErrorResponsesAreWrittenInOnePlace]), which means the rationale has to
// live together with the code.
type errorPathExemption struct {
	// file is the path relative to the repository root.
	file string
	// call is the call name as written in the source (e.g. "http.ServeContent").
	call string
	// reason is why this call does NOT PRODUCE a second definition of the error.
	reason string
}

// errorPathExemptions are the legitimate calls that write the body from outside
// the core.
var errorPathExemptions = []errorPathExemption{
	{
		file: "internal/modules/file/api/serve.go",
		call: "http.ServeContent",
		// This call serves the CONTENT of a file; its body is not a JSON
		// envelope, so it cannot pass through the core's helpers. The error path
		// is still in the core: if the file to be served cannot be found or
		// cannot be opened, the handler returns with corehttp.WriteError WITHOUT
		// EVER REACHING ServeContent (see serveFile). The only error class
		// ServeContent produces itself (416 Range Not Satisfiable) is computed
		// from the request header and carries no detail from inside the server —
		// it has no text to mask.
		reason: "serves file content; it is not a JSON envelope and the error path passes " +
			"through corehttp.WriteError before entering ServeContent",
	},
}

// httpSurfaceExemption is a package that writes an HTTP response OUTSIDE the
// api packages.
type httpSurfaceExemption struct {
	// pkg is the package directory relative to the repository root.
	pkg string
	// reason is why this surface may leave through a separate gate.
	reason string
}

// httpSurfaceExemptions are the legitimate HTTP surfaces outside api.
//
// Even when an exempt package writes the error body in its own envelope, it
// must apply the rule BY PASSING THROUGH THE CORE; the test looks for that by
// verifying that the package really calls corehttp.WriteError (see
// [TestHTTPSurfacesLiveOnlyInApiPackages]).
var httpSurfaceExemptions = []httpSurfaceExemption{
	{
		pkg: "internal/modules/product/graph",
		// GraphQL's response envelope is not HTTP's: the status code is always
		// 200 and the error is returned in the "errors" array. This surface
		// unavoidably builds the body itself. But it does NOT REPEAT the rule:
		// servisHatasi has the error written into an in-memory writer with
		// corehttp.WriteError and READS BACK the envelope the core produced. Both
		// the masking decision and the logging stay inside that call. The reason
		// this line exists at all is that it has already drifted once: the
		// condition used to be "let anything untyped through" and it was handing
		// pq's connection string to the client.
		reason: "the GraphQL envelope carries no HTTP status code; it builds the body itself " +
			"but takes the decision by having corehttp.WriteError write it and reading its " +
			"envelope back",
	},
}

// TestErrorResponsesAreWrittenInOnePlace verifies that in the module APIs there
// is NO path writing the error body outside the core.
//
// # Which error class
//
// The rule is defined in ONE place (corehttp.WriteError: an unclassified error
// counts as KindInternal, its message is masked, the real text is logged) but a
// SECOND surface can set out to repeat it and drift. The measured cost is this:
// when an unwrapped repository error is returned, the text "pq: SSL connection
// error host=db.internal user=gobit password=…" reaches the client and is
// written nowhere. The drift is silent — the tests pass, the endpoints work,
// only the wrong person sees the wrong text.
//
// # Why it WALKS the structure
//
// A hand-maintained list of endpoints applies the rule for TODAY only: a
// handler added tomorrow is not on the list and stays silently exempt. This
// test instead walks the AST of the api packages and asks: does every path in
// this function that touches http.ResponseWriter go to one of the core's
// helpers?
//
// The paths that count as legitimate:
//
//   - corehttp.WriteError — the ONLY gate for errors.
//   - corehttp.WriteJSON — the gate for success; its status MUST BE 2xx,
//     because if it is called with a 4xx/5xx status the error response has not
//     passed through the policy. If the status is forwarded through a parameter
//     (helpers such as writeItem) it is TRACED down to the call itself.
//   - Another function/method inside the package — that one is inside this scan
//     too, so the rule is preserved inductively.
//   - [safeWriterTakingCalls] — those that take the writer and do not write to
//     the body.
//
// Everything else (w.Write, w.WriteHeader, http.Error, json.NewEncoder(w),
// leaking the writer into another value) is a violation.
func TestErrorResponsesAreWrittenInOnePlace(t *testing.T) {
	t.Parallel()

	used := make([]bool, len(errorPathExemptions))
	packages := apiPackages(t)
	if len(packages) == 0 {
		t.Fatal("no api package found; the scan root may be wrong")
	}
	verifyWriterCarryingFiles(t, packages,
		"internal/modules/*/api", "if the handlers stopped taking http.ResponseWriter")

	for _, dir := range packages {
		auditPackage(t, dir, used)
	}

	for i, exemption := range errorPathExemptions {
		if !used[i] {
			t.Errorf("unused exemption: %s contains no %q call.\n"+
				"Its rationale (%q) no longer defends anything: either the call was removed "+
				"and the exemption should be deleted with it, or it moved to another file "+
				"and the exemption no longer sees it.",
				exemption.file, exemption.call, exemption.reason)
		}
	}
}

// TestHTTPSurfacesLiveOnlyInApiPackages verifies that every module package
// writing an HTTP response IS SCANNED.
//
// [TestErrorResponsesAreWrittenInOnePlace] only walks internal/modules/*/api.
// On its own that binds the rule to a DIRECTORY NAME: a modules/x/webhook or
// modules/x/graph package added tomorrow is never scanned and the invariant
// silently falls out of scope — exactly the "capability with no consumer"
// class, but from the other side: a surface with no auditor.
//
// This test audits the scope itself: every package in the modules where
// http.ResponseWriter appears is either under api (that is, scanned) or carries
// a rationale in [httpSurfaceExemptions]. The rationale of an exempt package
// does not HANG IN THE AIR either: because it claims to run the rule through
// the core, it is checked that it really calls corehttp.WriteError.
func TestHTTPSurfacesLiveOnlyInApiPackages(t *testing.T) {
	t.Parallel()

	exemptPackages := map[string]httpSurfaceExemption{}
	for _, exemption := range httpSurfaceExemptions {
		exemptPackages[filepath.FromSlash(exemption.pkg)] = exemption
	}
	seen := map[string]bool{}
	// The input of this test is not the module list but the files in the modules
	// that CARRY A WRITER: that set can empty out while the modules stay in
	// place, and at that moment the test would have said "there is no surface
	// outside api" — when in fact it never looked.
	writerCarryingFiles := 0

	for _, mod := range moduleNames(t) {
		root := filepath.Join(repoRoot, modulesDir, mod)
		for _, file := range productionFiles(t, root) {
			if !mentionsWriter(t, file) {
				continue
			}
			writerCarryingFiles++
			pkg := repoPath(filepath.Dir(file))
			if filepath.Base(pkg) == "api" {
				continue
			}
			seen[pkg] = true
			exemption, ok := exemptPackages[pkg]
			if !ok {
				t.Errorf("%s: there is an HTTP surface OUTSIDE api and its error path is not "+
					"audited.\nEither the package should move under "+
					"internal/modules/<mod>/api, or it should be added to "+
					"httpSurfaceExemptions WITH ITS RATIONALE. Every surface left without a "+
					"rationale is an opportunity to write a second copy of the core's error "+
					"policy.", pkg)

				continue
			}
			if !usesCoreErrorPath(t, filepath.Join(repoRoot, pkg)) {
				t.Errorf("%s: the exemption's rationale (%q) does not hold — the package "+
					"contains no corehttp.%s call at all.\nAn exempt surface may build its own "+
					"body but it MUST ASK the core which error may be handed to the client.",
					pkg, exemption.reason, errorWriterName)
			}
		}
	}

	for pkg := range exemptPackages {
		if !seen[pkg] {
			t.Errorf("unused surface exemption: %s no longer uses http.ResponseWriter "+
				"(or the package was removed). The exemption should be deleted.", pkg)
		}
	}

	require.Positive(t, writerCarryingFiles,
		"there is not a SINGLE production file in the modules carrying http.%s; the scope "+
			"audit must have gone BLIND.\n"+
			"This test asks \"is there an unaudited HTTP surface outside api\"; when it "+
			"cannot recognize the writer the answer is always \"no\" and a modules/x/webhook "+
			"package added tomorrow silently stays out of scope. The criterion by which the "+
			"writer is recognized (isWriterType) must be updated together with reality.",
		writerTypeName)
}

// bodyWritingNetHTTPCalls are net/http's helpers that write the response BODY.
//
// The match looks at the NAME only, not at the call's arguments: the ONLY job
// of these functions is to write a response. A check that looked at the
// arguments would miss the call once the writer is put into a wrapper
// ("http.Error(rw, ...)") — yet the fact that the body is produced outside the
// core does not change.
var bodyWritingNetHTTPCalls = map[string]bool{
	"Error":           true,
	"NotFound":        true,
	"NotFoundHandler": true,
	"Redirect":        true,
	"ServeContent":    true,
	"ServeFile":       true,
	"ServeFileFS":     true,
}

// bodyWritingExternalCalls are the external calls that TAKE the writer and
// write to its body.
//
// The key has the form "importPath.Name". These are the opposite of
// [safeWriterTakingCalls]: they take the writer AND write to the body, which
// means the envelope decision has been made outside the core.
var bodyWritingExternalCalls = map[string]bool{
	"encoding/json.NewEncoder": true,
	"fmt.Fprint":               true,
	"fmt.Fprintf":              true,
	"fmt.Fprintln":             true,
	"io.Copy":                  true,
	"io.WriteString":           true,
}

// templateWritingMethods are the names of the methods that STREAM a template
// set into the writer.
//
// # Why it looks at the NAME
//
// The rest of this audit resolves a call's target through the import path;
// template execution stays OUTSIDE that, because the receiver is not a package
// name but a value ("templates.Execute(w, …)"). Since the target cannot be
// resolved, the [bodyWritingExternalCalls] branch never runs and the call
// passes SILENTLY — measured.
//
// This is not a permission, it was the negative of the way the scan measures:
// the rule was not being lifted, it was GOING BLIND. And the place where it
// goes blind is not random — the admin panel (ADR 0011) is exactly the first
// and largest surface that uses this shape.
//
// The false-positive risk of looking at the name is low here: the branch only
// runs when the writer is given as an ARGUMENT, and handing an
// http.ResponseWriter to a call named "Execute" is nothing other than streaming
// the template straight into the response.
//
// The right way is to render the template into memory FIRST and hand it to
// corehttp.WriteHTML; then an error born halfway through can still be turned
// into a 500, whereas a streamed template leaves a HALF page behind with a 200
// status code.
var templateWritingMethods = map[string]bool{
	"Execute":         true,
	"ExecuteTemplate": true,
}

// coreWriterExemption is a function on a surface OUTSIDE the modules that
// writes the response outside the core writers, with a discussed rationale.
//
// The exemption is defined not by the NAME of the function but by the CALLS
// that are allowed to appear in it. A blanket exemption at function level was
// tried and turned out to be a HOLE: an http.Error added later to a function
// that was exempt because it "writes the body itself" also stayed silently
// exempt — that is, the exemption was opening from the inside exactly the class
// it exists to close.
type coreWriterExemption struct {
	// file is the path relative to the repository root.
	file string
	// function is the name of the exempted function (or method).
	function string
	// calls are the calls allowed to appear in this function, as written in the
	// source ("w.Write", "WriteJSON").
	//
	// Every call not on the list is a violation even if the function is exempt;
	// every call on the list that is NOT USED fails the test.
	calls []string
	// reason is why these calls do NOT PRODUCE a second definition of the error.
	reason string
}

// coreWriterExemptions are the legitimate write paths outside the modules.
//
// Every exemption passes TWO gates: a stale exemption fails the test (the
// rationale has to live together with the code) and it is checked that the
// exempt file REALLY calls the core's writers — an exempt surface may write its
// own body but it MUST ASK the core which error may be handed to the client.
var coreWriterExemptions = []coreWriterExemption{
	{
		file:     coreHTTPDir + "/idempotency.go",
		function: "replay",
		calls:    []string{"w.WriteHeader", "w.Write"},
		// The replayed response is the response this server produced EARLIER:
		// both its body and its status went through the core's writers back then.
		// Re-enveloping it would mean writing a SECOND body on top of the
		// recorded response, and idempotency's single promise — "the same key
		// returns the same response" — would break exactly here. The error path
		// is in the core again: an empty record and a fingerprint mismatch return
		// through corehttp.WriteError.
		reason: "replays the recorded response as it is; both the body and the status went " +
			"through the core writers earlier",
	},
	{
		file:     coreHTTPDir + "/middleware.go",
		function: "Recoverer",
		calls:    []string{successWriterName},
		// A panic value is NOT an error (recover() returns any), so there is no
		// error to hand to WriteError. The policy is nevertheless not copied: the
		// response is written with the core's OWN envelope (newErrorResponse) and
		// its OWN masked text (genericInternalMessage), and the stack trace goes
		// to the log only.
		reason: "a panic value is not an error; the response is still written with the core's " +
			"envelope and masked text",
	},
	{
		file:     coreHTTPDir + "/router.go",
		function: "readyHandler",
		calls:    []string{successWriterName},
		// A 503 here is not an ERROR response but the readiness report the
		// orchestrator PARSES: which check failed is the meaning of the body, and
		// if it is masked the endpoint loses its function. The status code too is
		// computed from the check results rather than at the call itself, so it
		// cannot be resolved by this scan.
		reason: "the /ready body is not an error envelope but a readiness report carrying the " +
			"check results",
	},
	{
		file:     "internal/core/openapi/openapi.go",
		function: "Handler",
		calls:    []string{"w.Write"},
		// The body is the ALREADY ENCODED and cached OpenAPI document; it is not
		// a JSON envelope and had it been handed to the core's writer it would be
		// walked and copied once more on every request — that is exactly why the
		// cache exists. The header and the status code are still written from the
		// core (WriteJSON, nil body), and the error path is entirely in
		// corehttp.WriteError.
		reason: "writes the previously encoded OpenAPI document; the header, the status and " +
			"the error path still pass through the core",
	},
}

// TestNonModuleHTTPSurfacesWriteThroughTheCore verifies that the HTTP surfaces
// left OUTSIDE the modules also pass through the core's writers.
//
// # Why this test exists
//
// [TestErrorResponsesAreWrittenInOnePlace] only walks internal/modules/*/api
// and [TestHTTPSurfacesLiveOnlyInApiPackages] audits that scope WITHIN the
// modules. Even together they do not see every place in the repository that
// writes a response: the core's own endpoints (/health, /ready,
// /openapi.json), its middlewares and the endpoints brought in by the plugins
// were inside no scan at all. The cost was measured — when the /openapi.json
// document could not be produced, a PLAIN TEXT 500 was returned with
// "http.Error(w, ...)": a client expecting JSON could not parse the body, the
// request id never appeared in the response, and the error's text (the package
// paths of the conflicting types) went out unmasked. The rule existed, the
// scope did not see it.
//
// # What is tested
//
// In every production package outside the modules, every path that writes a
// response must go to one of the core's writers:
//
//   - corehttp.WriteError — the ONLY gate for errors.
//   - corehttp.WriteJSON — the gate for success; its status MUST BE 2xx,
//     otherwise the error response has skipped the masking and logging
//     decisions.
//
// What counts as a violation: net/http's body-writing helpers
// ([bodyWritingNetHTTPCalls]), the write methods on the writer itself
// (Write/WriteHeader), the external calls that take the writer and write to its
// body ([bodyWritingExternalCalls]) and a WriteJSON that is not 2xx.
//
// # Known limit
//
// What is recognized as a writer: PARAMETERS of type http.ResponseWriter and
// local variables constructed from a WRAPPER type defined in the package (a
// struct carrying an embedded http.ResponseWriter). The wrapper type's OWN
// methods are not scanned; they do not produce the writer, they forward it —
// that is exactly a wrapper's job. Writing the limit down is deliberate:
// knowing that it is incomplete is better than believing that it is not.
// net/http's helpers are outside this limit, because they are caught BY NAME.
func TestNonModuleHTTPSurfacesWriteThroughTheCore(t *testing.T) {
	t.Parallel()

	verifyWriterDefinition(t)

	used := make([]map[string]bool, len(coreWriterExemptions))
	for i := range used {
		used[i] = map[string]bool{}
	}

	packages := nonModulePackages(t)
	if len(packages) == 0 {
		t.Fatal("no package found; the scan root may be wrong")
	}
	// The package count does not measure this test's FIELD OF VIEW: most of the
	// non-module packages have no path that writes a response and the list is
	// full in any case. What needs to be measured is that the writer is still
	// RECOGNIZED.
	verifyWriterCarryingFiles(t, packages,
		"non-module production packages", "if the writer moved behind a wrapping interface")

	for _, dir := range packages {
		auditCorePackage(t, dir, used)
	}

	for i, exemption := range coreWriterExemptions {
		stale := false

		for _, call := range exemption.calls {
			if used[i][call] {
				continue
			}

			stale = true

			t.Errorf("unused exemption: in %s, %q no longer calls %s.\n"+
				"Its rationale (%q) defends nothing for this call: either the path was "+
				"fixed and the line should be deleted, or the call moved to another "+
				"function and the exemption no longer sees it.",
				exemption.file, exemption.function, call, exemption.reason)
		}

		if stale {
			continue
		}

		if !callsCoreWriter(t, filepath.Join(repoRoot, exemption.file)) {
			t.Errorf("%s: the exemption's rationale (%q) does not hold — the file contains no "+
				"corehttp.%s or corehttp.%s call at all.\nAn exempt surface may write its own "+
				"body but it MUST ASK the core which error may be handed to the client.",
				exemption.file, exemption.reason, errorWriterName, successWriterName)
		}
	}
}

// verifyWriterDefinition verifies that the file exempted from the scan really
// defines the writers.
//
// The exemption was granted not to a FILE NAME but to that file's JOB. If the
// writers move to another file this name would turn into an exemption that "may
// write anything", with no rationale left behind — and during the move nobody
// would look here.
func verifyWriterDefinition(t *testing.T) {
	t.Helper()

	path := filepath.Join(repoRoot, coreWriterDefinition)

	tree, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("%s could not be parsed: %v", coreWriterDefinition, err)
	}

	found := map[string]bool{}
	for _, decl := range tree.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			found[fn.Name.Name] = true
		}
	}

	for _, name := range []string{errorWriterName, successWriterName} {
		if !found[name] {
			t.Fatalf("%s no longer defines %q.\nThis file is EXEMPT from the scan because it "+
				"is the place where the writers are defined; if a writer moved, "+
				"coreWriterDefinition must move with it, otherwise the exemption is left "+
				"without a rationale.",
				coreWriterDefinition, name)
		}
	}
}

// nonModulePackages returns the directories of the production packages outside
// the modules.
//
// The modules are DELIBERATELY left out: [TestErrorResponsesAreWrittenInOnePlace]
// walks them with a far more detailed scan (leaking the writer, tracing the
// status code). Everything else — the core, the workflows, the plugins and the
// composition root — falls here; that is, NO directory name in the repository
// is left unscanned.
func nonModulePackages(t *testing.T) []string {
	t.Helper()

	set := map[string]bool{}

	for _, file := range goFiles(t, repoRoot) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		path := repoPath(file)
		if strings.HasPrefix(path, modulesDir+string(filepath.Separator)) || path == coreWriterDefinition {
			continue
		}

		set[filepath.Dir(file)] = true
	}

	dirs := make([]string, 0, len(set))
	for dir := range set {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	return dirs
}

// coreAudit is the audit context of a single non-module file.
type coreAudit struct {
	t    *testing.T
	fset *token.FileSet
	file string
	// paths maps the file's import names to their paths.
	paths map[string]string
	// wrappers holds the names of the struct types defined in the package that
	// carry an embedded http.ResponseWriter.
	wrappers map[string]bool
	// inCoreHTTPPackage says that the file lives in the core's HTTP package;
	// there the writer calls are UNQUALIFIED (WriteError, not
	// corehttp.WriteError).
	inCoreHTTPPackage bool
	// used is, per exemption, the set of the calls that were used.
	used []map[string]bool
}

// auditCorePackage walks a single package and reports the violations.
//
// The audit is at PACKAGE level because wrapper types are defined in one file
// and used in other files (responseWriter is defined in middleware.go and
// constructed in telemetry.go); a scan looking file by file could not see them.
func auditCorePackage(t *testing.T, dir string, used []map[string]bool) {
	t.Helper()

	fset := token.NewFileSet()
	files := productionFiles(t, dir)
	trees := make(map[string]*ast.File, len(files))

	for _, file := range files {
		if repoPath(file) == coreWriterDefinition {
			continue
		}

		tree, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed: %v", file, err)
		}

		trees[file] = tree
	}

	wrappers := wrapperTypes(t, dir)

	for file, tree := range trees {
		d := &coreAudit{
			t:                 t,
			fset:              fset,
			file:              file,
			paths:             importPaths(tree),
			wrappers:          wrappers,
			inCoreHTTPPackage: repoPath(dir) == coreHTTPDir,
			used:              used,
		}

		for _, decl := range tree.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				d.auditFunction(fn)
			}
		}
	}
}

// wrapperTypes returns the names of the structs in the package that carry an
// embedded http.ResponseWriter.
//
// Putting the writer into a wrapper is the rule rather than the EXCEPTION in
// this repository (status counter, idempotency record, telemetry); a scan that
// did not recognize the wrapper would miss every path where the body is written
// from there.
func wrapperTypes(t *testing.T, dir string) map[string]bool {
	t.Helper()

	names := map[string]bool{}

	for _, file := range productionFiles(t, dir) {
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed: %v", file, err)
		}

		paths := importPaths(tree)

		ast.Inspect(tree, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}

			structType, ok := spec.Type.(*ast.StructType)
			if !ok || structType.Fields == nil {
				return true
			}

			for _, field := range structType.Fields.List {
				if len(field.Names) == 0 && isWriterType(field.Type, paths) {
					names[spec.Name.Name] = true
				}
			}

			return true
		})
	}

	return names
}

// auditFunction audits the response-writing paths in a function.
func (d *coreAudit) auditFunction(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}

	writers := writerVariables(fn, d.paths, d.wrappers)

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			d.auditCall(fn.Name.Name, call, writers)
		}

		return true
	})
}

// auditCall checks a single call against the rules.
func (d *coreAudit) auditCall(function string, call *ast.CallExpr, writers map[string]bool) {
	target := resolveCallTarget(call, d.paths)

	if target.pkg == netHTTPPath && bodyWritingNetHTTPCalls[target.name] {
		d.violation(function, target.source, call.Pos(),
			"%s writes the response body OUTSIDE the core.\n"+
				"The body will not be the shared JSON envelope: the client cannot parse the "+
				"error, the request id never enters the response and the error's text goes out "+
				"unmasked. The only gate for errors is corehttp.%s.", target.source, errorWriterName)

		return
	}

	if d.isCoreWriter(target) {
		if target.name == successWriterName {
			d.auditStatus(function, target.source, call)
		}

		return
	}

	if target.recv != "" && writers[target.recv] {
		if writerSafeMethods[target.name] {
			return
		}

		d.violation(function, target.source, call.Pos(),
			"the response is written DIRECTLY with %s.\n"+
				"The only gate for the error body is corehttp.%s and for success corehttp.%s; "+
				"a second write path means copying the masking and logging decisions.",
			target.source, errorWriterName, successWriterName)

		return
	}

	if !callTakesWriter(call, writers) {
		return
	}

	if target.pkg != "" && bodyWritingExternalCalls[target.pkg+"."+target.name] {
		d.violation(function, target.source, call.Pos(),
			"the writer is handed to the %s call and the body is written from "+
				"there.\nThe shape of the envelope and the masking decision must stay in the "+
				"core; a hand-coded body is a second copy of those decisions.", target.source)

		return
	}

	if templateWritingMethods[target.name] {
		d.violation(function, target.source, call.Pos(),
			"the writer is handed to the %s call: the template is STREAMED straight into the "+
				"response.\nThe template must first be rendered into memory, on an error "+
				"corehttp.%s must be called, and only on success may it be handed to "+
				"corehttp.%s. In a streamed template an error born halfway through leaves a "+
				"HALF page with a 200 status code: since the header has already been sent, "+
				"neither the panic recoverer nor the error writer can do anything.",
			target.source, errorWriterName, htmlWriterName)
	}
}

// auditStatus verifies that the status code of a WriteJSON call is 2xx.
//
// A WriteJSON called with a 4xx/5xx status writes the error response BY
// SKIPPING corehttp.WriteError's masking and logging decisions; the result is
// the same as never calling the core at all.
func (d *coreAudit) auditStatus(function, source string, call *ast.CallExpr) {
	const statusIndex = 2

	if len(call.Args) <= statusIndex {
		return
	}

	switch v := call.Args[statusIndex].(type) {
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		if ok && d.paths[x.Name] == netHTTPPath && successStatuses[v.Sel.Name] {
			return
		}
	case *ast.BasicLit:
		if v.Kind == token.INT {
			if code, err := strconv.Atoi(v.Value); err == nil && code >= 200 && code <= 299 {
				return
			}
		}
	}

	d.violation(function, source, call.Args[statusIndex].Pos(),
		"the status code of the corehttp.%s call CANNOT BE RESOLVED as 2xx.\n"+
			"A body that is not 2xx skips corehttp.%s's masking and logging decisions: "+
			"whatever is written into the body is what goes to the client.",
		successWriterName, errorWriterName)
}

// isCoreWriter says whether the call goes to the core's writers.
//
// Inside the core's OWN package the call is unqualified (WriteError), outside
// it is qualified with the import name (corehttp.WriteError); both are the same
// function and the scan has to recognize both.
func (d *coreAudit) isCoreWriter(target callTarget) bool {
	if target.name != errorWriterName && target.name != successWriterName {
		return false
	}

	if target.pkg == coreHTTPPath {
		return true
	}

	return d.inCoreHTTPPackage && target.local && target.recv == ""
}

// violation reports a violation unless it is exempt.
func (d *coreAudit) violation(function, source string, pos token.Pos, format string, args ...any) {
	d.t.Helper()

	if d.isExempt(function, source) {
		return
	}

	position := d.fset.Position(pos)
	d.t.Errorf("%s:%d: inside %s: "+format,
		append([]any{repoPath(d.file), position.Line, function}, args...)...)
}

// isExempt says whether the call is justified for this file and this function.
func (d *coreAudit) isExempt(function, source string) bool {
	path := repoPath(d.file)

	for i, exemption := range coreWriterExemptions {
		if filepath.FromSlash(exemption.file) != path || exemption.function != function {
			continue
		}

		for _, call := range exemption.calls {
			if call != source {
				continue
			}

			d.used[i][call] = true

			return true
		}
	}

	return false
}

// writerVariables collects the writer names in the function.
//
// There are two sources: PARAMETERS of type http.ResponseWriter (including
// those of inner function literals) and local variables constructed from a
// wrapper type defined in the package. Without the second one, putting the
// writer into a wrapper and writing the body from there would silently slip
// past the scan.
func writerVariables(fn ast.Node, paths map[string]string, wrappers map[string]bool) map[string]bool {
	names, _ := writerParameters(fn, paths)

	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for i, right := range assign.Rhs {
			if i >= len(assign.Lhs) || !constructsWrapper(right, wrappers) {
				continue
			}

			if target, ok := assign.Lhs[i].(*ast.Ident); ok {
				names[target.Name] = true
			}
		}

		return true
	})

	return names
}

// constructsWrapper says whether the expression constructs an instance of a
// wrapper type.
func constructsWrapper(expr ast.Expr, wrappers map[string]bool) bool {
	if unary, ok := expr.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		expr = unary.X
	}

	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return false
	}

	name, ok := lit.Type.(*ast.Ident)

	return ok && wrappers[name.Name]
}

// callsCoreWriter looks in the file for a call made to the core's writers.
func callsCoreWriter(t *testing.T, file string) bool {
	t.Helper()

	tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("%s could not be parsed: %v", file, err)
	}

	d := &coreAudit{
		paths:             importPaths(tree),
		inCoreHTTPPackage: repoPath(filepath.Dir(file)) == coreHTTPDir,
	}

	found := false

	ast.Inspect(tree, func(n ast.Node) bool {
		if found {
			return false
		}

		if call, ok := n.(*ast.CallExpr); ok && d.isCoreWriter(resolveCallTarget(call, d.paths)) {
			found = true
		}

		return true
	})

	return found
}

// auditPackage walks a single api package and reports the violations.
//
// The audit is at PACKAGE level because the helpers (such as writeItem) are
// defined in one file and called from other files; the trail of the status code
// can only be followed once the whole package is held in hand.
func auditPackage(t *testing.T, dir string, exemptionUsed []bool) {
	t.Helper()

	fset := token.NewFileSet()
	files := productionFiles(t, dir)
	trees := make(map[string]*ast.File, len(files))
	localNames := map[string]bool{}

	for _, file := range files {
		tree, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed: %v", file, err)
		}
		trees[file] = tree
		for _, decl := range tree.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				localNames[fn.Name.Name] = true
			}
		}
	}

	forwarders := findStatusForwarders(trees, localNames)

	for file, tree := range trees {
		paths := importPaths(tree)
		for _, decl := range tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			d := &auditCtx{
				t:          t,
				fset:       fset,
				file:       file,
				paths:      paths,
				localNames: localNames,
				forwarders: forwarders,
				used:       exemptionUsed,
			}
			d.auditFunction(fn)
		}
	}
}

// auditCtx carries the context needed to audit a single function.
type auditCtx struct {
	t          *testing.T
	fset       *token.FileSet
	file       string
	paths      map[string]string
	localNames map[string]bool
	forwarders map[string]map[int]bool
	used       []bool
}

// auditFunction audits the writer uses in a function body.
func (d *auditCtx) auditFunction(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	indexes := parameterIndexes(fn)
	forwardedHere := d.forwarders[fn.Name.Name]

	// The status code audit runs INDEPENDENTLY of the writer: an intermediate
	// helper that forwards the status to the core can be called without taking
	// the writer at all, and that call still determines with which status the
	// body is written.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			d.auditStatuses(call, indexes, forwardedHere)
		}

		return true
	})

	writers, declarations := writerParameters(fn, d.paths)
	if len(writers) == 0 {
		return
	}

	// accounted: the LEGITIMATE places where the writer is used. Every remaining
	// use means the writer has been leaked outside a call — into a struct, into
	// a field, into a variable — and from there on its trail cannot be followed.
	accounted := map[token.Pos]bool{}
	for _, declaration := range declarations {
		accounted[declaration] = true
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := node.X.(*ast.Ident); ok && writers[x.Name] {
				accounted[x.Pos()] = true
			}
		case *ast.CallExpr:
			for _, arg := range node.Args {
				if id, ok := arg.(*ast.Ident); ok && writers[id.Name] {
					accounted[id.Pos()] = true
				}
			}
			d.auditCall(node, writers)
		}

		return true
	})

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || !writers[id.Name] || accounted[id.Pos()] {
			return true
		}
		d.report(id.Pos(), "the writer %q is used outside a call.\n"+
			"Once the writer is leaked into another value, which body it writes cannot be "+
			"traced by this scan; the error response may bypass the core.", id.Name)

		return true
	})
}

// auditCall checks a single call that touches the writer against the rules.
func (d *auditCtx) auditCall(call *ast.CallExpr, writers map[string]bool) {
	target := resolveCallTarget(call, d.paths)

	// A method call ON the writer: everything other than Header() writes the
	// body or the status directly.
	if target.recv != "" && writers[target.recv] {
		if writerSafeMethods[target.name] {
			return
		}
		if d.isExempt(target.source) {
			return
		}
		d.report(call.Pos(), "the response is written DIRECTLY with %s.\n"+
			"The only gate for the error body is corehttp.%s and for success corehttp.%s; "+
			"a second write path means copying the masking and logging decisions.",
			target.source, errorWriterName, successWriterName)

		return
	}

	if !callTakesWriter(call, writers) {
		return
	}

	switch {
	case target.pkg == coreHTTPPath && (target.name == errorWriterName || target.name == successWriterName):
		// Whether the status is 2xx is audited separately (see auditStatuses).
		return
	case target.pkg != "" && safeWriterTakingCalls[target.pkg+"."+target.name] != "":
		return
	case target.local && d.localNames[target.name]:
		// The called function is inside this scan too; the rule is preserved
		// inductively.
		return
	case d.isExempt(target.source):
		return
	}

	if target.source == "" {
		d.report(call.Pos(), "the writer is handed to a call whose name cannot be resolved "+
			"(through a function value or through a field).\nEvery path that takes the "+
			"writer must be scannable; otherwise it cannot be known who writes the error "+
			"body.")

		return
	}
	d.report(call.Pos(), "the writer is handed to the %s call.\n"+
		"api packages may write the response only through corehttp.%s / corehttp.%s. If it "+
		"is really legitimate it should be added to errorPathExemptions WITH ITS "+
		"RATIONALE.", target.source, errorWriterName, successWriterName)
}

// auditStatuses verifies that the status codes carried by the call are 2xx.
//
// Two paths are audited. The first is the corehttp.WriteJSON call made DIRECTLY
// to the core. The second is the in-package helpers that pass the status on to
// the core as it is (such as writeItem): if the audit looked only at the core
// call that gate would stay open and writeItem(w, r, 500, body) would never be
// seen.
func (d *auditCtx) auditStatuses(call *ast.CallExpr, indexes map[string]int, forwardedHere map[int]bool) {
	target := resolveCallTarget(call, d.paths)

	if target.pkg == coreHTTPPath && target.name == successWriterName {
		const statusIndex = 2
		if len(call.Args) > statusIndex {
			d.auditStatusExpr(call.Args[statusIndex], indexes, forwardedHere,
				"corehttp."+successWriterName)
		}

		return
	}

	if !target.local || !d.localNames[target.name] {
		return
	}
	// The index numbers are sorted: map iteration order is random and an error
	// output that changed from run to run would make the same violation look
	// like two different findings.
	forwarded := make([]int, 0, len(d.forwarders[target.name]))
	for i := range d.forwarders[target.name] {
		forwarded = append(forwarded, i)
	}
	sort.Ints(forwarded)
	for _, i := range forwarded {
		if i < len(call.Args) {
			d.auditStatusExpr(call.Args[i], indexes, forwardedHere, target.name)
		}
	}
}

// auditStatusExpr verifies that a status code expression is 2xx.
func (d *auditCtx) auditStatusExpr(
	expr ast.Expr,
	indexes map[string]int,
	forwardedHere map[int]bool,
	target string,
) {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		if ok && d.paths[x.Name] == netHTTPPath {
			if successStatuses[v.Sel.Name] {
				return
			}
			d.report(expr.Pos(), "the %s call is handed a NON-2xx status (http.%s).\n"+
				"If the error response leaves through this path, corehttp.%s's masking and "+
				"logging decisions NEVER run: whatever the body is, that is what goes to the "+
				"client.", target, v.Sel.Name, errorWriterName)

			return
		}
	case *ast.BasicLit:
		if v.Kind == token.INT {
			if code, err := strconv.Atoi(v.Value); err == nil && code >= 200 && code <= 299 {
				return
			}
		}
	case *ast.Ident:
		// If the status comes from the caller's parameter, the trail is followed
		// at this function's own call sites (see findStatusForwarders).
		if i, ok := indexes[v.Name]; ok && forwardedHere[i] {
			return
		}
	}

	d.report(expr.Pos(), "the status code of the %s call CANNOT BE RESOLVED by this scan.\n"+
		"The status must either be a 2xx constant directly or be forwarded from the "+
		"caller's parameter; a status that cannot be resolved makes it unknowable whether "+
		"the error response bypasses the core.", target)
}

// isExempt says whether the call is justified for this file.
func (d *auditCtx) isExempt(source string) bool {
	if source == "" {
		return false
	}
	path := repoPath(d.file)
	for i, exemption := range errorPathExemptions {
		if filepath.FromSlash(exemption.file) == path && exemption.call == source {
			d.used[i] = true

			return true
		}
	}

	return false
}

// report reports a violation together with its location.
func (d *auditCtx) report(pos token.Pos, format string, args ...any) {
	d.t.Helper()
	position := d.fset.Position(pos)
	d.t.Errorf("%s:%d: "+format, append([]any{repoPath(d.file), position.Line}, args...)...)
}

// callTarget describes where a call goes.
type callTarget struct {
	// pkg is the import path if it is an external package, empty otherwise.
	pkg string
	// name is the name of the called function/method.
	name string
	// recv is the identifier of the receiver expression in a method call.
	recv string
	// local says that the call resolves within the same package.
	local bool
	// source is how the call is written in the source (like
	// "http.ServeContent"); empty if it could not be resolved.
	source string
}

// resolveCallTarget resolves the target of a call SYNTACTICALLY.
//
// The type checker is not run: the arch package walks the repository by parsing
// it, and binding to go/types would make the scan depend on compilability. The
// distinction is made through the import names — if it is a name the file
// imports it is an external package, otherwise it counts as an in-package value
// (the receiver).
func resolveCallTarget(call *ast.CallExpr, paths map[string]string) callTarget {
	fun := call.Fun
	for {
		switch v := fun.(type) {
		case *ast.ParenExpr:
			fun = v.X

			continue
		case *ast.IndexExpr: // explicit type argument of a generic call
			fun = v.X

			continue
		case *ast.IndexListExpr:
			fun = v.X

			continue
		}

		break
	}

	switch v := fun.(type) {
	case *ast.Ident:
		return callTarget{name: v.Name, local: true, source: v.Name}
	case *ast.SelectorExpr:
		x, ok := v.X.(*ast.Ident)
		if !ok {
			return callTarget{name: v.Sel.Name}
		}
		if path, found := paths[x.Name]; found {
			return callTarget{pkg: path, name: v.Sel.Name, source: x.Name + "." + v.Sel.Name}
		}

		return callTarget{name: v.Sel.Name, recv: x.Name, local: true, source: x.Name + "." + v.Sel.Name}
	}

	return callTarget{}
}

// callTakesWriter says whether there is a writer among the call's arguments.
func callTakesWriter(call *ast.CallExpr, writers map[string]bool) bool {
	for _, arg := range call.Args {
		if id, ok := arg.(*ast.Ident); ok && writers[id.Name] {
			return true
		}
	}

	return false
}

// findStatusForwarders finds the helpers that take the status code from their
// caller and pass it on to the core, together with the index of that parameter.
//
// It is repeated up to a fixed point: a second helper wrapping a helper counts
// as a forwarder too, otherwise putting a layer in between would slip past the
// audit.
func findStatusForwarders(trees map[string]*ast.File, localNames map[string]bool) map[string]map[int]bool {
	forwarders := map[string]map[int]bool{}

	mark := func(name string, index int) bool {
		if forwarders[name] == nil {
			forwarders[name] = map[int]bool{}
		}
		if forwarders[name][index] {
			return false
		}
		forwarders[name][index] = true

		return true
	}

	for changed := true; changed; {
		changed = false
		for _, tree := range trees {
			paths := importPaths(tree)
			for _, decl := range tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				indexes := parameterIndexes(fn)
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					target := resolveCallTarget(call, paths)
					var forwarded []int
					switch {
					case target.pkg == coreHTTPPath && target.name == successWriterName:
						forwarded = []int{2}
					case target.local && localNames[target.name]:
						for i := range forwarders[target.name] {
							forwarded = append(forwarded, i)
						}
					}
					for _, index := range forwarded {
						if index >= len(call.Args) {
							continue
						}
						id, ok := call.Args[index].(*ast.Ident)
						if !ok {
							continue
						}
						if i, found := indexes[id.Name]; found && mark(fn.Name.Name, i) {
							changed = true
						}
					}

					return true
				})
			}
		}
	}

	return forwarders
}

// parameterIndexes maps the function's parameter names to their index numbers.
func parameterIndexes(fn *ast.FuncDecl) map[string]int {
	indexes := map[string]int{}
	if fn.Type.Params == nil {
		return indexes
	}
	i := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			i++

			continue
		}
		for _, name := range field.Names {
			indexes[name.Name] = i
			i++
		}
	}

	return indexes
}

// writerParameters returns the names and the DECLARATION positions of the
// http.ResponseWriter parameters in the function.
//
// Inner function literals are walked too: a closure wrapping a handler also
// takes a writer and writing the body from there is the same violation.
//
// The declaration positions are returned separately because the declaration
// itself is not a "use"; the leak scan must be able to ignore it.
func writerParameters(fn ast.Node, paths map[string]string) (map[string]bool, []token.Pos) {
	names := map[string]bool{}
	var positions []token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		typ, ok := n.(*ast.FuncType)
		if !ok || typ.Params == nil {
			return true
		}
		for _, field := range typ.Params.List {
			if !isWriterType(field.Type, paths) {
				continue
			}
			for _, name := range field.Names {
				names[name.Name] = true
				positions = append(positions, name.Pos())
			}
		}

		return true
	})

	return names, positions
}

// isWriterType says whether the expression is the http.ResponseWriter type.
func isWriterType(typ ast.Expr, paths map[string]string) bool {
	sel, ok := typ.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != writerTypeName {
		return false
	}
	x, ok := sel.X.(*ast.Ident)

	return ok && paths[x.Name] == netHTTPPath
}

// importPaths maps the import names in the file to their paths.
func importPaths(tree *ast.File) map[string]string {
	paths := map[string]string{}
	for _, imp := range tree.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		paths[name] = path
	}

	return paths
}

// apiPackages returns the api package directories of the modules.
func apiPackages(t *testing.T) []string {
	t.Helper()
	var dirs []string
	for _, mod := range moduleNames(t) {
		dir := filepath.Join(repoRoot, modulesDir, mod, "api")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// productionFiles returns the NON-test .go files under the root.
func productionFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, file := range goFiles(t, root) {
		if !strings.HasSuffix(file, "_test.go") {
			out = append(out, file)
		}
	}

	return out
}

// verifyWriterCarryingFiles verifies that at least one file in the given
// packages carries an http.ResponseWriter.
//
// The input of all three error-path audits is not the PACKAGE list but the
// WRITER uses in those packages: the scan can stay empty while the package list
// is full, because a violation is only looked for on a path that touches the
// writer. The day the writer becomes unrecognizable (when the type moves behind
// an interface, when the handler signature turns into a framework type) all
// three audits look at no call at all and stay green.
//
// Today the EXEMPTIONS also catch this situation indirectly: an unused
// exemption fails the test and the marking of the exemptions depends on the
// scan seeing the writer. But that protection is accidental — the day the debt
// is paid and the last exemption is deleted, it disappears along with it. This
// gate does not depend on that day.
func verifyWriterCarryingFiles(t *testing.T, packages []string, scope, hint string) {
	t.Helper()

	found := 0
	for _, dir := range packages {
		for _, file := range productionFiles(t, dir) {
			if mentionsWriter(t, file) {
				found++
			}
		}
	}

	require.Positive(t, found,
		"in %s there is not a SINGLE production file carrying http.%s; the error path audit "+
			"must have gone BLIND (%s).\n"+
			"A scan that does not recognize the writer looks at no write path at all: an "+
			"error body written outside the core — unmasked text, an envelope-less response "+
			"— shows up nowhere. The criterion by which the writer is recognized "+
			"(isWriterType) must be updated together with reality.", scope, writerTypeName, hint)
}

// mentionsWriter says whether http.ResponseWriter is used in the file.
func mentionsWriter(t *testing.T, file string) bool {
	t.Helper()
	tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("%s could not be parsed: %v", file, err)
	}
	paths := importPaths(tree)
	found := false
	ast.Inspect(tree, func(n ast.Node) bool {
		if found {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok && isWriterType(sel, paths) {
			found = true
		}

		return true
	})

	return found
}

// usesCoreErrorPath looks for a corehttp.WriteError call in the package.
func usesCoreErrorPath(t *testing.T, dir string) bool {
	t.Helper()
	for _, file := range productionFiles(t, dir) {
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed: %v", file, err)
		}
		paths := importPaths(tree)
		found := false
		ast.Inspect(tree, func(n ast.Node) bool {
			if found {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			target := resolveCallTarget(call, paths)
			if target.pkg == coreHTTPPath && target.name == errorWriterName {
				found = true
			}

			return true
		})
		if found {
			return true
		}
	}

	return false
}

// repoPath normalizes an absolute or relative path against the repository root.
func repoPath(path string) string {
	clean := filepath.Clean(path)
	root := filepath.Clean(repoRoot) + string(filepath.Separator)

	return strings.TrimPrefix(clean, root)
}

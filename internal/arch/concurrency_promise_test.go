package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// concurrencyPromise matches the sentence a package uses to promise that
// something in it may be used from more than one goroutine.
//
// The phrasings are the ones the tree actually writes, in both languages, and
// they are matched as a CLOSED set rather than by the word "concurrent" alone:
// prose about concurrency is everywhere here, and a promise is a specific
// sentence rather than a topic.
//
// The Turkish alternative is spelled with \u escapes rather than with its
// letters, the same way core/db/casefold.go writes the pair it probes for: the
// phrase has to be the real one or the gate reads a smaller tree, and ADR 0012's
// diacritic lane reads THIS file's source. The escapes are the letters of
// "e\u015fzamanl\u0131 kullan\u0131ma g\u00fcvenli" — s with cedilla, dotless i,
// and u with diaeresis.
var concurrencyPromise = regexp.MustCompile(
	"(?i)e\u015fzamanl\u0131 kullan\u0131ma g\u00fcvenli|safe for concurrent|" +
		"concurrency-safe|goroutine-safe")

// concurrencyPrimitive matches a field type that only exists because two
// goroutines can be in the same value at once.
var concurrencyPrimitive = regexp.MustCompile(`^(sync\.(Mutex|RWMutex|Map)|atomic\.[A-Za-z]+)$`)

// concurrencySubjectFloor is the smallest population that could be the real one.
const concurrencySubjectFloor = 10

// concurrencyWitnesses names, for every type that carries a concurrency
// primitive inside a package that promises concurrency safety, the test that
// RUNS it from more than one goroutine.
//
// # Why a written map and not a search
//
// The first draft looked for a test in the same package that starts a goroutine
// and mentions the type's name. It over-credited on the first try: `core/http`
// came back covered because a message string in an unrelated test contained the
// words "has to go through" and the same test happened to name a type. A
// witness is a judgment about whether a test exercises a promise, and a judgment
// is written down rather than inferred from a substring.
//
// The KEYS are derived from the tree, so a new promise or a new primitive
// arrives here as a failure rather than as silence.
var concurrencyWitnesses = map[string]string{
	"core/container.registry":                                 "TestSingletonUnderConcurrentResolve",
	"core/http.CallbackRegistry":                              "TestTheCallbackRegistryFreezesExactlyOnceUnderConcurrentRegistration",
	"core/http.DeferredAuthenticator":                         "TestTheDeferredAuthenticatorIsReadableWhileItIsBeingBound",
	"core/http.MemoryIdempotencyStore":                        "TestIdempotencyASingleRunUnderARace",
	"core/http.MemoryLimiter":                                 "TestMemoryLimiterIsConsistentUnderConcurrentUse",
	"core/link.definitions":                                   "TestDefinitionsRegistryIsConcurrencySafe",
	"internal/adminui.Ring":                                   "TestTheRingIsReadableWhileItIsBeingBound",
	"internal/core/workflow.memoryStore":                      "TestMemoryStoreConcurrentUse",
	"internal/modules/file/service.ProviderRegistry":          "TestTheRegistryKeepsItsPromiseUnderConcurrentUse",
	"internal/modules/fulfillment/service.ProviderRegistry":   "TestTheRegistryKeepsItsPromiseUnderConcurrentUse",
	"internal/modules/notification/service.ProviderRegistry":  "TestTheRegistryKeepsItsPromiseUnderConcurrentUse",
	"internal/modules/notification/service.lazyOrderContacts": "TestOrderContactsSurviveAFailedResolutionUnderConcurrency",
	"internal/modules/payment/service.ProviderRegistry":       "TestTheRegistryKeepsItsPromiseUnderConcurrentUse",
	"internal/modules/tax/service.ProviderRegistry":           "TestTheRegistryKeepsItsPromiseUnderConcurrentUse",
	"plugins/searchpg.catalog":                                "TestTheCatalogSurvivesAFailedResolutionUnderConcurrency",
}

// concurrencyPromisesWithoutAWitness are the subjects that deliberately have no
// concurrent test.
//
// It is empty, and it is kept empty rather than deleted. A type whose primitive
// guards something a test cannot drive from two goroutines has a case for being
// here, and the case has to be WRITTEN — otherwise the only way past this gate
// is to edit it.
var concurrencyPromisesWithoutAWitness = map[string]string{}

// TestEveryConcurrencyPromiseHasATestThatRunsIt derives its population from two
// independent directions.
//
// # What was measured
//
// A lock is only checked by the race detector on code that ACTUALLY RAN
// concurrently. On 2026-09-09 five identical provider registries — the point
// every payment, notification, file, fulfillment and tax plugin extends — each
// promised in its own godoc that it is safe for concurrent use, and not one had
// a test that ran it from two goroutines. Deleting the mutex from payment's
// Register left the whole package green under `-race`.
//
// The coverage was not absent, it was SELECTIVE: core/container, core/link,
// core/http's idempotency store and limiter and the workflow memory store all
// had one. The core was covered and the module-level extension points were not.
//
// # Why the population comes from two directions
//
// The PROSE alone would let a promise be evaded by deleting the sentence, and
// the PRIMITIVE alone would demand a concurrent test for every lazily built
// cache in a single-threaded path. A promise plus a primitive is a type that
// says two goroutines can meet in it AND is built as if they do.
//
// The promise is read at PACKAGE granularity on purpose. It is not always
// written on the type: core/container puts it in the package doc,
// internal/core/workflow on the constructor, core/http on the interface the
// struct implements. A gate keyed to the type's own doc reports a smaller tree
// than the promise covers, which is the same unit mistake ADR 0026's gate made.
func TestEveryConcurrencyPromiseHasATestThatRunsIt(t *testing.T) {
	subjects := concurrencySubjects(t)

	require.GreaterOrEqualf(t, len(subjects), concurrencySubjectFloor,
		"only %d concurrency subjects were found; the walk has gone blind, and an empty "+
			"population is witnessed by definition", len(subjects))

	names := testFunctionNames(t)

	for _, subject := range subjects {
		if _, exempt := concurrencyPromisesWithoutAWitness[subject]; exempt {
			continue
		}

		witness, named := concurrencyWitnesses[subject]
		assert.Truef(t, named,
			"%s carries a concurrency primitive in a package that promises concurrency "+
				"safety, and no test is named as running it from two goroutines.\n"+
				"The race detector only looks at code that actually ran concurrently, so "+
				"without one the lock is unchecked. Add the test and name it here, or write "+
				"down in concurrencyPromisesWithoutAWitness why this one cannot have it.",
			subject)
		if !named {
			continue
		}

		assert.Containsf(t, names, witness,
			"the witness named for %s, %s, is not in the tree; deleting it puts the lock "+
				"back outside the race detector's reach", subject, witness)
	}

	for _, named := range slices.Sorted(maps.Keys(concurrencyWitnesses)) {
		assert.Containsf(t, subjects, named,
			"a witness is named for %s, which is no longer a concurrency subject; a witness "+
				"for a subject that is gone hides the next one that takes its name", named)
	}
}

// concurrencySubjects returns "<package>.<Type>" for every type that carries a
// concurrency primitive inside a package that promises concurrency safety.
func concurrencySubjects(t *testing.T) []string {
	t.Helper()

	promised := map[string]bool{}
	byPackage := map[string][]string{}

	fset := token.NewFileSet()
	for _, file := range goFiles(t, repoRoot) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		require.NoErrorf(t, err, "%s could not be parsed", file)

		pkg := filepath.ToSlash(filepath.Dir(strings.TrimPrefix(filepath.ToSlash(file), repoRoot+"/")))
		if !strings.HasPrefix(pkg, "core/") && !strings.HasPrefix(pkg, "internal/") &&
			!strings.HasPrefix(pkg, "plugins/") {
			continue
		}

		for _, group := range parsed.Comments {
			if concurrencyPromise.MatchString(group.Text()) {
				promised[pkg] = true

				break
			}
		}

		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structType.Fields.List {
					if concurrencyPrimitive.MatchString(fieldTypeName(field.Type)) {
						byPackage[pkg] = append(byPackage[pkg], ts.Name.Name)

						break
					}
				}
			}
		}
	}

	var subjects []string
	for pkg, types := range byPackage {
		if !promised[pkg] {
			continue
		}
		for _, name := range types {
			subjects = append(subjects, pkg+"."+name)
		}
	}
	slices.Sort(subjects)

	return subjects
}

// fieldTypeName renders a field's type as "pkg.Name", or "" for anything else.
func fieldTypeName(expr ast.Expr) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}

	return ident.Name + "." + sel.Sel.Name
}

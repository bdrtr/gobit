package webhookout

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file answers the one question a name-based subscriber cannot answer at
// run time: is the list of topics still the whole list?
//
// The bus subscribes BY NAME. A sender that wants "every event" cannot ask for
// that, so [ForwardedTopics] is written out, and a written-out list is a list
// that goes stale. The day the order module publishes "order.shipped", this
// plugin will not deliver it, no error will be raised anywhere, and the only
// symptom will be a receiver that registered for a topic and — correctly, per
// [validateTopics] — was told it does not exist.
//
// So the list is checked against the source. The scan is the same shape as
// internal/arch's TestTheEventTopicsHaveASubscriber, cut down to the one thing
// this needs: every event name an eventbus.Event can carry.
//
// # Why the plugin carries this and not internal/arch
//
// Because the assertion is about THIS plugin's list, and a gate that lives away
// from the thing it constrains is a gate somebody deletes while refactoring the
// other end. The precedent is ADR 0018's: plugin migrations are covered by no
// arch gate either, so plugins/webpush carries its own rollback test, "a
// requirement of this decision rather than a nicety".

// repoRoot is the repository root, from this package's directory.
const repoRoot = "../.."

// scannedTrees are the directories the census reads.
//
// It is the same list internal/arch calls productionTrees, minus the repository
// root itself: the published facade at the root declares no event. A tree
// missing from here is a tree whose publishes are invisible to this test, which
// is why [TestTheScannedTreesStillExist] checks every one of them is on disk.
var scannedTrees = []string{"cmd", "core", "internal", "plugins", "server"}

// unresolvableNames are the publish sites whose event name is deliberately not
// resolved, with the reason.
//
// Both are FORWARDING sites rather than declarations: neither decides a topic,
// each carries one another file already declared. Erroring on them would make
// this test fail on the framework's own plumbing instead of on a new topic,
// and skipping them silently would let a genuinely unresolvable publish hide
// among them.
var unresolvableNames = map[string]string{
	"core/eventbus/outbox/outbox.go": "the relay re-publishes a row somebody else wrote; " +
		"the name is data read back out of the table, not a topic declared here",
	"internal/modules/order/repository/aftersales.go": "WriteOutboxEvent takes the name as a " +
		"parameter and the order service is the caller that decides it; that call site is " +
		"resolved on its own",
}

// TestTheForwardedTopicsAreEveryPublishedTopic is the gate.
//
// It fails in both directions on purpose. A topic published and not forwarded
// is the stale list this test exists for; a topic forwarded and not published
// is the examples/plugin failure — a subscription registered against a name
// nobody emits, which looks wired and never runs.
func TestTheForwardedTopicsAreEveryPublishedTopic(t *testing.T) {
	t.Parallel()

	published := publishedTopics(t)

	require.NotEmpty(t, published,
		"no event publish was resolved, so the census has gone blind and every list "+
			"would pass. The publish surface must have changed; this test has to change "+
			"with it.")

	forwarded := slices.Clone(ForwardedTopics)
	slices.Sort(forwarded)
	slices.Sort(published)

	for _, topic := range published {
		assert.Contains(t, forwarded, topic,
			"the %q topic is PUBLISHED by this repository and this plugin does not forward "+
				"it.\nNothing fails at run time: the event goes out, its other subscribers "+
				"run, and a receiver that wants it cannot even register for it — "+
				"validateTopics refuses the name as unsupported, which reads to an "+
				"integrator as \"gobit does not emit that\".\nAdd it to ForwardedTopics AND "+
				"add the matching Host.Subscribe call in Setup; the list alone subscribes "+
				"to nothing.", topic)
	}

	for _, topic := range forwarded {
		assert.Contains(t, published, topic,
			"this plugin forwards %q and NO PRODUCTION FILE publishes it.\nThat fails "+
				"entirely silently: the handler is registered, the bus accepts it, and it "+
				"never runs. It is the examples/plugin failure, which listened for "+
				"\"order.created\" while gobit publishes \"order.placed\".", topic)
	}
}

// TestTheScannedTreesStillExist keeps [scannedTrees] honest.
//
// A tree that has been renamed is not a failure anywhere else: the walk simply
// does not go there, finds no publish, and the census silently shrinks. This is
// the memory of internal/arch's own lesson — the root list is DATA, and a scan
// with a wrong root approves everything.
func TestTheScannedTreesStillExist(t *testing.T) {
	t.Parallel()

	for _, tree := range scannedTrees {
		_, err := os.Stat(filepath.Join(repoRoot, tree))
		require.NoError(t, err,
			"the %q tree is on the census list and not on disk; every publish under it "+
				"is invisible to TestTheForwardedTopicsAreEveryPublishedTopic", tree)
	}
}

// TestTheUnresolvableExemptionsAreStillNeeded stops a dead exemption covering up
// a real one.
//
// The census reports what it could not resolve. If a file on the exemption list
// no longer appears there, the exemption has outlived the code it described and
// the next genuinely unresolvable publish would be waved through under its name.
func TestTheUnresolvableExemptionsAreStillNeeded(t *testing.T) {
	t.Parallel()

	_, unresolved := scanTopics(t)

	for path, reason := range unresolvableNames {
		assert.Contains(t, unresolved, path,
			"%s is exempt from the topic census (%q) but nothing unresolvable was found in "+
				"it any more. Delete the entry — a dead exemption covers up the next real "+
				"one.", path, reason)
	}
}

// publishedTopics returns every event name this repository can publish.
func publishedTopics(t *testing.T) []string {
	t.Helper()

	topics, unresolved := scanTopics(t)

	for _, path := range unresolved {
		if _, exempt := unresolvableNames[path]; exempt {
			continue
		}
		t.Errorf("%s: the NAME of a published event could not be resolved statically.\n"+
			"An unresolved publish is a topic this plugin cannot be checked against, so "+
			"skipping it would let a new topic arrive unnoticed. Bind the name to a string "+
			"constant (see order/service.EventOrderPlaced), or — if the site only FORWARDS "+
			"a name another file declared — record it in unresolvableNames with its reason.",
			path)
	}

	return topics
}

// censusFile is one parsed production file.
type censusFile struct {
	path    string
	pkgDir  string
	tree    *ast.File
	imports map[string]string
}

// scanTopics walks the production trees and resolves every eventbus.Event name.
//
// It returns the distinct topics and the paths of the files holding a publish it
// could not resolve. The two are separate because they are different failures:
// the first is what the gate compares, and the second is the gate admitting it
// did not read everything.
func scanTopics(t *testing.T) (topics, unresolved []string) {
	t.Helper()

	files := parseProduction(t)
	constants := collectConstants(files)

	seenTopic := map[string]bool{}
	seenUnresolved := map[string]bool{}

	for _, f := range files {
		var enclosing *ast.FuncDecl

		ast.Inspect(f.tree, func(node ast.Node) bool {
			if decl, ok := node.(*ast.FuncDecl); ok {
				enclosing = decl
			}

			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isEventLiteral(f, literal) {
				return true
			}

			name, found := fieldValue(literal, "Name")
			if !found {
				// An Event built without a Name is refused by the bus at
				// publish time; it declares no topic and is not this test's
				// business.
				return true
			}

			for _, resolved := range resolveName(f, enclosing, name, constants, files) {
				seenTopic[resolved] = true
			}
			if len(resolveName(f, enclosing, name, constants, files)) == 0 {
				seenUnresolved[f.path] = true
			}

			return true
		})
	}

	for topic := range seenTopic {
		topics = append(topics, topic)
	}
	for path := range seenUnresolved {
		unresolved = append(unresolved, path)
	}
	slices.Sort(topics)
	slices.Sort(unresolved)

	return topics, unresolved
}

// resolveName turns the Name expression into the string values it can hold.
//
// Three forms are handled and they are the three the repository uses: a
// literal, a constant (local or qualified), and a PARAMETER of the enclosing
// function — which is descended into by looking at what the callers in the same
// package pass at that position. The parameter case is not exotic; it is how
// the product module publishes all three of its events through one line.
func resolveName(
	f *censusFile, enclosing *ast.FuncDecl, expr ast.Expr,
	constants map[string]string, files []*censusFile,
) []string {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return nil
		}
		text, err := strconv.Unquote(value.Value)
		if err != nil {
			return nil
		}

		return []string{text}

	case *ast.Ident:
		if text, ok := constants[f.pkgDir+"."+value.Name]; ok {
			return []string{text}
		}

		return callerValues(f, enclosing, value.Name, constants, files)

	case *ast.SelectorExpr:
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return nil
		}
		dir := strings.TrimPrefix(f.imports[pkg.Name], modulePath+"/")
		if text, ok := constants[dir+"."+value.Sel.Name]; ok {
			return []string{text}
		}

		return nil

	default:
		return nil
	}
}

// callerValues resolves a name that is a parameter of the enclosing function.
func callerValues(
	f *censusFile, enclosing *ast.FuncDecl, param string,
	constants map[string]string, files []*censusFile,
) []string {
	if enclosing == nil || enclosing.Type.Params == nil {
		return nil
	}

	index, position := -1, 0
	for _, field := range enclosing.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == param {
				index = position
			}
			position++
		}
	}
	if index < 0 {
		return nil
	}

	var out []string
	for _, candidate := range files {
		if candidate.pkgDir != f.pkgDir {
			continue
		}
		ast.Inspect(candidate.tree, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) <= index || calleeName(call) != enclosing.Name.Name {
				return true
			}
			out = append(out, resolveName(candidate, nil, call.Args[index], constants, files)...)

			return true
		})
	}

	return out
}

// calleeName is the function name a call refers to, ignoring its receiver.
func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

// isEventLiteral reports whether a composite literal is an eventbus.Event.
func isEventLiteral(f *censusFile, literal *ast.CompositeLit) bool {
	selector, ok := literal.Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Event" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}

	return f.imports[pkg.Name] == modulePath+"/core/eventbus"
}

// fieldValue reads one field out of a composite literal.
func fieldValue(literal *ast.CompositeLit, field string) (ast.Expr, bool) {
	for _, element := range literal.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if ok && key.Name == field {
			return pair.Value, true
		}
	}

	return nil, false
}

// modulePath is this repository's Go module path.
const modulePath = "github.com/bdrtr/gobit"

// parseProduction reads every non-test Go file in the scanned trees.
func parseProduction(t *testing.T) []*censusFile {
	t.Helper()

	fset := token.NewFileSet()
	var files []*censusFile

	for _, tree := range scannedTrees {
		root := filepath.Join(repoRoot, tree)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}

			parsed, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return parseErr
			}
			relative, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			relative = filepath.ToSlash(relative)

			file := &censusFile{
				path:    relative,
				pkgDir:  filepath.ToSlash(filepath.Dir(relative)),
				tree:    parsed,
				imports: map[string]string{},
			}
			for _, imported := range parsed.Imports {
				importPath := strings.Trim(imported.Path.Value, `"`)
				local := filepath.Base(importPath)
				if imported.Name != nil {
					local = imported.Name.Name
				}
				file.imports[local] = importPath
			}
			files = append(files, file)

			return nil
		})
		require.NoError(t, err, "the %q tree could not be walked", tree)
	}

	return files
}

// collectConstants builds a "<package dir>.<name>" to value table.
//
// Only string constants, because only a string can be an event name. The key is
// the DIRECTORY rather than the package name: two packages in this repository
// are named differently from their directory, and a table keyed on the declared
// name would silently merge them.
func collectConstants(files []*censusFile) map[string]string {
	constants := map[string]string{}

	for _, f := range files {
		for _, decl := range f.tree.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.CONST {
				continue
			}
			for _, spec := range group.Specs {
				values, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range values.Names {
					if i >= len(values.Values) {
						continue
					}
					literal, ok := values.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					text, err := strconv.Unquote(literal.Value)
					if err != nil {
						continue
					}
					constants[f.pkgDir+"."+name.Name] = text
				}
			}
		}
	}

	return constants
}

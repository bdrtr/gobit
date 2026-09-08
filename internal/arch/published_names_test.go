package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishedNamesFile is the inventory of everything `core/` promises.
const publishedNamesFile = "testdata/published-names.txt"

// publishedNamesFloor is the smallest inventory that could be the real one.
//
// It is well under the live count on purpose: this is not a second copy of the
// number, it is the line under which the READER has plainly stopped working.
// A gate whose floor tracks the surface has to be edited on every ordinary
// change, and a floor edited that often stops being read.
const publishedNamesFloor = 400

// TestThePublishedNamesAreTheDeclaredOnes holds the promise at the granularity
// the promise is actually made in.
//
// # The hole this closes
//
// [TestThePublishedPackagesAreTheDeclaredOnes] compares DIRECTORIES. It is a
// real gate and it says something true — no package appears under `core/`
// without being declared — but a package is not the unit of the promise. ADR
// 0026 says every exported name in `core/` is kept until 1.0.0, and a name is
// what an embedder writes in their own source. Three names were added to
// `core/provider` on the day this was written and the whole arch suite stayed
// green, because they arrived in a file inside a directory that was already
// declared.
//
// So the promise was made at one granularity and audited at another, which is
// the shape of a gate that reports a clean tree it never looked at.
//
// # What is in the inventory
//
// Every top-level exported declaration — function, type, constant, variable —
// plus the exported METHODS of exported types and the method set of every
// exported interface. The methods are there because they are where a break
// hides: adding one method to a published interface breaks every embedder who
// implements it, and removing one breaks every embedder who calls it, while the
// type's own name never moves.
//
// # What it does NOT guarantee
//
// It reads NAMES and not signatures. Changing a parameter's type breaks an
// embedder exactly as hard as deleting the function, and this gate is silent
// about it — that is what the compiler does for the tree's own callers and what
// the out-of-tree example in [TestTheOutOfTreeExamplesCompile] does for a
// consumer's. It also says nothing about whether a name SHOULD be published;
// that is [TestThePublishedPackagesAreTheDeclaredOnes]'s question one level up,
// and a judgement besides.
//
// # Why a file and not a number
//
// A count would let one name leave as another arrives. The inventory is a list
// of NAMES for the reason [countClaimedToday] is: a promise that changes has to
// change in a diff somebody reads. Editing this file is the act of adding to or
// withdrawing from a published surface, and it should feel like one.
func TestThePublishedNamesAreTheDeclaredOnes(t *testing.T) {
	t.Parallel()

	// The two sides are compared as SETS and only the difference is reported.
	// An assertion that printed the collections would answer a one-name drift
	// with 485 lines, and a failure nobody reads to the end is a failure that
	// gets skimmed for the exit code.
	declared := map[string]bool{}
	for _, name := range declaredPublishedNames(t) {
		declared[name] = true
	}

	found := map[string]bool{}
	for _, name := range publishedNames(t) {
		found[name] = true
	}

	for _, name := range slices.Sorted(maps.Keys(found)) {
		if !declared[name] {
			t.Errorf("%s is exported from the published tree and is NOT in %s.\n"+
				"Every exported name under %s is a promise kept until 1.0.0 (ADR 0026). "+
				"If it is meant to be one, add the line; if it is not, unexport it — but "+
				"it may not arrive without somebody deciding which.",
				name, publishedNamesFile, publishedTree)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(declared)) {
		if !found[name] {
			t.Errorf("%s is in %s and is NOT exported from the tree.\n"+
				"A promise that was withdrawn is a BREAKING CHANGE and the line is the "+
				"only place it is recorded; a promise that was never made sends an "+
				"embedder looking for a name nobody has.",
				name, publishedNamesFile)
		}
	}
}

// TestThePublishedNameReaderIsNotBlind is the positive control.
//
// The inventory is only worth what the reader is worth, and a reader that
// quietly understands nothing reports a surface that matches perfectly — the
// two empty sets agree. This plants every declaration shape the reader claims
// to read and fails if one goes missing.
func TestThePublishedNameReaderIsNotBlind(t *testing.T) {
	t.Parallel()

	const source = `package planted

type Exported struct{}

func (e Exported) Method() {}
func (e *Exported) PointerMethod() {}
func (e Exported) unexportedMethod() {}

type unexported struct{}

func (u unexported) MethodOnUnexported() {}

type Contract interface {
	InterfaceMethod() error
	unexportedInterfaceMethod()
}

func TopLevel() {}
func unexportedTopLevel() {}

const ExportedConst = 1
const unexportedConst = 1

var ExportedVar = 1
var unexportedVar = 1

type (
	GroupedOne struct{}
	groupedTwo struct{}
)
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "planted.go", source, 0)
	require.NoError(t, err)

	got := map[string]bool{}
	for _, name := range namesOfFile("planted", file) {
		got[name] = true
	}

	for _, want := range []string{
		"planted Exported",
		"planted Exported.Method",
		"planted Exported.PointerMethod",
		"planted Contract",
		"planted Contract.InterfaceMethod",
		"planted TopLevel",
		"planted ExportedConst",
		"planted ExportedVar",
		"planted GroupedOne",
	} {
		assert.True(t, got[want],
			"the reader missed %q; every name of that shape is outside the inventory "+
				"and can be added to or removed from the published surface in silence", want)
	}

	for _, unwanted := range []string{
		"planted unexported",
		"planted unexportedTopLevel",
		"planted unexportedConst",
		"planted unexportedVar",
		"planted groupedTwo",
		"planted Exported.unexportedMethod",
		"planted unexported.MethodOnUnexported",
		"planted Contract.unexportedInterfaceMethod",
	} {
		assert.False(t, got[unwanted],
			"the reader took %q for a published name; an unexported name is not a "+
				"promise and putting one in the inventory makes the file a chore rather "+
				"than a decision", unwanted)
	}
}

// declaredPublishedNames reads the inventory file.
func declaredPublishedNames(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(publishedNamesFile)
	require.NoError(t, err, "%s could not be read", publishedNamesFile)

	var names []string
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		names = append(names, trimmed)
	}

	require.GreaterOrEqual(t, len(names), publishedNamesFloor,
		"%s holds only %d names; the file has plainly lost its contents, and an "+
			"inventory that has gone empty agrees with a surface that has gone empty",
		publishedNamesFile, len(names))

	return names
}

// publishedNames collects the exported names of the published tree.
func publishedNames(t *testing.T) []string {
	t.Helper()

	var names []string

	root := filepath.Join(repoRoot, publishedTree)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if slices.Contains(skippedDirs, entry.Name()) {
			return filepath.SkipDir
		}

		relative, relErr := filepath.Rel(repoRoot, current)
		if relErr != nil {
			return relErr
		}

		// parseDir rather than the standard library's ParseDir: the shared
		// helper is what every other gate in this package reads Go with, and
		// the deprecated one associates files with packages without looking at
		// build tags — which would make this inventory depend on which tags
		// happened to be set when it ran.
		fset := token.NewFileSet()
		for _, file := range parseDir(t, fset, current, false) {
			names = append(names, namesOfFile(filepath.ToSlash(relative), file.tree)...)
		}

		return nil
	})
	require.NoError(t, err, "the published tree could not be walked")

	require.GreaterOrEqual(t, len(names), publishedNamesFloor,
		"only %d exported names were read out of %s; the reader has gone blind and "+
			"an empty reading agrees with an empty inventory", len(names), publishedTree)

	slices.Sort(names)

	return slices.Compact(names)
}

// namesOfFile returns one file's exported names, each prefixed with its package
// path so two packages may hold the same name.
func namesOfFile(pkgPath string, file *ast.File) []string {
	var names []string

	add := func(name string) { names = append(names, pkgPath+" "+name) }

	for _, decl := range file.Decls {
		switch declaration := decl.(type) {
		case *ast.FuncDecl:
			if declaration.Recv == nil {
				if declaration.Name.IsExported() {
					add(declaration.Name.Name)
				}

				continue
			}
			// A method counts only when BOTH halves are exported: a method on
			// an unexported type cannot be reached from outside, and an
			// unexported method on an exported one is not a promise either.
			if receiver := publishedReceiver(declaration); ast.IsExported(receiver) &&
				declaration.Name.IsExported() {
				add(receiver + "." + declaration.Name.Name)
			}

		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch typed := spec.(type) {
				case *ast.TypeSpec:
					if !typed.Name.IsExported() {
						continue
					}

					add(typed.Name.Name)

					contract, ok := typed.Type.(*ast.InterfaceType)
					if !ok {
						continue
					}
					for _, method := range contract.Methods.List {
						for _, name := range method.Names {
							if name.IsExported() {
								add(typed.Name.Name + "." + name.Name)
							}
						}
					}

				case *ast.ValueSpec:
					for _, name := range typed.Names {
						if name.IsExported() {
							add(name.Name)
						}
					}
				}
			}
		}
	}

	return names
}

// publishedReceiver returns the type name a method is declared on.
//
// It is separate from [receiverName] next door, which answers a different
// question — that one takes an expression and returns "?" for anything it does
// not recognize, which is the right answer for a message about one method and
// the wrong one for a set that is compared for equality.
func publishedReceiver(decl *ast.FuncDecl) string {
	switch typed := decl.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := typed.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return typed.Name
	case *ast.IndexExpr:
		if ident, ok := typed.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.IndexListExpr:
		if ident, ok := typed.X.(*ast.Ident); ok {
			return ident.Name
		}
	}

	return ""
}

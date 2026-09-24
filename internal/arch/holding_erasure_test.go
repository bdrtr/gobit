package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A holding says what an erasure does to it (ADR 0172).
//
// The declaration a controller publishes a privacy notice from and the list an
// erasure reports as kept used to be written separately, and one had drifted: the
// link holder reported from_id as kept and never declared it (D128). Since the
// two are one list, what remains to hold is that every declared place says what
// an erasure does to it, and that a unit which cannot erase does not promise to
// empty anything.

// holdingTrees are the trees whose source declares holdings. The declaration
// is read from the source for plugin_personaldata_test.go's reason: installing
// every plugin to ask it would need every plugin's configuration.
var holdingTrees = []string{"internal", "plugins", "contrib"}

// declaredHolding is one Holding literal found in the source.
type declaredHolding struct {
	at        string
	unit      string
	onErasure string
}

// TestEveryHoldingSaysWhatErasureDoes requires every personaldata.Holding
// literal to name its OnErasure, and a unit with no Erase method to keep what
// it declares: nothing could empty it.
func TestEveryHoldingSaysWhatErasureDoes(t *testing.T) {
	holdings, erasers := sourceHoldings(t)
	require.NotEmpty(t, holdings, "no Holding literal was found; the scan has gone blind")
	t.Logf("%d holdings in %d units that can erase", len(holdings), len(erasers))

	for _, holding := range holdings {
		switch holding.onErasure {
		case "Emptied":
			assert.True(t, erasers[holding.unit],
				"%s promises its column is emptied on erasure, and %s has no Erase method: "+
					"nothing in it can empty anything, so the declaration would tell a controller "+
					"a place was cleaned that nobody touches", holding.at, holding.unit)
		case "Kept":
		case "":
			assert.Fail(t, "a holding says nothing about erasure",
				"%s declares a place without OnErasure. A controller publishes from the "+
					"declaration and an erasure reports what it kept from it; a holding that says "+
					"neither leaves both to guess", holding.at)
		default:
			assert.Fail(t, "a holding says something unknown about erasure",
				"%s declares OnErasure %q, which is neither Emptied nor Kept", holding.at, holding.onErasure)
		}
	}
}

// sourceHoldings reads every Holding literal and every unit that defines an
// Erase method.
func sourceHoldings(t *testing.T) (holdings []declaredHolding, erasers map[string]bool) {
	t.Helper()

	erasers = map[string]bool{}
	fset := token.NewFileSet()

	for _, tree := range holdingTrees {
		err := filepath.WalkDir(filepath.Join(repoRoot, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			parsed, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return parseErr
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			unit := holdingUnit(filepath.ToSlash(rel))

			record := func(lit *ast.CompositeLit) {
				holdings = append(holdings, declaredHolding{
					at:        fset.Position(lit.Pos()).String(),
					unit:      unit,
					onErasure: holdingOnErasure(lit),
				})
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.FuncDecl:
					if typed.Recv != nil && typed.Name.Name == "Erase" {
						erasers[unit] = true
					}
				case *ast.CompositeLit:
					// A Holding is found by its TYPE, written on the literal or on
					// the slice it sits in; a DTO with the same field names is
					// not a declaration.
					if isHoldingType(typed.Type) {
						record(typed)
					}
					if array, ok := typed.Type.(*ast.ArrayType); ok && isHoldingType(array.Elt) {
						for _, element := range typed.Elts {
							if inner, isLit := element.(*ast.CompositeLit); isLit && inner.Type == nil {
								record(inner)
							}
						}
					}
				}

				return true
			})

			return nil
		})
		require.NoError(t, err, "the %s tree could not be walked", tree)
	}

	return holdings, erasers
}

// holdingUnit is the unit a file belongs to: the directory under a container of
// units (modules, workflows, plugins, contrib), or the file's own directory.
func holdingUnit(rel string) string {
	parts := strings.Split(rel, "/")
	for i, part := range parts[:len(parts)-1] {
		switch part {
		case "modules", "workflows", "plugins", "contrib":
			if i+1 < len(parts)-1 {
				return strings.Join(parts[:i+2], "/")
			}
		}
	}

	return strings.Join(parts[:len(parts)-1], "/")
}

// isHoldingType reports whether an expression names personaldata.Holding.
func isHoldingType(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, isIdent := selector.X.(*ast.Ident)

	return isIdent && pkg.Name == "personaldata" && selector.Sel.Name == "Holding"
}

// holdingOnErasure is the OnErasure a Holding literal names, "" when none.
func holdingOnErasure(lit *ast.CompositeLit) string {
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, isIdent := pair.Key.(*ast.Ident); !isIdent || key.Name != "OnErasure" {
			continue
		}
		if selector, isSelector := pair.Value.(*ast.SelectorExpr); isSelector {
			return selector.Sel.Name
		}

		return "?"
	}

	return ""
}

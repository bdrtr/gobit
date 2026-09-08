package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// This file holds ONE rule over ONE published struct: EVERY FIELD OF THE
// SHIPPING QUOTE INPUT IS FILLED BY SOMETHING IN THIS TREE.
//
// # Why this struct and not every published struct
//
// The quote input is the surface a carrier prices on, and the thing it is most
// tempting to widen speculatively. A Turkish carrier's tariff is a function of
// the district and of the desi — the volumetric weight — and neither can be
// expressed here (ADR 0065). Adding the fields is a one-line edit; producing
// the numbers is a schema change in three modules. The tempting order is the
// wrong one, and the cost of getting it wrong is not a compile error but a
// carrier plugin that reads a zero and cannot tell "no parcel" from "this
// installation does not measure one".
//
// The repository already has the rehearsal: [coreprovider.QuoteInput] carries
// TotalWeight, the one field of this shape it already has, and the only
// production caller that fills the struct hands it a constant zero because the
// cart carries no weight. A field the tree cannot fill is a field that arrives
// dead, and this struct is PUBLISHED (ADR 0026), so it arrives dead and stays.
//
// # Why the producer side and not the reader side
//
// The reader of a quote input is a [coreprovider.FulfillmentProvider], and a
// provider is written by whoever integrates a carrier — outside this
// repository. A reader-side audit would go red today on CountryCode, which the
// one shipped provider does not look at and a real carrier certainly would. The
// tree can only be held to the half it owns: what it PRODUCES.
//
// # The population
//
// The fields come from the struct by reflection, the assignments come from a
// walk of the production trees. Neither is derived from the other and neither
// is a hand-written list, so a field added without a writer, and a writer
// deleted from under a field, are both findings rather than silence.

// quoteInputPackage is the import path the composite literals are qualified by.
const quoteInputPackage = modulePath + "/core/provider"

// quoteInputTypeName is the struct the audit walks.
const quoteInputTypeName = "QuoteInput"

// TestEveryQuoteInputFieldIsFilledByTheTree refuses a quote-input field that no
// production file writes.
//
// This is the capability-and-its-first-consumer rule (docs/gaps.md B7, B10)
// applied to a struct instead of to an event topic: a field nothing fills is a
// promise made to every embedder and kept by nobody, and on a published surface
// it cannot be taken back before 1.0.0.
//
// It does NOT freeze the struct, and that is the point. When the tree can
// produce a district and a parcel's volume, the fields land WITH the code that
// fills them and this audit stays green — which is exactly the order ADR 0065
// asks for.
func TestEveryQuoteInputFieldIsFilledByTheTree(t *testing.T) {
	t.Parallel()

	declared := quoteInputFields()
	require.NotEmpty(t, declared,
		"reflection found NO field on %s; the type has moved or emptied and this "+
			"audit is reading nothing.", quoteInputTypeName)

	filled, sites := quoteInputAssignments(t)
	require.NotEmpty(t, sites,
		"NOT ONE production file builds a %s with named fields.\n"+
			"The walk has gone BLIND: with no literal found, every field below counts "+
			"as unwritten and the audit would fail for the wrong reason — or, if the "+
			"struct were emptied too, pass having looked at nothing.",
		quoteInputTypeName)

	for _, field := range declared {
		assert.Contains(t, filled, field,
			"%s.%s is written by no production file; the literals found are in %s.\n"+
				"A field on a published provider contract that nothing fills reaches "+
				"every embedder as a promise and reaches every provider as a zero — and "+
				"a provider cannot tell that zero from a real one. Ship the field with "+
				"the code that produces it, or do not ship it (ADR 0065).",
			quoteInputTypeName, field, strings.Join(sites, ", "))
	}
}

// quoteInputFields returns the exported field names of the published struct.
//
// Reflection rather than a parse of the declaration: the compiler is the only
// reader that cannot be wrong about which fields the type has, and an embedded
// or renamed field cannot slip past it.
func quoteInputFields() []string {
	structType := reflect.TypeOf(coreprovider.QuoteInput{})

	out := make([]string, 0, structType.NumField())
	for i := range structType.NumField() {
		if field := structType.Field(i); field.IsExported() {
			out = append(out, field.Name)
		}
	}

	return out
}

// quoteInputAssignments walks the production trees and returns the field names
// assigned in every quote-input composite literal, plus the files holding one.
//
// A POSITIONAL literal is reported as a finding rather than counted. It would
// assign every field without naming one, so it would satisfy this audit while
// being the one spelling that breaks on the next field the struct gains.
func quoteInputAssignments(t *testing.T) (filled, sites []string) {
	t.Helper()

	for _, tree := range productionTrees {
		for _, path := range treeProductionFiles(t, tree) {
			names, keyed := quoteInputFileAssignments(t, path)
			if !keyed {
				continue
			}
			for _, name := range names {
				if !slices.Contains(filled, name) {
					filled = append(filled, name)
				}
			}
			sites = append(sites, short(path))
		}
	}

	return filled, sites
}

// quoteInputFileAssignments reads one file; keyed reports whether it holds a
// quote-input literal that names at least one field.
func quoteInputFileAssignments(t *testing.T, path string) (names []string, keyed bool) {
	t.Helper()

	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err, "%s could not be parsed", short(path))

	local := quoteInputLocalName(tree)
	if local == "" {
		return nil, false
	}

	ast.Inspect(tree, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok || !isQuoteInputType(lit.Type, local) {
			return true
		}
		for _, element := range lit.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				// One report per literal, not per element: a positional literal
				// is ONE defect, and six copies of the same line bury the
				// blindness message that follows it.
				t.Errorf("%s builds a %s with POSITIONAL fields.\n"+
					"On a published struct that is the one spelling that breaks when a "+
					"field is added, and it hides the addition from this audit: write "+
					"the field names.", short(path), quoteInputTypeName)
				break
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok {
				continue
			}
			names = append(names, key.Name)
			keyed = true
		}
		return true
	})

	return names, keyed
}

// quoteInputLocalName returns the name the file bound the provider package to,
// or the empty string when the file does not import it.
func quoteInputLocalName(tree *ast.File) string {
	for _, spec := range tree.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil || value != quoteInputPackage {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}

		return quoteInputPackage[strings.LastIndex(quoteInputPackage, "/")+1:]
	}

	return ""
}

// isQuoteInputType reports whether a composite literal's type is the quote
// input qualified by the given local package name.
func isQuoteInputType(expr ast.Expr, local string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != quoteInputTypeName {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)

	return ok && qualifier.Name == local
}

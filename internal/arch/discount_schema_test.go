package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file turns a rule that was kept in PROSE into a check.
//
// # The rule
//
// The promotion module answers the same question on two surfaces. The cart flow
// posts a discount request to its interop (`service/interop.go`) and an operator
// posts one to `POST /admin/v1/promotions/compute` (`api/admin.go`). Both godocs
// say the shapes must be IDENTICAL, and both say why: two schemas for one
// calculation means the request an operator tries in the panel can behave
// differently in the shop.
//
// # Why prose was not enough
//
// It was measured: ADR 0148 added one field — the line's membership lists — to the
// interop item and the admin item did NOT get it. Nothing failed. The operator's
// endpoint would have answered "this category rule discounts nothing" about a cart
// the shop discounts, which is the worst shape of wrong answer: it looks like a
// broken RULE and sends somebody to rewrite a rule that is correct.
//
// The same class as the language ledger's roots and the documentation scan's
// trees: a rule whose population grows while the rule stays a sentence.
//
// # What it compares
//
// The JSON field NAMES of three pairs of structs, read from the source. Not the
// Go types and not the order: the two sides deliberately differ in type where
// the surfaces differ (the admin body takes `at` as a *time.Time and the interop
// as a string), and a field's position is not part of a JSON contract.
//
// # What it cannot see
//
// Whether the two sides MEAN the same thing by a name. A field both structs call
// "lists" satisfies this gate even if one of them drops it on the floor — that is
// what the module's own tests are for, and the e2e discount tests are what prove
// the cart's copy reaches the engine.

// discountSchemaPair is one pair of structs that has to carry the same JSON
// field names.
type discountSchemaPair struct {
	// what names the pair in a failure message.
	what string
	// interop is the type name in internal/modules/promotion/service.
	interop string
	// admin is the type name in internal/modules/promotion/api.
	admin string
	// adminExtra are the field names the ADMIN body may carry that the interop
	// does not, each with the reason it is allowed to differ.
	adminExtra map[string]string
}

// discountSchemaPairs are the three request shapes of the discount calculation.
var discountSchemaPairs = []discountSchemaPair{
	{
		what:    "the discount request",
		interop: "interopRequest",
		admin:   "computeRequest",
	},
	{
		what:    "a cart line",
		interop: "interopRequestItem",
		admin:   "computeItemRequest",
	},
	{
		what:    "a shipping method",
		interop: "interopRequestShipping",
		admin:   "computeShippingRequest",
	},
}

// TestTheTwoDiscountRequestShapesCarryTheSameFields is the gate.
func TestTheTwoDiscountRequestShapesCarryTheSameFields(t *testing.T) {
	t.Parallel()

	interopFields := jsonFieldsOfPackage(t, filepath.Join(repoRoot, modulesDir, "promotion", "service"))
	adminFields := jsonFieldsOfPackage(t, filepath.Join(repoRoot, modulesDir, "promotion", "api"))

	for _, pair := range discountSchemaPairs {
		wanted, found := interopFields[pair.interop]
		require.Truef(t, found,
			"%s: the interop type %q was not found; if it was renamed this gate is now "+
				"comparing nothing", pair.what, pair.interop)
		require.NotEmpty(t, wanted, "%s: %q carries no json field at all", pair.what, pair.interop)

		got, found := adminFields[pair.admin]
		require.Truef(t, found,
			"%s: the admin type %q was not found; if it was renamed this gate is now "+
				"comparing nothing", pair.what, pair.admin)

		expected := slices.Clone(wanted)
		for name := range pair.adminExtra {
			expected = append(expected, name)
		}

		assert.ElementsMatchf(t, expected, got,
			"%s is shaped differently on the two surfaces.\n"+
				"interop (%s): %v\nadmin   (%s): %v\n"+
				"The cart flow posts to the interop and an operator posts the same "+
				"calculation to POST /admin/v1/promotions/compute. A field one of them "+
				"does not carry is a calculation that answers differently in the panel "+
				"than in the shop — and the operator reads that as a broken RULE, so they "+
				"go and rewrite a rule that is correct (ADR 0148 shipped exactly this and "+
				"nothing failed).\n"+
				"Add the field to both, or write it into adminExtra with the reason the "+
				"two surfaces differ.",
			pair.what, pair.interop, wanted, pair.admin, got)
	}
}

// jsonFieldsOfPackage reads every struct's json field names in a package.
//
// Test files are skipped, so a fixture struct cannot satisfy the gate. A field
// tagged "-" is skipped too: it is a field the JSON contract does not carry.
func jsonFieldsOfPackage(t *testing.T, dir string) map[string][]string {
	t.Helper()

	out := map[string][]string{}

	for _, file := range productionFiles(t, dir) {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoError(t, err, "%s could not be parsed", file)

		for _, decl := range parsed.Decls {
			generic, ok := decl.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}

			for _, spec := range generic.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}

				out[typeSpec.Name.Name] = jsonNamesOf(structType)
			}
		}
	}

	return out
}

// jsonNamesOf returns the json names of a struct's fields.
func jsonNamesOf(structType *ast.StructType) []string {
	names := make([]string, 0, len(structType.Fields.List))

	for _, field := range structType.Fields.List {
		if field.Tag == nil {
			continue
		}

		tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}

		names = append(names, name)
	}

	return names
}

package service_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// engraveLines adds count lines of one variant, each with its own words, so
// every line is new and none is a merge.
func engraveLines(ctx context.Context, t *testing.T, svc *service.Service, cartID, variant string, count int) {
	t.Helper()
	for i := range count {
		_, err := svc.AddLineItem(ctx, cartID, service.AddLineItemInput{
			VariantID: variant, Title: variant, Quantity: 1, UnitPrice: 1000,
			Properties: map[string]string{"Engraving": strconv.Itoa(i)},
		})
		require.NoError(t, err)
	}
}

// lineCount reads how many lines the cart holds.
func lineCount(ctx context.Context, t *testing.T, svc *service.Service, cartID string) int {
	t.Helper()
	detail, err := svc.GetCart(ctx, cartID)
	require.NoError(t, err)
	return len(detail.Items)
}

// TestAFullCartOpensNoLine is D154: the ceiling asks whether the line is NEW,
// which since ADR 0223 is the variant and its properties. A cart full of one
// variant under a hundred engravings refuses the same variant with a new
// engraving and another variant alike, and raises the quantity of a line
// whose engraving it already holds, spelled with other spaces.
func TestAFullCartOpensNoLine(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)
	engraveLines(ctx, t, svc, cart.ID, variantA, service.MaxLineItems)

	for name, in := range map[string]service.AddLineItemInput{
		"the same variant, new words": {
			VariantID: variantA, Title: variantA, Quantity: 1, UnitPrice: 1000,
			Properties: map[string]string{"Engraving": "new"},
		},
		"another variant": {VariantID: variantB, Title: variantB, Quantity: 1, UnitPrice: 1000},
	} {
		_, err := svc.AddLineItem(ctx, cart.ID, in)
		require.Error(t, err, name)
		assert.Equal(t, service.CodeLineLimit, errors.CodeOf(err), name)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), name)
		assert.Contains(t, err.Error(), strconv.Itoa(service.MaxLineItems), name)
	}
	assert.Equal(t, service.MaxLineItems, lineCount(ctx, t, svc, cart.ID))

	grown, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: variantA, Quantity: 2, UnitPrice: 1000,
		Properties: map[string]string{" Engraving ": "7 "},
	})
	require.NoError(t, err, "a line the cart holds is raised, not opened")
	assert.Equal(t, int64(3), grown.Quantity)
	assert.Equal(t, service.MaxLineItems, lineCount(ctx, t, svc, cart.ID))
}

// TestTheLastLineUnderTheCeilingOpens holds the place of the bound: one line
// below it a new line opens, and the next is refused.
func TestTheLastLineUnderTheCeilingOpens(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	cart := newCart(ctx, t, svc)
	engraveLines(ctx, t, svc, cart.ID, variantA, service.MaxLineItems-1)

	fill(ctx, t, svc, cart.ID, variantB, 1)
	assert.Equal(t, service.MaxLineItems, lineCount(ctx, t, svc, cart.ID))

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_C", Title: "variant_C", Quantity: 1, UnitPrice: 1000,
	})
	assert.Equal(t, service.CodeLineLimit, errors.CodeOf(err))
}

// TestAMergePastTheCeilingIsRefusedWhole holds the ceiling on the merge, the
// other path that opens a line: a fold that would open lines past it is
// refused, and the line it would have raised is not raised either.
func TestAMergePastTheCeilingIsRefusedWhole(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	engraveLines(ctx, t, svc, target.ID, variantA, service.MaxLineItems-1)
	engraveLines(ctx, t, svc, source.ID, variantA, 1) // the target holds these words
	fill(ctx, t, svc, source.ID, variantB, 1)
	fill(ctx, t, svc, source.ID, "variant_C", 1)

	_, err := svc.MergeCart(ctx, source.ID, target.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeLineLimit, errors.CodeOf(err))
	assert.Equal(t, service.MaxLineItems-1, lineCount(ctx, t, svc, target.ID))
	detail, err := svc.GetCart(ctx, target.ID)
	require.NoError(t, err)
	for _, item := range detail.Items {
		assert.Equal(t, int64(1), item.Quantity, "no line of the target moved")
	}
	assert.Equal(t, 3, lineCount(ctx, t, svc, source.ID), "and the source is whole")
}

// TestAMergeIntoAFullCartRaisesItsLines holds the other half: a fold whose
// every line the full target already holds opens nothing and is not refused.
func TestAMergeIntoAFullCartRaisesItsLines(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	target := newCart(ctx, t, svc)
	source := newCart(ctx, t, svc)
	engraveLines(ctx, t, svc, target.ID, variantA, service.MaxLineItems)
	engraveLines(ctx, t, svc, source.ID, variantA, 2)

	_, err := svc.MergeCart(ctx, source.ID, target.ID)

	require.NoError(t, err)
	detail, err := svc.GetCart(ctx, target.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, service.MaxLineItems)
	raised := map[string]int64{}
	for _, item := range detail.Items {
		if item.Quantity != 1 {
			raised[item.Properties["Engraving"]] = item.Quantity
		}
	}
	assert.Equal(t, map[string]int64{"0": 2, "1": 2}, raised, "the two engravings the target held are raised")
}

// TestEveryLineIsOpenedUnderTheCeiling derives the population of the ceiling
// from the package: every call that creates a line is inside openLine, so a
// third path that opens a line cannot be written past the ceiling the way the
// merge was (D154).
func TestEveryLineIsOpenedUnderTheCeiling(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	calls := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "CreateLineItem" {
					return true
				}
				calls++
				assert.Equal(t, "openLine", fn.Name.Name,
					"%s creates a line in %s, outside openLine; the line ceiling is not asked there",
					fset.Position(call.Pos()), fn.Name.Name)
				return true
			})
		}
	}
	require.Positive(t, calls, "the walk found no line creation at all; it is reading the package wrongly")
}

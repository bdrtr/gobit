package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A shop's cost is published only where it is declared (ADR 0401).
//
// A variant's cost and an order's margin are the shop's business. They reach
// the admin surface through types of their own because the storefront's types
// are shared: its product embeds the variant model, its order is the admin
// order's base, and a field added to either is on every storefront page. The
// compiler cannot see that, so this audit reads, in the production trees,
// generated files included:
//
//   - every name a struct field publishes to JSON: its json tag's name, or its
//     Go name when the tag gives none and the field is exported; a field tagged
//     json:"-" and an unexported one publish nothing;
//   - every named type of this module a publishing field holds, and every one
//     a field embeds, through pointers, slices, arrays and map values, so a type
//     that carries a cost makes its holder carry one;
//   - every name in the repository's GraphQL schemas, tracked or not yet, with
//     their descriptions and comments cut: gqlgen binds a schema type to a Go
//     type whose JSON names the schema does not have to share.
//
// A type carries a cost when a name it publishes contains one of [costWords],
// when it is one of [costValueTypes], or when it holds a type that carries one.
// Every type that carries a cost is named in [costPublishers] or
// [costValueTypes], with why; an anonymous struct never is, and no GraphQL name
// may contain a cost word. It reads declarations, not what a handler encodes:
// a cost put into a map or an `any` at run time is not seen.

// costWords are the words a published name carrying a cost or a margin
// contains.
var costWords = []string{"cost", "margin"}

// costValueTypes are the types whose values ARE costs although their names do
// not say so, by directory and type name, each with why.
var costValueTypes = map[string]string{
	"internal/modules/product/models.VariantCost": "a variant's unit cost in one currency; its names are " +
		"currency_code and amount",
}

// costPublishers are the types that may carry a cost, by directory and type
// name, each with why.
var costPublishers = map[string]string{
	// The admin surface.
	"internal/modules/product/api.variantCostsDTO":        "the admin read and write of a variant's costs",
	"internal/modules/product/api.setVariantCostsRequest": "the admin write of a variant's costs",
	"internal/modules/order/api.adminOrderDetailDTO":      "the admin order record, which shadows the storefront's lines",
	"internal/modules/order/api.adminLineItemDTO":         "the admin order's line",
	"internal/modules/order/api.adminOrderRowDTO":         "a row of the admin order list",
	"internal/modules/order/api.placedMarginDTO":          "the admin order's placed margin",
	// The panel's reads (ADR 0412), under order:read as on the admin API.
	"internal/modules/order.adminMargin":   "the order module's panel read of an order's placed margin",
	"internal/modules/order.adminLineCost": "the order module's panel read of a line's unit cost",
	"internal/adminui.placedMarginRecord":  "the panel's decoding of the order module's margin read",
	"internal/adminui.lineCostRecord":      "the panel's decoding of the order module's line cost read",
	"internal/adminui.marginView":          "an order's placed margin as the order list and page print it",
	"internal/adminui.orderCosts":          "the order page's margin and line costs",
	// The checkout's plan and the snapshot it places the order with.
	"internal/workflows/checkout.variantFacts":      "what the catalog said about a variant, in process",
	"internal/workflows/checkout.planLine":          "a line of the checkout's plan, kept in its execution record",
	"internal/workflows/checkout.checkoutPlan":      "the checkout's plan, kept in its execution record",
	"internal/workflows/checkout.orderSnapshotItem": "a line of the snapshot the checkout sends the order module",
	"internal/workflows/checkout.orderSnapshot":     "the snapshot the checkout sends the order module",
	// The order module's input, model and rows.
	"internal/modules/order/service.interopOrderItem": "a snapshot line the order is placed with, in process " +
		"and never served",
	"internal/modules/order/service.interopSnapshot":          "the snapshot the order is placed with",
	"internal/modules/order/service.CreateOrderItemInput":     "a line of the service's order input",
	"internal/modules/order/service.CreateOrderInput":         "the service's order input",
	"internal/modules/order/models.OrderLineItem":             "the order line model; the API converts it",
	"internal/modules/order/models.OrderDetail":               "the order model with its lines; the API converts it",
	"internal/modules/order/models.PlacedMargin":              "the placed margin model; the API converts it",
	"internal/modules/order/repository/orderdb.OrderLineItem": "sqlc's row of order_line_items",
	"internal/modules/order/repository/orderdb.CreateOrderLineItemParams": "sqlc's parameters of the " +
		"line insert",
	"internal/modules/order/repository/orderdb.PlacedMarginsOfOrdersRow": "sqlc's row of the margin query",
	"internal/modules/order/repository/orderdb.PlacedMarginsOfOrdersParams": "sqlc's parameters of the " +
		"margin query, whose CostLimit is the bound and no cost",
	// Not money.
	"internal/modules/auth.Options":         "BcryptCost, the password hash's work factor",
	"internal/modules/auth/service.Options": "BcryptCost, the password hash's work factor",
}

// costField is what one struct field publishes and holds.
type costField struct {
	// names are the JSON names it publishes; none for an embedded struct
	// whose fields are flattened into its holder.
	names []string
	// holds are the named types it holds or embeds, by "dir.Name".
	holds []string
}

// costStruct is one struct type as the audit reads it.
type costStruct struct {
	key    string
	fields []costField
}

// costStructs reads a parsed file's struct types. A named type's struct is
// keyed "dir.Name"; an anonymous struct nested in a field belongs to the
// field's owner, and one standing anywhere else is keyed "dir.anonymous".
func costStructs(dir string, file *ast.File) []costStruct {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasPrefix(path, modulePath+"/") {
			continue
		}
		rel := strings.TrimPrefix(path, modulePath+"/")
		local := filepath.Base(rel)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = rel
	}

	owned := map[*ast.StructType]bool{}
	var out []costStruct
	read := func(key string, st *ast.StructType) {
		s := costStruct{key: key}
		var walk func(st *ast.StructType)
		walk = func(st *ast.StructType) {
			owned[st] = true
			for _, field := range st.Fields.List {
				f, nested := costFieldOf(dir, imports, field)
				if nested != nil {
					walk(nested)
				}
				s.fields = append(s.fields, f)
			}
		}
		walk(st)
		out = append(out, s)
	}

	ast.Inspect(file, func(node ast.Node) bool {
		if spec, ok := node.(*ast.TypeSpec); ok {
			if st, isStruct := spec.Type.(*ast.StructType); isStruct {
				read(dir+"."+spec.Name.Name, st)
			}
		}
		return true
	})
	ast.Inspect(file, func(node ast.Node) bool {
		if st, ok := node.(*ast.StructType); ok && !owned[st] {
			read(dir+".anonymous", st)
		}
		return true
	})
	return out
}

// costFieldOf reads one field: the names encoding/json writes it under, and the
// named types it holds. An anonymous struct it holds is returned for its owner
// to read.
func costFieldOf(dir string, imports map[string]string, field *ast.Field) (costField, *ast.StructType) {
	var f costField
	tag := ""
	if field.Tag != nil {
		tag = reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("json")
	}
	// json:"-" is never written; json:"-," is written under the name "-".
	if tag == "-" {
		return f, nil
	}
	if name, _, _ := strings.Cut(tag, ","); name != "" {
		f.names = []string{name}
	} else {
		for _, ident := range field.Names {
			if ident.IsExported() {
				f.names = append(f.names, ident.Name)
			}
		}
	}
	// A named field that publishes no name is unexported, and encoding/json
	// writes nothing it holds; an embedded field publishes what it embeds.
	if len(field.Names) > 0 && len(f.names) == 0 {
		return f, nil
	}

	typ := field.Type
	for {
		switch t := typ.(type) {
		case *ast.StarExpr:
			typ = t.X
			continue
		case *ast.ArrayType:
			typ = t.Elt
			continue
		case *ast.MapType:
			typ = t.Value
			continue
		case *ast.ParenExpr:
			typ = t.X
			continue
		case *ast.IndexExpr:
			typ = t.X
			continue
		case *ast.IndexListExpr:
			typ = t.X
			continue
		case *ast.Ident:
			f.holds = append(f.holds, dir+"."+t.Name)
		case *ast.SelectorExpr:
			if pkg, ok := t.X.(*ast.Ident); ok {
				if rel, internal := imports[pkg.Name]; internal {
					f.holds = append(f.holds, rel+"."+t.Sel.Name)
				}
			}
		case *ast.StructType:
			return f, t
		}
		return f, nil
	}
}

// namesACost reports whether a published name contains a cost word.
func namesACost(name string) bool {
	lower := strings.ToLower(name)
	return slices.ContainsFunc(costWords, func(word string) bool { return strings.Contains(lower, word) })
}

// costCarriers reads the structs of the given files and returns, for every
// type that carries a cost, why: the name it publishes or the type it holds.
func costCarriers(structs []costStruct) map[string]string {
	why := map[string]string{}
	for key := range costValueTypes {
		why[key] = "it is a cost value"
	}
	for _, s := range structs {
		for _, f := range s.fields {
			for _, name := range f.names {
				if namesACost(name) {
					why[s.key] = "it publishes " + strconv.Quote(name)
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, s := range structs {
			if _, done := why[s.key]; done {
				continue
			}
			for _, f := range s.fields {
				for _, held := range f.holds {
					if _, carries := why[held]; carries {
						why[s.key] = "it holds " + held
						changed = true
					}
				}
			}
		}
	}
	return why
}

// productionStructs parses every Go file of the production trees, generated
// ones included, and returns their struct types.
func productionStructs(t *testing.T) []costStruct {
	t.Helper()

	fset := token.NewFileSet()
	var out []costStruct
	for _, tree := range productionTrees {
		for _, path := range treeFiles(t, tree) {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
			require.NoError(t, err, "%s could not be parsed", path)
			rel, err := filepath.Rel(repoRoot, filepath.Dir(path))
			require.NoError(t, err)
			out = append(out, costStructs(filepath.ToSlash(rel), file)...)
		}
	}
	return out
}

// TestACostIsPublishedOnlyWhereDeclared holds ADR 0401's boundary in the Go
// source: every type that carries a cost is named, with why, and every name
// still carries one, so a renamed type or a moved directory fails here rather
// than leaving an entry that allows nothing.
func TestACostIsPublishedOnlyWhereDeclared(t *testing.T) {
	t.Parallel()

	structs := productionStructs(t)
	declared := map[string]bool{}
	for _, s := range structs {
		declared[s.key] = true
	}
	require.Greater(t, len(declared), 1000, "only %d struct types were read; the scan has gone blind", len(declared))

	carriers := costCarriers(structs)
	for _, key := range slices.Sorted(maps.Keys(carriers)) {
		_, publisher := costPublishers[key]
		_, value := costValueTypes[key]
		assert.True(t, publisher || value,
			"%s carries a shop's cost or margin (%s), and no entry allows it.\n"+
				"The storefront's product embeds the variant model and its order is the admin "+
				"order's base; a cost goes on an admin type of its own (ADR 0401).", key, carriers[key])
	}
	for key := range costPublishers {
		_, carries := carriers[key]
		assert.True(t, carries,
			"%s is allowed to carry a cost and carries none; the type moved, was renamed or "+
				"lost the field, and the entry has to follow it", key)
	}
	for key := range costValueTypes {
		assert.True(t, declared[key], "%s is a cost value type and is declared nowhere; the entry "+
			"has to follow the type", key)
	}
}

// graphqlComments are what a GraphQL schema says in prose: block and line
// descriptions and comments. They are cut before the names are read.
var graphqlComments = regexp.MustCompile(`(?s)"""(.*?)"""|"(?:[^"\\\n]|\\.)*"|#[^\n]*`)

// graphqlName is a GraphQL name.
var graphqlName = regexp.MustCompile(`[_A-Za-z][_0-9A-Za-z]*`)

// graphqlCostNames returns every name in a schema, prose cut, that contains a
// cost word.
func graphqlCostNames(schema string) []string {
	var out []string
	for _, name := range graphqlName.FindAllString(graphqlComments.ReplaceAllString(schema, " "), -1) {
		if namesACost(name) {
			out = append(out, name)
		}
	}
	return out
}

// TestNoGraphQLSchemaNamesACost holds the boundary where Go's JSON names say
// nothing: gqlgen binds the storefront's Variant to service.StoreVariant, and a
// field the schema adds is served through a resolver. Every GraphQL schema of
// the repository, tracked or not yet, is read; none may name a cost or a
// margin, since the GraphQL surface is the storefront's.
func TestNoGraphQLSchemaNamesACost(t *testing.T) {
	t.Parallel()

	read := 0
	for _, rel := range slices.Sorted(maps.Keys(repositoryFiles(t))) {
		if !strings.HasSuffix(rel, ".graphqls") || slices.Contains(strings.Split(rel, "/"), "testdata") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		read++
		assert.Empty(t, graphqlCostNames(string(body)),
			"%s names a shop's cost or margin; the GraphQL surface is the storefront's (ADR 0401)", rel)
	}
	require.Positive(t, read, "no GraphQL schema was read; the scan has gone blind")
}

// TestTheCostScanIsNotBlind feeds the scans what each must find: a tagged and
// an untagged cost on a storefront type, a cost type held and embedded by one,
// an anonymous struct, and a schema field among descriptions that say "cost".
func TestTheCostScanIsNotBlind(t *testing.T) {
	t.Parallel()

	const src = `package api

import "github.com/bdrtr/gobit/internal/modules/product/models"

type lineItemDTO struct {
	Title    string ` + "`json:\"title\"`" + `
	UnitCost *int64 ` + "`json:\"unit_cost,omitempty\"`" + `
}

type emptyNameDTO struct {
	UnitCost *int64 ` + "`json:\",omitempty\"`" + `
}

type untaggedDTO struct {
	PlacedMargin int64
	hiddenCost   int64
	Skipped      int64 ` + "`json:\"-\"`" + `
}

type quietDTO struct {
	hiddenCost int64
	Margin     int64 ` + "`json:\"-\"`" + `
}

type rowDTO struct {
	Prices []models.VariantCost ` + "`json:\"prices\"`" + `
}

type pageDTO struct {
	*rowDTO
}

type listDTO struct {
	Rows map[string][]*pageDTO ` + "`json:\"rows\"`" + `
}

var row = struct {
	Margin int64 ` + "`json:\"placed_margin\"`" + `
}{}
`
	file, err := parser.ParseFile(token.NewFileSet(), "planted.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	carriers := costCarriers(costStructs("x/api", file))
	assert.Equal(t, map[string]string{
		"x/api.lineItemDTO":  `it publishes "unit_cost"`,
		"x/api.emptyNameDTO": `it publishes "UnitCost"`,
		"x/api.untaggedDTO":  `it publishes "PlacedMargin"`,
		"x/api.rowDTO":       "it holds internal/modules/product/models.VariantCost",
		"x/api.pageDTO":      "it holds x/api.rowDTO",
		"x/api.listDTO":      "it holds x/api.pageDTO",
		"x/api.anonymous":    `it publishes "placed_margin"`,
		"internal/modules/product/models.VariantCost": "it is a cost value",
	}, carriers, "quietDTO publishes neither its unexported field nor its json:\"-\" one")

	const schema = `"""
What a variant costs is not said here: the cost of a query is.
"""
type Variant {
  "a margin note"
  id: ID! # the cost of a lookup
  unitCost: Int
}
`
	assert.Equal(t, []string{"unitCost"}, graphqlCostNames(schema))
}

// TestTheCostNamesAreCaseFree pins namesACost to every spelling a JSON or Go
// name takes.
func TestTheCostNamesAreCaseFree(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"unit_cost", "UnitCost", "placedMargin", "MARGIN"} {
		assert.True(t, namesACost(name), name)
	}
	assert.False(t, namesACost("amount"))
}

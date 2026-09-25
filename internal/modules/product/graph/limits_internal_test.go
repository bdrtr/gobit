package graph

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// unpricedLists are the list fields of the schema that deliberately carry no
// cost function of their own, each with the reason.
var unpricedLists = map[string]string{
	"ProductList.items": "the page size is priced on Query.products, from its limit argument; " +
		"pricing the items too would charge the square of the page",
}

// TestEveryListFieldIsPriced derives its population from the schema.
//
// A list field without a cost function gets gqlgen's default, one plus its
// children: a list of fifty is priced as one element. The cost model's comment
// said every nested list is priced by the estimate, and nothing checked it;
// ADR 0184 added the first list that is a read of its own, and forgetting its
// line would have made the most expensive field in the schema look like the
// cheapest. The population is every list-typed field of every object type the
// generated schema has, so a list added tomorrow is inside the rule.
func TestEveryListFieldIsPriced(t *testing.T) {
	t.Parallel()

	var costs ComplexityRoot
	complexityCosts(&costs)
	root := reflect.ValueOf(costs)

	lists := 0
	for typeName, definition := range NewExecutableSchema(Config{}).Schema().Types {
		if definition.Kind != ast.Object || strings.HasPrefix(typeName, "__") {
			continue
		}
		for _, field := range definition.Fields {
			if strings.HasPrefix(field.Name, "__") || field.Type.Elem == nil {
				continue
			}
			lists++

			key := typeName + "." + field.Name
			if reason, unpriced := unpricedLists[key]; unpriced {
				assert.NotEmpty(t, reason)

				continue
			}

			typeCosts := root.FieldByNameFunc(sameGoName(typeName))
			require.True(t, typeCosts.IsValid(), "ComplexityRoot has no entry for the type %s", typeName)
			cost := typeCosts.FieldByNameFunc(sameGoName(field.Name))
			require.True(t, cost.IsValid(), "ComplexityRoot has no entry for %s", key)
			assert.False(t, cost.IsNil(),
				"%s is a list and complexityCosts gives it no cost, so it is priced as ONE "+
					"element; price it, or write the reason into unpricedLists", key)
		}
	}
	assert.Greater(t, lists, len(unpricedLists),
		"the walk found no priced list at all; it is reading the schema wrongly")

	for key := range unpricedLists {
		typeName, fieldName, _ := strings.Cut(key, ".")
		definition := NewExecutableSchema(Config{}).Schema().Types[typeName]
		require.NotNil(t, definition, "unpricedLists names the type %s, which the schema does not have", typeName)
		assert.NotNil(t, definition.Fields.ForName(fieldName),
			"unpricedLists names %s, which the schema does not have", key)
	}
}

// sameGoName matches a schema name to the Go name gqlgen generated for it:
// the same letters, whatever the case.
func sameGoName(schemaName string) func(string) bool {
	return func(goName string) bool { return strings.EqualFold(goName, schemaName) }
}

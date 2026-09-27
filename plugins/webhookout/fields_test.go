package webhookout

import (
	"go/ast"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file checks [TopicFields] against the publishers, as topics_test.go
// checks [ForwardedTopics] (ADR 0218).
//
// A receiver's filter and field list name payload fields, and a name the
// payload no longer carries is the quiet failure again: a filter on it matches
// nothing and the receiver hears nothing, and a field list naming it sends an
// emptier body. So the list is read out of the source: for every publish whose
// topic resolves, the keys of the payload it hands the bus.
//
// The payload is resolved in the two shapes the repository uses: a call to a
// function of the same package that returns a map literal, and a local variable
// built from a map literal and grown by index assignments in the publishing
// function. A publish whose topic resolves and whose payload does not is a
// failure, for the reason unresolvable names are: a payload this test cannot
// read is one it cannot check.

// neverCarried are the keys a payload builder can add and a topic's publisher
// never has it add, which a static reading cannot tell. Each entry has to be
// something the census still reads; one it no longer does is dead and fails.
var neverCarried = map[string]map[string]string{
	topicProductDeleted: {
		"status": "the product service publishes the deletion with an empty status, and " +
			"publishProductEvent adds the key only for a status that is not empty",
	},
}

// TestTheTopicFieldsAreEveryPayloadsFields is the gate.
func TestTheTopicFieldsAreEveryPayloadsFields(t *testing.T) {
	t.Parallel()

	published := payloadFields(t)
	require.NotEmpty(t, published, "no payload was read; the census has gone blind")

	for _, topic := range ForwardedTopics {
		want, ok := published[topic]
		require.Truef(t, ok, "no publish of %q had a payload this census could read", topic)
		var receivable []string
		for _, field := range want {
			_, redacted := redactedFields[field]
			_, never := neverCarried[topic][field]
			if !redacted && !never {
				receivable = append(receivable, field)
			}
		}
		for field := range neverCarried[topic] {
			assert.Containsf(t, want, field,
				"neverCarried excuses %s's %q, which the census no longer reads; delete the entry", topic, field)
		}
		assert.Equalf(t, receivable, TopicFields[topic],
			"%s carries %v (redacted fields left out); TopicFields says %v", topic, receivable, TopicFields[topic])
	}
	for topic := range TopicFields {
		assert.Containsf(t, ForwardedTopics, topic, "TopicFields names %q, which is not forwarded", topic)
	}
}

// payloadFields maps each resolved topic to the sorted keys of its payloads.
func payloadFields(t *testing.T) map[string][]string {
	t.Helper()

	files := parseProduction(t)
	constants := collectConstants(files)
	fields := map[string]map[string]bool{}

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
				return true
			}
			topics := resolveName(f, enclosing, name, constants, files)
			if len(topics) == 0 {
				return true
			}
			data, found := fieldValue(literal, "Data")
			require.Truef(t, found, "%s: a publish of %v carries no Data", f.path, topics)
			keys, resolved := payloadKeys(f, enclosing, data, constants, files)
			require.Truef(t, resolved, "%s: the payload of %v could not be read statically", f.path, topics)
			for _, topic := range topics {
				if fields[topic] == nil {
					fields[topic] = map[string]bool{}
				}
				for _, key := range keys {
					fields[topic][key] = true
				}
			}

			return true
		})
	}

	out := map[string][]string{}
	for topic, set := range fields {
		for key := range set {
			out[topic] = append(out[topic], key)
		}
		slices.Sort(out[topic])
	}

	return out
}

// payloadKeys reads the keys a Data expression carries.
func payloadKeys(
	f *censusFile, enclosing *ast.FuncDecl, data ast.Expr, constants map[string]string, files []*censusFile,
) ([]string, bool) {
	switch value := data.(type) {
	case *ast.CallExpr:
		builder, ok := value.Fun.(*ast.Ident)
		if !ok {
			return nil, false
		}
		for _, candidate := range files {
			if candidate.pkgDir != f.pkgDir {
				continue
			}
			for _, decl := range candidate.tree.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == builder.Name {
					return mapKeys(candidate, fn.Body, "", constants)
				}
			}
		}

		return nil, false
	case *ast.Ident:
		if enclosing == nil {
			return nil, false
		}

		return mapKeys(f, enclosing.Body, value.Name, constants)
	default:
		return nil, false
	}
}

// mapKeys collects the keys of the map literals in body — every one, or only
// the one assigned to variable — and of the index assignments to variable.
func mapKeys(f *censusFile, body *ast.BlockStmt, variable string, constants map[string]string) ([]string, bool) {
	var keys []string
	resolved := true
	add := func(expr ast.Expr) {
		key, ok := stringConstant(f, expr, constants)
		if !ok {
			resolved = false

			return
		}
		keys = append(keys, key)
	}
	literalKeys := func(literal *ast.CompositeLit) {
		for _, element := range literal.Elts {
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				add(pair.Key)
			}
		}
	}
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ReturnStmt:
			if variable != "" {
				return true
			}
			for _, result := range n.Results {
				if literal, ok := result.(*ast.CompositeLit); ok && isStringMap(literal) {
					literalKeys(literal)
					found = true
				}
			}
		case *ast.AssignStmt:
			if variable == "" {
				return true
			}
			for i, lhs := range n.Lhs {
				switch target := lhs.(type) {
				case *ast.Ident:
					if target.Name != variable || i >= len(n.Rhs) {
						continue
					}
					if literal, ok := n.Rhs[i].(*ast.CompositeLit); ok && isStringMap(literal) {
						literalKeys(literal)
						found = true
					}
				case *ast.IndexExpr:
					if ident, ok := target.X.(*ast.Ident); ok && ident.Name == variable {
						add(target.Index)
					}
				}
			}
		}

		return true
	})

	return keys, found && resolved
}

// isStringMap reports whether a literal is a map keyed by strings.
func isStringMap(literal *ast.CompositeLit) bool {
	mapType, ok := literal.Type.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := mapType.Key.(*ast.Ident)

	return ok && key.Name == "string"
}

// stringConstant resolves a key expression: a literal, or a constant of the
// file's package or of an imported one.
func stringConstant(f *censusFile, expr ast.Expr, constants map[string]string) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(value.Value)

		return text, err == nil
	case *ast.Ident:
		text, ok := constants[f.pkgDir+"."+value.Name]

		return text, ok
	case *ast.SelectorExpr:
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		text, ok := constants[strings.TrimPrefix(f.imports[pkg.Name], modulePath+"/")+"."+value.Sel.Name]

		return text, ok
	default:
		return "", false
	}
}

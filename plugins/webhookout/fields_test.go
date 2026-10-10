package webhookout

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

// eventTableDoc is the document whose payload table an integrator reads.
const eventTableDoc = "docs/extending.md"

// eventTableFloor is the fewest rows the table could have and still be read.
//
// Under the live count on purpose, as the published-names floor is: it is the
// line below which the parse has plainly stopped working, not a second copy of
// the number.
const eventTableFloor = 5

// The table's shape: its header, the separator under it, and a topic or a
// field in backticks.
var (
	eventTableHeader    = regexp.MustCompile(`^\|\s*Event\s*\|\s*Payload\s*\|\s*$`)
	eventTableSeparator = regexp.MustCompile(`^\|\s*:?-+:?\s*\|\s*:?-+:?\s*\|\s*$`)
	backticked          = regexp.MustCompile("`([^`]+)`")
)

// TestTheEventTableIsThePublishedPayloads holds docs/extending.md's payload
// table to the publishers, in both directions (ADR 0435).
//
// The table is what an integrator reads to learn what each topic carries, and
// until this test nothing read it: it fell behind once (D150) and was put right
// by hand. It is compared with the census [TestTheTopicFieldsAreEveryPayloadsFields]
// runs — the WHOLE bus payload, customer_id included, since the table describes
// the bus and not a webhook body, and less only what [neverCarried] excuses —
// so a field a publisher gains without a row saying so fails here, and so does
// a field the table names that no publisher writes.
//
// The parse is the fragile half, so it is guarded, and a row it cannot read is
// a failure rather than a row skipped: the table has to be found under its own
// header and have more rows than a floor, every row has to split into its two
// cells, a topic may stand on one row only, and every forwarded topic has to
// be on one. Everything in backticks in a payload cell counts as a field the
// table names, a parenthesised note included, so a note cannot name a field
// past the gate.
func TestTheEventTableIsThePublishedPayloads(t *testing.T) {
	t.Parallel()

	table := eventTable(t)
	require.GreaterOrEqualf(t, len(table), eventTableFloor,
		"%s's payload table has %d topics; the parse has gone blind", eventTableDoc, len(table))

	published := payloadFields(t)
	for _, topic := range ForwardedTopics {
		written, ok := table[topic]
		if !assert.Truef(t, ok, "%s's payload table has no row for %q", eventTableDoc, topic) {
			continue
		}
		var carried []string
		for _, field := range published[topic] {
			if _, never := neverCarried[topic][field]; !never {
				carried = append(carried, field)
			}
		}
		assert.ElementsMatchf(t, carried, written,
			"%s carries %v and %s's table says %v", topic, carried, eventTableDoc, written)
	}
	for topic := range table {
		assert.Containsf(t, ForwardedTopics, topic,
			"%s's payload table has a row for %q, which no publisher writes", eventTableDoc, topic)
	}
}

// eventTable reads the payload table: each topic in a row's first cell, to the
// fields its second cell names in backticks.
func eventTable(t *testing.T) map[string][]string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot, eventTableDoc))
	require.NoError(t, err)

	return parseEventTable(t, string(raw))
}

// parseEventTable is [eventTable] over a document's text, apart so the rules
// it refuses by can be tried on tables the real document does not hold.
func parseEventTable(t require.TestingT, document string) map[string][]string {
	if helper, ok := t.(interface{ Helper() }); ok {
		helper.Helper()
	}

	table := map[string][]string{}
	inTable := false
	for _, line := range strings.Split(document, "\n") {
		if eventTableHeader.MatchString(line) {
			inTable = true

			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		if eventTableSeparator.MatchString(line) {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		require.Lenf(t, cells, 2, "%s: the payload table row %q does not split into an event "+
			"and a payload; a row this test cannot read is a row it cannot check", eventTableDoc, line)
		topics := backticked.FindAllStringSubmatch(cells[0], -1)
		beside := strings.NewReplacer("/", "", " ", "").Replace(backticked.ReplaceAllString(cells[0], ""))
		require.Truef(t, len(topics) > 0 && beside == "", "%s: the payload table row %q does not name "+
			"its topics in backticks, and nothing else, in its first cell; a reader takes it for "+
			"something, and this test would compare nothing", eventTableDoc, line)
		var fields []string
		for _, match := range backticked.FindAllStringSubmatch(cells[1], -1) {
			fields = append(fields, match[1])
		}
		for _, match := range topics {
			_, twice := table[match[1]]
			require.Falsef(t, twice, "%s's payload table has %q on two rows; a reader takes the "+
				"first, and this test would compare only one of them", eventTableDoc, match[1])
			table[match[1]] = fields
		}
	}
	require.Truef(t, inTable, "no | Event | Payload | table was found in %s", eventTableDoc)

	return table
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
			require.Truef(t, resolved, "%s, %s: %s", f.path, siteName(enclosing), unreadPayload(topics))
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

// payloadEntry is one key a payload carries and the expression giving its value.
type payloadEntry struct {
	// file is the file the entry is written in; its package's constants
	// resolve the key, and its package's types give the value's.
	file *censusFile
	// key is the key's expression.
	key ast.Expr
	// value is the value's expression, nil when the assignment that sets it
	// does not pair one expression with it.
	value ast.Expr
}

// payloadEntries reads the entries a Data expression carries.
//
// It is the one reading of a payload's shape, so the keys the census checks and
// the values [TestEveryPayloadValueIsAString] checks come from the same walk and
// cannot part.
func payloadEntries(
	f *censusFile, enclosing *ast.FuncDecl, data ast.Expr, files []*censusFile,
) ([]payloadEntry, bool) {
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
					return mapEntries(candidate, fn.Body, "")
				}
			}
		}

		return nil, false
	case *ast.Ident:
		if enclosing == nil {
			return nil, false
		}

		return mapEntries(f, enclosing.Body, value.Name)
	default:
		return nil, false
	}
}

// payloadKeys reads the keys a Data expression carries.
func payloadKeys(
	f *censusFile, enclosing *ast.FuncDecl, data ast.Expr, constants map[string]string, files []*censusFile,
) ([]string, bool) {
	entries, found := payloadEntries(f, enclosing, data, files)
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		key, ok := stringConstant(entry.file, entry.key, constants)
		if !ok {
			return nil, false
		}
		keys = append(keys, key)
	}

	return keys, found
}

// mapEntries collects the entries of a payload: in a builder (variable empty),
// of the map literal each return hands back; in a publishing function, of the
// literal assigned to variable and of the index assignments to it.
//
// It FAILS CLOSED. A builder with a return that is not a map literal, or a
// variable used in any way but those three — defined from a literal, grown by
// an index assignment, handed to the event as its Data — reports the payload as
// unread, because a reassignment from a helper, a maps.Copy or a branch that
// grows a map before returning it would otherwise be read as the literal alone
// and every key and value it adds would pass unchecked.
func mapEntries(f *censusFile, body *ast.BlockStmt, variable string) ([]payloadEntry, bool) {
	if variable == "" {
		return builderEntries(f, body)
	}

	var entries []payloadEntry
	allowed := map[*ast.Ident]bool{}
	defined := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				switch target := lhs.(type) {
				case *ast.Ident:
					if target.Name != variable || len(n.Rhs) != len(n.Lhs) {
						continue
					}
					if literal, ok := n.Rhs[i].(*ast.CompositeLit); ok && isStringMap(literal) {
						allowed[target] = true
						defined = true
						for _, element := range literal.Elts {
							if pair, ok := element.(*ast.KeyValueExpr); ok {
								entries = append(entries, payloadEntry{file: f, key: pair.Key, value: pair.Value})
							}
						}
					}
				case *ast.IndexExpr:
					if ident, ok := target.X.(*ast.Ident); ok && ident.Name == variable && n.Tok == token.ASSIGN {
						allowed[ident] = true
						entry := payloadEntry{file: f, key: target.Index}
						if len(n.Rhs) == len(n.Lhs) {
							entry.value = n.Rhs[i]
						}
						entries = append(entries, entry)
					}
				}
			}
		case *ast.CompositeLit:
			if !isEventLiteral(f, n) {
				return true
			}
			if data, ok := fieldValue(n, "Data"); ok {
				if ident, ok := data.(*ast.Ident); ok && ident.Name == variable {
					allowed[ident] = true
				}
			}
		}

		return true
	})

	readable := defined
	ast.Inspect(body, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == variable && !allowed[ident] {
			readable = false
		}

		return readable
	})

	return entries, readable
}

// builderEntries reads a builder: every return in its own body hands back one
// map literal, and the entries are those literals'.
func builderEntries(f *censusFile, body *ast.BlockStmt) ([]payloadEntry, bool) {
	var entries []payloadEntry
	returns, literals := 0, 0
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncLit:
			// A closure's return is not the builder's.
			return false
		case *ast.ReturnStmt:
			returns++
			if len(n.Results) != 1 {
				return true
			}
			literal, ok := n.Results[0].(*ast.CompositeLit)
			if !ok || !isStringMap(literal) {
				return true
			}
			literals++
			for _, element := range literal.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					entries = append(entries, payloadEntry{file: f, key: pair.Key, value: pair.Value})
				}
			}
		}

		return true
	})

	return entries, returns > 0 && returns == literals
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

// failureRecorder is a require.TestingT that records a failure and stops the
// goroutine it runs in, as the testing package's FailNow does.
type failureRecorder struct {
	failed   bool
	messages []string
}

// Errorf records the failure.
func (r *failureRecorder) Errorf(format string, args ...any) {
	r.failed = true
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// FailNow records the failure and ends the goroutine.
func (r *failureRecorder) FailNow() {
	r.failed = true
	runtime.Goexit()
}

// TestTheEventTableParseRefusesWhatItCannotRead tries [parseEventTable] on the
// tables the real document does not hold: each is a way the table could say
// something the gate would otherwise not compare.
func TestTheEventTableParseRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	const head = "| Event | Payload |\n|---|---|\n"
	parse := func(document string) (map[string][]string, *failureRecorder) {
		recorder := &failureRecorder{}
		var table map[string][]string
		done := make(chan struct{})
		go func() {
			defer close(done)
			table = parseEventTable(recorder, document)
		}()
		<-done

		return table, recorder
	}

	table, recorder := parse(head + "| `order.placed` / `order.canceled` | `order_id`, `return_id` (empty, else `x_note`) |\n\nprose\n")
	require.False(t, recorder.failed, "a well-formed table was refused: %v", recorder.messages)
	assert.Equal(t, map[string][]string{
		"order.placed":   {"order_id", "return_id", "x_note"},
		"order.canceled": {"order_id", "return_id", "x_note"},
	}, table, "a field named inside a note is a field the table names")

	_, recorder = parse(head + "| `order.placed` | `order_id`, `customer_email` |\n| `order.placed` | `order_id` |\n")
	assert.True(t, recorder.failed, "a topic on two rows was read; the gate would compare only the last")

	_, recorder = parse(head + "| `order.placed` | `order_id` \\| `total` |\n")
	assert.True(t, recorder.failed, "a row that does not split into two cells was skipped rather than refused")

	for name, row := range map[string]string{
		"an empty first cell":       "|  | `customer_email`, `shipping_address` |",
		"a topic without backticks": "| order.shipped | `order_id`, `tracking_number` |",
		"a dash for a topic":        "| - | `customer_email` |",
		"words beside the topic":    "| `order.placed` (soon) | `order_id` |",
	} {
		_, recorder = parse(head + "| `order.placed` | `order_id` |\n" + row + "\n")
		assert.True(t, recorder.failed, "%s was skipped rather than refused", name)
	}
}

// unreadPayload is the failure for a publication whose payload the census
// cannot read.
func unreadPayload(topics []string) string {
	return fmt.Sprintf("the payload of %v is not in a shape this census reads — a map literal "+
		"every return of a same-package function hands back, or a local map defined from a "+
		"literal, grown only by index assignments and handed to the event — so its keys and "+
		"values cannot be checked; write it in one of the two", topics)
}

// siteName names the function a publication is written in, for a failure.
func siteName(enclosing *ast.FuncDecl) string {
	if enclosing == nil {
		return "(no function)"
	}

	return enclosing.Name.Name
}

// TestAPayloadThisCensusCannotReadIsRefused tries [mapEntries] on the shapes a
// publisher could write tomorrow: the two it reads, and three it used to read
// as their literal alone — a map reassigned from a helper, a map grown by
// maps.Copy, and a builder whose second branch grows a map before returning
// it. Each of the three would add a key and a value no gate saw.
func TestAPayloadThisCensusCannotReadIsRefused(t *testing.T) {
	t.Parallel()

	const source = `package probe

import (
	"maps"

	"github.com/bdrtr/gobit/core/eventbus"
)

func literal(n string) map[string]any {
	return map[string]any{"order_id": n}
}

func grown(status string) {
	data := map[string]any{"order_id": "ord_1"}
	if status != "" {
		data["status"] = status
	}
	publish(eventbus.Event{Name: "order.placed", Data: data})
}

func reassigned(n int) {
	data := map[string]any{"order_id": "ord_1"}
	data = widen(data, n)
	publish(eventbus.Event{Name: "order.placed", Data: data})
}

func copied(n int) {
	data := map[string]any{"order_id": "ord_1"}
	maps.Copy(data, map[string]any{"item_count": n})
	publish(eventbus.Event{Name: "order.placed", Data: data})
}

func branched(n int, early bool) map[string]any {
	if early {
		return map[string]any{"order_id": "ord_1"}
	}
	m := map[string]any{"order_id": "ord_1"}
	m["item_count"] = n
	return m
}
`
	tree, err := parser.ParseFile(token.NewFileSet(), "probe.go", source, parser.SkipObjectResolution)
	require.NoError(t, err)
	f := censusFileOf("probe", "probe.go", tree)

	for _, tc := range []struct {
		function, variable string
		readable           bool
	}{
		{"literal", "", true},
		{"grown", "data", true},
		{"reassigned", "data", false},
		{"copied", "data", false},
		{"branched", "", false},
	} {
		var body *ast.BlockStmt
		for _, decl := range tree.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == tc.function {
				body = fn.Body
			}
		}
		require.NotNil(t, body, tc.function)

		_, readable := mapEntries(f, body, tc.variable)
		assert.Equalf(t, tc.readable, readable, "%s: read as readable=%v", tc.function, readable)
	}
}

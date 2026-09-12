package arch_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An event that travels two ways has ONE payload builder.
//
// The outbox row is the guarantee and the direct publish is the speed, and both
// carry the SAME event id — that identity is what makes the two deliveries one
// event. It also means the two bodies must be the same body: built twice by
// hand, one copy can gain a field while the other does not, and a subscriber
// then sees a different event depending on which path delivered it.
//
// The rule was written down when the payment module was built and it named the
// order module as the place that broke it. It stayed prose, the order module
// stayed broken, and nothing compared the two literals until this gate existed
// (D109).

// outboxWriter is the method that puts an event in the outbox.
const outboxWriter = "WriteOutboxEvent"

// eventLiteral is the type whose Data field carries a direct publish's payload.
const eventLiteral = "eventbus.Event"

// payloadSite is one place a topic's body is handed over.
type payloadSite struct {
	// payload is the expression as it reads in the source.
	payload string
	// isCall says whether the expression is a function call rather than a
	// literal or a variable.
	isCall bool
	// where is file:line, for a failure a reader can open.
	where string
}

func TestATopicPublishedTwoWaysHasOnePayloadBuilder(t *testing.T) {
	fset := token.NewFileSet()
	outbox := map[string][]payloadSite{}
	direct := map[string][]payloadSite{}

	for _, path := range productionGoFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		require.NoErrorf(t, err, "%s could not be parsed", path)

		// The topic is keyed WITH the package path: two modules may both call a
		// variable "topic" and they are not the same topic.
		scope := filepath.Dir(path) + "#"

		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CallExpr:
				topic, site, found := outboxCall(fset, n)
				if found {
					outbox[scope+topic] = append(outbox[scope+topic], site)
				}
			case *ast.CompositeLit:
				topic, site, found := publishLiteral(fset, n)
				if found {
					direct[scope+topic] = append(direct[scope+topic], site)
				}
			}

			return true
		})
	}

	require.NotEmpty(t, outbox,
		"no outbox write was found at all; this audit has stopped seeing its population")

	both := 0
	for topic, rows := range outbox {
		published, alsoDirect := direct[topic]
		if !alsoDirect {
			// A topic that only reaches the outbox has one body by
			// construction, and there is nothing for a second copy to disagree
			// with.
			continue
		}
		both++

		sites := append(append([]payloadSite{}, rows...), published...)
		for _, site := range sites {
			assert.Truef(t, site.isCall,
				"%s hands %s a payload that is not a builder call (%s). Hand the builder's "+
					"CALL to both sites: the two bodies carry one event id, so they have to be "+
					"comparable by reading them, and a literal or a local variable at each "+
					"site is not. The strictness is deliberate — resolving a variable back to "+
					"its assignment would put a second mechanism between this gate and the "+
					"thing it checks", site.where, shortTopic(topic), site.payload)
		}
		for _, site := range sites[1:] {
			assert.Equalf(t, sites[0].payload, site.payload,
				"%s and %s give %s different payload expressions. Both deliveries are the "+
					"SAME event id, so a subscriber would see a different body depending on "+
					"whether the fast path or the relay reached it",
				sites[0].where, site.where, shortTopic(topic))
		}
	}

	t.Logf("topics traveling both ways: %d", both)
	assert.GreaterOrEqualf(t, both, 5, "only %d topics were found to travel BOTH ways, and the "+
		"tree had five when this gate was written. A smaller number means the audit stopped "+
		"matching the two sites to each other and is now passing by looking at nothing", both)
}

// outboxCall reports the topic and payload of a WriteOutboxEvent call.
func outboxCall(fset *token.FileSet, call *ast.CallExpr) (topic string, site payloadSite, found bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != outboxWriter {
		return "", payloadSite{}, false
	}
	// (ctx, id, name, data): the payload is last and the topic is before it.
	if len(call.Args) != 4 {
		return "", payloadSite{}, false
	}

	return render(fset, call.Args[2]), siteOf(fset, call.Args[3]), true
}

// publishLiteral reports the topic and payload of an eventbus.Event literal.
func publishLiteral(fset *token.FileSet, lit *ast.CompositeLit) (topic string, site payloadSite, found bool) {
	if render(fset, lit.Type) != eventLiteral {
		return "", payloadSite{}, false
	}

	var data ast.Expr
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		switch render(fset, pair.Key) {
		case "Name":
			topic = render(fset, pair.Value)
		case "Data":
			data = pair.Value
		}
	}
	if topic == "" || data == nil {
		return "", payloadSite{}, false
	}

	return topic, siteOf(fset, data), true
}

// siteOf describes one payload expression.
func siteOf(fset *token.FileSet, expr ast.Expr) payloadSite {
	_, isCall := expr.(*ast.CallExpr)
	position := fset.Position(expr.Pos())

	return payloadSite{
		payload: render(fset, expr),
		isCall:  isCall,
		where:   filepath.Base(position.Filename) + ":" + strconv.Itoa(position.Line),
	}
}

// render prints an expression the way it reads in the source.
func render(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return ""
	}

	return strings.Join(strings.Fields(buf.String()), " ")
}

// shortTopic drops the package path a topic key carries for scoping.
func shortTopic(key string) string {
	_, topic, _ := strings.Cut(key, "#")

	return topic
}

// productionGoFiles lists every non-test Go file in the repository.
//
// The population is walked rather than listed: a new module, plugin or example
// that writes an outbox row is audited the day it is written, which a
// hand-written directory list would not do.
func productionGoFiles(t *testing.T) []string {
	t.Helper()

	root := filepath.Join("..", "..")
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "testdata", "bin":
				return filepath.SkipDir
			}

			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}

		return nil
	})
	require.NoError(t, err, "the tree could not be walked")
	require.NotEmpty(t, files, "no production Go file was found")

	return files
}

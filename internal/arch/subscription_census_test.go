package arch_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The census both event gates count from (D276, ADR 0437) is checked here on
// trees built for it, because the production tree holds no topic that only a
// relay shows: a floor nothing in the tree trips is a floor a deletion would
// take away unnoticed.

// censusRecorder keeps what the census reported.
type censusRecorder struct {
	errors []string
}

// Helper satisfies censusReporter.
func (*censusRecorder) Helper() {}

// Errorf records one report.
func (r *censusRecorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// syntheticTree indexes the given sources, keyed by their repository path, as
// [buildProductionSource] indexes the production tree.
func syntheticTree(t *testing.T, sources map[string]string) *sourceTree {
	t.Helper()

	tree := &sourceTree{
		fset:        token.NewFileSet(),
		constants:   map[string]map[string]constDefinition{},
		packageName: map[string]string{},
		calls:       map[string][]callSite{},
	}
	for _, relative := range slices.Sorted(maps.Keys(sources)) {
		parsed, err := parser.ParseFile(tree.fset, relative, sources[relative], parser.SkipObjectResolution)
		require.NoError(t, err)
		file := &sourceFile{
			path:       relative,
			importPath: modulePath + "/" + path.Dir(relative),
			tree:       parsed,
			imports:    map[string]string{},
		}
		tree.files = append(tree.files, file)
		tree.packageName[file.importPath] = parsed.Name.Name
	}
	for _, file := range tree.files {
		tree.collectImports(file)
		tree.collectConstants(file)
	}
	for _, file := range tree.files {
		tree.scanDeclarations(file)
	}

	return tree
}

// busSource is a bus and a relay that hands its caller's topic to it.
const busSource = `package host

type bus struct{}

func (bus) Subscribe(name string, fn func()) {}

var b bus

// Subscribe is the relay: its topic is a parameter.
func Subscribe(topic string, fn func()) { b.Subscribe(topic, fn) }
`

// hostRelay is busSource's relay, as knownRelays names one.
var hostRelay = map[relaySite]string{{pkg: "core/host", fn: "Subscribe"}: "the synthetic bus's relay"}

// TestTheSubscriptionCensusSkipsARelay: a relay's site is counted neither as a
// subscriber that chose the topic nor as a forwarder, a constant at a plugin's
// call is, and a forwarder's subscription is the forwarder's.
func TestTheSubscriptionCensusSkipsARelay(t *testing.T) {
	t.Parallel()

	tree := syntheticTree(t, map[string]string{
		"core/host/host.go": busSource,
		"plugins/mailer/mailer.go": `package mailer

import "github.com/bdrtr/gobit/core/host"

const topicPlaced = "order.placed"

func setup() { host.Subscribe(topicPlaced, nil) }
`,
		"plugins/webhookout/plugin.go": `package webhookout

import "github.com/bdrtr/gobit/core/host"

const topicPlaced = "order.placed"

func setup() { host.Subscribe(topicPlaced, nil) }
`,
	})
	recorder := &censusRecorder{}

	census := tree.subscriptionCensus(recorder, hostRelay)

	assert.Empty(t, recorder.errors, "every topic the relay carries is visible where it is named")
	assert.Equal(t, []string{"plugins/mailer/mailer.go"}, census.chosen["order.placed"],
		"only the plugin that named the topic chose it; the relay that handed it on did not")
	assert.Equal(t, []string{"plugins/webhookout/plugin.go"}, census.forwarded["order.placed"])
	assert.Equal(t, []string{"core/host/host.go"}, census.relays(), "the relay is named, once")
}

// TestTheSubscriptionCensusRefusesATopicOnlyARelayShows is the floor: a topic
// that reaches the relay through a caller no declaring site names would be
// judged against nothing once the relay is skipped, so the census says so.
func TestTheSubscriptionCensusRefusesATopicOnlyARelayShows(t *testing.T) {
	t.Parallel()

	tree := syntheticTree(t, map[string]string{
		"core/host/host.go": busSource,
		"core/host/listen.go": `package host

// listen reaches the relay under another name.
func listen(topic string) { Subscribe(topic, nil) }
`,
		"plugins/mailer/mailer.go": `package mailer

func setup() { listen("cart.created") }
`,
	})
	recorder := &censusRecorder{}

	census := tree.subscriptionCensus(recorder, hostRelay)

	assert.True(t, slices.ContainsFunc(recorder.errors, func(message string) bool {
		return strings.Contains(message, `"cart.created" topic is visible ONLY through a relayed subscription`)
	}), "the floor reports the hidden topic: %v", recorder.errors)
	assert.Empty(t, census.chosen["cart.created"])
	assert.Empty(t, census.forwarded["cart.created"])
}

// TestTheSubscriptionCensusFailsClosed: a site the census cannot read is a
// failure naming it, never a skip, and so is a relay listed and gone (ADR 0437).
func TestTheSubscriptionCensusFailsClosed(t *testing.T) {
	t.Parallel()

	reported := func(t *testing.T, mailer string, relays map[relaySite]string) []string {
		t.Helper()
		sources := map[string]string{"plugins/mailer/mailer.go": mailer}
		if len(relays) > 0 {
			sources["core/host/host.go"] = busSource
		}
		recorder := &censusRecorder{}
		tree := syntheticTree(t, sources)
		tree.subscriptionCensus(recorder, relays)

		return recorder.errors
	}
	has := func(errors []string, parts ...string) bool {
		return slices.ContainsFunc(errors, func(message string) bool {
			for _, part := range parts {
				if !strings.Contains(message, part) {
					return false
				}
			}

			return true
		})
	}

	t.Run("a subscriber written as a helper", func(t *testing.T) {
		t.Parallel()

		errors := reported(t, `package mailer

import "github.com/bdrtr/gobit/core/host"

func subscribeTo(topic string) { host.Subscribe(topic, nil) }

func setup() { subscribeTo("order.return_opened") }
`, hostRelay)

		assert.True(t, has(errors, "plugins/mailer/mailer.go:5:",
			"takes its topic from a parameter of plugins/mailer.subscribeTo"),
			"the helper is named, not skipped as a relay: %v", errors)
	})

	t.Run("a topic that does not resolve", func(t *testing.T) {
		t.Parallel()

		errors := reported(t, `package mailer

import "github.com/bdrtr/gobit/core/host"

var topics = []string{"order.placed"}

func setup() { host.Subscribe(topics[0], nil) }
`, hostRelay)

		assert.True(t, has(errors, "plugins/mailer/mailer.go:7:", "cannot be resolved statically"),
			"the unread site is named: %v", errors)
	})

	t.Run("a relay listed and gone", func(t *testing.T) {
		t.Parallel()

		errors := reported(t, `package mailer

func setup() {}
`, map[relaySite]string{{pkg: "core/gone", fn: "Bus.Subscribe"}: "a relay that was deleted"})

		assert.True(t, has(errors, "knownRelays names core/gone.Bus.Subscribe, and no subscription sits there"),
			"a dead relay entry fails: %v", errors)
	})
}

// TestKnownRelaysAreTheTreesRelays: every listed relay subscribes somewhere,
// and every site whose topic is a parameter is listed — the production tree,
// counted with knownRelays, reports nothing.
func TestKnownRelaysAreTheTreesRelays(t *testing.T) {
	t.Parallel()

	recorder := &censusRecorder{}
	census := scanProductionSource(t).subscriptionCensus(recorder, knownRelays)

	assert.Empty(t, recorder.errors)
	assert.Equal(t, []string{"core/plugin/plugin.go", "internal/app/commandbus.go"}, census.relays(),
		"the relays skipped are the two knownRelays names")
}

// TestOnlyARecordsDecisionDecidesATopic is the record half of the exemption
// gate on the two records that name topics outside their Decision: ADR 0437
// rejects fulfillment.shipped, and ADR 0386's Status line names the return
// topics another record decided. Neither decides them.
func TestOnlyARecordsDecisionDecidesATopic(t *testing.T) {
	t.Parallel()

	record := func(number string) string {
		t.Helper()
		found, err := filepath.Glob(filepath.Join(append([]string{repoRoot}, append(adrDir, number+"-*.md")...)...))
		require.NoError(t, err)
		require.Len(t, found, 1)

		return readADR(t, found[0])
	}
	r0437, r0386 := record("0437"), record("0386")

	for _, mentioned := range []struct {
		record, topic string
		decides       bool
	}{
		{r0437, "fulfillment.delivered", true},
		{r0437, "order.return_canceled", true},
		{r0437, "fulfillment.shipped", false},
		{r0386, "order.return_opened", false},
	} {
		require.Contains(t, mentioned.record, "`"+mentioned.topic+"`",
			"the premise: the record names %s somewhere", mentioned.topic)
		assert.Equalf(t, mentioned.decides, decidesTopic(mentioned.record, mentioned.topic),
			"does the record's Decision name %s?", mentioned.topic)
	}
}

//go:build smoke

package smoke

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstRunDoc holds the block this scenario executes.
//
// `docs/first-run.md` is the only document that walks an operator from an empty
// database to a PAID ORDER. The security document walks the first four steps and
// stops at reading the catalog, which on an empty database is an empty list —
// that call succeeding says nothing about whether anything can be bought.
const firstRunDoc = "../../docs/first-run.md"

// firstRunCommands is the smallest number of commands the block can hold and
// still be the flow.
//
// A blindness guard rather than a count: a parser that stopped seeing would
// return an empty script, and an empty script runs perfectly.
const firstRunCommands = 16

// The amounts the document's own numbers produce. They are computed HERE rather
// than read out of the response, because the point of the assertion is that the
// server arrives at the figure a reader of the document would expect.
const (
	firstRunUnitPrice int64 = 32_000
	firstRunQuantity  int64 = 2
	firstRunSubtotal        = firstRunUnitPrice * firstRunQuantity
	// The tax module's default rate for the country, 2000 basis points. NOT the
	// region's own rate — a region that resolves to one country is taxed by the
	// tax module and its answer is taken as it is.
	firstRunTax   = firstRunSubtotal * 2_000 / 10_000
	firstRunTotal = firstRunSubtotal + firstRunTax
)

// TestTheDocumentedFirstRunReachesAnOrder executes the document instead of
// re-implementing it.
//
// # Why this scenario exists
//
// The path from an empty database to a shopper's order is fifteen calls and
// eleven of them were written down nowhere: they lived inside this package's own
// storefront helper and inside internal/e2e's harness, each of which creates its
// own region, price binding and stock. Every lane was green and an operator
// reading the tree had no way to learn the sequence — the gap was invisible
// precisely because both harnesses did the work themselves.
//
// Writing it down found something neither harness could: the storefront helper
// binds TWO countries to its region and is therefore taxed by the REGION's rate,
// through the branch that fires when no single jurisdiction can be named. A
// one-country region — the ordinary first installation — is taxed by the tax
// module instead, and with no tax region there the cart came back untaxed while
// the region carried a rate.
//
// # What is asserted, and why it is the statuses
//
// The block captures every id into a variable and prints a status per BINDING.
// The bindings are the steps whose absence is silent: a variant with no price
// binding is refused a line, a variant with no inventory binding is counted out
// of stock, and a region with no country serves nobody. Asserting each status by
// position makes the broken link answerable on its own — otherwise the first
// failure turns every later line into an error envelope and the test says only
// that the end is missing.
//
// The order is the LAST assertion and the reason for all the others.
func TestTheDocumentedFirstRunReachesAnOrder(t *testing.T) {
	commands := documentedShellCommands(t, firstRunDoc)
	require.GreaterOrEqualf(t, len(commands), firstRunCommands,
		"only %d commands were read out of %s; the block parser has gone blind, and an "+
			"empty script passes every assertion below", len(commands), firstRunDoc)

	cfg := baseSettings(scenarioDatabase(t), freePort(t))
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	// The same three substitutions the other executed document takes: the
	// address, because the document names the default port and the harness picks
	// a free one, and the credentials, because the document writes a placeholder
	// where a password belongs. Step 0's `make run` is dropped — the harness has
	// already started the process that line asks the reader to start.
	script := strings.Join(commands, "\n")
	script = strings.ReplaceAll(script, "localhost:9000", strings.TrimPrefix(s.addr, "http://"))
	script = strings.ReplaceAll(script, "admin@example.com", seedEmail)
	script = strings.ReplaceAll(script, "'…'", "'"+seedPassword+"'")
	script = strings.ReplaceAll(script, "\"password\":\"…\"", "\"password\":\""+seedPassword+"\"")

	out := runDocumentedScript(t, script)
	printed := nonEmptyLines(out)

	// Eight lines: six statuses, the cart's total and the order. A different count
	// means the block changed shape and the positions below are reading the wrong
	// lines — which would make every assertion meaningless while looking fine.
	require.Lenf(t, printed, 8,
		"the block printed %d lines and eight were expected.\n--- output ---\n%s",
		len(printed), out)

	for name, step := range map[string]struct {
		at     int
		status string
		why    string
	}{
		"the country bound to the region": {at: 0, status: "201",
			why: "without a country the region serves nobody and POST /store/v1/carts is " +
				"refused for every country code there is"},
		"the default tax rate in the tax module": {at: 1, status: "201",
			why: "a region that resolves to ONE country is taxed by the tax module, whose " +
				"answer is taken as it is — so without a rate there the cart is untaxed " +
				"and the region's own tax_rate_bps is dead configuration"},
		"the price set bound to the variant": {at: 2, status: "200",
			why: "a variant with no price binding has no price, and the storefront sends " +
				"none — so the line can never be added"},
		"the inventory item bound to the variant": {at: 3, status: "200",
			why: "an unbound variant is counted out of stock and its cart can never " +
				"become an order"},
		"the stock level at the location": {at: 4, status: "200",
			why: "the item with no level has nothing anywhere"},
		"the line added to the cart": {at: 5, status: "201",
			why: "this is the first step that can only succeed if all four bindings above " +
				"were made"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equalf(t, step.status, strings.TrimSpace(printed[step.at]),
				"%s answered %q and %q was expected.\n%s\n--- output ---\n%s",
				name, strings.TrimSpace(printed[step.at]), step.status, step.why, out)
		})
	}

	t.Run("the total is the one the document's numbers produce", func(t *testing.T) {
		total, err := strconv.ParseInt(strings.TrimSpace(printed[6]), 10, 64)
		require.NoErrorf(t, err, "the cart's total did not come back as a number: %q\n"+
			"A jq path that no longer resolves prints \"null\", which is what a reader "+
			"pasting this document would see", strings.TrimSpace(printed[6]))

		assert.Equalf(t, firstRunTotal, total,
			"the cart's total is %d and the document's own numbers give %d (%d x %d plus "+
				"the tax module's 20%% default rate for the country). A reader who follows "+
				"the block and gets another figure cannot tell which of the two is wrong",
			total, firstRunTotal, firstRunUnitPrice, firstRunQuantity)
	})

	t.Run("the cart became an order", func(t *testing.T) {
		line := strings.TrimSpace(printed[7])
		require.True(t, strings.HasPrefix(line, "order "),
			"the last line was %q and the block ends by echoing the order", line)

		orderID := strings.TrimSpace(strings.TrimPrefix(line, "order "))
		require.NotEmptyf(t, orderID, "the completion produced no order id.\n--- output ---\n%s", out)
		assert.NotEqualf(t, "null", orderID,
			"the completion answered without an order_id, so the jq path printed null. "+
				"Every binding above reported success, which leaves the completion itself "+
				"or the manual payment provider.\n--- output ---\n%s", out)
	})
}

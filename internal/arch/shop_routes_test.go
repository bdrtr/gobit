package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shopScript is the storefront example's whole client (ADR 0159, ADR 0174).
const shopScript = "examples/storefront/storefront/assets/storefront.js"

// shopStorePrefix is the surface the script calls.
const shopStorePrefix = "/store/v1"

// shopRoute is one call the script writes down: a string literal holding a verb,
// a space and a store template, which is how the script's route table spells
// every address it uses.
var shopRoute = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (` + shopStorePrefix + `[^"]*)"`)

// TestTheShopCallsOnlyBoundRoutes holds every call the example shop makes to a
// route the tree binds, verb included.
//
// # Why the script needs a gate of its own
//
// The route audit reads prose — a Markdown line or a Go comment naming "POST
// /x" — and a script is neither. The shop is the one place in the repository
// where a browser calls the store surface, and what it calls was checked by
// nothing but a person driving it: a route renamed on the server, or a verb
// changed, would leave the example answering 404 or 405 with every lane green.
// Its checkout grew from three calls to eleven in ADR 0174, which is where that
// stopped being a small risk.
//
// # The population is every mention, not every match
//
// The regular expression finds the calls written in the route table's shape. A
// call written some other way — a path built by joining strings, which is how
// the script spelled its catalog address before this gate — would not match and
// would go unchecked. So every occurrence of the store prefix in the file has to
// BE one of the matches; a path spelled any other way fails here first.
func TestTheShopCallsOnlyBoundRoutes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, shopScript))
	require.NoError(t, err, "the shop's script could not be read")
	text := string(raw)

	calls := shopRoute.FindAllStringSubmatch(text, -1)
	require.NotEmpty(t, calls, "no store call was read out of %s; the pattern has gone blind", shopScript)
	require.Equalf(t, strings.Count(text, shopStorePrefix), len(calls),
		"%s names %s %d times and %d of them are route-table entries (\"VERB %s/…\"). "+
			"An address written any other way is one this gate cannot check: move it into "+
			"the table, or keep the prefix out of the prose",
		shopScript, shopStorePrefix, strings.Count(text, shopStorePrefix), len(calls), shopStorePrefix)

	surface := scanBoundRoutes(t)
	for _, call := range calls {
		method, pattern := call[1], call[2]
		assert.Truef(t, surface.binds(method, pattern),
			"%s calls %s %s and the tree binds no such route. The template has to be written "+
				"as the route is bound, placeholder names included, or the shop answers a 404 "+
				"or a 405 that no lane sees",
			shopScript, method, pattern)
	}
}

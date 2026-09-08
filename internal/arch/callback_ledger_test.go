package arch_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ONE census: HOW MANY PROVIDERS THE TREE TAKES A CALLBACK FROM.
//
// It is not a rule about callbacks. It is the trigger of a decision, made
// observable by the suite instead of by memory — see
// [TestASecondCallbackRouteReopensTheCallbackLedger].

// callbackSourcesToday are the providers the tree binds an inbound callback for.
//
// There is one, and the number is the whole point.
var callbackSourcesToday = []string{"paytr"}

// callbackRouteType is the type a provider's inbound endpoint is declared as.
const callbackRouteType = "CallbackRoute"

// coreHTTPImportPath is the import path of the package that declares it.
const coreHTTPImportPath = modulePath + "/core/http"

// TestASecondCallbackRouteReopensTheCallbackLedger is ADR 0062's trigger.
//
// # What was decided
//
// A callback gets no ledger table of its own. Its durable record is the table
// the RECEIVING MODULE already owns — a callback-driven provider cannot answer
// "is the money held?" without one, so it brings a table whatever this
// repository decides — and the ring's log carries the outcomes no module can
// see: the ones a guard refused, where the handler never ran. The reader, the
// scope and the retention a shared `callback_log` would have needed all exist
// already inside that module: a listing, a module scope, and rows a decision
// says are never deleted.
//
// # Why a SECOND route is the fact that reopens it
//
// With one provider, "which of my providers called, and which were refused" has
// one answer, and a shared table would hold a copy of what that provider's
// module already stores. With two it is a question no module's table answers
// and no single module's listing should — the shape that put the audit log at
// the top of the admin surface rather than under a resource. That is the day
// the reader has a subject, the scope has something to guard, and the retention
// has to be ANSWERED rather than inherited: the population of a callback record
// is chosen by an unauthenticated caller, so the audit log's "no window, the
// operator prunes" is not available to it.
//
// # Why a census and not a rule
//
// It does not say a second callback is wrong. It says a second callback is the
// event the decision named, and it fails so that whoever adds one reads the
// decision before the shape gets fixed by accident.
//
// The population is the production source rather than a list kept here, so a
// route added anywhere in the tree is in scope and cannot remove itself; and a
// scan that resolves nothing fails as BLINDNESS rather than passing as
// agreement, which is the direction this audit would otherwise rot in.
func TestASecondCallbackRouteReopensTheCallbackLedger(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)

	bound := map[string]string{}
	for _, literal := range tree.literals {
		for _, route := range qualifiedLiterals(literal, coreHTTPImportPath, callbackRouteType) {
			// A literal with no fields is a zero value rather than a route.
			if len(route.Elts) == 0 {
				continue
			}

			where := tree.location(literal.file, route.Pos())
			sources := tree.stringValues(literal.file, literal.fn, fieldExpr(route, "Source"), 0)
			if len(sources) == 0 {
				t.Errorf("%s: the Source of this callback route could not be resolved "+
					"statically.\n"+
					"An unresolvable source hides the route from this census, and the census "+
					"is what makes ADR 0062's trigger observable at all; bind the source to a "+
					"string constant the way the PayTR plugin does.", where)

				continue
			}
			for _, source := range sources {
				bound[source] = where
			}
		}
	}

	require.NotEmpty(t, bound,
		"not a SINGLE callback route was found in the production source; this census has "+
			"gone BLIND.\n"+
			"One route is bound today, so an empty result does not mean the callbacks are "+
			"gone — it means the declaration form changed (a builder, a different type) and "+
			"this audit would go on reporting agreement with a tree it can no longer read.")

	assert.Equal(t, callbackSourcesToday, slices.Sorted(maps.Keys(bound)),
		"the callback population has changed, and that is ADR 0062's named trigger.\n"+
			"Bound now: %s.\n"+
			"That record closed docs/gaps.md's A19 by deciding that a callback needs no "+
			"ledger table of its own WHILE there is one provider: the durable record lives "+
			"in that provider's own module, and the refusals live in the ring's log. A "+
			"second provider is the fact it named as the one that makes a shared ledger "+
			"buildable — a cross-provider question no module's table answers, a reader with "+
			"a subject, and a retention answer that can no longer be inherited.\n"+
			"This is NOT a defect in the route you added: read ADR 0062, decide, and update "+
			"this census in the same change.",
		boundCallbackSummary(bound))
}

// boundCallbackSummary renders the census for the failure message.
//
// The location goes with each source because the first question the message
// raises is "which route is the second one", and a bare list of names sends the
// reader grepping for the answer this audit already had in hand.
func boundCallbackSummary(bound map[string]string) string {
	parts := make([]string, 0, len(bound))
	for _, source := range slices.Sorted(maps.Keys(bound)) {
		parts = append(parts, source+" ("+bound[source]+")")
	}

	return strings.Join(parts, ", ")
}

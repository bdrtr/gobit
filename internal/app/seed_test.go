package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// The database the refusals below are rendered against, and the one an operator
// might mistakenly believe they are pointed at.
const (
	connectedDatabase = "gobit_prod"
	otherDatabase     = "gobit_dev"
)

// reportPrefix is what [seedCatalog] has already written by the time the reset
// is judged: the line naming the database the connection reached.
const reportPrefix = "seed: database \"gobit_prod\"\n"

// TestAForgottenFlagIsNotReportedAsAWrongDatabase keeps the two refusals apart.
//
// They are different mistakes and they need different next steps. An operator
// who typed no confirmation at all forgot a flag; an operator whose
// confirmation names another database believes this connection goes somewhere
// it does not, and that belief is the dangerous one — it is the state in which
// somebody adds -confirm to a script and points it at production. Answering
// the first with the second's sentence would tell them to check a name they
// never gave, and answering the second with the first's would hide the only
// fact that matters: the two databases are not the same.
func TestAForgottenFlagIsNotReportedAsAWrongDatabase(t *testing.T) {
	t.Parallel()

	out := resetPlanText(connectedDatabase, "", reportPrefix)

	assert.Contains(t, out, "no confirmation given",
		"a missing -confirm has to be reported as a missing flag")
	assert.NotContains(t, out, "the confirmation names",
		"nothing was named, so the refusal must not send the operator looking for a "+
			"database name they did not type")
}

// TestAConfirmationForAnotherDatabaseNamesBothSides is the sentence that stops
// a wipe of the wrong installation.
//
// The operator gave a name, so they have a database in mind; the connection has
// another. A refusal that printed only one of the two would leave them to guess
// which half is wrong, and the likeliest guess — "I mistyped it" — leads
// straight to retyping the name the connection reports and deleting the rows of
// an installation they never meant to touch.
func TestAConfirmationForAnotherDatabaseNamesBothSides(t *testing.T) {
	t.Parallel()

	out := resetPlanText(connectedDatabase, otherDatabase, reportPrefix)

	assert.Contains(t, out, otherDatabase, "the refusal has to repeat what the operator typed")
	assert.Contains(t, out, connectedDatabase, "and the database this connection actually reached")
	assert.Contains(t, out, "the confirmation names \""+otherDatabase+"\", but this connection is to \""+
		connectedDatabase+"\"",
		"the two names have to be told apart by ROLE; printed in either order without "+
			"saying which is which they are just two strings")
}

// TestTheRefusalRepeatsTheConnectionsOwnName is the difference between a
// refusal an operator can act on and one they fight.
//
// The last line of the plan is meant to be copied. If it carried the name the
// operator typed, copying it would produce the identical refusal for as long as
// they kept trying, and the only way out would be to read the wording closely
// during exactly the kind of hurry that produced the wrong name in the first
// place.
func TestTheRefusalRepeatsTheConnectionsOwnName(t *testing.T) {
	t.Parallel()

	out := resetPlanText(connectedDatabase, otherDatabase, reportPrefix)

	command := lineContaining(t, out, "-"+flagConfirm)
	assert.Contains(t, command, "-"+flagConfirm+" "+connectedDatabase,
		"the command to copy has to name the database that would be emptied; offering "+
			"the operator's own word back would refuse every retry of it")
	assert.NotContains(t, command, otherDatabase)
	assert.Contains(t, command, "-"+flagReset,
		"a confirmation without -reset is itself refused, so the line has to carry both flags")
}

// TestARefusalSaysNothingWasChanged is the fact the operator needs first.
//
// A destructive command that stops halfway and one that never started call for
// opposite next steps — investigate versus retry — and the reset has a halfway
// state (its steps run in one transaction, but the seed that follows does not).
// This line is the promise that the run is in the second case, and the plan
// under it describes a deletion in the conditional, as something that WOULD
// happen.
func TestARefusalSaysNothingWasChanged(t *testing.T) {
	t.Parallel()

	for _, confirm := range []string{"", otherDatabase} {
		out := resetPlanText(connectedDatabase, confirm, reportPrefix)

		assert.Contains(t, out, "REFUSED")
		assert.Contains(t, out, "Nothing was changed.",
			"an operator reading a refusal must not have to wonder whether rows are "+
				"already gone (confirmation %q)", confirm)
	}
}

// TestTheRefusalKeepsWhatTheReportAlreadySaid stops the plan from arriving
// nameless.
//
// The line identifying the connection's database is written by [seedCatalog]
// before the reset is judged and handed in as the prefix. Dropped here, the
// refusal would still be correct and would be printed on a terminal whose
// scrollback holds several databases, with nothing tying the warning to one of
// them.
func TestTheRefusalKeepsWhatTheReportAlreadySaid(t *testing.T) {
	t.Parallel()

	out := resetPlanText(connectedDatabase, "", reportPrefix)

	require.True(t, strings.HasPrefix(out, reportPrefix),
		"the report written so far has to stay at the top of the refusal, and first:\n%s", out)
}

// TestTheResetPromisesToSpareTheIdentities is the half of the plan that says
// what SURVIVES.
//
// The rows go, the sales channels and API keys stay, and an operator who
// believed otherwise would rebuild a storefront's key for no reason — or, worse,
// hold off on a reset they needed because they thought it would revoke a key a
// storefront is using.
func TestTheResetPromisesToSpareTheIdentities(t *testing.T) {
	t.Parallel()

	out := resetPlanText(connectedDatabase, "", reportPrefix)

	assert.Contains(t, out, "Sales channels and API keys",
		"the plan has to say which identities the deletion leaves alone")
	assert.Contains(t, out, "NOT touched")
}

// lineContaining returns the single line of text holding needle.
func lineContaining(t *testing.T, text, needle string) string {
	t.Helper()

	found := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			require.Empty(t, found,
				"%q is on more than one line, so asserting about \"the\" line is meaningless",
				needle)
			found = line
		}
	}

	require.NotEmpty(t, found, "no line of the refusal contains %q:\n%s", needle, text)

	return found
}

// TestAConfirmationWithoutTheResetFlagIsRefused catches the typo that would
// otherwise seed on top of the rows the operator believes are gone.
//
// Somebody who typed the database name is holding a mental model in which
// something is about to be deleted. Ignoring the confirmation because -reset is
// missing would leave them measuring a catalog they think they replaced — and
// the numbers would look plausible, because a seed on top of an existing rig
// succeeds and reports the counts it finds.
func TestAConfirmationWithoutTheResetFlagIsRefused(t *testing.T) {
	t.Parallel()

	_, err := parseSeedFlags([]string{"-" + flagConfirm, connectedDatabase})

	require.Error(t, err, "a confirmation that guards nothing must not be accepted quietly")
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
	assert.Contains(t, err.Error(), flagReset,
		"the refusal has to name the flag that is missing, not just the one that is there")
}

// TestTheResetFlagAloneIsAcceptedByTheParser keeps the two refusals in their
// own places.
//
// -reset without a confirmation is a REFUSAL TO DELETE, and it is made by
// [seedCatalog] because only a live connection knows the database's name. If
// the parser rejected it here, the operator would never see the plan telling
// them what would be deleted and which word to repeat.
func TestTheResetFlagAloneIsAcceptedByTheParser(t *testing.T) {
	t.Parallel()

	flags, err := parseSeedFlags([]string{"-" + flagReset})

	require.NoError(t, err)
	assert.True(t, flags.reset)
	assert.Empty(t, flags.confirm)
}

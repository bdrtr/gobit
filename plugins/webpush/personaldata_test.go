package webpush

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// This file is the gate for the declaration next to it, and it lives HERE for a
// measured reason rather than a stylistic one.
//
// internal/app/personaldata_test.go audits exactly this property across the
// module tree — that every person-shaped column is declared, and that every
// declaration names a column that exists. It cannot see this module. Its
// registry is built by registeredModules, which calls registerModules with an
// empty config.Config, so no plugin is installed and no plugin-brought module
// ever enters the walk. That is why a table holding a push endpoint, two device
// keys and a customer id sat undeclared until 2026-09-07 with every gate green.
//
// The same reasoning ADR 0018 used for this plugin's rollback test applies
// unchanged: what the arch gates cannot reach, the plugin carries itself.

// TestTheDeclarationCoversEveryPersonColumnInTheSchema is the audit the app
// cannot run for this module.
//
// It checks BOTH directions, because each catches a different mistake: a
// declared column that does not exist sends a controller looking where there is
// nothing, and an undeclared person column is a holder that answers a
// data-subject request while leaving something out.
func TestTheDeclarationCoversEveryPersonColumnInTheSchema(t *testing.T) {
	t.Parallel()

	module := newModule(moduleOptions{})
	for _, holding := range module.PersonalData().Holdings {
		assert.NotEmptyf(t, strings.TrimSpace(holding.Why),
			"%s.%s is declared with an empty Why; a disclosure repeats that sentence to a person, "+
				"so a blank one is a holding nobody can explain", holding.Table, holding.Column)
	}

	// Every column is judged, not only the ones whose names look like a
	// person (ADR 0278): the name heuristic this audit used before would have
	// waved through a column called anything else.
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}

// notPersonalColumns are the subscription's columns that hold nothing about
// a person: its id and stamps, the locale — a preference, see PersonalData —
// and the fingerprint of the SERVER key the subscription was minted against.
var notPersonalColumns = map[string][]string{
	tableSubscription: {"id", "locale", "vapid_fingerprint", "created_at", "updated_at"},
}

// TestErasingByEmailAloneReportsZeroRatherThanFailing pins the answer this
// holder gives to a handle it cannot be asked by.
//
// The table binds a device by customer id and holds no address. A subject
// carrying only an e-mail therefore matches nothing here, and the contract's
// requirement is that the holder still ANSWER — a sweep that got an error from
// one holder would report a partial erasure for a person this module simply has
// nothing about.
func TestErasingByEmailAloneReportsZeroRatherThanFailing(t *testing.T) {
	t.Parallel()

	result, err := newModule(moduleOptions{}).
		Erase(context.Background(), personaldata.Subject{Email: "someone@example.com"})

	require.NoError(t, err, "a handle this holder cannot be asked by is not a fault")
	assert.Equal(t, personaldata.Deleted, result.Outcome)
	assert.Zero(t, result.Rows)
	assert.NotEmpty(t, result.Why,
		"answering zero without saying WHICH handle this holder can be asked by leaves the "+
			"controller unable to tell 'nothing here' from 'not asked properly'")
}

// TestErasingBeforeRegisterIsAnErrorAndNotASilentSuccess is the other half.
//
// A module whose store was never wired has read nothing. Answering Deleted there
// would put this holder in the report as done, which is the false-completeness
// the erasure contract exists to prevent.
func TestErasingBeforeRegisterIsAnErrorAndNotASilentSuccess(t *testing.T) {
	t.Parallel()

	_, err := newModule(moduleOptions{}).
		Erase(context.Background(), personaldata.Subject{CustomerID: "cus_1"})

	require.Error(t, err,
		"an unwired holder must not report itself erased; the sweep would count it done")
}

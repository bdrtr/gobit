package identitypasskey_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/workflows/datasubject"
)

// The tests in this file are about the three ANSWERS, not about the SQL.
//
// What the statements do against a real table is in store_integration_test.go:
// that an erasure takes abandoned rows too, and that a dossier carries the whole
// credential. What is here is the part a store cannot decide — which outcome a
// controller is told, and whether the two kinds of empty stay apart.

// TestADeclarationNamesEveryColumnOfTheTable is the gate under the other two.
//
// A declaration is what makes the other obligations honest (ADR 0029): without it
// nothing tells a controller where to look. The risk is not that it says something
// false but that it goes SHORT — a column added to the table and not to the
// declaration is invisible to every sweep from then on, and the report still looks
// complete.
//
// The population comes from the migration rather than from a list in this test.
func TestADeclarationNamesEveryColumnOfTheTable(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	for _, holding := range newHarness(t).module.PersonalData().Holdings {
		assert.Equal(t, "passkey_credentials", holding.Table)
		assert.NotEmpty(t, holding.Why, "a holding with no reason tells a controller nothing")
		declared[holding.Column] = true
	}

	// rp_id is deliberately absent: it is the installation's configuration copied
	// onto the row, the same for everybody, and declaring it would send a
	// controller looking for a person in a column that describes the shop.
	assert.Equal(t, map[string]bool{
		"customer_id": true, "credential_id": true, "credential": true,
		"created_at": true, "last_used_at": true,
	}, declared)
}

// TestAStoreThatCannotEraseSaysSOAndSaysWhat holds the answer an LDAP-backed
// installation gets.
//
// The capability is optional because [identitypasskey.Credentials] exists so an
// installation can bind its own store, and such a store must not be asked to
// delete rows out of a table it does not own. What matters is that the sweep is
// told — an outcome of "deleted, 0 rows" would report a deletion that never
// happened, and the person would be told they were forgotten.
func TestAStoreThatCannotEraseSaysSOAndSaysWhat(t *testing.T) {
	t.Parallel()

	// memoryCredentials does not implement PersonalRecords, which is the shape of
	// every store bound by an installation that keeps keys elsewhere.
	h := withKeys(t, identitypasskey.NoOtherSignIn(), "phone")

	result, err := h.module.Erase(t.Context(), personaldata.Subject{CustomerID: testCustomer})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Retained, result.Outcome,
		"a holder that kept what it holds says so; it does not report a deletion")
	assert.Zero(t, result.Rows)
	assert.NotEmpty(t, result.Why, "and a retention with no reason is a refusal nobody can act on")
	assert.Contains(t, result.Kept, "passkey_credentials.credential_id",
		"what is kept is listed from the declaration, so the two cannot drift")

	disclosure, err := h.module.PersonalDataOf(t.Context(),
		personaldata.Subject{CustomerID: testCustomer})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Unresolvable, disclosure.State,
		"could not look is NOT the same as looked and found nothing")
}

// TestAnAddressAloneCannotBeResolvedHere is the two-kinds-of-empty distinction.
//
// This module stores no e-mail — the account is whoever the bound identity proves
// — so an address is not a subject it stores nothing under, it is a subject it
// cannot search for. Answering "deleted, 0 rows" would report a search that never
// happened, and the controller would cross this holder off.
func TestAnAddressAloneCannotBeResolvedHere(t *testing.T) {
	t.Parallel()

	h := newHarnessWithStore(t, identitypasskey.NoOtherSignIn(), newErasableStore())

	result, err := h.module.Erase(t.Context(),
		personaldata.Subject{Email: "somebody@example.test"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Retained, result.Outcome)
	assert.Contains(t, result.Why, "customer id")

	disclosure, err := h.module.PersonalDataOf(t.Context(),
		personaldata.Subject{Email: "somebody@example.test"})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Unresolvable, disclosure.State)
}

// TestARequestThatNamesNobodyIsRefused keeps an empty subject from reading as a
// clean sweep.
func TestARequestThatNamesNobodyIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.module.Erase(t.Context(), personaldata.Subject{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), identitypasskey.CodeSubjectEmpty)

	_, err = h.module.PersonalDataOf(t.Context(), personaldata.Subject{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), identitypasskey.CodeSubjectEmpty)
}

// TestAnErasureReportsTheRowsItTook is the ordinary path.
func TestAnErasureReportsTheRowsItTook(t *testing.T) {
	t.Parallel()

	store := newErasableStore()
	store.erased = 4
	h := newHarnessWithStore(t, identitypasskey.NoOtherSignIn(), store)

	result, err := h.module.Erase(t.Context(), personaldata.Subject{CustomerID: testCustomer})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Deleted, result.Outcome)
	assert.Equal(t, 4, result.Rows, "the count is what a controller quotes back to the person")
	assert.Empty(t, result.Why, "a deletion needs no excuse")
}

// TestAFailingErasureIsAnERRORAndNotARetention is the outcome vocabulary's own
// rule, held.
//
// [personaldata.Retained] is a refusal somebody DECIDED on; a holder that could
// not finish its work returns an error, and the sweep reports itself partial. A
// broken query reported as a retention would look like a policy.
func TestAFailingErasureIsAnERRORAndNotARetention(t *testing.T) {
	t.Parallel()

	store := newErasableStore()
	store.eraseErr = errors.New("the connection was refused")
	h := newHarnessWithStore(t, identitypasskey.NoOtherSignIn(), store)

	_, err := h.module.Erase(t.Context(), personaldata.Subject{CustomerID: testCustomer})
	require.Error(t, err,
		"a holder that cannot finish returns an error; the sweep then says it is PARTIAL")
}

// erasableStore is a store that DOES answer the data-subject capability.
type erasableStore struct {
	*memoryCredentials

	erased    int
	eraseErr  error
	records   []identitypasskey.StoredKey
	recordErr error
}

// newErasableStore is an empty one.
func newErasableStore() *erasableStore {
	return &erasableStore{memoryCredentials: &memoryCredentials{}}
}

// ErasePasskeysOf answers the configured count.
func (s *erasableStore) ErasePasskeysOf(context.Context, string) (int, error) {
	return s.erased, s.eraseErr
}

// PasskeyRecordsOf answers the configured rows.
func (s *erasableStore) PasskeyRecordsOf(
	context.Context, string,
) ([]identitypasskey.StoredKey, error) {
	return s.records, s.recordErr
}

var _ identitypasskey.PersonalRecords = (*erasableStore)(nil)

// TestTheSWEEPReallyReachesThisModule is the claim nobody should take on trust.
//
// # Why this test exists and why it is allowed to
//
// Everything above proves that the three methods answer correctly. None of it
// proves the thing an installation actually depends on: that the data-subject
// sweep FINDS a module living in a separate Go module. The capabilities are
// optional and discovered by type assertion, so a drifted signature would be
// skipped with a green build and a report that looks complete — the exact failure
// ADR 0035 measured on a different capability.
//
// It was nearly written down as impossible. The reasoning was that
// internal/workflows/datasubject cannot be imported from here, and the reasoning
// was wrong: Go's internal rule is about the import PATH, not the module, and
// contrib/identity-passkey sits under github.com/bdrtr/gobit/ like everything
// else. Probed rather than assumed.
//
// The cost is real and accepted: this test depends on one of gobit's internal
// packages, so a refactor there breaks it. That is the signal somebody wants —
// the sweep this module relies on has moved.
func TestTheSWEEPReallyReachesThisModule(t *testing.T) {
	t.Parallel()

	store := newErasableStore()
	store.erased = 2
	h := newHarnessWithStore(t, identitypasskey.NoOtherSignIn(), store)

	registry := module.NewRegistry(nil, nil)
	registry.Add(h.module)

	sweep, err := datasubject.FromContainer(container.New(nil), registry.Modules())
	require.NoError(t, err)

	report, err := sweep.Erase(t.Context(),
		personaldata.Subject{CustomerID: testCustomer})
	require.NoError(t, err)

	// The report carries more than this module: a coordinator also reports the
	// holders that live outside the module tree — the saga store, the audit log,
	// the link tables — and with a bare container each of them answers Retained
	// with its reason. That is the mechanism working rather than noise, so the
	// assertion FINDS this module instead of demanding it be alone.
	var mine *personaldata.Result
	for i := range report.Results {
		if report.Results[i].Holder == identitypasskey.ErasureHolder {
			mine = &report.Results[i]
		}
	}

	require.NotNil(t, mine,
		"the sweep did not reach this module at all; it is in the registry and its "+
			"capabilities are found by type assertion, so a drifted signature would be "+
			"skipped exactly like this — silently, with a report that looks complete.\n"+
			"Holders asked: %v", report.Results)
	assert.Equal(t, personaldata.Deleted, mine.Outcome)
	assert.Equal(t, 2, mine.Rows, "and the count the store reported traveled intact")

	// The other two capabilities travel the same way.
	var declared bool
	for _, declaration := range sweep.PersonalData() {
		if declaration.Holder == identitypasskey.ErasureHolder {
			declared = len(declaration.Holdings) > 0
		}
	}
	assert.True(t, declared,
		"a sweep that found the eraser and not the declarer would report an erasure "+
			"nobody can explain")

	dossier, err := sweep.Disclose(t.Context(),
		personaldata.Subject{CustomerID: testCustomer})
	require.NoError(t, err)

	var disclosed bool
	for _, part := range dossier.Parts {
		if part.Holder == identitypasskey.ErasureHolder {
			disclosed = true
		}
	}
	assert.True(t, disclosed, "and the dossier has a part from this module")
}

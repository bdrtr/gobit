package identitypasskey_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
)

// The tests in this file are about the RULE, not the storage.
//
// What they can hold is every branch the handler takes: who may list, who may
// remove, what one code answers, and the difference between "you have no other
// way in" and "we could not check" — which is the distinction the whole seam
// exists for. What they cannot hold is the lock, so the race has an integration
// test of its own.

// listed reads the listing envelope.
type listed struct {
	Data []struct {
		ID                 string   `json:"id"`
		LastUsedAt         *string  `json:"last_used_at"`
		Transports         []string `json:"transports"`
		Removable          bool     `json:"removable"`
		NotRemovableReason string   `json:"not_removable_reason"`
	} `json:"data"`
}

// withKeys gives a customer some keys and an answer about other ways in.
func withKeys(t *testing.T, other identitypasskey.OtherSignIn, ids ...string) *harness {
	t.Helper()

	h := newHarnessWith(t, other)
	for _, id := range ids {
		require.NoError(t, h.store.Put(t.Context(), testCustomer,
			webauthn.Credential{ID: []byte(id), PublicKey: []byte("pk")}))
	}

	return h
}

// TestListingAnswersTheCallersOwnKeysAndNothingElse is the surface a person reads
// before they remove one.
func TestListingAnswersTheCallersOwnKeysAndNothingElse(t *testing.T) {
	t.Parallel()

	h := withKeys(t, identitypasskey.NoOtherSignIn(), "phone", "laptop")
	require.NoError(t, h.store.Put(t.Context(), "cust_SOMEBODY_ELSE",
		webauthn.Credential{ID: []byte("theirs"), PublicKey: []byte("pk")}))

	rec := h.get(t, "/store/v1/auth/passkey/keys", h.signedInAs(t, testCustomer))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var body listed
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 2, "the caller's keys and only theirs")

	for _, key := range body.Data {
		assert.NotEqual(t, base64.RawURLEncoding.EncodeToString([]byte("theirs")), key.ID,
			"somebody else's key must not be in this list")
		assert.True(t, key.Removable, "with two keys either one may go")
		assert.Empty(t, key.NotRemovableReason)
		assert.NotNil(t, key.Transports, "transports is an ARRAY, never null")
	}

	assert.NotContains(t, rec.Body.String(), "public_key",
		"the public key is not a person's business and the store never hands it over")
	assert.NotContains(t, rec.Body.String(), "sign_count")
	assert.NotContains(t, rec.Body.String(), "backup")
}

// TestAnEmptyListingIsAnEmptyArray keeps a client from branching on null.
func TestAnEmptyListingIsAnEmptyArray(t *testing.T) {
	t.Parallel()

	h := withKeys(t, identitypasskey.NoOtherSignIn())

	rec := h.get(t, "/store/v1/auth/passkey/keys", h.signedInAs(t, testCustomer))

	require.Equal(t, http.StatusOK, rec.Code, "a person with no keys is not a 404")
	assert.JSONEq(t, `{"data":[]}`, rec.Body.String())
}

// TestTheLastKeyIsNotRemovableWithoutAnotherWayIn is the rule.
func TestTheLastKeyIsNotRemovableWithoutAnotherWayIn(t *testing.T) {
	t.Parallel()

	h := withKeys(t, identitypasskey.NoOtherSignIn(), "only")
	signedIn := h.signedInAs(t, testCustomer)

	var body listed
	rec := h.get(t, "/store/v1/auth/passkey/keys", signedIn)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.False(t, body.Data[0].Removable, "the listing says so in advance")
	assert.Equal(t, identitypasskey.CodeLastWayIn, body.Data[0].NotRemovableReason)

	refused := h.remove(t, body.Data[0].ID, signedIn)

	require.Equal(t, http.StatusConflict, refused.Code, "body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeLastWayIn)
	assert.Contains(t, refused.Body.String(), "ANOTHER device",
		"two keys are not two devices, so the sentence has to say which it means")
	assert.Contains(t, refused.Body.String(), "credentials_remaining")
	assert.Len(t, h.store.byCustomer[testCustomer], 1, "and nothing was removed")
}

// TestTheLastKeyGoesWhenTheInstallationSaysThereIsAnotherWayIn is the same rule
// read the other way.
func TestTheLastKeyGoesWhenTheInstallationSaysThereIsAnotherWayIn(t *testing.T) {
	t.Parallel()

	h := withKeys(t, alwaysAnotherWayIn{}, "only")
	signedIn := h.signedInAs(t, testCustomer)

	var body listed
	rec := h.get(t, "/store/v1/auth/passkey/keys", signedIn)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.True(t, body.Data[0].Removable,
		"a person with a password may remove their only passkey")

	removed := h.remove(t, body.Data[0].ID, signedIn)

	require.Equal(t, http.StatusNoContent, removed.Code, "body: %s", removed.Body.String())
	assert.Empty(t, h.store.byCustomer[testCustomer])
}

// TestARemovalThatCannotBeCheckedChangesNothing is why the unknown answer is a
// NAMED error.
//
// "You have no other way in" is a sentence for the person; "we could not check"
// is a fault of the installation. Folding the second into the first would tell
// somebody their account has one door when nobody looked — and folding it the
// other way would remove their last key on a failed query.
func TestARemovalThatCannotBeCheckedChangesNothing(t *testing.T) {
	t.Parallel()

	h := withKeys(t, cannotSay{}, "only")
	signedIn := h.signedInAs(t, testCustomer)

	listing := h.get(t, "/store/v1/auth/passkey/keys", signedIn)
	assert.Equal(t, http.StatusInternalServerError, listing.Code,
		"a listing with `removable` omitted would make a client guess; body: %s",
		listing.Body.String())

	refused := h.remove(t, base64.RawURLEncoding.EncodeToString([]byte("only")), signedIn)

	require.Equal(t, http.StatusInternalServerError, refused.Code, "body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeUnavailable)
	assert.NotContains(t, refused.Body.String(), identitypasskey.CodeLastWayIn,
		"a failed check must not be reported as an answer about the account")
	assert.Len(t, h.store.byCustomer[testCustomer], 1, "and nothing was removed")
	assert.Contains(t, h.logs.String(), "another way in could not be determined",
		"the WIRE cannot carry this distinction — core/http masks the message of every "+
			"internal error so a DSN cannot leak — so the log line is the only place an "+
			"operator learns which fault happened")
}

// TestNobodyCouldAnswerAndTheAnswererIsBrokenAreDIFFERENTSENTENCES holds the
// distinction the named error exists for.
//
// Both are 500 and both change nothing, so the status cannot tell them apart.
// What can is the sentence, and an operator reading "could not be checked" looks
// at their credential store while one reading the other looks at the network.
func TestNobodyCouldAnswerAndTheAnswererIsBrokenAreDIFFERENTSENTENCES(t *testing.T) {
	t.Parallel()

	unknown := withKeys(t, cannotSay{}, "only")
	broken := withKeys(t, brokenOtherSignIn{}, "only")
	id := base64.RawURLEncoding.EncodeToString([]byte("only"))

	firstResponse := unknown.remove(t, id, unknown.signedInAs(t, testCustomer))
	secondResponse := broken.remove(t, id, broken.signedInAs(t, testCustomer))
	first, second := unknown, broken

	require.Equal(t, http.StatusInternalServerError, firstResponse.Code)
	require.Equal(t, http.StatusInternalServerError, secondResponse.Code)
	// The BODIES are identical, and that is the framework's decision rather than a
	// defect: core/http masks every KindInternal message. So the distinction is
	// asserted where it actually lives.
	assert.Equal(t, firstResponse.Body.String(), secondResponse.Body.String(),
		"both are masked 500s on the wire, which is what core/http guarantees")
	assert.Contains(t, first.logLine(t), "another way in could not be determined",
		"a credential store that cannot answer names itself")
	assert.NotContains(t, second.logLine(t), "another way in could not be determined",
		"an answerer that BROKE is a different fault and must not borrow that sentence; "+
			"folding them leaves an operator with nowhere to look")
	assert.Contains(t, second.logLine(t), "other sign-in could not be asked")
}

// TestOneCodeForEveryIdThatIsNotTheCallersKey holds the four cases together.
func TestOneCodeForEveryIdThatIsNotTheCallersKey(t *testing.T) {
	t.Parallel()

	h := withKeys(t, alwaysAnotherWayIn{}, "mine", "also_mine")
	require.NoError(t, h.store.Put(t.Context(), "cust_SOMEBODY_ELSE",
		webauthn.Credential{ID: []byte("theirs"), PublicKey: []byte("pk")}))
	signedIn := h.signedInAs(t, testCustomer)

	for name, id := range map[string]string{
		"never existed":   base64.RawURLEncoding.EncodeToString([]byte("nobody's")),
		"somebody else's": base64.RawURLEncoding.EncodeToString([]byte("theirs")),
		"not base64url":   "!!! not base64 !!!",
		"empty":           " ",
	} {
		t.Run(name, func(t *testing.T) {
			rec := h.remove(t, id, signedIn)

			require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), identitypasskey.CodeNoSuchKey,
				"one code: telling these apart answers whether an id belongs to somebody")
		})
	}

	assert.Len(t, h.store.byCustomer["cust_SOMEBODY_ELSE"], 1,
		"and the other customer keeps their key")
}

// TestListingAndRemovingNeedAnAccount refuses the caller nobody can identify.
func TestListingAndRemovingNeedAnAccount(t *testing.T) {
	t.Parallel()

	h := withKeys(t, alwaysAnotherWayIn{}, "mine")

	listing := h.get(t, "/store/v1/auth/passkey/keys")
	assert.Equal(t, http.StatusUnauthorized, listing.Code)
	assert.Contains(t, listing.Body.String(), identitypasskey.CodeNotSignedIn)

	removal := h.remove(t, base64.RawURLEncoding.EncodeToString([]byte("mine")))
	assert.Equal(t, http.StatusUnauthorized, removal.Code)
	assert.Contains(t, removal.Body.String(), identitypasskey.CodeNotSignedIn)
}

// TestARemovalWithTwoKeysAsksNobody is the shape that keeps the ordinary case off
// the cross-module path.
//
// The question can reach another module and another database, so asking it when
// the answer cannot change the outcome would put a second query on every removal.
func TestARemovalWithTwoKeysAsksNobody(t *testing.T) {
	t.Parallel()

	counting := &countingOtherSignIn{}
	h := withKeys(t, counting, "phone", "laptop")

	removed := h.remove(t, base64.RawURLEncoding.EncodeToString([]byte("phone")),
		h.signedInAs(t, testCustomer))

	require.Equal(t, http.StatusNoContent, removed.Code, "body: %s", removed.Body.String())
	assert.Zero(t, counting.asked,
		"a person with two keys needs no answer from another module")
}

// alwaysAnotherWayIn is an installation that says a password exists.
type alwaysAnotherWayIn struct{}

// Exists answers yes.
func (alwaysAnotherWayIn) Exists(context.Context, string) (bool, error) { return true, nil }

// cannotSay is a store that cannot answer the question.
//
// It WRAPS identitysession.ErrPasswordUnknown rather than returning an error
// whose text merely mentions it, because that is the difference the handler
// branches on. An unwrapped error answers the same status and a different
// sentence, so a test built on one would have passed over the branch it was
// written for — noticed here rather than shipped.
type cannotSay struct{}

// Exists answers the named unknown, which is what an LDAP directory with no
// lookup produces.
func (cannotSay) Exists(context.Context, string) (bool, error) {
	return false, fmt.Errorf("%w: the directory has no lookup",
		identitysession.ErrPasswordUnknown)
}

// brokenOtherSignIn fails for a reason that is NOT "nobody could answer".
type brokenOtherSignIn struct{}

// Exists fails the way a database being down fails.
func (brokenOtherSignIn) Exists(context.Context, string) (bool, error) {
	return false, errors.New("the connection was refused")
}

// countingOtherSignIn records whether it was asked.
type countingOtherSignIn struct{ asked int }

// Exists counts and answers yes.
func (c *countingOtherSignIn) Exists(context.Context, string) (bool, error) {
	c.asked++

	return true, nil
}

// TestAStaleCountDoesNotWidenTheRule holds the claim mayRemoveTheLast's own
// documentation makes about the window it leaves open.
//
// The count is taken WITHOUT a lock and the decision is made on it, so another
// request can remove a row in between and the locked count is then lower. The
// documented claim is that this direction is safe because the question is merely
// SKIPPED and the locked guard refuses on its own.
//
// This is that claim under test rather than in prose. The store reports two rows
// and holds one: the handler asks nobody, passes "the last may not go", and the
// store — counting under its lock — refuses. Passing "the last MAY go" here
// would remove somebody's only way in because a count was a moment old.
func TestAStaleCountDoesNotWidenTheRule(t *testing.T) {
	t.Parallel()

	// cannotSay makes the failure loud if the question is asked at all: a handler
	// that consulted the answer in this state would answer 500 instead.
	h := withKeys(t, cannotSay{}, "only")
	h.store.phantomKeys = 1
	signedIn := h.signedInAs(t, testCustomer)

	refused := h.remove(t, base64.RawURLEncoding.EncodeToString([]byte("only")), signedIn)

	require.Equal(t, http.StatusConflict, refused.Code, "body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeLastWayIn)
	assert.Len(t, h.store.byCustomer[testCustomer], 1,
		"the only real key survived a count that said there were two")
}

// TestOneKeyHasONEName closes an aliasing nobody would look for.
//
// Go's base64 decoder does not insist that the trailing bits of an unpadded
// group be zero, so "b25sea" and "b25seQ" decode to the same four bytes. This was
// found by accident: the fixture for a different test used the first spelling,
// the removal worked, and the id had been mistyped.
//
// The removal therefore accepted a name for a key that the listing never issues.
// It is refused now, and refused as "no such key" — a non-canonical id is an id
// this caller has no key for, which is the same answer as every other miss, and
// telling it apart would be telling a caller something about how ids are
// checked.
func TestOneKeyHasONEName(t *testing.T) {
	t.Parallel()

	h := withKeys(t, identitypasskey.NoOtherSignIn(), "only", "second")
	signedIn := h.signedInAs(t, testCustomer)

	canonical := base64.RawURLEncoding.EncodeToString([]byte("only"))
	require.Equal(t, "b25seQ", canonical, "the encoding this module issues")

	refused := h.remove(t, "b25sea", signedIn)

	require.Equal(t, http.StatusNotFound, refused.Code, "body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), identitypasskey.CodeNoSuchKey)
	assert.Len(t, h.store.byCustomer[testCustomer], 2,
		"a spelling the listing never issued removes nothing")

	accepted := h.remove(t, canonical, signedIn)
	require.Equal(t, http.StatusNoContent, accepted.Code,
		"and the canonical spelling of the SAME key still works: %s", accepted.Body.String())
}

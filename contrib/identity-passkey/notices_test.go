package identitypasskey_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
)

// The rules of the passkey notices (ADR 0381).

// recordingKeyNotices records the notices it was asked to send, with how many
// keys the account held at that moment. Given a context already ended it sends
// nothing, as a messenger that honors its context does.
type recordingKeyNotices struct {
	mu    sync.Mutex
	store *memoryCredentials
	sent  []string
	err   error
}

func (n *recordingKeyNotices) record(ctx context.Context, verb, customerID, keyID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	n.sent = append(n.sent, fmt.Sprintf("%s %s %s holding %d",
		verb, customerID, keyID, len(n.store.byCustomer[customerID])))

	return n.err
}

func (n *recordingKeyNotices) SendPasskeyAdded(ctx context.Context, customerID, keyID string) error {
	return n.record(ctx, "added", customerID, keyID)
}

func (n *recordingKeyNotices) SendPasskeyRemoved(ctx context.Context, customerID, keyID string) error {
	return n.record(ctx, "removed", customerID, keyID)
}

// noticedHarness is a harness whose module tells notices, over the in-memory
// store.
func noticedHarness(
	t *testing.T, notices identitypasskey.KeyNotices, other identitypasskey.OtherSignIn,
) *harness {
	t.Helper()

	store := &memoryCredentials{}
	h := newHarnessWithStore(t, other, store, func(o *identitypasskey.Options) {
		o.KeyNotices = notices
	})
	h.store = store
	if recording, ok := notices.(*recordingKeyNotices); ok && recording != nil {
		recording.store = store
	}

	return h
}

// keyIDOf is a key's id as the listing shows it.
func keyIDOf(credential virtualwebauthn.Credential) string {
	return base64.RawURLEncoding.EncodeToString(credential.ID)
}

// TestAnAccountIsToldOfEveryKeyAddedAndRemoved: each key registered and each
// key removed is told, with the key's id, once the store holds the change.
func TestAnAccountIsToldOfEveryKeyAddedAndRemoved(t *testing.T) {
	t.Parallel()

	notices := &recordingKeyNotices{}
	h := noticedHarness(t, notices, alwaysAnotherWayIn{})
	signedIn := h.signedInAs(t, testCustomer)
	first := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	second := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	require.Equal(t, http.StatusNoContent, h.register(t, signedIn, first).Code)
	require.Equal(t, http.StatusNoContent, h.register(t, signedIn, second).Code)
	require.Equal(t, http.StatusNoContent, h.remove(t, keyIDOf(first), signedIn).Code)

	assert.Equal(t, []string{
		"added " + testCustomer + " " + keyIDOf(first) + " holding 1",
		"added " + testCustomer + " " + keyIDOf(second) + " holding 2",
		"removed " + testCustomer + " " + keyIDOf(first) + " holding 1",
	}, notices.sent)
}

// TestAKeyChangeThatDidNotHappenTellsNobody: a refused or failed registration
// or removal is answered as before and tells nobody.
func TestAKeyChangeThatDidNotHappenTellsNobody(t *testing.T) {
	t.Parallel()

	held := webauthn.Credential{ID: []byte("held-key"), PublicKey: []byte("pk")}
	cases := []struct {
		name  string
		other identitypasskey.OtherSignIn
		want  int
		act   func(t *testing.T, h *harness, signedIn *http.Cookie) int
	}{
		{"a finish with no ceremony", alwaysAnotherWayIn{}, http.StatusUnprocessableEntity,
			func(t *testing.T, h *harness, signedIn *http.Cookie) int {
				begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
				options, err := virtualwebauthn.ParseAttestationOptions(begun.Body.String())
				require.NoError(t, err)
				attestation := virtualwebauthn.CreateAttestationResponse(h.rp, h.auth,
					virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2), *options)

				return h.post(t, "/store/v1/auth/passkey/register/finish", attestation, signedIn).Code
			}},
		{"a refused ceremony", alwaysAnotherWayIn{}, http.StatusUnauthorized,
			func(t *testing.T, h *harness, signedIn *http.Cookie) int {
				h.rp.Origin = "https://elsewhere.test"

				return h.register(t, signedIn, virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)).Code
			}},
		{"a key that could not be stored", alwaysAnotherWayIn{}, http.StatusInternalServerError,
			func(t *testing.T, h *harness, signedIn *http.Cookie) int {
				h.store.putErr = errors.New("the database is down")

				return h.register(t, signedIn, virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)).Code
			}},
		{"a key that is not the caller's", alwaysAnotherWayIn{}, http.StatusNotFound,
			func(t *testing.T, h *harness, signedIn *http.Cookie) int {
				return h.remove(t, base64.RawURLEncoding.EncodeToString([]byte("not-theirs")), signedIn).Code
			}},
		{"the last way in", identitypasskey.NoOtherSignIn(), http.StatusConflict,
			func(t *testing.T, h *harness, signedIn *http.Cookie) int {
				return h.remove(t, base64.RawURLEncoding.EncodeToString(held.ID), signedIn).Code
			}},
		{"a way in nobody could confirm", cannotSay{}, http.StatusInternalServerError,
			func(t *testing.T, h *harness, signedIn *http.Cookie) int {
				return h.remove(t, base64.RawURLEncoding.EncodeToString(held.ID), signedIn).Code
			}},
		{"a removal that could not be written", alwaysAnotherWayIn{}, http.StatusInternalServerError,
			func(t *testing.T, h *harness, signedIn *http.Cookie) int {
				h.store.removeErr = errors.New("the database is down")

				return h.remove(t, base64.RawURLEncoding.EncodeToString(held.ID), signedIn).Code
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			notices := &recordingKeyNotices{}
			h := noticedHarness(t, notices, tc.other)
			require.NoError(t, h.store.Put(t.Context(), testCustomer, held))

			assert.Equal(t, tc.want, tc.act(t, h, h.signedInAs(t, testCustomer)))
			assert.Empty(t, notices.sent)
		})
	}
}

// TestARepeatedRegistrationIsNotASecondNotice: a key the account holds, sent
// again, writes nothing and tells nobody a second time.
func TestARepeatedRegistrationIsNotASecondNotice(t *testing.T) {
	t.Parallel()

	notices := &recordingKeyNotices{}
	h := noticedHarness(t, notices, alwaysAnotherWayIn{})
	signedIn := h.signedInAs(t, testCustomer)
	key := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	require.Equal(t, http.StatusNoContent, h.register(t, signedIn, key).Code)
	require.Equal(t, http.StatusNoContent, h.register(t, signedIn, key).Code)
	assert.Equal(t, []string{"added " + testCustomer + " " + keyIDOf(key) + " holding 1"}, notices.sent)
}

// TestAKeyNoticeThatCannotBeSentFailsNothing: the change has been made, so a
// notice that fails is logged, with the customer and the key an operator needs
// to reach the person another way, and the request is answered as if it went.
func TestAKeyNoticeThatCannotBeSentFailsNothing(t *testing.T) {
	t.Parallel()

	notices := &recordingKeyNotices{err: errors.New("the mail server is down")}
	h := noticedHarness(t, notices, alwaysAnotherWayIn{})
	signedIn := h.signedInAs(t, testCustomer)
	key := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	require.Equal(t, http.StatusNoContent, h.register(t, signedIn, key).Code)
	require.Len(t, h.store.byCustomer[testCustomer], 1, "the key is stored")
	require.Equal(t, http.StatusNoContent, h.remove(t, keyIDOf(key), signedIn).Code)
	assert.Empty(t, h.store.byCustomer[testCustomer], "and removed")

	assert.Len(t, notices.sent, 2, "both notices were tried")
	for _, verb := range []string{"added", "removed"} {
		assert.Contains(t, h.logs.String(),
			`level=WARN msg="identity-passkey: an account could not be told a passkey was `+verb+
				`" customer_id=`+testCustomer+" key_id="+keyIDOf(key))
	}
}

// TestAKeyNoticeOutlivesTheCallerHangingUp: a caller that hangs up the moment
// the change is written does not cancel the notice, which is for the account's
// owner and not for whoever holds the connection.
func TestAKeyNoticeOutlivesTheCallerHangingUp(t *testing.T) {
	t.Parallel()

	notices := &recordingKeyNotices{}
	h := noticedHarness(t, notices, alwaysAnotherWayIn{})
	signedIn := h.signedInAs(t, testCustomer)
	key := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
	options, err := virtualwebauthn.ParseAttestationOptions(begun.Body.String())
	require.NoError(t, err)
	attestation := virtualwebauthn.CreateAttestationResponse(h.rp, h.auth, key, *options)

	registering, hangUp := context.WithCancel(t.Context())
	defer hangUp()
	h.store.afterWrite = hangUp
	require.Equal(t, http.StatusNoContent, h.doIn(registering, http.MethodPost,
		"/store/v1/auth/passkey/register/finish", attestation, signedIn, ceremonyCookieOf(t, begun)).Code)

	removing, hangUpAgain := context.WithCancel(t.Context())
	defer hangUpAgain()
	h.store.afterWrite = hangUpAgain
	require.Equal(t, http.StatusNoContent, h.doIn(removing, http.MethodDelete,
		"/store/v1/auth/passkey/keys/"+url.PathEscape(keyIDOf(key)), "", signedIn).Code)

	assert.Equal(t, []string{
		"added " + testCustomer + " " + keyIDOf(key) + " holding 1",
		"removed " + testCustomer + " " + keyIDOf(key) + " holding 0",
	}, notices.sent)
}

// stallingKeyNotices waits for its context to end, as a messenger whose server
// never answers.
type stallingKeyNotices struct{}

func (stallingKeyNotices) SendPasskeyAdded(ctx context.Context, _, _ string) error {
	<-ctx.Done()

	return ctx.Err()
}

func (stallingKeyNotices) SendPasskeyRemoved(ctx context.Context, _, _ string) error {
	<-ctx.Done()

	return ctx.Err()
}

// TestAStalledKeyNoticeDoesNotHoldTheRequest: a notice that never returns is
// ended by its own bound, and the person is answered.
func TestAStalledKeyNoticeDoesNotHoldTheRequest(t *testing.T) {
	t.Parallel()

	h := noticedHarness(t, stallingKeyNotices{}, alwaysAnotherWayIn{})
	identitypasskey.SetNoticeTimeout(h.module, 200*time.Millisecond)
	signedIn := h.signedInAs(t, testCustomer)
	key := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	begun := h.post(t, "/store/v1/auth/passkey/register/begin", "", signedIn)
	options, err := virtualwebauthn.ParseAttestationOptions(begun.Body.String())
	require.NoError(t, err)
	attestation := virtualwebauthn.CreateAttestationResponse(h.rp, h.auth, key, *options)
	ceremony := ceremonyCookieOf(t, begun)

	answered := make(chan int, 1)
	go func() {
		answered <- h.doIn(context.Background(), http.MethodPost,
			"/store/v1/auth/passkey/register/finish", attestation, signedIn, ceremony).Code
	}()
	select {
	case code := <-answered:
		assert.Equal(t, http.StatusNoContent, code)
	case <-time.After(10 * time.Second):
		t.Fatal("the registration was still waiting on the notice after ten seconds")
	}
}

// TestNoKeyNoticesIsAnOption: without notices bound, or with notices that hold
// a nil pointer, registering and removing work as they did.
func TestNoKeyNoticesIsAnOption(t *testing.T) {
	t.Parallel()

	for name, notices := range map[string]identitypasskey.KeyNotices{
		"none":          nil,
		"a nil pointer": (*recordingKeyNotices)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := noticedHarness(t, notices, alwaysAnotherWayIn{})
			signedIn := h.signedInAs(t, testCustomer)
			key := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
			require.NotPanics(t, func() {
				assert.Equal(t, http.StatusNoContent, h.register(t, signedIn, key).Code)
				assert.Equal(t, http.StatusNoContent, h.remove(t, keyIDOf(key), signedIn).Code)
			})
		})
	}
}

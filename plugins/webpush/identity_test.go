package webpush

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The customer a subscription binds its device to (ADR 0440, D293).

// headerIdentity proves the customer a test header names, and counts the
// requests it was asked about.
type headerIdentity struct {
	asked atomic.Int32
}

const provenHeader = "X-Test-Customer"

func (h *headerIdentity) CustomerID(r *http.Request) (string, error) {
	h.asked.Add(1)
	switch id := r.Header.Get(provenHeader); id {
	case "":
		return "", errors.New("no session")
	case "down":
		return "", coreerrors.Unavailable("test_identity_down", "the session store is down")
	default:
		return id, nil
	}
}

// moduleWithIdentity is a module whose container holds what is given under
// the identity's name, nothing when it is nil.
func moduleWithIdentity(t *testing.T, provided any) *webpushModule {
	t.Helper()

	c := container.New(slog.New(slog.DiscardHandler))
	if provided != nil {
		require.NoError(t, c.Provide(corehttp.IdentityName, provided))
	}
	m := newModule(moduleOptions{})
	m.identity = &identityBinding{c: c, log: slog.New(slog.DiscardHandler)}

	return m
}

// provingRequest is a subscription request proving the customer, none when
// empty.
func provingRequest(customerID string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, pathSubscribe, http.NoBody)
	if customerID != "" {
		r.Header.Set(provenHeader, customerID)
	}

	return r
}

// TestADeviceIsBoundOnlyToTheCustomerTheRequestProves is the rule: the claim
// is put to the identity, and the proven customer is what the row gets.
func TestADeviceIsBoundOnlyToTheCustomerTheRequestProves(t *testing.T) {
	identity := &headerIdentity{}
	m := moduleWithIdentity(t, identity)

	bound, err := m.boundCustomer(provingRequest("cus_A"), " cus_A ")
	require.NoError(t, err)
	assert.Equal(t, "cus_A", bound)

	_, err = m.boundCustomer(provingRequest("cus_B"), "cus_A")
	require.Error(t, err, "a request proving another customer binds nothing")
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindForbidden), "%v", err)
	assert.Equal(t, corehttp.CodeIdentityMismatch, coreerrors.CodeOf(err), "%v", err)

	_, err = m.boundCustomer(provingRequest(""), "cus_A")
	require.Error(t, err, "a request proving nobody binds nothing")
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnauthorized), "%v", err)
	assert.Equal(t, corehttp.CodeIdentityRefused, coreerrors.CodeOf(err), "%v", err)

	_, err = m.boundCustomer(provingRequest("down"), "cus_A")
	require.Error(t, err, "an identity that cannot answer is no proof")
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable), "%v", err)
}

// TestABodyNamingNobodyIsNeverAsked keeps a guest's subscription off the
// identity: a device bound to nobody needs no proof.
func TestABodyNamingNobodyIsNeverAsked(t *testing.T) {
	identity := &headerIdentity{}
	m := moduleWithIdentity(t, identity)

	for _, claimed := range []string{"", "   "} {
		bound, err := m.boundCustomer(provingRequest(""), claimed)
		require.NoError(t, err)
		assert.Empty(t, bound)
	}
	assert.Zero(t, identity.asked.Load())
}

// TestWithNoIdentityBoundACustomerIsRefused is fail-closed: an installation
// that bound no identity binds no device to a customer.
func TestWithNoIdentityBoundACustomerIsRefused(t *testing.T) {
	for name, m := range map[string]*webpushModule{
		"nothing bound":      moduleWithIdentity(t, nil),
		"module not wired":   newModule(moduleOptions{}),
		"binding of nothing": {identity: &identityBinding{}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := m.boundCustomer(provingRequest("cus_A"), "cus_A")
			require.Error(t, err)
			assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnauthorized), "%v", err)
			assert.Equal(t, corehttp.CodeIdentityNotBound, coreerrors.CodeOf(err), "%v", err)

			bound, err := m.boundCustomer(provingRequest(""), "")
			require.NoError(t, err, "a guest's device still subscribes")
			assert.Empty(t, bound)
		})
	}
}

// TestAnIdentityOfTheWrongTypeIsAWiringFault is the third branch of the
// lookup: a name filled with something that is not an identity is the
// installation's fault, not the request's.
func TestAnIdentityOfTheWrongTypeIsAWiringFault(t *testing.T) {
	m := moduleWithIdentity(t, "not an identity")

	_, err := m.boundCustomer(provingRequest("cus_A"), "cus_A")
	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindInternal), "%v", err)
	assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err), "%v", err)
}

// TestTheIdentityIsResolvedOnFirstUse binds an identity provided after the
// module was wired, as an embedder's module provides it after every plugin.
func TestTheIdentityIsResolvedOnFirstUse(t *testing.T) {
	c := container.New(slog.New(slog.DiscardHandler))
	m := newModule(moduleOptions{})
	m.identity = &identityBinding{c: c, log: slog.New(slog.DiscardHandler)}

	require.NoError(t, c.Provide(corehttp.IdentityName, &headerIdentity{}))

	bound, err := m.boundCustomer(provingRequest("cus_A"), "cus_A")
	require.NoError(t, err)
	assert.Equal(t, "cus_A", bound)
}

// TestARefusedSubscriptionReachesNoStore holds the handler's two refusals in
// the unit lane: an endpoint off the rule and a customer the request does not
// prove are each answered before the store is touched. The module has no
// store, so a refusal that fell through would panic rather than pass.
func TestARefusedSubscriptionReachesNoStore(t *testing.T) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	secret := make([]byte, authSecretLength)
	_, err = rand.Read(secret)
	require.NoError(t, err)
	keys := map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(secret),
	}

	for name, tc := range map[string]struct {
		endpoint, customerID, proven string
		status                       int
		code                         string
	}{
		"a loopback endpoint": {
			endpoint: "http://127.0.0.1:8080/internal", status: http.StatusUnprocessableEntity,
			code: codeEndpointRefused,
		},
		"a host beside a push service": {
			endpoint: "https://fcm.googleapis.com.attacker.test/p", status: http.StatusUnprocessableEntity,
			code: codeEndpointRefused,
		},
		"a customer the request does not prove": {
			endpoint: "https://fcm.googleapis.com/fcm/send/x", customerID: "cust_victim",
			status: http.StatusUnauthorized,
		},
		"a customer another request proves": {
			endpoint: "https://fcm.googleapis.com/fcm/send/y", customerID: "cust_victim",
			proven: "cust_stranger", status: http.StatusForbidden, code: corehttp.CodeIdentityMismatch,
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := moduleWithIdentity(t, corehttp.Identity(&headerIdentity{}))
			require.Nil(t, m.store, "the test is about a handler that never reaches the store")

			body, err := json.Marshal(map[string]any{
				"endpoint": tc.endpoint, "keys": keys, "customer_id": tc.customerID,
			})
			require.NoError(t, err)
			r := httptest.NewRequest(http.MethodPost, pathSubscribe, bytes.NewReader(body))
			if tc.proven != "" {
				r.Header.Set(provenHeader, tc.proven)
			}
			w := httptest.NewRecorder()

			m.handleSubscribe(w, r)

			assert.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.code != "" {
				assert.Contains(t, w.Body.String(), tc.code)
			}
		})
	}
}

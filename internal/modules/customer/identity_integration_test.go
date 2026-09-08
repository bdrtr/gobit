//go:build integration

package customer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer"
)

// This file is ADR 0043's seam end to end, and it is the only place the whole
// chain runs at once: an embedder's implementation registered in a container
// under corehttp.IdentityName, the module's own Register, the real chi tree,
// and a customer row that really exists in PostgreSQL.
//
// Every link was already held alone — the core's Provide/Resolve, the module's
// lazy wrapper, the handler's comparison — and the JOIN was held by nothing.
// The unit half of this pin lives in wiring_internal_test.go, which needs no
// database because its requests never reach one; here the request goes all the
// way to the row and back, which is what says the identity gate lets the
// PROVEN customer through rather than merely refusing everybody.

// poolServiceName is the name the composition root registers the core pool
// under (internal/app, svcDB). The module resolves it by the same string, and
// spelling it here is what lets this test build the module the way the app does
// rather than by reaching into it.
const poolServiceName = "core.db"

// provingIdentity is the embedder's verifier: a session cookie, a JWT, a header
// from an upstream proxy — reduced to the one thing gobit is allowed to know
// about it, the identifier it proves.
type provingIdentity struct{ id string }

var _ corehttp.Identity = provingIdentity{}

func (p provingIdentity) CustomerID(*http.Request) (string, error) { return p.id, nil }

// wiredStorefront builds the customer module the way the composition root does
// and returns the router its storefront requests travel through.
func wiredStorefront(ctx context.Context, t *testing.T, identity corehttp.Identity) chi.Router {
	t.Helper()

	c := container.New(nil)
	require.NoError(t, c.Provide(poolServiceName, testPool))
	if identity != nil {
		require.NoError(t, c.Provide(corehttp.IdentityName, identity))
	}

	m := customer.New(nil)
	require.NoError(t, m.Register(ctx, c))

	r := chi.NewRouter()
	m.Routes(r)

	return r
}

// storeGet drives one storefront GET against the wired router.
func storeGet(ctx context.Context, t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody))

	return rec
}

// TestTheEmbeddersIdentityDecidesWhoseAddressBookIsServed is the property
// ADR 0043 exists for, measured rather than assembled from parts.
//
// Two customers exist in the database and the bound identity proves ONE of
// them. The same route, the same wiring, the same publishable-key-less test
// router: the only difference between the two requests is which customer the
// path names. That pairing is what says the reason is the PROOF — a gate that
// refused everything would satisfy the second half alone, and a wiring that
// dropped the identity would satisfy neither.
func TestTheEmbeddersIdentityDecidesWhoseAddressBookIsServed(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sahip := yeniHesap(ctx, t, svc)
	yabanci := yeniHesap(ctx, t, svc)

	_, err := svc.CreateAddress(ctx, sahip.ID, gecerliAdres())
	require.NoError(t, err)

	r := wiredStorefront(ctx, t, provingIdentity{id: sahip.ID})

	t.Run("the proven customer is served", func(t *testing.T) {
		rec := storeGet(ctx, t, r, "/store/v1/customers/"+sahip.ID+"/addresses")

		require.Equal(t, http.StatusOK, rec.Code,
			"an identity registered under %q proved %q and the address book still "+
				"refused it. The contract's whole point is that an embedder who binds "+
				"one gets served.\nbody: %s", corehttp.IdentityName, sahip.ID, rec.Body.String())

		var body struct {
			Data []struct {
				CustomerID string `json:"customer_id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
		require.Len(t, body.Data, 1)
		assert.Equal(t, sahip.ID, body.Data[0].CustomerID,
			"the row served has to belong to the customer the identity proved")
	})

	t.Run("a stranger's address book is refused", func(t *testing.T) {
		rec := storeGet(ctx, t, r, "/store/v1/customers/"+yabanci.ID+"/addresses")

		require.Equal(t, http.StatusForbidden, rec.Code,
			"knowing %q was enough to read that person's address book, which is the "+
				"measurement ADR 0008 made and this decision closed.\nbody: %s",
			yabanci.ID, rec.Body.String())
		assert.Equal(t, corehttp.CodeIdentityMismatch, refusalCodeOf(t, rec))
	})
}

// TestAnInstallationThatBoundNoIdentityLosesTheAddressBook is the upgrade
// consequence, on a real installation rather than on a hand-built handler.
//
// It is the negative half of the same wiring: nothing is registered under
// corehttp.IdentityName, the lazy wrapper finds nothing on the first request,
// and the route refuses with the code that names the missing binding. An
// operator upgrading from v0.8.0 without binding one reads this exact body.
func TestAnInstallationThatBoundNoIdentityLosesTheAddressBook(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sahip := yeniHesap(ctx, t, svc)

	r := wiredStorefront(ctx, t, nil)

	rec := storeGet(ctx, t, r, "/store/v1/customers/"+sahip.ID+"/addresses")

	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"an installation that bound no identity answered %d. ADR 0043 chose the "+
			"CLOSED row: an absent identity does not mean every caller is who they say "+
			"they are, it means nobody looked.\nbody: %s", rec.Code, rec.Body.String())
	assert.Equal(t, corehttp.CodeIdentityNotBound, refusalCodeOf(t, rec))
}

// refusalCodeOf reads the machine-readable code out of an error response.
func refusalCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"the error body could not be read: %s", rec.Body.String())

	return body.Error.Code
}

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
)

// provingIdentity proves the customer it names, or fails with its error.
type provingIdentity struct {
	customer string
	err      error
}

func (p provingIdentity) CustomerID(*http.Request) (string, error) { return p.customer, p.err }

// storefrontRouter mounts the handler with the given identity; nil binds none.
func storefrontRouter(svc *fakePayments, identity corehttp.Identity) chi.Router {
	r := chi.NewRouter()
	handler := api.New(svc)
	if identity != nil {
		handler = handler.WithIdentity(identity)
	}
	handler.Routes(r)

	return r
}

// storefrontGet sends a storefront GET, which carries no principal.
func storefrontGet(r chi.Router, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec
}

// balanceRoutes are the two storefront balance reads, each with the ledger
// query the fake records for it.
var balanceRoutes = []struct {
	name    string
	path    string
	queried func(*fakePayments) [2]string
}{
	{
		name: "store credit", path: "/store/v1/customers/cus_1/store-credit/balance?currency_code=try",
		queried: func(f *fakePayments) [2]string { return f.lastCreditQuery },
	},
	{
		name: "loyalty points", path: "/store/v1/customers/cus_1/loyalty-points/balance?currency_code=try",
		queried: func(f *fakePayments) [2]string { return f.lastLoyaltyQuery },
	},
}

// TestACustomerReadsTheirOwnBalances serves the proven customer their store
// credit and their points, from the ledger the path and the currency name
// (ADR 0253).
func TestACustomerReadsTheirOwnBalances(t *testing.T) {
	t.Parallel()

	for _, route := range balanceRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakePayments{creditBalance: 12_500, loyaltyBalance: 340}
			rec := storefrontGet(storefrontRouter(svc, provingIdentity{customer: "cus_1"}), route.path)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, [2]string{"cus_1", "try"}, route.queried(svc))

			var envelope struct {
				Data struct {
					CustomerID   string `json:"customer_id"`
					CurrencyCode string `json:"currency_code"`
					Balance      *int64 `json:"balance"`
					Points       *int64 `json:"points"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
			assert.Equal(t, "cus_1", envelope.Data.CustomerID)
			assert.Equal(t, "TRY", envelope.Data.CurrencyCode)
			switch {
			case envelope.Data.Balance != nil:
				assert.Equal(t, int64(12_500), *envelope.Data.Balance)
			case envelope.Data.Points != nil:
				assert.Equal(t, int64(340), *envelope.Data.Points)
			default:
				t.Fatalf("the answer carries neither a balance nor points: %s", rec.Body.String())
			}
		})
	}
}

// TestABalanceIsReadOnlyForTheProvenCustomer refuses every request whose
// customer is not proven, and asks the ledger nothing: somebody else's
// customer is refused with 403, an installation with no identity with 401, and
// an identity's own error is passed through.
func TestABalanceIsReadOnlyForTheProvenCustomer(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		identity corehttp.Identity
		status   int
		code     string
	}{
		"somebody else":         {identity: provingIdentity{customer: "cus_2"}, status: http.StatusForbidden, code: corehttp.CodeIdentityMismatch},
		"no identity bound":     {status: http.StatusUnauthorized, code: corehttp.CodeIdentityNotBound},
		"a session that failed": {identity: provingIdentity{err: errors.Unauthorized("session_expired", "the session expired")}, status: http.StatusUnauthorized, code: "session_expired"},
		"an empty proof":        {identity: provingIdentity{}, status: http.StatusInternalServerError, code: corehttp.CodeIdentityUnproven},
	} {
		for _, route := range balanceRoutes {
			t.Run(name+"/"+route.name, func(t *testing.T) {
				t.Parallel()

				svc := &fakePayments{creditBalance: 12_500, loyaltyBalance: 340}
				rec := storefrontGet(storefrontRouter(svc, tc.identity), route.path)

				assert.Equal(t, tc.status, rec.Code, rec.Body.String())
				assert.Contains(t, rec.Body.String(), tc.code)
				assert.Equal(t, [2]string{}, route.queried(svc), "the ledger is not asked")
			})
		}
	}
}

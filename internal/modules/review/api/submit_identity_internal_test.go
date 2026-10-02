package api

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// submissions is a review service that records what it was asked to submit.
type submissions struct {
	Reviews
	got   *service.SubmitInput
	calls int
}

func (s *submissions) Submit(_ context.Context, in service.SubmitInput) (models.Review, error) {
	s.calls++
	s.got = &in

	return models.Review{ID: "rev_1", ProductID: in.ProductID, Status: models.StatusSubmitted}, nil
}

// answering is a customer identity reduced to its answer.
type answering struct {
	id  string
	err error
}

func (a answering) CustomerID(*http.Request) (string, error) { return a.id, a.err }

// TestTheStorefrontSubmissionAsksWhoTheRequestProves is ADR 0372 at the HTTP
// edge: the customer a request proves reaches the service, a refusal of any
// kind writes the review as a stranger's, and an identity that could not check
// ends the submission before anything is written.
func TestTheStorefrontSubmissionAsksWhoTheRequestProves(t *testing.T) {
	t.Parallel()

	const body = `{"rating":5,"body":"lovely","author_name":"A customer"}`
	for name, tc := range map[string]struct {
		identity corehttp.Identity
		status   int
		customer string
	}{
		"nothing bound":      {identity: nil, status: http.StatusCreated},
		"a proven customer":  {identity: answering{id: "cust_PROVEN"}, status: http.StatusCreated, customer: "cust_PROVEN"},
		"no session":         {identity: answering{err: stderrors.New("no session")}, status: http.StatusCreated},
		"an identity outage": {identity: answering{err: coreerrors.Unavailable("x", "down")}, status: http.StatusServiceUnavailable},
	} {
		svc := &submissions{}
		r := chi.NewRouter()
		New(svc).WithIdentity(tc.identity).Routes(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/store/v1/products/prod_1/reviews", strings.NewReader(body)))

		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
		if tc.status != http.StatusCreated {
			assert.Zero(t, svc.calls, "%s: nothing was submitted", name)

			continue
		}
		require.NotNil(t, svc.got, name)
		assert.Equal(t, tc.customer, svc.got.CustomerID, "%s: the customer comes from the identity", name)
	}

	// And never from the body: a body naming a customer is no review at all.
	svc := &submissions{}
	r := chi.NewRouter()
	New(svc).WithIdentity(answering{id: "cust_PROVEN"}).Routes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/store/v1/products/prod_1/reviews",
		strings.NewReader(`{"rating":5,"body":"lovely","author_name":"A customer","customer_id":"cust_CLAIMED"}`)))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, svc.calls)
}

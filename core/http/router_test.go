package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
)

func TestHealthEndpoint(t *testing.T) {
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "v1.2.3"})

	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, expected %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, expected %q", body.Status, "ok")
	}
	if body.Version != "v1.2.3" {
		t.Errorf("version = %q, expected %q", body.Version, "v1.2.3")
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "dev"})

	req := httptest.NewRequest(http.MethodGet, "/no-such-path", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, expected %d", rec.Code, http.StatusNotFound)
	}
}

// TestAPathNoRouteTakesIsAnsweredInTheEnvelope pins ADR 0458: chi's own
// answer is text/plain, and a client reading every error as JSON could not
// parse it. The mounted router is the shape a module's routes take, built
// after the root, so the answer has to reach it too.
func TestAPathNoRouteTakesIsAnsweredInTheEnvelope(t *testing.T) {
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "dev"})
	r.Route("/store/v1/carts", func(sub chi.Router) {
		sub.Get("/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})

	for _, path := range []string{"/no-such-path", "/store/v1/auth/register", "/store/v1/carts/cart_1/nothing"} {
		req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, expected %d", path, rec.Code, http.StatusNotFound)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", path, ct)
		}
		var body struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				RequestID string `json:"request_id"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: the body is not JSON: %v (%s)", path, err, rec.Body.String())
		}
		if body.Error.Code != corehttp.CodeRouteNotFound {
			t.Errorf("%s: code = %q, expected %q", path, body.Error.Code, corehttp.CodeRouteNotFound)
		}
		if body.Error.Message != "no route answers POST "+path {
			t.Errorf("%s: message = %q", path, body.Error.Message)
		}
		if body.Error.RequestID == "" {
			t.Errorf("%s: the answer carries no request id", path)
		}
	}
}

func TestHealthRejectsPost(t *testing.T) {
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "dev"})

	req := httptest.NewRequest(http.MethodPost, "/health", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, expected %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

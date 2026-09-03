package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

func TestHealthEndpoint(t *testing.T) {
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "v1.2.3"})

	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, beklenen %d", rec.Code, http.StatusOK)
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
		t.Errorf("status = %q, beklenen %q", body.Status, "ok")
	}
	if body.Version != "v1.2.3" {
		t.Errorf("version = %q, beklenen %q", body.Version, "v1.2.3")
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "dev"})

	req := httptest.NewRequest(http.MethodGet, "/no-such-path", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, beklenen %d", rec.Code, http.StatusNotFound)
	}
}

func TestHealthRejectsPost(t *testing.T) {
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "dev"})

	req := httptest.NewRequest(http.MethodPost, "/health", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, beklenen %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

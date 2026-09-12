package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOperatorStatusRequiresAuth(t *testing.T) {
	srv := &Server{
		Postgres: stubPing{},
		Redis:    stubPing{},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/operator/status", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOperatorStatusOK(t *testing.T) {
	srv := withAuth(&Server{
		Postgres: stubPing{},
		Redis:    stubPing{},
		Limits: Limits{
			MaxProjectsPerOwner: 3,
			MaxInflightDeploys:  1,
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/operator/status", nil)
	testHandler(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got operatorStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.MaxProjectsPerOwner != 3 || got.MaxInflightDeploys != 1 {
		t.Fatalf("got %+v", got)
	}
	if got.TLSEnabled {
		t.Fatal("SecureCookies defaults false")
	}
}

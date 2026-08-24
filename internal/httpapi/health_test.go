package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubPing is a StatusChecker that does not talk to a real store.
// Tests prove HTTP status/body mapping without Docker.
type stubPing struct {
	err error
}

func (s stubPing) Ping(context.Context) error { return s.err }

func TestHealthOK(t *testing.T) {
	srv := &Server{
		Postgres: stubPing{},
		Redis:    stubPing{},
	}
	// httptest records the response without opening a TCP port.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Postgres != "ok" || body.Redis != "ok" {
		t.Fatalf("body = %+v", body)
	}
}

func TestHealthDegradedWhenPostgresFails(t *testing.T) {
	// One store down must be 503 + degraded, not 500, and Redis should
	// still be reported independently.
	srv := &Server{
		Postgres: stubPing{err: errors.New("down")},
		Redis:    stubPing{},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "degraded" || body.Postgres != "error" || body.Redis != "ok" {
		t.Fatalf("body = %+v", body)
	}
}

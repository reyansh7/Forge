package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/reyansh7/Forge/internal/queue"
)

type stubJobs struct {
	err  error
	last queue.Job
}

func (s *stubJobs) Enqueue(_ context.Context, job queue.Job) error {
	s.last = job
	return s.err
}

func jobServer(q JobQueue) *Server {
	return &Server{
		Postgres: stubPing{},
		Redis:    stubPing{},
		Jobs:     q,
	}
}

func TestEnqueueJobAccepted(t *testing.T) {
	st := &stubJobs{}
	srv := jobServer(st)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader([]byte(`{"type":"example"}`)))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body enqueueJobResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "queued" || body.Type != queue.TypeExample || body.ID == "" {
		t.Fatalf("%+v", body)
	}
	if string(st.last.Payload) != "{}" {
		t.Fatalf("payload = %s", st.last.Payload)
	}
}

func TestEnqueueJobRejectsUnknownType(t *testing.T) {
	srv := jobServer(&stubJobs{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader([]byte(`{"type":"shell"}`)))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestEnqueueJobIgnoresClientCommandField(t *testing.T) {
	st := &stubJobs{}
	srv := jobServer(st)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader([]byte(`{"type":"example","command":"rm -rf /"}`)))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	if string(st.last.Payload) != "{}" {
		t.Fatalf("must not forward client payload: %s", st.last.Payload)
	}
}

func TestEnqueueJobServiceUnavailable(t *testing.T) {
	st := &stubJobs{err: errors.New("redis down")}
	srv := jobServer(st)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/jobs", bytes.NewReader([]byte(`{"type":"example"}`)))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHealthStillOKWithJobsWired(t *testing.T) {
	srv := jobServer(&stubJobs{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// Test operators. HTTP tests prove authorization with two users so a
// UUID in the path cannot be treated as proof of access.
const (
	testUserID       = "00000000-0000-4000-8000-aaaaaaaaaaa1"
	testOtherUserID  = "00000000-0000-4000-8000-aaaaaaaaaaa2"
	testSessionToken = "forge-test-session-token"
	testOtherToken   = "forge-test-other-session"
)

type memAuth struct {
	mu         sync.Mutex
	users      map[string]store.User
	byName     map[string]string
	sessions   map[string]string // token hash → user id
	sessionExp map[string]time.Time
	audits     []store.AuditEvent
}

func newMemAuth() *memAuth {
	return &memAuth{
		users:      map[string]store.User{},
		byName:     map[string]string{},
		sessions:   map[string]string{},
		sessionExp: map[string]time.Time{},
	}
}

func populatedAuth() *memAuth {
	m := newMemAuth()
	now := time.Now().UTC()
	m.users[testUserID] = store.User{ID: testUserID, Username: "operator", CreatedAt: now}
	m.users[testOtherUserID] = store.User{ID: testOtherUserID, Username: "other", CreatedAt: now}
	m.byName["operator"] = testUserID
	m.byName["other"] = testOtherUserID
	exp := now.Add(time.Hour)
	m.sessions[store.HashSessionToken(testSessionToken)] = testUserID
	m.sessions[store.HashSessionToken(testOtherToken)] = testOtherUserID
	m.sessionExp[store.HashSessionToken(testSessionToken)] = exp
	m.sessionExp[store.HashSessionToken(testOtherToken)] = exp
	return m
}

func (m *memAuth) UserCount(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *memAuth) CreateFirstUser(_ context.Context, username, passwordHash string) (store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.users) != 0 {
		return store.User{}, store.ErrConflict
	}
	return m.insertUserLocked(username, passwordHash)
}

func (m *memAuth) insertUserLocked(username, passwordHash string) (store.User, error) {
	name, err := store.ValidateUsername(username)
	if err != nil {
		return store.User{}, err
	}
	if _, ok := m.byName[toLower(name)]; ok {
		return store.User{}, store.ErrConflict
	}
	id := testUserID
	if len(m.users) != 0 {
		id = testOtherUserID
	}
	u := store.User{
		ID:           id,
		Username:     name,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().UTC(),
	}
	m.users[u.ID] = u
	m.byName[toLower(name)] = u.ID
	return u, nil
}

func (m *memAuth) GetUserByUsername(_ context.Context, username string) (store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byName[toLower(username)]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return m.users[id], nil
}

func (m *memAuth) LookupSession(_ context.Context, tokenHash string) (store.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.sessions[tokenHash]
	if !ok {
		return store.User{}, store.ErrUnauthorized
	}
	if time.Now().After(m.sessionExp[tokenHash]) {
		return store.User{}, store.ErrUnauthorized
	}
	return m.users[id], nil
}

func (m *memAuth) CreateSession(_ context.Context, userID, tokenHash string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[tokenHash] = userID
	m.sessionExp[tokenHash] = expiresAt
	return nil
}

func (m *memAuth) DeleteSession(_ context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, tokenHash)
	delete(m.sessionExp, tokenHash)
	return nil
}

func (m *memAuth) ClaimOrphanedProjects(context.Context, string) (int64, error) {
	return 0, nil
}

func (m *memAuth) RecordAudit(_ context.Context, e store.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audits = append(m.audits, e)
	return nil
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

func withAuth(s *Server) *Server {
	if s.Auth == nil {
		s.Auth = populatedAuth()
	}
	return s
}

func testHandler(s *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+testSessionToken)
		}
		s.Handler().ServeHTTP(w, r)
	})
}

func serveOther(s *Server, req *http.Request) *httptest.ResponseRecorder {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+testOtherToken)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

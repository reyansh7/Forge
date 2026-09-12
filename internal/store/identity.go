package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// ErrUnauthorized is a credential or session failure. HTTP maps it to
// 401. It is distinct from ErrNotFound (404) so handlers do not leak
// whether a username exists by using the same status for both — login
// still returns a generic 401 either way; this sentinel is for tests.
var ErrUnauthorized = errors.New("unauthorized")

// User is a control-plane operator. Deployed apps do not get rows here.
//
// Responsibility: identity for authorization. Projects belong to a user
// (owner_id). A UUID in a request path is not proof that this user may
// see the resource.
//
// Called by: HTTP auth handlers and middleware.
// PasswordHash is bcrypt. Never log it. Never put it in JSON responses.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// PublicUser is the JSON-safe view (no hash).
type PublicUser struct {
	ID        string
	Username  string
	CreatedAt time.Time
}

func (u User) Public() PublicUser {
	return PublicUser{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt}
}

const (
	minUsernameLen = 3
	maxUsernameLen = 32
	minPasswordLen = 10
	maxPasswordLen = 72 // bcrypt truncates after 72 bytes

	sessionTTL = 7 * 24 * time.Hour
)

// ValidateUsername is the signup/login contract. Lowercasing for lookup
// happens at query time; we persist the operator's chosen casing.
func ValidateUsername(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n < minUsernameLen || n > maxUsernameLen {
		return "", fmt.Errorf("username must be %d–%d characters", minUsernameLen, maxUsernameLen)
	}
	for i, r := range name {
		if r > unicode.MaxASCII || unicode.IsControl(r) {
			return "", fmt.Errorf("username contains invalid characters")
		}
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
		if i == 0 {
			ok = unicode.IsLetter(r)
		}
		if !ok {
			return "", fmt.Errorf("username must start with a letter and contain only letters, digits, _ or -")
		}
	}
	return name, nil
}

// ValidatePassword checks length only. We do not invent a complexity
// theatre (must include punctuation) — length is the practical bar for
// a local operator password. Hashing happens in HashPassword.
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < minPasswordLen || n > maxPasswordLen {
		return fmt.Errorf("password must be %d–%d characters", minPasswordLen, maxPasswordLen)
	}
	for _, r := range pw {
		if r == 0 {
			return fmt.Errorf("password contains invalid characters")
		}
	}
	return nil
}

// HashPassword is bcrypt. Cost is the library default (currently 10):
// slow enough to make bulk guessing expensive, fast enough for local
// login. The hash includes a salt — two hashes of the same password
// are not equal, so we CompareHashAndPassword at check time.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	sum, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(sum), nil
}

// CheckPassword compares plaintext to a stored bcrypt hash.
//
// subtle is not needed here — bcrypt.CompareHashAndPassword is already
// constant-time with respect to the password bytes. A mismatch is
// ErrUnauthorized, never a driver error, so HTTP can 401 without
// distinguishing "no such user" from "wrong password" at this layer.
func CheckPassword(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrUnauthorized
	}
	return nil
}

// NewSessionToken returns the raw token (give this to the client once)
// and the hex SHA-256 we persist. A stolen sessions table cannot be
// replayed: the cookie/bearer value is not recoverable from the hash.
func NewSessionToken() (raw string, hash string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", fmt.Errorf("session token: %w", err)
	}
	raw = hex.EncodeToString(buf[:])
	return raw, HashSessionToken(raw), nil
}

// HashSessionToken is hex(SHA-256(raw)). Used both when inserting a
// session and when looking one up from a cookie or Authorization header.
func HashSessionToken(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}

// TokenEqual is a constant-time compare for hashes we already computed.
// Defense in depth if a future caller compares raw tokens.
func TokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// SessionExpiry is now + sessionTTL. Tests can use a fixed clock by
// passing expiresAt into CreateSession directly.
func SessionExpiry(now time.Time) time.Time {
	return now.UTC().Add(sessionTTL)
}

// AuditEvent is one control-plane security record.
//
// Invariant: Metadata must not contain secrets (env values, passwords,
// session tokens, database URLs). Keys of env changes are OK; values
// are not. HTTP constructs the event; Postgres only persists it.
type AuditEvent struct {
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
	IP           string
	Metadata     map[string]string
}

// UserCount is used by bootstrap: the first operator may create an
// account only when this is zero. A race between two bootstraps is
// serialized with a table lock in CreateFirstUser.
func (p *Postgres) UserCount(ctx context.Context) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("user count: %w", err)
	}
	return n, nil
}

// CreateFirstUser inserts the bootstrap operator.
//
// LOCK TABLE users prevents two concurrent POSTs from both seeing
// count=0 and inserting two "first" users. That would skip the
// intended single-bootstrap gate. Later team invites are a different
// phase; extra users can still be inserted by tests via CreateUser.
func (p *Postgres) CreateFirstUser(ctx context.Context, username, passwordHash string) (User, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin bootstrap user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
		return User{}, fmt.Errorf("lock users: %w", err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if n != 0 {
		return User{}, ErrConflict
	}
	u, err := insertUser(ctx, tx, username, passwordHash)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit bootstrap user: %w", err)
	}
	return u, nil
}

// CreateUser inserts an operator without the bootstrap lock.
// POST /auth/signup uses this after the first user exists. Tests also
// call it directly. Team invites remain a later phase.
func (p *Postgres) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	return insertUser(ctx, p.db, username, passwordHash)
}

type queryable interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func insertUser(ctx context.Context, q queryable, username, passwordHash string) (User, error) {
	name, err := ValidateUsername(username)
	if err != nil {
		return User{}, err
	}
	if strings.TrimSpace(passwordHash) == "" {
		return User{}, fmt.Errorf("password hash is required")
	}
	var out User
	err = q.QueryRowContext(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING id::text, username, password_hash, created_at
	`, name, passwordHash).Scan(&out.ID, &out.Username, &out.PasswordHash, &out.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return out, nil
}

// GetUserByUsername is case-insensitive. Login should not fail because
// the operator typed "Rey" vs "rey".
func (p *Postgres) GetUserByUsername(ctx context.Context, username string) (User, error) {
	name := strings.TrimSpace(username)
	if name == "" {
		return User{}, ErrNotFound
	}
	var out User
	err := p.db.QueryRowContext(ctx, `
		SELECT id::text, username, password_hash, created_at
		FROM users
		WHERE lower(username) = lower($1)
	`, name).Scan(&out.ID, &out.Username, &out.PasswordHash, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return out, nil
}

// GetUser loads by id (session resolution).
func (p *Postgres) GetUser(ctx context.Context, id string) (User, error) {
	id, err := ParseUUID(id)
	if err != nil {
		return User{}, err
	}
	var out User
	err = p.db.QueryRowContext(ctx, `
		SELECT id::text, username, password_hash, created_at
		FROM users
		WHERE id = $1::uuid
	`, id).Scan(&out.ID, &out.Username, &out.PasswordHash, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return out, nil
}

// CreateSession persists token_hash (not the raw token). expiresAt
// should come from SessionExpiry. Expired rows are ignored on lookup.
func (p *Postgres) CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	userID, err := ParseUUID(userID)
	if err != nil {
		return err
	}
	if len(tokenHash) != 64 {
		return fmt.Errorf("invalid session token hash")
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at)
		VALUES ($1::uuid, $2, $3)
	`, userID, tokenHash, expiresAt.UTC())
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// LookupSession returns the user for a still-valid token hash.
func (p *Postgres) LookupSession(ctx context.Context, tokenHash string) (User, error) {
	if len(tokenHash) != 64 {
		return User{}, ErrUnauthorized
	}
	var out User
	err := p.db.QueryRowContext(ctx, `
		SELECT u.id::text, u.username, u.password_hash, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.expires_at > now()
	`, tokenHash).Scan(&out.ID, &out.Username, &out.PasswordHash, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup session: %w", err)
	}
	return out, nil
}

// DeleteSession revokes one token (logout). Missing rows are success —
// logout is idempotent.
func (p *Postgres) DeleteSession(ctx context.Context, tokenHash string) error {
	if tokenHash == "" {
		return nil
	}
	_, err := p.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// ClaimOrphanedProjects assigns pre-Phase-3 rows (owner_id IS NULL) to
// the bootstrap user. After that, NULL-owner projects are invisible to
// ListProjects and 404 on Get-by-owner checks.
func (p *Postgres) ClaimOrphanedProjects(ctx context.Context, ownerID string) (int64, error) {
	ownerID, err := ParseUUID(ownerID)
	if err != nil {
		return 0, err
	}
	res, err := p.db.ExecContext(ctx, `
		UPDATE projects
		SET owner_id = $1::uuid, updated_at = now()
		WHERE owner_id IS NULL
	`, ownerID)
	if err != nil {
		return 0, fmt.Errorf("claim projects: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RecordAudit inserts one event. Failure must not include the event
// payload in the returned error string (metadata could someday be
// mis-built). Callers log the error and continue — audit is not a
// reason to fail a deploy that already succeeded.
func (p *Postgres) RecordAudit(ctx context.Context, e AuditEvent) error {
	if strings.TrimSpace(e.Action) == "" || strings.TrimSpace(e.ResourceType) == "" {
		return fmt.Errorf("audit action and resource_type are required")
	}
	meta := e.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("audit metadata: %w", err)
	}
	var actor any
	if e.ActorID != "" {
		id, err := ParseUUID(e.ActorID)
		if err != nil {
			return err
		}
		actor = id
	}
	var resource any
	if e.ResourceID != "" {
		resource = e.ResourceID
	}
	var ip any
	if e.IP != "" {
		ip = e.IP
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO audit_events (actor_id, action, resource_type, resource_id, ip, metadata)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
	`, actor, e.Action, e.ResourceType, resource, ip, raw)
	if err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}
	return nil
}

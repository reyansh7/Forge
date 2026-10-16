package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// GitHubAccount is one operator's connected GitHub identity.
//
// AccessToken is the OAuth token. It is sealed at rest when crypter
// is set. HTTP must never put it on a JSON response. The worker uses
// it only as a git extraHeader so private clones do not put a PAT in
// the repository_url column.
type GitHubAccount struct {
	UserID       string
	GitHubUserID int64
	Login        string
	AccessToken  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ApplicationGitHub binds one application to owner/repo + branch.
//
// A push to that branch becomes a TypeDeploy job on this application
// and environment. WebhookSecret is HMAC-SHA256 material GitHub sends
// as X-Hub-Signature-256. Never log it or return it to the dashboard.
type ApplicationGitHub struct {
	ApplicationID string
	OwnerID       string
	RepoFullName  string
	Branch        string
	Environment   string
	WebhookID     int64
	WebhookSecret string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CommitMeta is git identity recorded on a deployment row.
//
// Empty on manual deploys. Auto-deploy fills it from the push payload
// so the dashboard can show which commit is live without a second
// GitHub round trip. Lengths are capped so a hostile payload cannot
// bloat the deployments table.
type CommitMeta struct {
	SHA     string
	Message string
	Author  string
}

var (
	repoFullNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	commitSHAPattern    = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
)

const (
	maxRepoFullName = 200
	maxBranchLen    = 255
	maxCommitMsg    = 400
	maxCommitAuthor = 200
	oauthStateTTL   = 10 * time.Minute
)

// ValidateRepoFullName accepts GitHub's owner/name form only.
func ValidateRepoFullName(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, ".git")
	if s == "" {
		return "", fmt.Errorf("repository is required")
	}
	if utf8.RuneCountInString(s) > maxRepoFullName {
		return "", fmt.Errorf("repository name is too long")
	}
	if !repoFullNamePattern.MatchString(s) {
		return "", fmt.Errorf("repository must be owner/name")
	}
	owner, name, _ := strings.Cut(s, "/")
	if strings.HasPrefix(owner, ".") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("repository must be owner/name")
	}
	return owner + "/" + name, nil
}

// ValidateBranch rejects git-ref metacharacters and refs/ prefixes.
//
// Callers pass the short name (main), not refs/heads/main. Dots and
// slashes are allowed (release/1.0) but `..` and leading slash are not
// so a value cannot become a path traversal inside a clone.
func ValidateBranch(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "refs/heads/")
	if s == "" {
		return "", fmt.Errorf("branch is required")
	}
	if utf8.RuneCountInString(s) > maxBranchLen {
		return "", fmt.Errorf("branch name is too long")
	}
	if strings.Contains(s, "..") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return "", fmt.Errorf("branch name is invalid")
	}
	if !branchPattern.MatchString(s) {
		return "", fmt.Errorf("branch name is invalid")
	}
	return s, nil
}

// GitHubCloneURL is the https remote stored on the application.
//
// No userinfo. The PAT stays in github_accounts and is injected at
// fetch time. HostFetcher already rejects userinfo on the URL.
func GitHubCloneURL(fullName string) string {
	fullName, err := ValidateRepoFullName(fullName)
	if err != nil {
		return ""
	}
	return "https://github.com/" + fullName + ".git"
}

// SanitizeCommitMeta caps and validates webhook-supplied identity.
func SanitizeCommitMeta(in CommitMeta) CommitMeta {
	sha := strings.TrimSpace(in.SHA)
	if sha != "" && !commitSHAPattern.MatchString(sha) {
		sha = ""
	}
	msg := strings.TrimSpace(in.Message)
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = msg[:i]
	}
	if utf8.RuneCountInString(msg) > maxCommitMsg {
		msg = string([]rune(msg)[:maxCommitMsg])
	}
	author := strings.TrimSpace(in.Author)
	if i := strings.IndexAny(author, "\r\n"); i >= 0 {
		author = author[:i]
	}
	if utf8.RuneCountInString(author) > maxCommitAuthor {
		author = string([]rune(author)[:maxCommitAuthor])
	}
	return CommitMeta{SHA: sha, Message: msg, Author: author}
}

// NewWebhookSecret is 32 random bytes, hex-encoded, for GitHub HMAC.
func NewWebhookSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webhook secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// NewOAuthNonce is the CSRF state sent to GitHub.
func NewOAuthNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("oauth state: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// SanitizeReturnTo allows only a same-origin dashboard path.
func SanitizeReturnTo(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") {
		return "/"
	}
	if strings.ContainsAny(s, "\r\n\\") || strings.Contains(s, "://") {
		return "/"
	}
	if utf8.RuneCountInString(s) > 256 {
		return "/"
	}
	return s
}

func (p *Postgres) sealSecret(plain string) (string, error) {
	if p.crypter == nil {
		return plain, nil
	}
	return p.crypter.Seal(plain)
}

func (p *Postgres) openSecret(stored string) (string, error) {
	if p.crypter == nil {
		return stored, nil
	}
	return p.crypter.Open(stored)
}

// UpsertGitHubAccount stores the OAuth token for this operator.
//
// Unique github_user_id prevents two Forge users from claiming the
// same GitHub identity (a stolen callback must not rebind silently).
func (p *Postgres) UpsertGitHubAccount(ctx context.Context, userID string, githubUserID int64, login, token string) (GitHubAccount, error) {
	userID, err := ParseUUID(userID)
	if err != nil {
		return GitHubAccount{}, err
	}
	login = strings.TrimSpace(login)
	token = strings.TrimSpace(token)
	if login == "" || token == "" || githubUserID <= 0 {
		return GitHubAccount{}, fmt.Errorf("github account is incomplete")
	}
	stored, err := p.sealSecret(token)
	if err != nil {
		return GitHubAccount{}, err
	}
	var out GitHubAccount
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO github_accounts (user_id, github_user_id, login, access_token)
		VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE
		SET github_user_id = EXCLUDED.github_user_id,
		    login = EXCLUDED.login,
		    access_token = EXCLUDED.access_token,
		    updated_at = now()
		RETURNING user_id::text, github_user_id, login, access_token, created_at, updated_at
	`, userID, githubUserID, login, stored).Scan(
		&out.UserID, &out.GitHubUserID, &out.Login, &out.AccessToken, &out.CreatedAt, &out.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return GitHubAccount{}, fmt.Errorf("%w: github account is already connected to another operator", ErrConflict)
	}
	if err != nil {
		return GitHubAccount{}, fmt.Errorf("upsert github account: %w", err)
	}
	plain, err := p.openSecret(out.AccessToken)
	if err != nil {
		return GitHubAccount{}, err
	}
	out.AccessToken = plain
	return out, nil
}

// GetGitHubAccount loads the operator's connection. ErrNotFound if none.
func (p *Postgres) GetGitHubAccount(ctx context.Context, userID string) (GitHubAccount, error) {
	userID, err := ParseUUID(userID)
	if err != nil {
		return GitHubAccount{}, err
	}
	var out GitHubAccount
	err = p.db.QueryRowContext(ctx, `
		SELECT user_id::text, github_user_id, login, access_token, created_at, updated_at
		FROM github_accounts
		WHERE user_id = $1::uuid
	`, userID).Scan(&out.UserID, &out.GitHubUserID, &out.Login, &out.AccessToken, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubAccount{}, ErrNotFound
	}
	if err != nil {
		return GitHubAccount{}, fmt.Errorf("get github account: %w", err)
	}
	plain, err := p.openSecret(out.AccessToken)
	if err != nil {
		return GitHubAccount{}, err
	}
	out.AccessToken = plain
	return out, nil
}

// GetGitHubAccountByGitHubUserID finds the Forge operator already
// linked to this GitHub user. Sign-in uses this, not the login string,
// so a GitHub name that matches a password account does not take it over.
func (p *Postgres) GetGitHubAccountByGitHubUserID(ctx context.Context, githubUserID int64) (GitHubAccount, error) {
	if githubUserID <= 0 {
		return GitHubAccount{}, ErrNotFound
	}
	var out GitHubAccount
	err := p.db.QueryRowContext(ctx, `
		SELECT user_id::text, github_user_id, login, access_token, created_at, updated_at
		FROM github_accounts
		WHERE github_user_id = $1
	`, githubUserID).Scan(&out.UserID, &out.GitHubUserID, &out.Login, &out.AccessToken, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubAccount{}, ErrNotFound
	}
	if err != nil {
		return GitHubAccount{}, fmt.Errorf("get github account by id: %w", err)
	}
	plain, err := p.openSecret(out.AccessToken)
	if err != nil {
		return GitHubAccount{}, err
	}
	out.AccessToken = plain
	return out, nil
}

// DeleteGitHubAccount removes the token. Bindings are deleted separately
// so HTTP can drop GitHub webhooks first (needs the token).
func (p *Postgres) DeleteGitHubAccount(ctx context.Context, userID string) error {
	userID, err := ParseUUID(userID)
	if err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `DELETE FROM github_accounts WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return fmt.Errorf("delete github account: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateOAuthState persists CSRF nonce for the connect redirect.
//
// user_id is the operator who is already signed in. Login uses
// CreateLoginOAuthState, which stores NULL, so a connect nonce cannot
// be mistaken for a sign-in.
func (p *Postgres) CreateOAuthState(ctx context.Context, userID, nonce, returnTo string) error {
	userID, err := ParseUUID(userID)
	if err != nil {
		return err
	}
	return p.insertOAuthState(ctx, userID, nonce, returnTo)
}

// CreateLoginOAuthState persists a sign-in nonce with no Forge user.
//
// The HTTP handler also sets an HttpOnly cookie of the same nonce.
// The callback accepts the login only when that cookie matches, so
// the database row alone is not a bearer credential.
func (p *Postgres) CreateLoginOAuthState(ctx context.Context, nonce, returnTo string) error {
	return p.insertOAuthState(ctx, "", nonce, returnTo)
}

func (p *Postgres) insertOAuthState(ctx context.Context, userID, nonce, returnTo string) error {
	nonce = strings.TrimSpace(nonce)
	if len(nonce) < 16 {
		return fmt.Errorf("oauth state is too short")
	}
	returnTo = SanitizeReturnTo(returnTo)
	var userArg any
	if userID != "" {
		userArg = userID
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO github_oauth_states (nonce, user_id, return_to, expires_at)
		VALUES ($1, $2::uuid, $3, $4)
	`, nonce, userArg, returnTo, time.Now().UTC().Add(oauthStateTTL))
	if err != nil {
		return fmt.Errorf("create oauth state: %w", err)
	}
	return nil
}

// ConsumeOAuthState deletes the nonce (single use).
//
// An empty userID means this was a sign-in, not a connect. Expired
// rows are not found. COALESCE turns SQL NULL into "" so Scan does
// not fail on a login row.
func (p *Postgres) ConsumeOAuthState(ctx context.Context, nonce string) (userID, returnTo string, err error) {
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return "", "", ErrNotFound
	}
	err = p.db.QueryRowContext(ctx, `
		DELETE FROM github_oauth_states
		WHERE nonce = $1 AND expires_at > now()
		RETURNING COALESCE(user_id::text, ''), return_to
	`, nonce).Scan(&userID, &returnTo)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("consume oauth state: %w", err)
	}
	return userID, SanitizeReturnTo(returnTo), nil
}

// UpsertApplicationGitHub binds repo+branch+environment to one app.
func (p *Postgres) UpsertApplicationGitHub(ctx context.Context, in ApplicationGitHub) (ApplicationGitHub, error) {
	appID, err := ParseUUID(in.ApplicationID)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	ownerID, err := ParseUUID(in.OwnerID)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	repo, err := ValidateRepoFullName(in.RepoFullName)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	branch, err := ValidateBranch(in.Branch)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	env, err := ParseEnvironment(in.Environment)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	secret := strings.TrimSpace(in.WebhookSecret)
	if secret == "" {
		return ApplicationGitHub{}, fmt.Errorf("webhook secret is required")
	}
	stored, err := p.sealSecret(secret)
	if err != nil {
		return ApplicationGitHub{}, err
	}

	var out ApplicationGitHub
	err = p.db.QueryRowContext(ctx, `
		INSERT INTO application_github (
			application_id, owner_id, repo_full_name, branch, environment, webhook_id, webhook_secret
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
		ON CONFLICT (application_id) DO UPDATE
		SET owner_id = EXCLUDED.owner_id,
		    repo_full_name = EXCLUDED.repo_full_name,
		    branch = EXCLUDED.branch,
		    environment = EXCLUDED.environment,
		    webhook_id = EXCLUDED.webhook_id,
		    webhook_secret = EXCLUDED.webhook_secret,
		    updated_at = now()
		RETURNING application_id::text, owner_id::text, repo_full_name, branch, environment,
		          COALESCE(webhook_id, 0), webhook_secret, created_at, updated_at
	`, appID, ownerID, repo, branch, env, nullInt64(in.WebhookID), stored).Scan(
		&out.ApplicationID, &out.OwnerID, &out.RepoFullName, &out.Branch, &out.Environment,
		&out.WebhookID, &out.WebhookSecret, &out.CreatedAt, &out.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return ApplicationGitHub{}, fmt.Errorf("%w: repository and branch are already connected", ErrConflict)
	}
	if err != nil {
		return ApplicationGitHub{}, fmt.Errorf("upsert application github: %w", err)
	}
	plain, err := p.openSecret(out.WebhookSecret)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	out.WebhookSecret = plain
	return out, nil
}

// GetApplicationGitHub loads the binding for one app.
func (p *Postgres) GetApplicationGitHub(ctx context.Context, applicationID string) (ApplicationGitHub, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	out, err := scanApplicationGitHub(p.db.QueryRowContext(ctx, applicationGitHubSelect+` WHERE application_id = $1::uuid`, applicationID))
	if err != nil {
		return ApplicationGitHub{}, err
	}
	return p.openApplicationGitHub(out)
}

// GetApplicationGitHubByRepoBranch is the webhook routing lookup.
func (p *Postgres) GetApplicationGitHubByRepoBranch(ctx context.Context, repo, branch string) (ApplicationGitHub, error) {
	repo, err := ValidateRepoFullName(repo)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	branch, err = ValidateBranch(branch)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	out, err := scanApplicationGitHub(p.db.QueryRowContext(ctx, applicationGitHubSelect+`
		WHERE lower(repo_full_name) = lower($1) AND lower(branch) = lower($2)
	`, repo, branch))
	if err != nil {
		return ApplicationGitHub{}, err
	}
	return p.openApplicationGitHub(out)
}

// ListApplicationGitHubByOwner is used on disconnect to drop hooks.
func (p *Postgres) ListApplicationGitHubByOwner(ctx context.Context, ownerID string) ([]ApplicationGitHub, error) {
	ownerID, err := ParseUUID(ownerID)
	if err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, applicationGitHubSelect+` WHERE owner_id = $1::uuid`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list application github: %w", err)
	}
	defer rows.Close()
	out := make([]ApplicationGitHub, 0)
	for rows.Next() {
		item, err := scanApplicationGitHubRow(rows)
		if err != nil {
			return nil, err
		}
		item, err = p.openApplicationGitHub(item)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteApplicationGitHub removes the binding. HTTP deletes the hook first.
func (p *Postgres) DeleteApplicationGitHub(ctx context.Context, applicationID string) error {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return err
	}
	res, err := p.db.ExecContext(ctx, `DELETE FROM application_github WHERE application_id = $1::uuid`, applicationID)
	if err != nil {
		return fmt.Errorf("delete application github: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteApplicationGitHubByOwner drops every binding for disconnect.
func (p *Postgres) DeleteApplicationGitHubByOwner(ctx context.Context, ownerID string) error {
	ownerID, err := ParseUUID(ownerID)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `DELETE FROM application_github WHERE owner_id = $1::uuid`, ownerID)
	if err != nil {
		return fmt.Errorf("delete application github by owner: %w", err)
	}
	return nil
}

// ClaimWebhookDelivery returns true when this delivery id is new.
//
// GitHub retries with the same X-GitHub-Delivery. A unique insert is
// the only lock we need: a crash after insert but before enqueue can
// drop that push (operator re-delivers or pushes again). Double enqueue
// is the worse failure.
func (p *Postgres) ClaimWebhookDelivery(ctx context.Context, deliveryID string) (bool, error) {
	deliveryID = strings.TrimSpace(deliveryID)
	if deliveryID == "" || len(deliveryID) < 8 || len(deliveryID) > 128 {
		return false, fmt.Errorf("delivery id is invalid")
	}
	res, err := p.db.ExecContext(ctx, `
		INSERT INTO github_webhook_deliveries (delivery_id)
		VALUES ($1)
		ON CONFLICT (delivery_id) DO NOTHING
	`, deliveryID)
	if err != nil {
		return false, fmt.Errorf("claim webhook delivery: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// GitHubCloneToken is the worker fetch credential for this application.
//
// A repo binding wins. If there is no binding, a github.com URL still
// uses the project owner's OAuth token. That is how a project created
// from the signed-in repo list can clone a private repository. The
// token stays out of repository_url. forge:// and other hosts get "".
func (p *Postgres) GitHubCloneToken(ctx context.Context, applicationID string) (string, error) {
	applicationID, err := ParseUUID(applicationID)
	if err != nil {
		return "", err
	}
	var stored string
	err = p.db.QueryRowContext(ctx, `
		SELECT ga.access_token
		FROM application_github ag
		JOIN github_accounts ga ON ga.user_id = ag.owner_id
		WHERE ag.application_id = $1::uuid
	`, applicationID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		err = p.db.QueryRowContext(ctx, `
			SELECT ga.access_token
			FROM applications a
			JOIN projects p ON p.id = a.project_id
			JOIN github_accounts ga ON ga.user_id = p.owner_id
			WHERE a.id = $1::uuid
			  AND a.repository_url ILIKE 'https://github.com/%'
		`, applicationID).Scan(&stored)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("github clone token: %w", err)
	}
	return p.openSecret(stored)
}

const applicationGitHubSelect = `
	SELECT application_id::text, owner_id::text, repo_full_name, branch, environment,
	       COALESCE(webhook_id, 0), webhook_secret, created_at, updated_at
	FROM application_github
`

func (p *Postgres) openApplicationGitHub(in ApplicationGitHub) (ApplicationGitHub, error) {
	plain, err := p.openSecret(in.WebhookSecret)
	if err != nil {
		return ApplicationGitHub{}, err
	}
	in.WebhookSecret = plain
	return in, nil
}

func scanApplicationGitHub(row *sql.Row) (ApplicationGitHub, error) {
	var out ApplicationGitHub
	err := row.Scan(
		&out.ApplicationID, &out.OwnerID, &out.RepoFullName, &out.Branch, &out.Environment,
		&out.WebhookID, &out.WebhookSecret, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ApplicationGitHub{}, ErrNotFound
	}
	if err != nil {
		return ApplicationGitHub{}, fmt.Errorf("get application github: %w", err)
	}
	return out, nil
}

func scanApplicationGitHubRow(rows *sql.Rows) (ApplicationGitHub, error) {
	var out ApplicationGitHub
	err := rows.Scan(
		&out.ApplicationID, &out.OwnerID, &out.RepoFullName, &out.Branch, &out.Environment,
		&out.WebhookID, &out.WebhookSecret, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return ApplicationGitHub{}, fmt.Errorf("scan application github: %w", err)
	}
	return out, nil
}

func nullInt64(n int64) any {
	if n <= 0 {
		return nil
	}
	return n
}

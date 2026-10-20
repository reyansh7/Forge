// Package githubapi is Forge's GitHub OAuth + REST + webhook HMAC
// boundary.
//
// What: a small net/http client, not the official SDK. Why: we need
// token exchange, repo list, hook create/delete, and signature verify.
// How: HTTPS to github.com / api.github.com with timeouts. Tokens are
// request-scoped arguments and must never be logged or returned to the
// dashboard. Tests inject an HTTP transport so unit tests never dial.
//
// Called by: httpapi (OAuth + connect repo + webhook). Not called by
// the worker — clone uses the stored token as a git extraHeader.
package githubapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	defaultAPI   = "https://api.github.com"
	defaultOAuth = "https://github.com"
	userAgent    = "forge-paas"
	maxBody      = 1 << 20
)

// User is the GitHub identity after OAuth.
type User struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// Repo is a repository the operator can see.
type Repo struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

// Push is the subset of a push event Forge needs to enqueue a deploy.
type Push struct {
	Ref       string
	After     string
	Repo      string
	Branch    string
	Deleted   bool
	CommitSHA string
	Message   string
	Author    string
}

// API is the GitHub operations HTTP uses. Tests inject a stub.
type API interface {
	Exchange(ctx context.Context, code, redirectURI string) (token string, err error)
	User(ctx context.Context, token string) (User, error)
	ListRepos(ctx context.Context, token string) ([]Repo, error)
	CreatePushWebhook(ctx context.Context, token, repo, hookURL, secret string) (hookID int64, err error)
	DeleteWebhook(ctx context.Context, token, repo string, hookID int64) error
}

// Client talks to GitHub over HTTPS.
type Client struct {
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	APIBase      string
	OAuthBase    string
}

func (c *Client) http() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 12 * time.Second}
}

func (c *Client) apiBase() string {
	if c != nil && strings.TrimSpace(c.APIBase) != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return defaultAPI
}

func (c *Client) oauthBase() string {
	if c != nil && strings.TrimSpace(c.OAuthBase) != "" {
		return strings.TrimRight(c.OAuthBase, "/")
	}
	return defaultOAuth
}

// AuthorizeURL is the browser redirect that starts OAuth.
func (c *Client) AuthorizeURL(redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "repo admin:repo_hook read:user")
	q.Set("state", state)
	return c.oauthBase() + "/login/oauth/authorize?" + q.Encode()
}

// Exchange trades the one-time code for an access token.
func (c *Client) Exchange(ctx context.Context, code, redirectURI string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("oauth code is required")
	}
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthBase()+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	body, err := c.do(req)
	if err != nil {
		return "", err
	}
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("oauth token: invalid response")
	}
	if out.AccessToken == "" {
		if out.Error != "" {
			return "", fmt.Errorf("oauth token: %s", out.Error)
		}
		return "", fmt.Errorf("oauth token: empty")
	}
	return out.AccessToken, nil
}

// User loads the GitHub login for the token.
func (c *Client) User(ctx context.Context, token string) (User, error) {
	var u User
	if err := c.getJSON(ctx, token, "/user", &u); err != nil {
		return User{}, err
	}
	if u.ID == 0 || strings.TrimSpace(u.Login) == "" {
		return User{}, fmt.Errorf("github user is incomplete")
	}
	return u, nil
}

// ListRepos returns up to 300 repos the token can see.
func (c *Client) ListRepos(ctx context.Context, token string) ([]Repo, error) {
	out := make([]Repo, 0)
	for page := 1; page <= 3; page++ {
		var batch []Repo
		path := fmt.Sprintf("/user/repos?per_page=100&page=%d&affiliation=owner,collaborator,organization_member&sort=updated", page)
		if err := c.getJSON(ctx, token, path, &batch); err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return out, nil
}

// CreatePushWebhook registers a push hook. GitHub stores the secret;
// we persist a copy sealed so the webhook handler can HMAC-verify.
func (c *Client) CreatePushWebhook(ctx context.Context, token, repo, hookURL, secret string) (int64, error) {
	payload, err := json.Marshal(map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"push"},
		"config": map[string]string{
			"url":          hookURL,
			"content_type": "json",
			"secret":       secret,
			"insecure_ssl": "0",
		},
	})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase()+"/repos/"+repo+"/hooks", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	c.auth(req, token)
	req.Header.Set("Content-Type", "application/json")
	body, err := c.do(req)
	if err != nil {
		return 0, err
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == 0 {
		return 0, fmt.Errorf("create webhook: unexpected response")
	}
	return out.ID, nil
}

// EnvExampleVar is one line from a .env.example file.
//
// Value is parsed only so keys can be split between folders. The HTTP
// handler clears it before the response. A committed .env is never read.
type EnvExampleVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// EnvTemplate is the environment for one folder.
//
// Path is the file in git when that folder has its own example.
// Source is set when the keys were taken from a shared root file and
// assigned to this folder, so the form can say where they came from.
// Directory "." is the repository root. Any other directory is saved
// onto the service whose root is that folder.
type EnvTemplate struct {
	Path      string          `json:"path"`
	Directory string          `json:"directory"`
	Source    string          `json:"source,omitempty"`
	Vars      []EnvExampleVar `json:"vars"`
}

// EnvExampleVars reads KEY=value lines from a dotenv example.
func EnvExampleVars(content string) []EnvExampleVar {
	var vars []EnvExampleVar
	seen := map[string]struct{}{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if !envKeyOK(key) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		vars = append(vars, EnvExampleVar{Key: key, Value: unquoteEnv(line[eq+1:])})
	}
	return vars
}

func unquoteEnv(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}

func envKeyOK(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// EnvTemplates lists example env files at the repo root and one folder down,
// then gives each service folder its own set of keys.
//
// The git tree is not cloned. A folder with its own .env.example,
// .env.sample, or .env.template keeps that file. When the only file is
// a shared root example, keys are split: browser build keys and
// CLIENT_PORT go to the one frontend folder (client, frontend, web, ui),
// and every other key goes to the one backend folder (server, api,
// backend). A committed .env is never read, so a real secret in git is
// not copied into the API. Database keys stay off the frontend.
func (c *Client) EnvTemplates(ctx context.Context, token, repo, ref string) ([]EnvTemplate, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "main"
	}
	root, err := c.listContents(ctx, token, repo, "", ref)
	if err != nil {
		return nil, err
	}
	var found []EnvTemplate
	add := func(filePath string) error {
		body, err := c.fileContents(ctx, token, repo, filePath, ref)
		if err != nil {
			return err
		}
		dir := "."
		if i := strings.LastIndex(filePath, "/"); i >= 0 {
			dir = filePath[:i]
		}
		found = append(found, EnvTemplate{Path: filePath, Directory: dir, Vars: EnvExampleVars(body)})
		return nil
	}
	for _, ent := range root {
		if ent.Type == "file" && isEnvExampleName(ent.Name) {
			if err := add(ent.Path); err != nil {
				return nil, err
			}
		}
	}
	var services []string
	for _, ent := range root {
		if ent.Type != "dir" || skipEnvDir(ent.Name) {
			continue
		}
		kids, err := c.listContents(ctx, token, repo, ent.Path, ref)
		if err != nil {
			return nil, err
		}
		marker := false
		for _, kid := range kids {
			if kid.Type != "file" {
				continue
			}
			if isEnvExampleName(kid.Name) {
				if err := add(kid.Path); err != nil {
					return nil, err
				}
			}
			if isServiceMarker(kid.Name) {
				marker = true
			}
		}
		if marker {
			services = append(services, ent.Path)
		}
	}
	return partitionEnvTemplates(found, services), nil
}

func isEnvExampleName(name string) bool {
	switch name {
	case ".env.example", ".env.sample", ".env.template":
		return true
	default:
		return false
	}
}

func isServiceMarker(name string) bool {
	switch name {
	case "Dockerfile", "package.json", "go.mod", "requirements.txt", "pyproject.toml", "Cargo.toml":
		return true
	default:
		return false
	}
}

func skipEnvDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".git", "dist", ".next", "build":
		return true
	default:
		return false
	}
}

// folderRole classifies a service directory by its name. An unknown
// name is left blank so a shared env file is not guessed onto it.
func folderRole(dir string) string {
	switch strings.ToLower(path.Base(dir)) {
	case "client", "frontend", "web", "ui":
		return "frontend"
	case "server", "api", "backend":
		return "backend"
	default:
		return ""
	}
}

// isFrontendEnvKey is a value the browser build or the client publish
// port needs. It matches the VITE_ / NEXT_PUBLIC_ / REACT_APP_ prefixes
// the runtime bakes into an image. CLIENT_PORT is the compose host port
// for the client container, not a server setting. Database keys are
// never frontend keys.
func isFrontendEnvKey(key string) bool {
	return key == "CLIENT_PORT" ||
		strings.HasPrefix(key, "VITE_") ||
		strings.HasPrefix(key, "NEXT_PUBLIC_") ||
		strings.HasPrefix(key, "REACT_APP_")
}

// partitionEnvTemplates gives each service folder its own environment.
//
// A folder that already has an example file keeps that file. A shared
// root file is split when one frontend folder, one backend folder, or
// one of each is waiting for keys. Several folders of the same role
// are not guessed: the root file stays whole.
func partitionEnvTemplates(found []EnvTemplate, services []string) []EnvTemplate {
	found = dedupeEnvFiles(found)
	own := map[string]EnvTemplate{}
	var root *EnvTemplate
	var owned []EnvTemplate
	for i := range found {
		t := found[i]
		if t.Directory == "." {
			cp := t
			root = &cp
			continue
		}
		own[t.Directory] = t
		owned = append(owned, t)
	}
	if root == nil || len(services) == 0 {
		return found
	}
	var frontends, backends []string
	for _, dir := range services {
		if _, ok := own[dir]; ok {
			continue
		}
		switch folderRole(dir) {
		case "frontend":
			frontends = append(frontends, dir)
		case "backend":
			backends = append(backends, dir)
		}
	}
	if len(frontends) > 1 || len(backends) > 1 || (len(frontends) == 0 && len(backends) == 0) {
		return found
	}
	var frontVars, backVars, leftover []EnvExampleVar
	for _, v := range root.Vars {
		switch {
		case len(frontends) == 1 && isFrontendEnvKey(v.Key):
			frontVars = append(frontVars, v)
		case len(backends) == 1 && !isFrontendEnvKey(v.Key):
			backVars = append(backVars, v)
		default:
			leftover = append(leftover, v)
		}
	}
	out := make([]EnvTemplate, 0, len(owned)+3)
	if len(frontends) == 1 && len(frontVars) > 0 {
		out = append(out, EnvTemplate{
			Path:      frontends[0],
			Directory: frontends[0],
			Source:    root.Path,
			Vars:      frontVars,
		})
	}
	if len(backends) == 1 && len(backVars) > 0 {
		out = append(out, EnvTemplate{
			Path:      backends[0],
			Directory: backends[0],
			Source:    root.Path,
			Vars:      backVars,
		})
	}
	out = append(out, owned...)
	if len(leftover) > 0 {
		root.Vars = leftover
		out = append(out, *root)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Directory == "." {
			return false
		}
		if out[j].Directory == "." {
			return true
		}
		return out[i].Directory < out[j].Directory
	})
	return out
}

func dedupeEnvFiles(in []EnvTemplate) []EnvTemplate {
	best := map[string]EnvTemplate{}
	var order []string
	for _, t := range in {
		prev, ok := best[t.Directory]
		if !ok {
			order = append(order, t.Directory)
			best[t.Directory] = t
			continue
		}
		if envExampleRank(t.Path) < envExampleRank(prev.Path) {
			best[t.Directory] = t
		}
	}
	out := make([]EnvTemplate, 0, len(order))
	for _, dir := range order {
		out = append(out, best[dir])
	}
	return out
}

func envExampleRank(filePath string) int {
	switch path.Base(filePath) {
	case ".env.example":
		return 0
	case ".env.sample":
		return 1
	default:
		return 2
	}
}

type contentEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func (c *Client) listContents(ctx context.Context, token, repo, path, ref string) ([]contentEntry, error) {
	u := c.apiBase() + "/repos/" + repo + "/contents/" + path + "?ref=" + url.QueryEscape(ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req, token)
	body, err := c.doLimited(req, 1<<20)
	if err != nil {
		return nil, err
	}
	var entries []contentEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("github contents: %w", err)
	}
	return entries, nil
}

func (c *Client) fileContents(ctx context.Context, token, repo, path, ref string) (string, error) {
	u := c.apiBase() + "/repos/" + repo + "/contents/" + path + "?ref=" + url.QueryEscape(ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	c.auth(req, token)
	body, err := c.doLimited(req, 1<<20)
	if err != nil {
		return "", err
	}
	var file contentEntry
	if err := json.Unmarshal(body, &file); err != nil {
		return "", fmt.Errorf("github file: %w", err)
	}
	if file.Encoding != "base64" {
		return "", fmt.Errorf("github file: unexpected encoding")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("github file: %w", err)
	}
	// Every snapshot file shares this cap. An example environment file
	// over the cap is skipped by the caller. Any other file fails the
	// plan rather than being truncated.
	if len(raw) > 64<<10 {
		return "", errSnapshotTooLarge
	}
	return string(raw), nil
}

// errSnapshotTooLarge is returned when a GitHub file exceeds the plan
// download cap. The body is not included in the error.
var errSnapshotTooLarge = errors.New("snapshot file is too large")

func (c *Client) doLimited(req *http.Request, limit int64) ([]byte, error) {
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("github read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github status %d", resp.StatusCode)
	}
	return body, nil
}

// DeleteWebhook is best-effort on disconnect. 404 is success.
func (c *Client) DeleteWebhook(ctx context.Context, token, repo string, hookID int64) error {
	if hookID <= 0 {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/repos/%s/hooks/%d", c.apiBase(), repo, hookID), nil)
	if err != nil {
		return err
	}
	c.auth(req, token)
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("delete webhook: status %d", resp.StatusCode)
}

func (c *Client) getJSON(ctx context.Context, token, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase()+path, nil)
	if err != nil {
		return err
	}
	c.auth(req, token)
	body, err := c.do(req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("github json: %w", err)
	}
	return nil
}

func (c *Client) auth(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("github read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github status %d", resp.StatusCode)
	}
	return body, nil
}

// VerifySignature checks X-Hub-Signature-256 against the raw body.
//
// GitHub HMAC-SHA256s the exact bytes with the hook secret. Compare
// with hmac.Equal so a timing leak cannot cheapen brute force.
// Missing or malformed headers fail closed.
func VerifySignature(secret, header string, body []byte) bool {
	secret = strings.TrimSpace(secret)
	header = strings.TrimSpace(header)
	if secret == "" || header == "" {
		return false
	}
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	raw, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(raw, mac.Sum(nil))
}

// ParsePush extracts repo, branch, and commit identity from a push body.
func ParsePush(body []byte) (Push, error) {
	var raw struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		HeadCommit *struct {
			ID      string `json:"id"`
			Message string `json:"message"`
			Author  struct {
				Name  string `json:"name"`
				Login string `json:"username"`
			} `json:"author"`
		} `json:"head_commit"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Push{}, fmt.Errorf("invalid push payload")
	}
	branch, _ := strings.CutPrefix(raw.Ref, "refs/heads/")
	if raw.Ref != "" && branch == raw.Ref {
		// Tag or other ref — not a branch push.
		branch = ""
	}
	p := Push{
		Ref:     raw.Ref,
		After:   raw.After,
		Repo:    raw.Repository.FullName,
		Branch:  branch,
		Deleted: raw.Deleted || isZeroSHA(raw.After),
	}
	if raw.HeadCommit != nil {
		p.CommitSHA = raw.HeadCommit.ID
		p.Message = raw.HeadCommit.Message
		p.Author = raw.HeadCommit.Author.Name
		if p.Author == "" {
			p.Author = raw.HeadCommit.Author.Login
		}
	}
	if p.CommitSHA == "" {
		p.CommitSHA = raw.After
	}
	return p, nil
}

func isZeroSHA(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

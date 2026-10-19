package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/reyansh7/Forge/internal/store"
)

// GitFetcher clones a specific ref (branch or SHA) and may attach a
// GitHub PAT without putting it in the repository URL.
//
// HostFetcher implements this. Tests that only implement Fetcher keep
// using Fetch — the worker type-asserts and falls back.
type GitFetcher interface {
	FetchRef(ctx context.Context, repositoryURL, dest, ref, token string) error
}

// FetchRef clones repositoryURL at ref.
//
// Token is sent as git http.extraHeader via process env (not argv and
// not userinfo in the URL). CombinedOutput is truncated and must never
// include the token: we do not interpolate it into the error string.
func (HostFetcher) FetchRef(ctx context.Context, repositoryURL, dest, ref, token string) error {
	repositoryURL = strings.TrimSpace(repositoryURL)
	ref = strings.TrimSpace(ref)
	token = strings.TrimSpace(token)

	if store.UsesBundledHello(repositoryURL) {
		return copyHello(dest)
	}
	if ref == "" && token == "" {
		return (HostFetcher{}).Fetch(ctx, repositoryURL, dest)
	}

	u, err := url.Parse(repositoryURL)
	if err != nil {
		return fmt.Errorf("fetch: parse url: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("fetch: phase 0 supports https and %s only", store.SampleHelloURL)
	}
	if u.Host == "" || u.User != nil {
		return fmt.Errorf("fetch: repository url must not include userinfo")
	}
	if err := rejectBlockedHost(ctx, u.Hostname()); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return fmt.Errorf("fetch: mkdir: %w", err)
	}

	env := gitAuthEnv(token)
	if ref != "" && looksLikeSHA(ref) {
		return fetchSHA(ctx, env, repositoryURL, dest, ref)
	}
	args := append(gitCommandConfig(), "clone", "--depth", "1")
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", repositoryURL, dest)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil && token != "" && gitAskedForCredentials(string(out)) {
		// A rejected Authorization header makes GitHub answer 401 even
		// for a public repository, and Git then tries to prompt. The
		// first attempt may have left an empty directory. Remove it
		// and retry with no token. A private repository fails this
		// second attempt too; the first error is the one we keep.
		_ = os.RemoveAll(dest)
		if mkErr := os.MkdirAll(dest, 0o700); mkErr == nil {
			plain := exec.CommandContext(ctx, "git", args...)
			plain.Env = gitSafeEnv()
			if _, perr := plain.CombinedOutput(); perr == nil {
				return refuseIncompleteCheckout(dest)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("fetch: git clone failed: %s", truncate(string(out), 300))
	}
	return refuseIncompleteCheckout(dest)
}

// gitAskedForCredentials is Git's message when GIT_TERMINAL_PROMPT=0
// stops an interactive username or password prompt. It is not a
// network error and not a missing git binary.
func gitAskedForCredentials(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "could not read username") || strings.Contains(s, "could not read password")
}

func fetchSHA(ctx context.Context, env []string, repositoryURL, dest, sha string) error {
	// Shallow clone of a SHA: init + fetch that object + checkout.
	// `clone --depth 1` only has the default branch tip and would miss
	// an older webhook SHA.
	init := exec.CommandContext(ctx, "git", append(gitCommandConfig(), "init", "--", dest)...)
	init.Env = env
	if out, err := init.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git init failed: %s", truncate(string(out), 300))
	}
	remote := exec.CommandContext(ctx, "git", "-C", dest, "remote", "add", "origin", repositoryURL)
	remote.Env = env
	if out, err := remote.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git remote failed: %s", truncate(string(out), 300))
	}
	fetch := exec.CommandContext(ctx, "git", append(append([]string{"-C", dest}, gitCommandConfig()...),
		"fetch", "--depth", "1", "origin", sha)...)
	fetch.Env = env
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git fetch failed: %s", truncate(string(out), 300))
	}
	checkout := exec.CommandContext(ctx, "git", "-C", dest, "checkout", "--force", "FETCH_HEAD")
	checkout.Env = env
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git checkout failed: %s", truncate(string(out), 300))
	}
	return refuseIncompleteCheckout(dest)
}

func gitAuthEnv(token string) []string {
	env := gitSafeEnv()
	if token == "" {
		return env
	}
	// GIT_CONFIG_* passes Authorization without putting the token on
	// argv or in the clone URL. Never log this environment.
	//
	// The REST API accepts "Authorization: Bearer". GitHub's git
	// server does not: a gho_ OAuth token sent that way is ignored,
	// and git then asks for a username. Basic with user
	// x-access-token is the form git clone accepts.
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return append(env,
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
	)
}

func looksLikeSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

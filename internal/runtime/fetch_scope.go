package runtime

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/reyansh7/Forge/internal/store"
)

// ScopedFetcher checks out one service directory.
//
// HostFetcher implements it. A test double that only implements
// Fetcher still clones the whole repository; ConfineRoot then keeps
// that service's directory out of the Docker context, but the quota
// walk still counts the rest of the tree. Production uses FetchScope
// so a frontend deploy does not download a sibling model tree.
type ScopedFetcher interface {
	FetchScope(ctx context.Context, repositoryURL, dest, ref, token, root string) error
}

// serviceScope is true when the application root is a subdirectory.
// "." and empty stay a full shallow clone. Sparse checkout alone does
// not shrink the pack; the blob filter is what omits sibling blobs.
func serviceScope(root string) bool {
	root = strings.Trim(strings.TrimSpace(root), "/")
	return root != "" && root != "."
}

func validateScopeRoot(root string) error {
	root = strings.Trim(strings.TrimSpace(root), "/")
	if root == "" || root == "." {
		return nil
	}
	if filepath.IsAbs(root) || strings.Contains(root, "\\") || strings.Contains(root, "..") {
		return fmt.Errorf("fetch: root directory escapes the repository")
	}
	return nil
}

// scopeCloneArgs is appended to `git clone` only for a service
// subdirectory. --filter=blob:none is the transfer reduction.
// --sparse without that filter still downloads every blob and only
// changes which files are written to the working tree.
func scopeCloneArgs(root string) []string {
	if !serviceScope(root) {
		return nil
	}
	return []string{"--filter=blob:none", "--sparse"}
}

// FetchScope materializes repositoryURL at ref, limited to root when
// root is a subdirectory. Token handling matches FetchRef: the token
// is process environment, never argv and never part of the error.
//
// Git LFS objects and submodule URLs are still refused after checkout.
// Those URLs are chosen by the repository, not by the host already
// checked here. A service-scoped checkout does not start fetching them.
func (HostFetcher) FetchScope(ctx context.Context, repositoryURL, dest, ref, token, root string) error {
	repositoryURL = strings.TrimSpace(repositoryURL)
	ref = strings.TrimSpace(ref)
	token = strings.TrimSpace(token)
	if err := validateScopeRoot(root); err != nil {
		return err
	}
	if !serviceScope(root) {
		if ref != "" || token != "" {
			return (HostFetcher{}).FetchRef(ctx, repositoryURL, dest, ref, token)
		}
		return (HostFetcher{}).Fetch(ctx, repositoryURL, dest)
	}
	if store.UsesBundledHello(repositoryURL) {
		return fmt.Errorf("fetch: the bundled sample has no service subdirectory")
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
	return cloneScoped(ctx, gitAuthEnv(token), repositoryURL, dest, ref, root, token != "")
}

// cloneScoped is the git process for a service subdirectory.
//
// allowCredentialRetry is true only when a token was supplied. A
// public repository can reject that header; the retry drops the token
// and keeps the same filter and sparse flags. The remote string is
// passed as one argv element. It is not a shell command.
func cloneScoped(ctx context.Context, env []string, remote, dest, ref, root string, allowCredentialRetry bool) error {
	if ref != "" && looksLikeSHA(ref) {
		if err := fetchSHAScoped(ctx, env, remote, dest, ref, root); err != nil {
			return err
		}
		return refuseIncompleteCheckout(dest)
	}
	args := append(gitCommandConfig(), "clone", "--depth", "1")
	args = append(args, scopeCloneArgs(root)...)
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", remote, dest)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil && allowCredentialRetry && gitAskedForCredentials(string(out)) {
		_ = os.RemoveAll(dest)
		if mkErr := os.MkdirAll(dest, 0o700); mkErr == nil {
			plain := exec.CommandContext(ctx, "git", args...)
			plain.Env = gitSafeEnv()
			if _, perr := plain.CombinedOutput(); perr == nil {
				if coneErr := applySparseCone(ctx, gitSafeEnv(), dest, root); coneErr != nil {
					return coneErr
				}
				return refuseIncompleteCheckout(dest)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("fetch: git clone failed: %s", truncate(string(out), 300))
	}
	if err := applySparseCone(ctx, env, dest, root); err != nil {
		return err
	}
	return refuseIncompleteCheckout(dest)
}

// fetchSHAScoped is the SHA path with the blob filter applied before
// checkout. Setting the sparse cone first keeps `git checkout` from
// asking for blobs outside the service directory.
func fetchSHAScoped(ctx context.Context, env []string, remote, dest, sha, root string) error {
	init := exec.CommandContext(ctx, "git", append(gitCommandConfig(), "init", "--", dest)...)
	init.Env = env
	if out, err := init.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git init failed: %s", truncate(string(out), 300))
	}
	add := exec.CommandContext(ctx, "git", "-C", dest, "remote", "add", "origin", remote)
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git remote failed: %s", truncate(string(out), 300))
	}
	cone := exec.CommandContext(ctx, "git", append(gitCommandConfig(), "-C", dest, "sparse-checkout", "init", "--cone")...)
	cone.Env = env
	if out, err := cone.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: sparse checkout failed: %s", truncate(string(out), 300))
	}
	if err := applySparseCone(ctx, env, dest, root); err != nil {
		return err
	}
	fetch := exec.CommandContext(ctx, "git", append(append([]string{"-C", dest}, gitCommandConfig()...),
		"fetch", "--filter=blob:none", "--depth", "1", "origin", sha)...)
	fetch.Env = env
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git fetch failed: %s", truncate(string(out), 300))
	}
	checkout := exec.CommandContext(ctx, "git", "-C", dest, "checkout", "--force", "FETCH_HEAD")
	checkout.Env = env
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch: git checkout failed: %s", truncate(string(out), 300))
	}
	return nil
}

// applySparseCone writes the service directory into the sparse cone.
// The path is one argv element after "--", so a root containing spaces
// cannot become extra flags. ".." is rejected before this runs.
func applySparseCone(ctx context.Context, env []string, dest, root string) error {
	if !serviceScope(root) {
		return nil
	}
	root = strings.Trim(strings.TrimSpace(root), "/")
	cmd := exec.CommandContext(ctx, "git", append(gitCommandConfig(), "-C", dest, "sparse-checkout", "set", "--cone", "--", root)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fetch: sparse checkout failed: %s", truncate(string(out), 300))
	}
	return nil
}

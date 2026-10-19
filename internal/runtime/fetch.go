package runtime

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/reyansh7/Forge/internal/store"
)

// helloFS is the bundled Phase 0 sample. forge://hello copies this tree
// — it is not a network fetch and cannot be pointed at an arbitrary path.
//
//go:embed testdata/hello/*
var helloFS embed.FS

// Fetcher materializes source into dest. Production uses HostFetcher.
// Tests inject a stub so git/network are not required.
type Fetcher interface {
	Fetch(ctx context.Context, repositoryURL, dest string) error
}

// HostFetcher clones https remotes with the git CLI, or copies the
// embedded sample. It never uses a shell, never accepts file://, and
// refuses loopback/private resolved IPs (SSRF into the control plane).
type HostFetcher struct{}

func (HostFetcher) Fetch(ctx context.Context, repositoryURL, dest string) error {
	repositoryURL = strings.TrimSpace(repositoryURL)
	// Bundled sample: no git, no DNS. Includes forge://hello and the
	// GitHub docs placeholder github.com/example/* (not a real repo).
	if store.UsesBundledHello(repositoryURL) {
		return copyHello(dest)
	}

	u, err := url.Parse(repositoryURL)
	if err != nil {
		return fmt.Errorf("fetch: parse url: %w", err)
	}
	// Phase 0 fetch is https only (plus forge://hello). Stored git@/ssh
	// remotes fail here with a clear error rather than opening SSH.
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("fetch: phase 0 supports https and %s only", store.SampleHelloURL)
	}
	if u.Host == "" || u.User != nil {
		// Userinfo in a clone URL is often a token. Reject rather than
		// log or pass credentials on a process command line.
		return fmt.Errorf("fetch: repository url must not include userinfo")
	}
	if err := rejectBlockedHost(ctx, u.Hostname()); err != nil {
		return err
	}

	if err := os.MkdirAll(dest, 0o700); err != nil {
		return fmt.Errorf("fetch: mkdir: %w", err)
	}

	// protocol.file.allow=never: git must not be tricked into reading
	// the host filesystem. --depth 1: we do not need history.
	// CommandContext + argv (no shell): the URL cannot become `url; rm`.
	cmd := exec.CommandContext(ctx, "git", append(gitCommandConfig(),
		"clone", "--depth", "1", "--", repositoryURL, dest,
	)...)
	cmd.Env = gitSafeEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fetch: git clone failed: %s", truncate(string(out), 300))
	}
	return refuseIncompleteCheckout(dest)
}

// refuseIncompleteCheckout rejects a tree that still needs Git LFS
// objects or submodule checkouts. Those URLs are chosen by the
// repository, not by the clone host already checked in
// rejectBlockedHost, so Forge does not fetch them. Continuing would
// build pointer files or empty directories and present that as the
// application. A missing file is success.
func refuseIncompleteCheckout(root string) error {
	mods, err := readCheckoutFile(root, ".gitmodules")
	if err != nil {
		return err
	}
	if submoduleDeclared(mods) {
		return fmt.Errorf("fetch: .gitmodules declares submodules. Forge did not clone them, so this deploy stopped instead of building empty submodule directories. Vendor those files into the repository or remove the submodule entries")
	}
	attrs, err := readCheckoutFile(root, ".gitattributes")
	if err != nil {
		return err
	}
	if lfsFilter(attrs) {
		return fmt.Errorf("fetch: .gitattributes uses Git LFS (filter=lfs). Forge did not download those objects, so this deploy stopped instead of building pointer files. Commit the real files or remove the LFS filter")
	}
	return nil
}

func readCheckoutFile(root, name string) ([]byte, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetch: %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("fetch: %s is a symlink and was not followed", name)
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("fetch: %s: %w", name, err)
	}
	defer f.Close()
	buf := make([]byte, maxManifestRead+1)
	n, err := f.Read(buf)
	if n == 0 && err != nil {
		return nil, fmt.Errorf("fetch: %s: %w", name, err)
	}
	return buf[:n], nil
}

const maxManifestRead = 256 << 10

func submoduleDeclared(body []byte) bool {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[submodule ") {
			return true
		}
	}
	return false
}

func lfsFilter(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "filter=lfs") || strings.Contains(s, "filter = lfs")
}

func copyHello(dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	return fs.WalkDir(helloFS, "testdata/hello", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("testdata/hello", path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		src, err := helloFS.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func rejectBlockedHost(ctx context.Context, host string) error {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("fetch: host %q is not allowed", host)
	}
	// Link-local / metadata names used by cloud providers.
	if host == "metadata.google.internal" || host == "metadata" {
		return fmt.Errorf("fetch: host %q is not allowed", host)
	}

	resolver := net.DefaultResolver
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("fetch: resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("fetch: resolve %s: no addresses", host)
	}
	for _, ipa := range ips {
		ip := ipa.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("fetch: host resolves to a blocked address")
		}
	}
	return nil
}

// gitCommandConfig stops the operator's Git setup from rewriting the
// clone. credential.username in ~/.gitconfig turns
// https://github.com/... into https://reyansh7@github.com and Git then
// asks for a password. An empty -c value overrides that file.
// credential.helper= drops Windows Credential Manager for this command.
func gitCommandConfig() []string {
	return []string{
		"-c", "protocol.file.allow=never",
		"-c", "credential.helper=",
		"-c", "credential.username=",
	}
}

func gitSafeEnv() []string {
	// Drop GIT_* hooks from the operator environment so a repo cannot
	// run host-side hook scripts during clone.
	//
	// Also ignore the operator's gitconfig. On Windows that file often
	// rewrites https://github.com into https://<user>@github.com and
	// then asks for a password. Forge authenticates with the OAuth
	// token instead, and GIT_TERMINAL_PROMPT=0 would turn that prompt
	// into "could not read Password".
	cfg := filepath.Join(os.TempDir(), "forge-empty-gitconfig")
	if f, err := os.OpenFile(cfg, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = f.Close()
	}
	out := []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + cfg,
		"GCM_INTERACTIVE=never",
	}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GIT_") || strings.EqualFold(key, "GCM_INTERACTIVE") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

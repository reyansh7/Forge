package githubapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// WriteDiscoverTree downloads only the small manifests and entry files
// needed to classify a repository. It does not clone the repo, so a
// multi-gigabyte model directory is never pulled just to list services.
func (c *Client) WriteDiscoverTree(ctx context.Context, token, repo, ref, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	n := 0
	return c.snapshotDir(ctx, token, repo, ref, "", dest, 0, &n)
}

func (c *Client) snapshotDir(ctx context.Context, token, repo, ref, rel, dest string, depth int, n *int) error {
	if *n > 80 || depth > 2 {
		return nil
	}
	entries, err := c.listContents(ctx, token, repo, rel, ref)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if *n > 80 {
			return nil
		}
		if ent.Type == "file" && snapshotFile(ent.Name) {
			body, err := c.fileContents(ctx, token, repo, ent.Path, ref)
			if err != nil {
				return err
			}
			path := filepath.Join(dest, filepath.FromSlash(ent.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				return err
			}
			*n++
		}
	}
	if depth == 2 {
		return nil
	}
	for _, ent := range entries {
		if ent.Type != "dir" || skipSnapshotDir(ent.Name) {
			continue
		}
		// One extra level under cmd/ so Go main packages are visible.
		next := depth + 1
		if ent.Name == "cmd" {
			next = depth
		}
		if err := c.snapshotDir(ctx, token, repo, ref, ent.Path, dest, next, n); err != nil {
			return err
		}
	}
	return nil
}

func snapshotFile(name string) bool {
	switch name {
	case "Dockerfile", "package.json", "go.mod", "requirements.txt", "pyproject.toml", "Pipfile",
		"Cargo.toml", "Gemfile", "composer.json", "pom.xml", "build.gradle", "build.gradle.kts",
		"CMakeLists.txt", "Makefile", "Procfile", "forge.json", "index.html", "main.go", "app.py",
		"main.py", "wsgi.py", "api.py":
		return true
	default:
		return strings.HasSuffix(name, ".csproj")
	}
}

func skipSnapshotDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", ".next", "venv", ".venv", "__pycache__", "testdata", ".github", "docs":
		return true
	default:
		return false
	}
}

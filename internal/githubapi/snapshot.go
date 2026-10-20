package githubapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// WriteDiscoverTree downloads only the small manifests and entry files
// needed to classify a repository. It does not clone the repo, so a
// multi-gigabyte model directory is never pulled just to list services.
// Example environment files are included so the doctor can read key
// names. Their values stay in dest until the caller deletes it. An
// example file larger than 64 KiB is skipped.
func (c *Client) WriteDiscoverTree(ctx context.Context, token, repo, ref, dest string) error {
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	n := 0
	return c.snapshotDir(ctx, token, repo, ref, "", dest, 0, &n)
}

func (c *Client) snapshotDir(ctx context.Context, token, repo, ref, rel, dest string, depth int, n *int) error {
	// Eight path segments match detect.maxDiscoverDepth. Past that the
	// plan stops. The file cap still applies, so a wide tree cannot
	// turn the plan into a full clone.
	if *n > 80 || depth > 8 {
		return nil
	}
	entries, err := c.listContents(ctx, token, repo, rel, ref)
	if err != nil {
		return err
	}
	service := false
	for _, ent := range entries {
		if *n > 80 {
			return nil
		}
		if ent.Type == "file" && snapshotFile(ent.Name) {
			body, err := c.fileContents(ctx, token, repo, ent.Path, ref)
			if err != nil {
				// A huge example file must not fail the rest of the
				// plan, and its body is not kept.
				if exampleEnvName(ent.Name) && errors.Is(err, errSnapshotTooLarge) {
					continue
				}
				return err
			}
			path := filepath.Join(dest, filepath.FromSlash(ent.Path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			// A large example file is not a manifest. Skipping it keeps a
			// sample secret dump out of the plan directory.
			if exampleEnvName(ent.Name) && len(body) > 64<<10 {
				continue
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				return err
			}
			*n++
			if snapshotServiceFile(ent.Name) {
				service = true
			}
		}
	}
	if service {
		// This directory is an application root. Do not open source
		// children. Still open cmd/ so a Go main package is visible.
		for _, ent := range entries {
			if ent.Type != "dir" || ent.Name != "cmd" || skipSnapshotDir(ent.Name) {
				continue
			}
			if err := c.snapshotDir(ctx, token, repo, ref, ent.Path, dest, depth, n); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ent := range entries {
		if ent.Type != "dir" || skipSnapshotDir(ent.Name) {
			continue
		}
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

func snapshotServiceFile(name string) bool {
	switch name {
	case "Dockerfile", "package.json", "go.mod", "requirements.txt", "pyproject.toml", "Pipfile",
		"Cargo.toml", "Gemfile", "composer.json", "pom.xml", "build.gradle", "build.gradle.kts",
		"CMakeLists.txt", "Makefile", "Procfile", "forge.json", "forge.yaml", "docker-compose.yml", "index.html":
		return true
	default:
		return strings.HasSuffix(name, ".csproj")
	}
}

func snapshotFile(name string) bool {
	switch name {
	case "Dockerfile", "package.json", "go.mod", "requirements.txt", "pyproject.toml", "Pipfile",
		"Cargo.toml", "Gemfile", "composer.json", "pom.xml", "build.gradle", "build.gradle.kts",
		"CMakeLists.txt", "Makefile", "Procfile", "forge.json", "forge.yaml", "docker-compose.yml", "index.html",
		".env.example", ".env.sample", ".env.template", "main.go", "app.py",
		"main.py", "wsgi.py", "api.py":
		return true
	default:
		return strings.HasSuffix(name, ".csproj")
	}
}

func exampleEnvName(name string) bool {
	switch name {
	case ".env.example", ".env.sample", ".env.template":
		return true
	default:
		return false
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

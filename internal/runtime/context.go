package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/reyansh7/Forge/internal/detect"
)

// forgeIgnoreMarker marks the block Forge appends to .dockerignore.
// A second prepare does not append it again. Lines above the marker
// are the repository's own rules and are left unchanged.
const forgeIgnoreMarker = "# forge-build-context"

// mergeBuildIgnore appends Forge's context exclusions to .dockerignore.
//
// Docker sends every file under the build directory except what this
// file names. Resume Analyzer's 2.62 GB transfer was the working tree
// after .git removal: datasets, models, and assets. Those stay. This
// list is only metadata and caches.
//
// .git is excluded for every recipe, including a user Dockerfile.
// removeGitMetadata already deletes the root git dir; this still drops
// a nested submodule git dir the root cleanup does not walk.
//
// node_modules, virtualenvs, and bytecode caches are excluded only when
// Forge generated the Dockerfile, because that recipe runs npm install
// or pip install. A user Dockerfile may COPY node_modules or a venv, so
// those names are not added for KindDockerfile.
//
// Not listed, on purpose: dist, build, .next, out, data, models,
// assets, ml-model, and file types such as csv, pdf, png, and joblib.
// A generated recipe may serve a committed dist, and a dataset is
// runtime input. The workspace quota is unchanged.
func mergeBuildIgnore(dir string, kind detect.Kind, generated bool) error {
	path := filepath.Join(dir, ".dockerignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(existing), forgeIgnoreMarker) {
		return nil
	}
	block := forgeIgnoreBlock(kind, generated)
	var b strings.Builder
	if len(existing) > 0 {
		b.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(block)
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func forgeIgnoreBlock(kind detect.Kind, generated bool) string {
	lines := []string{
		forgeIgnoreMarker,
		"# VCS metadata is not a runtime file. Nested .git dirs survive",
		"# the root cleanup when they belong to a submodule.",
		".git",
		"**/.git",
		".DS_Store",
		"Thumbs.db",
	}
	if generated {
		lines = append(lines, recipeIgnorePatterns(kind)...)
	}
	return strings.Join(lines, "\n") + "\n"
}

// recipeIgnorePatterns are caches the matching generated Dockerfile
// recreates inside the image. They are not applied to a user Dockerfile.
func recipeIgnorePatterns(kind detect.Kind) []string {
	switch kind {
	case detect.KindNode:
		return []string{
			"# Reinstalled by npm ci / npm install in the generated recipe.",
			"node_modules",
			".npm",
			".pnpm-store",
			".yarn/cache",
			"**/.yarn/cache",
		}
	case detect.KindPython:
		return []string{
			"# Recreated by pip in the generated recipe. Source .py files stay.",
			"__pycache__",
			"*.pyc",
			".pytest_cache",
			".mypy_cache",
			".ruff_cache",
			".tox",
			".venv",
			"venv",
		}
	case detect.KindRust:
		return []string{
			// cargo build --release writes target/ inside the image.
			// The host directory is a previous compile, not an input.
			"# Recreated by cargo build --release in the generated recipe.",
			"target",
		}
	case detect.KindJava:
		return []string{
			// mvn package writes target/. Gradle’s cache is .gradle.
			// The directory name "build" is not listed: it also names
			// source trees, and a Gradle recipe recreates build/libs
			// after COPY.
			"# Recreated by Maven or Gradle in the generated recipe.",
			"target",
			".gradle",
		}
	case detect.KindDotnet:
		return []string{
			// dotnet publish -o /out recompiles. bin and obj are the
			// SDK’s intermediate directories, not the published output.
			"# Recreated by dotnet publish in the generated recipe.",
			"bin",
			"obj",
		}
	default:
		// Go is intentionally empty. vendor/ and go.sum are inputs when
		// the module is built offline. The recipe is COPY . . and
		// go build, so dropping them would change the result.
		return nil
	}
}

// BuildKit prints "transferring context: 2.62GB 522.6s done". The
// classic builder prints "Sending build context to Docker daemon  2.62GB".
// Both sizes are decimal (1 kB = 1000). The dockerfile transfer is a
// separate small line, so the caller keeps the largest.
var (
	contextTransfer = regexp.MustCompile(`transferring context:\s+(\S+)\s+[\d.]+s(?:\s+done)?`)
	contextSending  = regexp.MustCompile(`Sending build context to Docker daemon\s+(\S+)`)
)

// LargestContextBytes is the biggest context size Docker reported in a
// build log. ok is false when the log has no such line. A reported 0 is
// ok with n == 0; callers must not substitute 0 for a missing line.
func LargestContextBytes(log string) (n int64, ok bool) {
	for _, re := range []*regexp.Regexp{contextTransfer, contextSending} {
		for _, m := range re.FindAllStringSubmatch(log, -1) {
			parsed, err := parseDockerSize(m[1])
			if err != nil {
				continue
			}
			if !ok || parsed > n {
				n = parsed
				ok = true
			}
		}
	}
	return n, ok
}

func parseDockerSize(token string) (int64, error) {
	token = strings.TrimSpace(token)
	mult := float64(1)
	for _, suf := range []struct {
		s string
		m float64
	}{
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"kB", 1000},
		{"B", 1},
	} {
		if strings.HasSuffix(token, suf.s) {
			mult = suf.m
			token = strings.TrimSuffix(token, suf.s)
			break
		}
	}
	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0, err
	}
	return int64(value * mult), nil
}

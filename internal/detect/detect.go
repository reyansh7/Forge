// Package detect classifies a fetched source tree without executing it.
//
// Detection is file presence only. A malicious package.json or Makefile
// is not run on the host — the worker only reads names, then builds
// inside Docker.
package detect

import (
	"fmt"
	"os"
	"path/filepath"
)

// Kind is the Phase 0 build strategy.
//
// dockerfile: use the repository Dockerfile (untrusted, isolated in docker build).
// node / go: Forge writes a Dockerfile so npm/go never run on the host.
type Kind string

const (
	KindDockerfile Kind = "dockerfile"
	KindNode       Kind = "node"
	KindGo         Kind = "go"
)

// Result is what the worker persists as runtime_kind.
type Result struct {
	Kind Kind
}

// Tree inspects dir. It must not exec scripts or follow repository hooks.
func Tree(dir string) (Result, error) {
	if dir == "" {
		return Result{}, fmt.Errorf("detect: empty directory")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return Result{}, fmt.Errorf("detect: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("detect: %s is not a directory", dir)
	}

	// Dockerfile wins: the author already chose an image. It still runs
	// only as `docker build`, never as a host shell script.
	if exists(filepath.Join(dir, "Dockerfile")) {
		return Result{Kind: KindDockerfile}, nil
	}
	if exists(filepath.Join(dir, "go.mod")) {
		return Result{Kind: KindGo}, nil
	}
	if exists(filepath.Join(dir, "package.json")) {
		return Result{Kind: KindNode}, nil
	}
	return Result{}, fmt.Errorf("detect: no Dockerfile, go.mod, or package.json")
}

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

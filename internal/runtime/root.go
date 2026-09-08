package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfineRoot maps operator root_directory onto the cloned tree.
//
// What: after git clone (or sample copy), detect and docker build must
// run in a subdirectory if the app lives in apps/web rather than repo
// root.
//
// Why a second check: Postgres already stores a validated relative path,
// but a corrupted or hand-edited row with ".." would otherwise let
// docker build read /etc from the worker host. filepath.Rel must stay
// inside cloneDir.
//
// cloneDir is the fetch destination (unique per deployment). root is the
// application setting (default ".").
func ConfineRoot(cloneDir, root string) (string, error) {
	if strings.TrimSpace(cloneDir) == "" {
		return "", fmt.Errorf("confine root: empty clone directory")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	if filepath.IsAbs(root) || strings.ContainsAny(root, `\:`) {
		return "", fmt.Errorf("confine root: root_directory must be relative")
	}
	base, err := filepath.Abs(cloneDir)
	if err != nil {
		return "", fmt.Errorf("confine root: %w", err)
	}
	full, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(root)))
	if err != nil {
		return "", fmt.Errorf("confine root: %w", err)
	}
	rel, err := filepath.Rel(base, full)
	if err != nil {
		return "", fmt.Errorf("confine root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("confine root: root_directory escapes the clone")
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("confine root: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("confine root: %s is not a directory", root)
	}
	return full, nil
}

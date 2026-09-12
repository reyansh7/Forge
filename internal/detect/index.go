package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileIndex is a read-only view of the confined project root.
//
// Only the root directory is listed. Recursing would pick up
// node_modules, vendor, and .git and could mis-classify. Manifests we
// care about (go.mod, package.json, …) live at the build root after
// ConfineRoot. Contents are read with a size cap so a huge file cannot
// become a host-side DoS.
type fileIndex struct {
	root  string
	files map[string]os.FileInfo
}

const maxManifestBytes = 256 << 10

func indexRoot(dir string) (fileIndex, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return fileIndex{}, fmt.Errorf("detect: read root: %w", err)
	}
	idx := fileIndex{root: dir, files: map[string]os.FileInfo{}}
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		idx.files[e.Name()] = info
	}
	return idx, nil
}

func (idx fileIndex) has(name string) bool {
	st, ok := idx.files[name]
	return ok && st.Mode().IsRegular()
}

func (idx fileIndex) hasDir(name string) bool {
	st, ok := idx.files[name]
	return ok && st.IsDir()
}

func (idx fileIndex) hasAny(names ...string) bool {
	for _, n := range names {
		if idx.has(n) {
			return true
		}
	}
	return false
}

func (idx fileIndex) hasExt(exts ...string) bool {
	for name, st := range idx.files {
		if !st.Mode().IsRegular() {
			continue
		}
		low := strings.ToLower(name)
		for _, ext := range exts {
			if strings.HasSuffix(low, ext) {
				return true
			}
		}
	}
	return false
}

func (idx fileIndex) firstWithExt(ext string) string {
	var names []string
	for name, st := range idx.files {
		if st.Mode().IsRegular() && strings.HasSuffix(strings.ToLower(name), ext) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	// Deterministic: first in lexical order, not map iteration.
	best := names[0]
	for _, n := range names[1:] {
		if n < best {
			best = n
		}
	}
	return best
}

func (idx fileIndex) read(name string) ([]byte, error) {
	if !idx.has(name) {
		return nil, fmt.Errorf("detect: %s is not a regular file", name)
	}
	path := filepath.Join(idx.root, name)
	// Clean+prefix check: name comes from our readdir map, but treat
	// it as untrusted anyway so a future caller cannot pass ../etc.
	if !strings.EqualFold(filepath.Base(name), name) || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("detect: refused path %q", name)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, maxManifestBytes+1)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	if n > maxManifestBytes {
		return buf[:maxManifestBytes], nil
	}
	return buf[:n], nil
}

func (idx fileIndex) names() []string {
	out := make([]string, 0, len(idx.files))
	for name, st := range idx.files {
		if st.Mode().IsRegular() || st.IsDir() {
			out = append(out, name)
		}
	}
	return out
}

func (idx fileIndex) relevantNames() []string {
	var out []string
	for name := range idx.files {
		if isAssetName(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func (idx fileIndex) assetCount() int {
	n := 0
	for name, st := range idx.files {
		if st.Mode().IsRegular() && isAssetName(name) {
			n++
		}
	}
	return n
}

// hasSourceExt reports a source file at the root or one level under a
// conventional source directory. Asset files (images, video, fonts) are
// never treated as source. Detection does not recurse into vendor trees.
func (idx fileIndex) hasSourceExt(exts ...string) bool {
	if idx.hasExt(exts...) {
		return true
	}
	for _, dir := range sourceDirs {
		if !idx.hasDir(dir) {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(idx.root, dir))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || isAssetName(e.Name()) {
				continue
			}
			low := strings.ToLower(e.Name())
			for _, ext := range exts {
				if strings.HasSuffix(low, ext) {
					return true
				}
			}
		}
	}
	return false
}

func (idx fileIndex) readRel(rel string) ([]byte, error) {
	if !safeRel(rel) {
		return nil, fmt.Errorf("detect: refused path %q", rel)
	}
	path := filepath.Join(idx.root, filepath.FromSlash(rel))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, maxManifestBytes+1)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	if n > maxManifestBytes {
		return buf[:maxManifestBytes], nil
	}
	return buf[:n], nil
}

func (idx fileIndex) hasRel(rel string) bool {
	if !safeRel(rel) {
		return false
	}
	st, err := os.Stat(filepath.Join(idx.root, filepath.FromSlash(rel)))
	return err == nil && st.Mode().IsRegular()
}

var sourceDirs = []string{"src", "app", "lib", "cmd", "include", "source"}

var skipWalkDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, "vendor": {}, "venv": {}, ".venv": {},
	"__pycache__": {}, "static": {}, "assets": {}, "media": {}, "uploads": {},
}

func safeRel(rel string) bool {
	rel = strings.TrimSpace(rel)
	if rel == "" || strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) {
		return false
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 1 || len(parts) > 3 {
		return false
	}
	for _, p := range parts {
		if !fileNameOK(p) {
			return false
		}
	}
	return true
}

func fileNameOK(p string) bool {
	if p == "" || len(p) > 80 {
		return false
	}
	for _, r := range p {
		if r == '.' || r == '_' || r == '-' {
			continue
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func isAssetName(name string) bool {
	low := strings.ToLower(name)
	i := strings.LastIndex(low, ".")
	if i < 0 {
		return false
	}
	switch low[i:] {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp",
		".mp4", ".webm", ".mov", ".mkv", ".avi",
		".mp3", ".wav", ".ogg", ".flac",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".pdf", ".csv", ".tsv", ".parquet",
		".zip", ".tar", ".gz", ".7z":
		return true
	default:
		return false
	}
}

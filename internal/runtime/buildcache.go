package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// BuildCacheKey is a sha256 of the inputs that can change the image.
//
// The git commit is not the key. Two commits can build the same tree,
// and one commit can build two images when a public build value
// changes. The worker hashes the confined directory after the
// Dockerfile and .env.production.local have been written, plus the
// sorted public build env. Runtime secrets are omitted: they are
// passed at docker run, not baked into the image, and must not appear
// in the key or in logs of the key.
//
// A symlink is recorded as its target string and is not opened, so a
// link cannot pull a file from outside the workspace into the hash.
// .git is skipped because fetch already removes it and Docker is told
// to ignore it. Any other regular file is included. Hashing a file
// Docker later ignores can only cause a cache miss, which rebuilds.
// Omitting a file Docker copies would reuse a stale image.
func BuildCacheKey(dir string, env []EnvPair) (string, error) {
	sum := sha256.New()
	pub := PublicBuildEnv(env)
	sort.Slice(pub, func(i, j int) bool { return pub[i].Key < pub[j].Key })
	for _, ev := range pub {
		fmt.Fprintf(sum, "env %s=%s\n", ev.Key, ev.Value)
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(sum, "link %s %s\n", rel, target)
			return nil
		}
		if !info.Mode().IsRegular() {
			fmt.Fprintf(sum, "other %s\n", rel)
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		fileSum := sha256.New()
		_, copyErr := io.Copy(fileSum, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprintf(sum, "file %s %d %x\n", rel, info.Size(), fileSum.Sum(nil))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
